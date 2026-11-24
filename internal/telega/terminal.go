package telega

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/danilrwx/telega/internal/session"
	"github.com/danilrwx/telega/tui"
	"golang.org/x/term"
)

// Options configure the terminal client at startup.
type Options struct {
	Demo    bool
	QR      bool
	Version string
}

type config struct {
	session.Account
	scratch, version string
}

const (
	enterScreen = "\x1b[?1049h\x1b[?2004h\x1b[?1004h"
	leaveScreen = "\x1b[?1004l\x1b[?2004l\x1b[0m\x1b[?25h\x1b[?1049l"
)

// Run starts the terminal client and waits for it to exit.
func Run(opts Options) error {
	demo := opts.Demo

	in, out := int(os.Stdin.Fd()), int(os.Stdout.Fd())
	if !term.IsTerminal(in) || !term.IsTerminal(out) {
		return errors.New("telega needs a terminal")
	}

	chatStatus := tui.OpenChatStatus()

	chatStatus.Clear()
	defer chatStatus.Clear()

	cfg := config{version: opts.Version}

	if !demo {
		var err error

		cfg.Account, err = session.Load()
		if err != nil {
			return err
		}

		cfg.scratch, err = os.MkdirTemp("", "telega-")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(cfg.scratch) }()
	}

	dark := tui.DarkTheme()

	state, err := term.MakeRaw(in)
	if err != nil {
		return err
	}

	defer func() { _ = term.Restore(in, state) }()

	_, _ = io.WriteString(os.Stdout, enterScreen)
	defer func() { _, _ = io.WriteString(os.Stdout, leaveScreen) }()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer cancel()

	resize := make(chan os.Signal, 1)

	signal.Notify(resize, syscall.SIGWINCH)
	defer signal.Stop(resize)

	keys := make(chan key)

	keyDone := make(chan bool)
	go readKeys(ctx, in, os.Stdin, keys, keyDone)

	requests, events := make(chan request, 1), make(chan serverEvent, 24)

	inTmux := os.Getenv("TMUX") != ""

	a := app{demo: demo, ready: demo, focused: true}
	if inTmux && chatStatus != nil && !demo {
		pane := os.Getenv("TMUX_PANE")
		a.autoReadVisible = func() bool { return tmuxPaneVisible(ctx, pane) }
	}

	if err := tui.ReadSettings(tui.LayoutSettingsName, &a.layoutSettings); err != nil {
		a.status = "Layout settings: " + err.Error()
	}

	if !demo {
		a.draftStore, err = tui.OpenDraftStore("telega")
		if err != nil {
			a.status = "Drafts: " + err.Error()
		}
	}

	defer a.persistDraft()
	defer a.cancelHistory()

	a.console = slices.Contains([]string{"vt100", "vt220", "linux", "rcons-color", "wsvt25"}, os.Getenv("TERM"))
	reactionMode := os.Getenv("TELEGA_REACTIONS")

	colorTerm := os.Getenv("COLORTERM")
	applyTheme := func(dark bool) {
		a.colors = choosePalette(dark, colorTerm)
		a.textReactions = reactionMode == "text" || reactionMode != "emoji" && (a.console || a.colors == &ansiDarkPalette)
	}
	applyTheme(dark)
	themeChanges := tui.WatchDesktopTheme(ctx, dark)

	noticeChecks := make(chan noticeCheck, 16)

	var notices, noticeFailures chan desktopNotice

	notifier, _ := exec.LookPath("notify-send")
	if !demo && notifier != "" {
		notices = make(chan desktopNotice, 16)
		if !inTmux {
			noticeFailures = make(chan desktopNotice, 1)
		}

		go notifyDesktop(ctx, notifier, notices, noticeFailures)
	}

	if demo {
		a.chats = demoChats()
		a.openChat(requests, 0)
	} else {
		a.pending = "connect"

		done := make(chan struct{})
		go func() {
			defer close(done)

			runTelegram(ctx, cfg, opts.QR, requests, events, noticeChecks)
		}()

		defer func() {
			cancel()

			select {
			case <-done:
			case <-time.After(2 * time.Second):
			}
		}()
	}

	output := io.Writer(os.Stdout)

	a.inlineImages = tui.NewInlineImages(os.Stdout)
	if a.inlineImages != nil {
		defer a.inlineImages.Clear()

		output = a.inlineImages
	}

	var displayedImage *tui.TerminalImage

	previewCols, previewRows := 0, 0

	closeDisplayedImage := func() {
		if displayedImage != nil {
			_, _ = io.WriteString(os.Stdout, displayedImage.Delete())
			displayedImage.Release()
			displayedImage = nil
		}
	}
	defer closeDisplayedImage()

	var chatStatusTick <-chan time.Time

	if chatStatus != nil && !demo {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()

		chatStatusTick = ticker.C
	}

	statusAvailable := false

	for {
		if chatStatusTick != nil {
			unread, mentions, available := a.chatStatusCounts()
			if available {
				chatStatus.Publish(unread, mentions)
			} else if statusAvailable {
				chatStatus.Clear()
			}

			statusAvailable = available
		}

		w, h, err := term.GetSize(out)
		if err != nil {
			return err
		}

		if displayedImage != nil && displayedImage != a.imagePreview {
			closeDisplayedImage()
		}

		if a.imagePreview == nil || a.imagePreview != displayedImage || w != previewCols || h != previewRows {
			if err := paint(output, &a, w, h); err != nil {
				return err
			}
		}

		displayedImage, previewCols, previewRows = a.imagePreview, w, h

		viewportOK := w >= minTerminalCols && h >= minTerminalRows
		if viewportOK {
			a.markVisibleRead(requests)
		}

		a.prefetchHistory(requests)
		a.requestMissingAuthor(requests)

		if !demo {
			a.loadVisibleImages(requests)
		}

		var (
			historyReady <-chan time.Time
			historyTimer *time.Timer
		)
		if !a.historyDue.IsZero() {
			historyTimer = time.NewTimer(time.Until(a.historyDue))
			historyReady = historyTimer.C
		}

		select {
		case <-ctx.Done():
			return nil
		case <-resize:
		case <-chatStatusTick:
		case dark, ok := <-themeChanges:
			if ok {
				applyTheme(dark)
			} else {
				themeChanges = nil
			}
		case <-historyReady:
			a.flushChatHistory(requests)
		case notice := <-noticeFailures:
			a.alertInTerminal(os.Stdout, notice)
		case e := <-events:
			if e.kind == "notify" {
				a.deliverNotice(os.Stdout, notices, e.notice, inTmux)
			} else if notice := a.notification(e, viewportOK); notice.body != "" {
				select {
				case noticeChecks <- a.noticeCheck(e, notice):
				default:
				}
			}

			a.receive(requests, e)
		case k, ok := <-keys:
			if !ok || !a.input(k, requests) {
				return nil
			}

			edited := a.vimRequested || a.editAttachmentRequested
			if a.editAttachmentRequested {
				a.editAttachmentRequested = false
				if err := a.editDraftAttachment(ctx, func(cmd *exec.Cmd) (error, error) {
					return a.runVim(cmd, in, state)
				}); err != nil {
					return err
				}
			}

			if a.vimRequested {
				a.vimRequested = false
				a.persistDraft()

				if err := a.editInVim(ctx, in, state); err != nil {
					return err
				}
			}

			select {
			case keyDone <- edited:
			case <-ctx.Done():
				return nil
			}
		}

		if historyTimer != nil {
			historyTimer.Stop()
		}

		a.persistDraft()

		if text := a.copyText; text != "" {
			a.copyText = ""
			if err := copyClipboard(ctx, os.Stdout, text); err != nil {
				a.status = err.Error()
			} else {
				a.status = "Copied message text"
			}
		}

		target, external := a.openTarget, a.openExternal != ""
		if external {
			target = a.openExternal
		}

		if target != "" {
			previewOnly := a.previewOnly
			a.openTarget, a.openExternal, a.previewOnly = "", "", false

			if !external && filepath.IsAbs(target) && tui.ImageGraphics() {
				preview, err := tui.LoadTerminalImage(target)
				if err == nil {
					a.imagePreview, a.imagePreviewTitle = preview, filepath.Base(target)

					a.imagePreviewPath = ""
					if !previewOnly {
						a.imagePreviewPath = target
					}

					continue
				}

				if previewOnly || tui.InlineImageFile(target, "") {
					a.status = "Preview: " + err.Error()
					continue
				}
			}

			var (
				cmd *exec.Cmd
				err error
			)
			if filepath.IsAbs(target) {
				cmd, err = openAttachment(target)
			} else if safe := webLink(target, false); safe != "" {
				cmd, err = openSystem(safe)
			}

			if err != nil {
				a.imagePreview, a.imagePreviewPath = nil, ""
				a.status = err.Error()
			} else if cmd != nil {
				go func() {
					if err := cmd.Wait(); err != nil {
						_ = emit(ctx, events, serverEvent{kind: "openError", text: "System opener failed: " + target})
					}
				}()
			}
		}
	}
}

