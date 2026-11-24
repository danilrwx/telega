package telega

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

type voicePlayback struct {
	path    string
	cancel  context.CancelFunc
	done    chan error
	stderr  bytes.Buffer
	process *os.Process
	paused  bool
}

func (p *voicePlayback) hint() string {
	if p.paused {
		return "Voice paused · Alt+P resume"
	}

	return "Playing voice · Alt+P pause"
}

func (a *app) toggleVoicePause() {
	p := a.playback

	signal := syscall.SIGSTOP
	if p.paused {
		signal = syscall.SIGCONT
	}

	if err := p.process.Signal(signal); err == nil {
		p.paused = !p.paused
	} else if !errors.Is(err, os.ErrProcessDone) {
		a.status = "Voice playback: " + err.Error()
	}
}

func (a *app) playVoice(ctx context.Context, path string) {
	sameVoice := a.playback != nil && a.playback.path == path
	a.stopVoicePlayback()

	if sameVoice {
		return
	}

	ctx, cancel := context.WithCancel(ctx)
	playback := &voicePlayback{path: path, cancel: cancel, done: make(chan error, 1)}
	// Pace decoding and let the last speech samples clear the PulseAudio buffer.
	cmd := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-loglevel", "error", "-re",
		"-protocol_whitelist", "file", "-i", path, "-map", "0:a:0",
		"-af", "apad=pad_dur=0.25", "-f", "pulse", "-buffer_duration", "80",
		"-name", "Telega", "-stream_name", "Voice message", "default")
	cmd.Stderr = &playback.stderr

	if err := cmd.Start(); err != nil {
		cancel()

		a.status = "Play voice: " + err.Error()

		return
	}

	playback.process = cmd.Process
	a.playback = playback

	go func() { playback.done <- cmd.Wait() }()
}

func (a *app) stopVoicePlayback() {
	if a.playback != nil {
		a.playback.cancel()
		a.finishVoicePlayback(<-a.playback.done, true)
	}
}

func (a *app) finishVoicePlayback(err error, stopped bool) {
	playback := a.playback
	a.playback = nil

	playback.cancel()

	if err != nil && !stopped {
		detail := strings.TrimSpace(playback.stderr.String())
		if detail == "" {
			detail = err.Error()
		}

		a.status = "Play voice: " + detail
	}
}
