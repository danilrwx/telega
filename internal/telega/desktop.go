package telega

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/downloader/fileref"
	"github.com/gotd/td/tg"
)

type attachment struct {
	name     string
	kind     string
	dc       int
	size     int64
	location tg.InputFileLocationClass
}

func fileFromMedia(media tg.MessageMediaClass) *attachment {
	switch media := media.(type) {
	case *tg.MessageMediaDocument:
		doc, ok := media.Document.(*tg.Document)
		if !ok {
			return nil
		}

		file := &attachment{dc: doc.DCID, size: doc.Size, location: doc.AsInputDocumentFileLocation("")}
		if strings.HasPrefix(doc.MimeType, "video/") {
			file.kind = "Video"
		}

		for _, attr := range doc.Attributes {
			if name, ok := attr.(*tg.DocumentAttributeFilename); ok && name.FileName != "" {
				file.name = fileName(name.FileName)
				return file
			}
		}

		file.name = "attachment"
		if ext, _ := mime.ExtensionsByType(doc.MimeType); len(ext) > 0 {
			file.name += ext[0]
		}

		return file
	case *tg.MessageMediaPhoto:
		photo, ok := media.Photo.(*tg.Photo)
		if !ok {
			return nil
		}

		var (
			kind string
			size int
		)

		for _, thumb := range photo.Sizes {
			switch thumb := thumb.(type) {
			case *tg.PhotoSize:
				if thumb.Size > size {
					kind, size = thumb.Type, thumb.Size
				}
			case *tg.PhotoSizeProgressive:
				if n := len(thumb.Sizes); n > 0 && thumb.Sizes[n-1] > size {
					kind, size = thumb.Type, thumb.Sizes[n-1]
				}
			}
		}

		if kind != "" {
			return &attachment{
				name: "photo.jpg", kind: "Photo", dc: photo.DCID, size: int64(size),
				location: photo.AsInputPhotoFileLocation(kind),
			}
		}
	}

	return nil
}

func fileName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return '_'
		}

		return r
	}, name)

	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || name == "/" || len(name) > 200 {
		return "attachment"
	}

	return name
}

func downloadAttachment(ctx context.Context, client *telegram.Client, req request) (string, error) {
	file := req.attachment

	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}

	cache = filepath.Join(cache, "telega", "attachments")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		return "", err
	}

	info, err := os.Lstat(cache)
	if err != nil {
		return "", err
	}

	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("%s must be a private directory", cache)
	}

	dir, err := os.MkdirTemp(cache, "file-")
	if err != nil {
		return "", err
	}

	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(dir)
		}
	}()

	path := filepath.Join(dir, file.name)

	out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}

	invoker, err := client.FileDC(ctx, file.dc)
	if err != nil {
		_ = out.Close()
		return "", err
	}

	builder := client.DownloadVia(invoker, file.location).
		WithRefresh(fileref.Message(client.API(), req.chat.input, req.anchor, file.location))
	if file.size > 0 {
		builder.WithSize(file.size)
	}

	_, err = builder.Stream(ctx, out)
	if err := errors.Join(err, out.Close()); err != nil {
		return "", err
	}

	keep = true

	return path, nil
}

func openAttachment(path string) (*exec.Cmd, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("attachment path is not absolute")
	}

	return openSystem(path)
}

func openSystem(target string) (*exec.Cmd, error) {
	opener, err := exec.LookPath("open")
	if err != nil {
		opener, err = exec.LookPath("xdg-open")
	}

	if err != nil {
		return nil, errors.New("system opener unavailable")
	}

	cmd := exec.Command(opener, target)

	return cmd, cmd.Start()
}

func copyClipboard(ctx context.Context, out io.Writer, text string) error {
	for _, tool := range []struct {
		display, name string
		args          []string
	}{
		{"WAYLAND_DISPLAY", "wl-copy", []string{"--type", "text/plain;charset=utf-8"}},
		{"DISPLAY", "xclip", []string{"-in", "-selection", "clipboard"}},
	} {
		if os.Getenv(tool.display) == "" {
			continue
		}

		deadline, cancel := context.WithTimeout(ctx, time.Second)
		cmd := exec.CommandContext(deadline, tool.name, tool.args...)
		cmd.Stdin = strings.NewReader(text)
		err := cmd.Run()

		cancel()

		if err == nil {
			return nil
		}
	}
	// ESC and other control characters in message text stay inside base64.
	_, err := fmt.Fprintf(out, "\x1b]52;c;%s\x07", base64.StdEncoding.EncodeToString([]byte(text)))

	return err
}