// Focus escape sequences can be missing in nested tmux sessions, especially
// when they start detached. Check the current pane before sending a read.
func tmuxPaneVisible(ctx context.Context, pane string) bool {
	if pane == "" {
		return false
	}

	ctx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()

	const format = "#{?session_attached,1,0}:#{window_active}:#{pane_active}"

	data, err := exec.CommandContext(ctx, "tmux", "-N", "display-message", "-p", "-t", pane, format).Output()

	return err == nil && strings.TrimSpace(string(data)) == "1:1:1"
}

func (a *app) editInVim(ctx context.Context, in int, state *term.State) error {
	draft, err := tui.NewVimDraft(ctx, string(a.draft))

	var (
		text        string
		terminalErr error
	)

	if err == nil {
		editorErr, runErr := a.runVim(draft.Command, in, state)
		terminalErr = runErr
		text, err = draft.Finish(errors.Join(editorErr, terminalErr))
	}

	switch {
	case terminalErr != nil:
		return terminalErr
	case err != nil:
		a.status = "Vim: " + err.Error()
	default:
		text = strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(text)
		a.draft = append(a.draft[:0], []rune(text)...)
		a.draftCursor = len(a.draft)
	}

	return nil
}

func (a *app) runVim(cmd *exec.Cmd, in int, state *term.State) (editorErr, terminalErr error) {
	if err := term.Restore(in, state); err != nil {
		return nil, err
	}

	output := io.Writer(os.Stdout)
	if a.inlineImages != nil {
		output = a.inlineImages
	}

	_, _ = io.WriteString(output, leaveScreen)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

	editorErr = cmd.Run()
	if _, err := term.MakeRaw(in); err != nil {
		return editorErr, err
	}

	_, _ = io.WriteString(output, enterScreen)

	return editorErr, nil
}
