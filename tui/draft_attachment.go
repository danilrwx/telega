package tui

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

func attachmentDraftDir(app string) (string, error) {
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}

		state = filepath.Join(home, ".local", "state")
	}

	return filepath.Abs(filepath.Join(state, app, "draft-attachments"))
}

func CopyDraftAttachment(app, name string, content io.Reader) (string, error) {
	root, err := attachmentDraftDir(app)
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}

	info, err := os.Lstat(root)
	if err != nil {
		return "", err
	}

	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return "", errors.New("attachment draft directory must be private")
	}

	dir, err := os.MkdirTemp(root, "draft-")
	if err != nil {
		return "", err
	}

	file := filepath.Join(dir, AttachmentName(name))

	out, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		_ = os.Remove(dir)
		return "", err
	}

	_, copyErr := io.Copy(out, content)
	if err := errors.Join(copyErr, out.Close()); err != nil {
		RemoveDraftAttachment(app, file)
		return "", err
	}

	return file, nil
}

func RemoveDraftAttachment(app, file string) {
	root, err := attachmentDraftDir(app)

	dir := filepath.Dir(file)
	if err != nil || !filepath.IsAbs(file) || filepath.Clean(file) != file || filepath.Dir(dir) != root ||
		!strings.HasPrefix(filepath.Base(dir), "draft-") {
		return
	}

	for _, path := range []string{root, dir} {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() {
			return
		}
	}

	_ = os.Remove(file)
	_ = os.Remove(dir)
}

func AttachmentName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))

	name = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}

		return r
	}, name))
	if name == "." || name == ".." || name == "/" || name == "" {
		return "attachment"
	}

	return name
}

func DraftAttachmentListHint(count int) string {
	if count == 0 {
		return "a attach  Esc back"
	}

	return "e edit  a attach  d remove  " + OpenAttachmentHint
}

func AttachmentListAction(name string, index, count int) (action string, next int) {
	switch name {
	case "esc", "escape", "h", "left":
		return "close", index
	case "up", "k":
		index = max(0, index-1)
	case "down", "j":
		index = min(max(0, count-1), index+1)
	case "a":
		return "attach", index
	case "enter", "l":
		if count > 0 {
			return "open", index
		}
	case "e":
		if count > 0 {
			return "edit", index
		}
	case "d":
		if count > 0 {
			return "remove", index
		}
	}

	return "", index
}
