package telega

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
)

func TestVoiceDraftSendsAsTelegramVoice(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	recorder := voiceRecording{path: filepath.Join(t.TempDir(), "capture.ogg")}
	if err := os.WriteFile(recorder.path, []byte("synthetic voice payload"), 0o600); err != nil {
		t.Fatal(err)
	}

	recorder.progress.WriteString("out_time_us=N/A\nout_time_us=1200001\nprogress=end\n")

	staged, err := recorder.stage()
	if err != nil {
		t.Fatal(err)
	}

	defer staged.discard()

	upload := savedAttachment(staged, false).upload()
	if upload.voiceSeconds != 2 || !upload.managed || upload.changed {
		t.Fatalf("voice metadata did not survive draft restoration: %+v", upload)
	}

	api := tg.NewClient(invokeFunc(func(in bin.Encoder, out bin.Decoder) error {
		switch query := in.(type) {
		case *tg.UploadSaveFilePartRequest:
			if string(query.Bytes) != "synthetic voice payload" {
				t.Fatal("upload changed the recording")
			}

			out.(*tg.BoolBox).Bool = &tg.BoolTrue{}
		case *tg.MessagesSendMediaRequest:
			doc, ok := query.Media.(*tg.InputMediaUploadedDocument)
			if !ok || doc.ForceFile || doc.MimeType != "audio/ogg" || len(doc.Attributes) != 1 {
				t.Fatalf("expected a voice media upload: %+v", query.Media)
			}

			audio, ok := doc.Attributes[0].(*tg.DocumentAttributeAudio)
			if !ok || !audio.Voice || audio.Duration != 2 {
				t.Fatalf("wrong Telegram voice attributes: %+v", doc.Attributes[0])
			}

			out.(*tg.UpdatesBox).Updates = &tg.UpdateShortSentMessage{ID: 10}
		default:
			t.Fatalf("unexpected request %T", in)
		}

		return nil
	}))

	req := request{chat: chat{input: &tg.InputPeerSelf{}}, upload: upload, randomID: 1}
	if _, err := sendAttachment(t.Context(), api, req, "", nil); err != nil {
		t.Fatal(err)
	}
}

func TestVoiceAttachmentsUsePlaybackTarget(t *testing.T) {
	a := app{
		ready: true, open: true, composing: true,
		staged: &uploadAttachment{path: "/draft/voice.ogg", voiceSeconds: 2},
	}
	a.showDraftAttachments()
	a.input(key{name: "enter"}, nil)

	if a.playTarget != a.staged.path || a.openTarget != "" {
		t.Fatal("draft voice should go to the internal player")
	}

	a.receive(nil, serverEvent{kind: "download", text: "/cache/voice.ogg", playVoice: true})

	if a.playTarget != "/cache/voice.ogg" || a.openTarget != "" {
		t.Fatal("downloaded voice should go to the internal player")
	}

	a.receive(nil, serverEvent{kind: "download", text: "/cache/music.ogg"})

	if a.openTarget != "/cache/music.ogg" {
		t.Fatal("ordinary audio files should use the normal attachment opener")
	}
}

func TestVoiceTranscriptKeepsFinalUpdateAndReusesResult(t *testing.T) {
	c := chat{key: peerKey{'c', 42}, input: &tg.InputPeerChannel{ChannelID: 42}}
	msg := message{id: 8, attachment: &attachment{kind: "Voice"}}
	a := app{ready: true, open: true, chats: []chat{c}, log: []message{msg}}
	out := make(chan request, 1)

	a.input(key{name: "alt+r"}, out)

	req := <-out
	api := tg.NewClient(invokeFunc(func(in bin.Encoder, out bin.Decoder) error {
		query := in.(*tg.MessagesTranscribeAudioRequest)
		if query.Peer != c.input || query.MsgID != msg.id {
			t.Fatalf("wrong voice message for transcription: %+v", query)
		}

		result := out.(*tg.MessagesTranscribedAudio)
		result.SetPending(true)
		result.TranscriptionID, result.Text = 19, "partial"

		return nil
	}))

	reply := fetchVoiceTranscript(t.Context(), api, req, serverEvent{kind: req.kind, chat: c.key, anchor: msg.id})
	a.receive(out, serverEvent{kind: req.kind, chat: c.key, anchor: msg.id, transcriptID: 19, text: "Final text"})
	a.receive(out, reply)

	var frame bytes.Buffer
	if err := paint(&frame, &a, 100, 24); err != nil || !strings.Contains(frame.String(), "Final text") || strings.Contains(frame.String(), "partial") {
		t.Fatalf("the popup should show the completed transcript: %v", err)
	}

	a.input(key{name: "escape"}, out)

	frame.Reset()

	if err := paint(&frame, &a, 100, 24); err != nil {
		t.Fatal(err)
	}

	voice := strings.Index(frame.String(), "[Voice]")
	transcript := strings.Index(frame.String(), "↳ Final text")

	if voice < 0 || transcript <= voice || strings.Contains(frame.String(), "Voice transcript") {
		t.Fatal("the completed transcript should appear under the voice message in the chat")
	}

	a.input(key{name: "alt+r"}, out)

	if len(out) != 0 || a.transcript == nil || a.transcript.pending || a.transcript.text != "Final text" {
		t.Fatal("reopening an already transcribed message should reuse the final result")
	}
}
