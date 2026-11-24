package telega

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gotd/td/tg"
)

func TestAttachmentCacheReusesOnlyCompleteDownload(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)

	location := &tg.InputDocumentFileLocation{ID: 42, FileReference: []byte("first")}
	file := attachment{name: "clip.mp4", size: 5, location: location}
	path := filepath.Join(cache, "telega", "attachments", "document-42-", "clip.mp4")

	broken := errors.New("connection lost")
	if _, err := file.cacheDownload(func(w io.Writer) error {
		if _, err := io.WriteString(w, "ab"); err != nil {
			return err
		}

		return broken
	}); !errors.Is(err, broken) {
		t.Fatalf("expected download failure, got %v", err)
	}

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed download left cache entries: %v, %v", entries, err)
	}

	stream := func(w io.Writer) error {
		_, err := io.WriteString(w, "abcde")
		return err
	}

	if got, err := file.cacheDownload(stream); err != nil || got != path {
		t.Fatalf("download path = %q, error = %v", got, err)
	}

	// A fresh location for the same media can have a different file reference.
	fresh := attachment{name: file.name, size: file.size, location: &tg.InputDocumentFileLocation{ID: 42, FileReference: []byte("refreshed")}}

	got, err := downloadAttachment(t.Context(), nil, request{attachment: &fresh})
	if err != nil || got != path {
		t.Fatalf("fresh request did not reuse the cached file: %q, %v", got, err)
	}

	if err := os.Truncate(path, 2); err != nil {
		t.Fatal(err)
	}

	if _, err := file.cacheDownload(stream); err != nil {
		t.Fatal(err)
	}

	if content, err := os.ReadFile(path); err != nil || string(content) != "abcde" {
		t.Fatalf("cache was not repaired: %q, %v", content, err)
	}
}

func TestVideoLabelShowsMetadataOnNarrowScreen(t *testing.T) {
	doc := &tg.Document{
		MimeType: "video/mp4", Size: 12 << 20,
		Attributes: []tg.DocumentAttributeClass{
			&tg.DocumentAttributeFilename{FileName: "A particularly long video filename.mp4"},
			&tg.DocumentAttributeVideo{Duration: 83, W: 1920, H: 1080},
		},
	}
	msg := message{text: "[Attachment]", attachment: fileFromMedia(&tg.MessageMediaDocument{Document: doc})}
	line := formattedMessage(msg, 32, 1, false, &standardPalette)[0]

	if !strings.Contains(line, "[Video] 1:23 · 12.0 MiB") {
		t.Fatalf("narrow screen lost duration or size: %q", line)
	}

	line = formattedMessage(msg, 90, 1, false, &standardPalette)[0]
	if !strings.Contains(line, "A particularly long video filename.mp4 · 1:23 · 12.0 MiB · 1920×1080") {
		t.Fatalf("wide screen lost filename or metadata: %q", line)
	}
}
