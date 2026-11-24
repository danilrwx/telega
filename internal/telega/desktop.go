package telega

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/danilrwx/telega/tui"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/downloader/fileref"
	"github.com/gotd/td/tg"
)

type attachment struct {
	name          string
	kind          string
	dc            int
	size          int64
	duration      int
	width, height int
	location      tg.InputFileLocationClass
	mime          string
	thumbnail     *attachment
	inlineData    []byte
}

func fileFromMedia(media tg.MessageMediaClass) *attachment {
	switch media := media.(type) {
	case *tg.MessageMediaDocument:
		doc, ok := media.Document.(*tg.Document)
		if !ok {
			return nil
		}

		file := &attachment{dc: doc.DCID, size: doc.Size, location: doc.AsInputDocumentFileLocation(""), mime: doc.MimeType}
		if media.Video || media.Round || strings.HasPrefix(doc.MimeType, "video/") {
			file.kind = "Video"
		}

		for _, attr := range doc.Attributes {
			switch attr := attr.(type) {
			case *tg.DocumentAttributeFilename:
				if attr.FileName != "" {
					file.name = fileName(attr.FileName)
				}
			case *tg.DocumentAttributeVideo:
				file.kind, file.width, file.height = "Video", attr.W, attr.H
				if attr.Duration > 0 {
					file.duration = int(min(math.Ceil(attr.Duration), math.MaxInt32))
				}
			case *tg.DocumentAttributeAudio:
				if attr.Voice {
					file.kind, file.duration = "Voice", attr.Duration
				}
			}
		}

		if file.name == "" {
			file.name = "attachment"
			if ext, _ := mime.ExtensionsByType(doc.MimeType); len(ext) > 0 {
				file.name += ext[0]
			}
		}

		if file.kind == "Video" || tui.InlineImageFile(file.name, file.mime) {
			file.thumbnail = stillThumbnail(doc.Thumbs, func(kind string) tg.InputFileLocationClass {
				return doc.AsInputDocumentFileLocation(kind)
			}, doc.DCID)
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
				thumbnail: stillThumbnail(photo.Sizes, func(kind string) tg.InputFileLocationClass {
					return photo.AsInputPhotoFileLocation(kind)
				}, photo.DCID),
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

func (file *attachment) videoDetails() string {
	var parts []string

	if file.duration > 0 {
		hours, minutes, seconds := file.duration/3600, file.duration/60%60, file.duration%60
		if hours > 0 {
			parts = append(parts, fmt.Sprintf("%d:%02d:%02d", hours, minutes, seconds))
		} else {
			parts = append(parts, fmt.Sprintf("%d:%02d", minutes, seconds))
		}
	}

	switch {
	case file.size >= 1<<30:
		parts = append(parts, fmt.Sprintf("%.1f GiB", float64(file.size)/(1<<30)))
	case file.size >= 1<<20:
		parts = append(parts, fmt.Sprintf("%.1f MiB", float64(file.size)/(1<<20)))
	case file.size >= 1<<10:
		parts = append(parts, fmt.Sprintf("%.1f KiB", float64(file.size)/(1<<10)))
	case file.size > 0:
		parts = append(parts, fmt.Sprintf("%d B", file.size))
	}

	if file.width > 0 && file.height > 0 {
		parts = append(parts, fmt.Sprintf("%d×%d", file.width, file.height))
	}

	return strings.Join(parts, " · ")
}

func privateCacheDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}

	info, err := os.Lstat(path)
	if err != nil {
		return err
	}

	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s must be a private directory", path)
	}

	return nil
}

func (file *attachment) cacheDownload(stream func(io.Writer) error) (string, error) {
	var key string

	switch location := file.location.(type) {
	case *tg.InputDocumentFileLocation:
		key = fmt.Sprintf("document-%d-%x", location.ID, location.ThumbSize)
	case *tg.InputPhotoFileLocation:
		key = fmt.Sprintf("photo-%d-%x", location.ID, location.ThumbSize)
	default:
		return "", errors.New("unsupported attachment")
	}

	return cacheAttachment(key, file.name, file.size, stream)
}

func cacheAttachment(key, name string, size int64, stream func(io.Writer) error) (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}

	root := filepath.Join(cache, "telega", "attachments")
	if err := privateCacheDir(root); err != nil {
		return "", err
	}

	dir := filepath.Join(root, key)
	if err := privateCacheDir(dir); err != nil {
		return "", err
	}

	path := filepath.Join(dir, fileName(name))
	if info, err := os.Lstat(path); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o077 == 0 &&
		(size <= 0 || info.Size() == size) {
		return path, nil
	}

	out, err := os.CreateTemp(dir, ".download-*")
	if err != nil {
		return "", err
	}

	defer func() { _ = os.Remove(out.Name()) }()

	if err := errors.Join(stream(out), out.Close()); err != nil {
		return "", err
	}

	if size > 0 {
		info, err := os.Stat(out.Name())
		if err != nil {
			return "", err
		}

		if info.Size() != size {
			return "", fmt.Errorf("incomplete attachment: got %d of %d bytes", info.Size(), size)
		}
	}

	if err := os.Rename(out.Name(), path); err != nil {
		return "", err
	}

	return path, nil
}

func downloadAttachment(ctx context.Context, client *telegram.Client, req request) (string, error) {
	file := req.attachment

	return file.cacheDownload(func(out io.Writer) error {
		invoker, err := client.FileDC(ctx, file.dc)
		if err != nil {
			return err
		}

		builder := client.DownloadVia(invoker, file.location).
			WithRefresh(fileref.Message(client.API(), req.chat.input, req.anchor, file.location))
		if file.size > 0 {
			builder.WithSize(file.size)
		}

		_, err = builder.Stream(ctx, out)

		return err
	})
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
