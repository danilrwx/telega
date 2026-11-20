package main

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

	"golang.org/x/term"
)

func run(demo, qr bool) error {
	in, out := int(os.Stdin.Fd()), int(os.Stdout.Fd())
	if !term.IsTerminal(in) || !term.IsTerminal(out) {
		return errors.New("telega needs a terminal")
	}

	var cfg config

	if !demo {
		var err error

		cfg, err = loadConfig()
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
	defer a.discardImage()

	titles := make(chan string, 16)

	notifier, _ := exec.LookPath("notify-send")
	if !demo && notifier != "" {
		go notifyDesktop(ctx, notifier, titles)
	}

	if demo {
		a.chats = []chat{{key: peerKey{'u', 1}, title: "Избранное"}, {key: peerKey{'g', 2}, title: "Тихий чат", group: true}}
	} else {
		a.pending = "connect"

		done := make(chan struct{})
		go func() {
			defer close(done)

			if err := telegramLoop(ctx, cfg, qr, requests, events); err != nil && ctx.Err() == nil {
				_ = emit(ctx, events, serverEvent{kind: "offline", text: err.Error()})
			}
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

		if w >= minTerminalCols && h >= minTerminalRows {
			a.markVisibleRead(requests)
		}

		a.prefetchHistory(requests)
		a.requestMissingAuthor(requests)

		select {
		case <-ctx.Done():
			return nil
		case <-resize:
		case e := <-events:
			if title := a.notification(e); title != "" {
				if notifier == "" {
					_, _ = io.WriteString(os.Stdout, "\a")
				} else {
					select {
					case titles <- title:
					default:
					}
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
