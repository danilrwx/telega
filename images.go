package main

import (
	"context"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	"golang.org/x/sys/unix"
)

type uploadImage struct {
	path, format string
	temporary    bool
	randomID     int64
}

func openImage(path string) (*os.File, string, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, "", err
	}

	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = f.Close()

		if err != nil {
			return nil, "", err
		}

		return nil, "", errors.New("choose a regular image file")
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

func stageImage(ctx context.Context, req request, scratch string) (*uploadImage, error) {
	path := strings.TrimSpace(req.text)

	temporary := req.kind != "image"
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

	f, format, err := openImage(path)
	if err != nil {
		if temporary {
			_ = os.Remove(path)
		}

		return nil, err
	}

	_ = f.Close()

	return &uploadImage{path: path, format: format, temporary: temporary, randomID: randomMessageID()}, nil
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

func (a *app) discardImage() {
	if a.staged != nil && a.staged.temporary {
		_ = os.Remove(a.staged.path)
	}

	a.staged = nil
}

func (a *app) pictureInput(k key, out chan<- request) {
	switch k.name {
	case "escape":
		a.imagePath = false
		a.clearDraft()
		a.clearReply()
	case "enter":
		if path := strings.TrimSpace(string(a.draft)); path != "" {
			a.ask(out, request{kind: "image", text: path, chat: a.chats[a.selected]})
			a.imagePath = false
		}
	default:
		a.edit(k)
	}
}

func sendImage(ctx context.Context, api *tg.Client, req request, caption string, entities []tg.MessageEntityClass) (tg.UpdatesClass, error) {
	f, format, err := openImage(req.upload.path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	if format != req.upload.format {
		return nil, errors.New("image type changed since attachment was selected")
	}

	file, err := uploader.NewUploader(api).WithThreads(1).FromFile(ctx, f)
	if err != nil {
		return nil, err
	}

	var media tg.InputMediaClass = &tg.InputMediaUploadedPhoto{File: file}
	if format == "gif" {
		media = &tg.InputMediaUploadedDocument{
			File: file, MimeType: "image/gif",
			Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: filepath.Base(req.upload.path)}},
		}
	}

	return api.MessagesSendMedia(ctx, &tg.MessagesSendMediaRequest{
		Peer: req.chat.input, Media: media, Message: caption, Entities: entities, RandomID: req.upload.randomID, ReplyTo: req.inputReply(),
	})
}
