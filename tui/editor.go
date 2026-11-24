package tui

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ComposerLineLimit leaves the other half of the window available for messages.
func ComposerLineLimit(rows int) int {
	return max(1, rows/2-1)
}

// EditorDraft holds a temporary draft and its editor command.
type EditorDraft struct {
	Command *exec.Cmd
	path    string
}

// NewEditorDraft prepares a private Markdown file for interactive editing.
func NewEditorDraft(ctx context.Context, text string) (*EditorDraft, error) {
	file, err := os.CreateTemp("", "chat-draft-*.md")
	if err != nil {
		return nil, err
	}

	_, writeErr := file.WriteString(text)

	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		_ = os.Remove(file.Name())
		return nil, err
	}

	cmd, err := EditorFileCommand(ctx, file.Name())
	if err != nil {
		_ = os.Remove(file.Name())
		return nil, err
	}

	return &EditorDraft{Command: cmd, path: file.Name()}, nil
}

// EditorFileCommand opens the path with VISUAL, EDITOR, or Vim in that order.
func EditorFileCommand(ctx context.Context, path string) (*exec.Cmd, error) {
	spec := strings.TrimSpace(os.Getenv("VISUAL"))
	if spec == "" {
		spec = strings.TrimSpace(os.Getenv("EDITOR"))
	}

	if spec == "" {
		spec = "vim"
	}

	// An executable path can contain spaces. Otherwise split the configured options.
	args := []string{spec}

	executable, err := exec.LookPath(spec)
	if err != nil {
		args = strings.Fields(spec)

		executable, err = exec.LookPath(args[0])
		if err != nil {
			return nil, err
		}
	}

	options := args[1:]

	switch filepath.Base(executable) {
	case "vim", "nvim":
		options = append(options, "-n", "-i", "NONE", "--cmd", "set nomodeline",
			"-c", "setlocal nofixendofline | set nobackup nowritebackup", "+", "--")
	}

	return exec.CommandContext(ctx, executable, append(options, path)...), nil
}

// Finish reads the result on success and removes the temporary file.
func (d *EditorDraft) Finish(editorErr error) (string, error) {
	defer func() { _ = os.Remove(d.path) }()

	if editorErr != nil {
		return "", editorErr
	}

	content, err := os.ReadFile(d.path)

	return string(content), err
}
