package telega

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/danilrwx/telega/internal/session"
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

// Run starts the terminal client and waits for it to exit.
func Run(opts Options) error {
	demo := opts.Demo

	in, out := int(os.Stdin.Fd()), int(os.Stdout.Fd())
	if !term.IsTerminal(in) || !term.IsTerminal(out) {
		return errors.New("telega needs a terminal")
	}

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

	state, err := term.MakeRaw(in)
	if err != nil {
		return err
	}
	defer func() { _ = term.Restore(in, state) }()

	_, _ = io.WriteString(os.Stdout, "\x1b[?1049h\x1b[?2004h\x1b[?1004h")
	defer func() { _, _ = io.WriteString(os.Stdout, "\x1b[?1004l\x1b[?2004l\x1b[0m\x1b[?25h\x1b[?1049l") }()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer cancel()

	resize := make(chan os.Signal, 1)

	signal.Notify(resize, syscall.SIGWINCH)
	defer signal.Stop(resize)

	keys := make(chan key)
	go readKeys(ctx, in, os.Stdin, keys)

	requests, events := make(chan request, 1), make(chan serverEvent, 24)

	a := app{demo: demo, ready: demo, focused: true}
	defer a.discardAttachment()

	notices := make(chan desktopNotice, 16)
	noticeChecks := make(chan noticeCheck, 16)
	noticeFailures := make(chan desktopNotice, 1)

	notifier, _ := exec.LookPath("notify-send")
	if !demo && notifier != "" {
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

	for {
		w, h, err := term.GetSize(out)
		if err != nil {
			return err
		}

		if err := paint(os.Stdout, &a, w, h); err != nil {
			return err
		}

		viewportOK := w >= minTerminalCols && h >= minTerminalRows
		if viewportOK {
			a.markVisibleRead(requests)
		}

		a.prefetchHistory(requests)
		a.requestMissingAuthor(requests)

		select {
		case <-ctx.Done():
			return nil
		case <-resize:
		case notice := <-noticeFailures:
			a.alertInTerminal(os.Stdout, notice)
		case e := <-events:
			if e.kind == "notify" {
				notice := e.notice
				if notifier == "" {
					a.alertInTerminal(os.Stdout, notice)
				} else {
					select {
					case notices <- notice:
					default:
						a.alertInTerminal(os.Stdout, notice)
					}
				}
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
		}

		if text := a.copyText; text != "" {
			a.copyText = ""
			if err := copyClipboard(ctx, os.Stdout, text); err != nil {
				a.status = err.Error()
			} else {
				a.status = "Copied message text"
			}
		}

		if target := a.openTarget; target != "" {
			a.openTarget = ""

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
