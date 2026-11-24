package tui

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// DraftStore stores local drafts in private files, one per conversation.
type DraftStore struct {
	dir string
}

// OpenDraftStore opens the app's draft directory under XDG_STATE_HOME.
func OpenDraftStore(app string) (*DraftStore, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}

		base = filepath.Join(home, ".local", "state")
	}

	dir := filepath.Join(base, app, "drafts")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}

	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}

	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("draft directory must be private: %s", dir)
	}

	return &DraftStore{dir: dir}, nil
}

func (s *DraftStore) path(key string) string {
	hash := sha256.Sum256([]byte(key))
	return filepath.Join(s.dir, fmt.Sprintf("%x.json", hash))
}

// Load decodes a draft and reports whether the conversation has one.
func (s *DraftStore) Load(key string, value any) (bool, error) {
	path := s.path(key)

	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}

	if err != nil {
		return false, err
	}

	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return false, errors.New("draft file is not a private regular file")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}

	if err := json.Unmarshal(data, value); err != nil {
		return false, err
	}

	return true, nil
}

// Save atomically replaces a draft. A nil value removes it.
func (s *DraftStore) Save(key string, value any) error {
	path := s.path(key)
	if value == nil {
		if err := os.Remove(path); !errors.Is(err, os.ErrNotExist) {
			return err
		}

		return nil
	}

	return writeJSON(path, value)
}

func writeJSON(path string, value any) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".pending-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()

	writeErr := json.NewEncoder(file).Encode(value)

	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return err
	}

	return os.Rename(file.Name(), path)
}
