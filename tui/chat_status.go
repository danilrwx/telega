package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const chatStatusRefresh = 15 * time.Second

// ChatStatus writes an optional status file for a terminal client's event loop.
// A status path should have just one publisher.
type ChatStatus struct {
	path             string
	lastPublish      time.Time
	unread, mentions int
}

// OpenChatStatus uses TUI_CHAT_STATUS_FILE as the status path, or returns nil
// when no path was configured. The parent directory must already exist.
func OpenChatStatus() *ChatStatus {
	if path := os.Getenv("TUI_CHAT_STATUS_FILE"); path != "" {
		return &ChatStatus{path: path}
	}

	return nil
}

// Publish atomically writes unread and mentions as two nonnegative integers.
// Unchanged counts are refreshed at most once per chatStatusRefresh so readers
// can distinguish idle clients from stale status. I/O errors are best effort.
func (s *ChatStatus) Publish(unread, mentions int) {
	if s == nil {
		return
	}

	unread, mentions = max(0, unread), max(0, mentions)

	now := time.Now()
	if unread == s.unread && mentions == s.mentions && now.Sub(s.lastPublish) < chatStatusRefresh {
		return
	}

	s.unread, s.mentions, s.lastPublish = unread, mentions, now

	file, err := os.CreateTemp(filepath.Dir(s.path), ".chat-status-*")
	if err != nil {
		return
	}
	defer func() { _ = os.Remove(file.Name()) }()

	_, writeErr := fmt.Fprintf(file, "%d %d\n", unread, mentions)

	closeErr := file.Close()
	if writeErr == nil && closeErr == nil {
		_ = os.Rename(file.Name(), s.path)
	}
}

// Clear removes the status file when status becomes unavailable or on exit.
func (s *ChatStatus) Clear() {
	if s == nil {
		return
	}

	s.lastPublish = time.Time{}
	_ = os.Remove(s.path)
}
