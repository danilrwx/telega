package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestWrapTerminalText(t *testing.T) {
	got := wrap("界я\n\x1babc", 3, 3)
	if !slices.Equal(got, []string{"界я", " ab", "c"}) {
		t.Fatalf("unexpected rows: %q", got)
	}
}

func TestHistoryKeepsLiveEdit(t *testing.T) {
	var a app
	a.insert(message{id: 3, text: "live"}, true)
	a.insert(message{id: 3, text: "stale"}, false)
	a.insert(message{id: 2, text: "older"}, false)

	if len(a.log) != 2 || a.log[0].text != "live" || a.log[1].id != 2 {
		t.Fatalf("unexpected history: %+v", a.log)
	}

	a.draft = []rune("draft")
	a.pending = "send"
	a.receive(nil, serverEvent{kind: "error", text: "offline"})

	if !a.composing || a.pending != "" || string(a.draft) != "draft" {
		t.Fatal("send error did not restore the draft")
	}
}

func TestPrivateSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mtproto.session")

	s := privateFile{path: path}
	for _, want := range []string{"first", "second"} {
		if err := s.store([]byte(want)); err != nil {
			t.Fatal(err)
		}

		got, err := s.load()
		if err != nil || string(got) != want {
			t.Fatalf("session contents %q, error %v", got, err)
		}
	}

	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := s.load(); err == nil {
		t.Fatal("accepted a session readable by other users")
	}
}

func TestSavedAPIConfig(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_DATA_HOME", base)

	for _, key := range []string{"TELEGA_API_ID", "TELEGA_API_HASH"} {
		t.Setenv(key, "")

		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}

	dir := filepath.Join(base, "telega")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	file := privateFile{filepath.Join(dir, "api")}

	hash := "0123456789abcdef0123456789abcdef"
	if err := file.store([]byte("123\n" + hash + "\n")); err != nil {
		t.Fatal(err)
	}

	got, err := loadConfig()
	if err != nil || got.id != 123 || got.hash != hash {
		t.Fatal("failed to load saved Telegram API credentials")
	}

	t.Setenv("TELEGA_API_ID", "456")
	t.Setenv("TELEGA_API_HASH", hash)

	got, err = loadConfig()
	if err != nil || got.id != 456 {
		t.Fatal("environment must override saved Telegram API credentials")
	}
}
