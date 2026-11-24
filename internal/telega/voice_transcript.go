package telega

import (
	"context"
	"strings"
	"time"

	"github.com/danilrwx/telega/tui"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

type voiceTranscript struct {
	chat              chat
	messageID         int
	id                int64
	text, status      string
	loading, pending  bool
	offset, page, end int
}

func (a *app) beginVoiceTranscript(out chan<- request, scope chat, msg message) {
	if a.demo || msg.attachment == nil || msg.attachment.kind != "Voice" || scope.input == nil {
		a.status = "Select a Telegram voice message"

		return
	}

	pos := chatPosition{chat: scope.key, id: msg.id}
	entry := a.voiceTranscripts[pos]

	if entry == nil {
		if a.voiceTranscripts == nil || len(a.voiceTranscripts) >= 32 {
			a.voiceTranscripts = make(map[chatPosition]*voiceTranscript)
		}

		entry = &voiceTranscript{chat: scope, messageID: msg.id}
		a.voiceTranscripts[pos] = entry
	}

	entry.offset = 0
	a.transcript, a.status = entry, ""

	if entry.id == 0 && !entry.loading {
		entry.request(out)
	}
}

func (t *voiceTranscript) request(out chan<- request) {
	t.loading, t.status = true, ""

	out <- request{kind: "voiceTranscript", chat: t.chat, anchor: t.messageID, transcript: t}
}

func fetchVoiceTranscript(ctx context.Context, api *tg.Client, req request, e serverEvent) serverEvent {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	e.transcript = req.transcript
	result, err := api.MessagesTranscribeAudio(ctx, &tg.MessagesTranscribeAudioRequest{Peer: req.chat.input, MsgID: req.anchor})

	switch {
	case tgerr.Is(err, "PREMIUM_ACCOUNT_REQUIRED"):
		e.hint = "Telegram Premium is required for this transcription"
	case tgerr.Is(err, "MSG_VOICE_TOO_LONG"):
		e.hint = "This voice message exceeds Telegram's transcription limit"
	case err != nil:
		e.hint = err.Error()
	default:
		e.transcriptID, e.transcriptPending, e.text = result.TranscriptionID, result.Pending, result.Text
	}

	return e
}

func (a *app) receiveVoiceTranscript(e serverEvent) {
	t := a.voiceTranscripts[chatPosition{chat: e.chat, id: e.anchor}]
	if t == nil || e.transcript != nil && e.transcript != t {
		return
	}

	if e.transcript != nil {
		t.loading = false
	}

	if e.hint != "" {
		if t.id == 0 || t.pending {
			t.status = e.hint
		}

		return
	}

	// Push updates may arrive before the reply to the original request.
	if t.id != 0 && (t.id != e.transcriptID || e.transcriptPending && (!t.pending || e.transcript != nil)) {
		return
	}

	t.id, t.text, t.pending, t.status = e.transcriptID, e.text, e.transcriptPending, ""
}

func (a *app) voiceTranscriptInput(k key, out chan<- request) {
	t := a.transcript

	name := k.name
	if name == "" {
		name = k.text
	}

	switch name {
	case "escape", "alt+r":
		a.transcript = nil
	case "up", "k":
		t.offset--
	case "down", "j":
		t.offset++
	case "pageup":
		t.offset -= t.page
	case "pagedown":
		t.offset += t.page
	case "home", "g":
		t.offset = 0
	case "end", "G":
		t.offset = t.end
	case "R":
		if !t.loading && (t.pending || t.status != "") {
			t.request(out)
		}
	}

	t.offset = min(t.end, max(0, t.offset))
}

func (t *voiceTranscript) render(width, height int, row func(string, bool, bool, bool)) {
	status := t.status
	hint := "Esc back · jk scroll"

	switch {
	case t.loading || t.pending && status == "":
		status = "Transcribing…"
	case status == "" && strings.TrimSpace(t.text) == "":
		status = "Telegram returned no transcript"
	}

	if !t.loading && (t.pending || t.status != "") {
		hint += " · R retry"
	}

	room := height - 2
	if status != "" {
		room--
	}

	lines := tui.Wrap(strings.TrimSpace(t.text), width, 0)
	t.page = max(1, room)
	t.end = max(0, len(lines)-t.page)
	t.offset = min(t.offset, t.end)

	row("Voice transcript", true, false, false)

	for _, line := range lines[t.offset:min(len(lines), t.offset+t.page)] {
		row(line, false, false, false)
	}

	if status != "" {
		row(tui.Clip(status, width), false, false, true)
	}

	row(tui.Clip(hint, width), false, false, true)
}
