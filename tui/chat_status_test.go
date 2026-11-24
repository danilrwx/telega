package tui

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestChatStatusDisabled(t *testing.T) {
	t.Setenv("TUI_CHAT_STATUS_FILE", "")

	status := OpenChatStatus()
	if status != nil {
		t.Fatal("expected no publisher without a configured path")
	}

	status.Publish(1, 2)
	status.Clear()
}

func TestChatStatusPublishesAndRefreshes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "status")
	t.Setenv("TUI_CHAT_STATUS_FILE", path)

	status := OpenChatStatus()
	checkLine := func(want string) {
		t.Helper()

		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("status = %q, %v; want %q", got, err, want)
		}
	}
	mtime := func() time.Time {
		t.Helper()

		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}

		return info.ModTime()
	}

	status.Publish(-1, -2)
	checkLine("0 0\n")

	original, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = original.Close() }()

	status.Publish(2, 3)
	checkLine("2 3\n")

	oldLine, err := io.ReadAll(original)
	if err != nil || string(oldLine) != "0 0\n" {
		t.Fatalf("publication did not replace file: old descriptor contains %q, %v", oldLine, err)
	}

	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}

	before := mtime()

	status.Publish(2, 3)

	if !mtime().Equal(before) {
		t.Fatal("unchanged counts were written again immediately")
	}

	status.lastPublish = time.Now().Add(-chatStatusRefresh)
	status.Publish(2, 3)
	checkLine("2 3\n")

	if !mtime().After(before) {
		t.Fatal("unchanged counts did not refresh mtime")
	}

	status.Clear()
	status.Clear()

	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stat after clear: %v", err)
	}

	status.Publish(2, 3)
	checkLine("2 3\n")
}
