package tui

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestEditorCommandPriorityAndFilename(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)

	for _, name := range []string{"vim", "visual editor", "editor"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	file := filepath.Join(dir, "my notes.md")
	t.Setenv("VISUAL", filepath.Join(dir, "visual editor"))
	t.Setenv("EDITOR", "editor --wait")

	cmd, err := EditorFileCommand(t.Context(), file)
	if err != nil || !slices.Equal(cmd.Args, []string{filepath.Join(dir, "visual editor"), file}) {
		t.Fatalf("VISUAL should keep its path and the document intact: %v %v", cmd, err)
	}

	t.Setenv("VISUAL", " ")

	cmd, err = EditorFileCommand(t.Context(), file)
	if err != nil || !slices.Equal(cmd.Args, []string{filepath.Join(dir, "editor"), "--wait", file}) {
		t.Fatalf("EDITOR should accept terminal editor options: %v %v", cmd, err)
	}

	t.Setenv("EDITOR", "")

	cmd, err = EditorFileCommand(t.Context(), file)
	if err != nil || cmd.Path != filepath.Join(dir, "vim") || !slices.Contains(cmd.Args, "set nomodeline") || cmd.Args[len(cmd.Args)-1] != file {
		t.Fatalf("Vim should remain the fallback with its draft options: %v %v", cmd, err)
	}
}
