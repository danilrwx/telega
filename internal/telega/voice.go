package telega

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/danilrwx/telega/tui"
)

type voiceRecording struct {
	cancel            context.CancelFunc
	stdin             io.WriteCloser
	path              string
	done              chan error
	progress, stderr  bytes.Buffer
	stopping, discard bool
}

func startVoiceRecording(ctx context.Context, scratch string, input []string) (*voiceRecording, error) {
	file, err := os.CreateTemp(scratch, "voice-*.ogg")
	if err != nil {
		return nil, err
	}

	path := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(path)

		return nil, err
	}

	ctx, cancel := context.WithCancel(ctx)
	recording := &voiceRecording{path: path, cancel: cancel, done: make(chan error, 1)}

	args := append([]string{"-y", "-loglevel", "error", "-nostats", "-progress", "pipe:1"}, input...)
	args = append(args, "-t", "600", "-vn", "-ac", "1", "-ar", "48000", "-c:a", "libopus", "-application", "voip", "-b:a", "32k", path)
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.Stdout, cmd.Stderr = &recording.progress, &recording.stderr

	recording.stdin, err = cmd.StdinPipe()
	if err == nil {
		err = cmd.Start()
	}

	if err != nil {
		cancel()

		_ = os.Remove(path)

		return nil, err
	}

	go func() { recording.done <- cmd.Wait() }()

	return recording, nil
}

func (r *voiceRecording) stop() {
	if !r.stopping && !r.discard {
		r.stopping = true
		_, _ = io.WriteString(r.stdin, "q\n")
	}
}

func (a *app) finishVoiceRecording(runErr error) {
	recording := a.voice
	a.voice = nil

	recording.cancel()
	defer func() { _ = os.Remove(recording.path) }()

	if recording.discard {
		if a.open {
			a.status = "Voice recording discarded"
		}

		return
	}

	if runErr != nil {
		a.status = "Record voice: " + strings.TrimSpace(recording.stderr.String())
		if recording.stderr.Len() == 0 {
			a.status = "Record voice: " + runErr.Error()
		}

		return
	}

	upload, err := recording.stage()
	if err != nil {
		a.status = "Record voice: " + err.Error()

		return
	}

	a.retrySend = nil
	a.addStaged(upload, false)
	a.status = "Voice recording added to draft"
}

func (r *voiceRecording) stage() (*uploadAttachment, error) {
	var microseconds int64

	for line := range strings.Lines(r.progress.String()) {
		if value, ok := strings.CutPrefix(line, "out_time_us="); ok {
			if timestamp, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil {
				microseconds = max(microseconds, timestamp)
			}
		}
	}

	if microseconds <= 0 {
		return nil, errors.New("no audio recorded")
	}

	source, err := openRegularFile(r.path)
	if err != nil {
		return nil, err
	}

	defer func() { _ = source.Close() }()

	path, err := tui.CopyDraftAttachment("telega", "voice.ogg", source)
	if err != nil {
		return nil, err
	}

	upload, err := localUpload(path, true)
	if err != nil {
		tui.RemoveDraftAttachment("telega", path)

		return nil, err
	}

	upload.managed = true
	upload.voiceSeconds = int((microseconds + time.Second.Microseconds() - 1) / time.Second.Microseconds())

	return upload, nil
}
