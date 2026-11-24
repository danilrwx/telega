package telega

import (
	"context"
	"errors"
	"image"
	"io"
	"mime"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/danilrwx/telega/tui"

	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	"golang.org/x/sys/unix"
)

type uploadAttachment struct {
	path, format       string
	temporary, managed bool
	changed            bool
	original           os.FileInfo
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
	path, err := screenshot(ctx, scratch, req.kind == "screenshot")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.Remove(path) }()

	source, err := openRegularFile(path)
	if err != nil {
		return nil, err
	}

	defer func() { _ = source.Close() }()

	copyPath, err := tui.CopyDraftAttachment("telega", "Screenshot.png", source)
	if err != nil {
		return nil, err
	}

	file, format, err := openImage(copyPath)
	if err != nil {
		tui.RemoveDraftAttachment("telega", copyPath)
		return nil, err
	}

	info, err := file.Stat()
	_ = file.Close()

	if err != nil {
		tui.RemoveDraftAttachment("telega", copyPath)
		return nil, err
	}

	return &uploadAttachment{path: copyPath, format: format, temporary: true, managed: true, original: info}, nil
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
		commands = tui.ClipboardCommands(ctx, "image/png")

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

func (f *uploadAttachment) discard() {
	if f.managed {
		tui.RemoveDraftAttachment("telega", f.path)
	} else if f.temporary {
		_ = os.Remove(f.path)
	}
}

func (a *app) discardAttachment() {
	if a.staged != nil {
		a.staged.discard()
	}

	for _, item := range a.moreUploads {
		item.upload.discard()
	}

	a.staged, a.moreUploads = nil, nil
}

func localUpload(path string, forceFile bool) (*uploadAttachment, error) {
	file, err := openRegularFile(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return nil, err
	}

	if info.Size() == 0 {
		return nil, errors.New("choose a non-empty regular file")
	}

	upload := &uploadAttachment{path: path, original: info}
	if !forceFile {
		_, upload.format, _ = image.DecodeConfig(file)
	}

	return upload, nil
}

func sendAttachment(ctx context.Context, api *tg.Client, req request, caption string, entities []tg.MessageEntityClass) (tg.UpdatesClass, error) {
	upload := req.upload
	if upload.changed {
		return nil, errors.New("attachment changed after selection")
	}

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
