package tui

import (
	"context"
	"errors"
	"os"
	"os/exec"
)

// ComposerLineLimit leaves the other half of the window available for messages.
func ComposerLineLimit(rows int) int {
	return max(1, rows/2-1)
}

// VimDraft holds a temporary draft and its editor command.
type VimDraft struct {
	Command *exec.Cmd
	path    string
}

// NewVimDraft prepares a private Markdown file for interactive editing.
func NewVimDraft(ctx context.Context, text string) (*VimDraft, error) {
	vim, err := exec.LookPath("vim")
	if err != nil {
		return nil, err
	}

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

	return &VimDraft{Command: vimFileCommand(ctx, vim, file.Name()), path: file.Name()}, nil
}

// VimFileCommand opens a local file in Vim without swap, backup or history files.
func VimFileCommand(ctx context.Context, path string) (*exec.Cmd, error) {
	vim, err := exec.LookPath("vim")
	if err != nil {
		return nil, err
	}

	return vimFileCommand(ctx, vim, path), nil
}

func vimFileCommand(ctx context.Context, vim, path string) *exec.Cmd {
	return exec.CommandContext(ctx, vim, "-n", "-i", "NONE", "--cmd", "set nomodeline",
		"-c", "setlocal nofixendofline | set nobackup nowritebackup", "+", "--", path)
}

// Finish reads the result on success and removes the temporary file.
func (d *VimDraft) Finish(editorErr error) (string, error) {
	defer func() { _ = os.Remove(d.path) }()

	if editorErr != nil {
		return "", editorErr
	}

	content, err := os.ReadFile(d.path)

	return string(content), err
}
