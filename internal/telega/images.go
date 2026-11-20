package telega

import (
	"context"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	"golang.org/x/sys/unix"
)

type uploadAttachment struct {
	path, format string
	temporary    bool
	original     os.FileInfo
}

func openRegularFile(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}

	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = f.Close()

		if err != nil {
			return nil, err
		}

		return nil, errors.New("choose a regular file")
	}

	return f, nil
}

func openImage(path string) (*os.File, string, error) {
	f, err := openRegularFile(path)
	if err != nil {
		return nil, "", err
	}

	_, format, err := image.DecodeConfig(f)
	if err != nil {
		_ = f.Close()
		return nil, "", errors.New("choose a JPEG, PNG or GIF image")
	}

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		_ = f.Close()
		return nil, "", err
	}

	return f, format, nil
}

func stageAttachment(ctx context.Context, req request, scratch string) (*uploadAttachment, error) {
	path := strings.TrimSpace(req.text)

	temporary := req.kind != "image" && req.kind != "file"
	if temporary {
		var err error

		path, err = screenshot(ctx, scratch, req.kind == "screenshot")
		if err != nil {
			return nil, err
		}
	} else if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}

		path = filepath.Join(home, path[2:])
	}

	var (
		f      *os.File
		err    error
		format string
	)
	if req.kind == "file" {
		f, err = openRegularFile(path)
	} else {
		f, format, err = openImage(path)
	}

	if err != nil {
		if temporary {
			_ = os.Remove(path)
		}

		return nil, err
	}

	original, err := f.Stat()
	_ = f.Close()

	if err != nil {
		if temporary {
			_ = os.Remove(path)
		}

		return nil, err
	}

	return &uploadAttachment{path: path, format: format, temporary: temporary, original: original}, nil
}

func screenshot(ctx context.Context, scratch string, region bool) (string, error) {
	wait := 4 * time.Second
	if region {
		wait = time.Minute
	}

	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()

	var commands []*exec.Cmd

	if region {
		if os.Getenv("WAYLAND_DISPLAY") == "" {
			return "", errors.New("region capture needs Wayland; use s for a clipboard image")
		}

		geometry, err := exec.CommandContext(ctx, "slurp").Output()
		if err != nil || strings.TrimSpace(string(geometry)) == "" {
			return "", errors.New("could not select a region with slurp")
		}

		commands = append(commands, exec.CommandContext(ctx, "grim", "-g", strings.TrimSpace(string(geometry)), "-t", "png", "-"))
	} else {
		if os.Getenv("WAYLAND_DISPLAY") != "" {
			commands = append(commands, exec.CommandContext(ctx, "wl-paste", "--no-newline", "--type", "image/png"))
		}

		if os.Getenv("DISPLAY") != "" {
			commands = append(commands, exec.CommandContext(ctx, "xclip", "-selection", "clipboard", "-t", "image/png", "-o"))
		}

		if len(commands) == 0 {
			return "", errors.New("no desktop clipboard; use a to attach an image file")
		}
	}

	for _, cmd := range commands {
		f, err := os.CreateTemp(scratch, "screenshot-*.png")
		if err != nil {
			return "", err
		}

		cmd.Stdout = f
		runErr := cmd.Run()
		info, statErr := f.Stat()

		closeErr := f.Close()
		if runErr == nil && statErr == nil && closeErr == nil && info.Size() > 0 {
			return f.Name(), nil
		}

		_ = os.Remove(f.Name())
	}

	if region {
		return "", errors.New("could not capture the selected region with grim")
	}

	return "", errors.New("no PNG image in clipboard")
}

func (a *app) discardAttachment() {
	if a.staged != nil && a.staged.temporary {
		_ = os.Remove(a.staged.path)
	}

	a.staged = nil
}

func (a *app) attachmentInput(k key, out chan<- request) {
	switch k.name {
	case "escape":
		a.imagePath = false
		a.clearDraft()
		a.clearReply()
	case "enter":
		if path := strings.TrimSpace(string(a.draft)); path != "" {
			kind := "image"
			if a.filePath {
				kind = "file"
			}

			a.ask(out, request{kind: kind, text: path, chat: a.currentChat()})
			a.imagePath = false
		}
	default:
		a.edit(k)
	}
}

func sendAttachment(ctx context.Context, api *tg.Client, req request, caption string, entities []tg.MessageEntityClass) (tg.UpdatesClass, error) {
	upload := req.upload
	document := req.asFile || upload.format == "" || upload.format == "gif"

	var (
		f   *os.File
		err error
	)

	if document {
		f, err = openRegularFile(upload.path)
	} else {
		var format string

		f, format, err = openImage(upload.path)
		if err == nil && format != upload.format {
			_ = f.Close()
			return nil, errors.New("image type changed since attachment was selected")
		}
	}

	if err != nil {
		return nil, err
	}

	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}

	if upload.original != nil && (!os.SameFile(info, upload.original) || info.Size() != upload.original.Size() ||
		!info.ModTime().Equal(upload.original.ModTime())) {
		return nil, errors.New("attachment changed after selection")
	}

	file, err := uploader.NewUploader(api).WithThreads(1).FromFile(ctx, f)
	if err != nil {
		return nil, err
	}

	var media tg.InputMediaClass = &tg.InputMediaUploadedPhoto{File: file}

	if document {
		name := filepath.Base(upload.path)
		if upload.temporary {
			name = "Screenshot.png"
		}

		contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))
		if parsed, _, parseErr := mime.ParseMediaType(contentType); parseErr == nil {
			contentType = parsed
		} else {
			contentType = "application/octet-stream"
		}

		media = &tg.InputMediaUploadedDocument{
			File: file, MimeType: contentType, ForceFile: true,
			Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: name}},
		}
	}

	return api.MessagesSendMedia(ctx, &tg.MessagesSendMediaRequest{
		Peer: req.chat.input, Media: media, Message: caption, Entities: entities, RandomID: req.randomID, ReplyTo: req.inputReply(),
	})
}
