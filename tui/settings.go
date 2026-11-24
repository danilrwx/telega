package tui

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

func settingsPath(app string) (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, app, "settings.json"), nil
}

// ReadSettings loads a config file, leaving defaults intact if it is absent.
func ReadSettings(app string, value any) error {
	path, err := settingsPath(app)
	if err != nil {
		return err
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	if err != nil {
		return err
	}

	return json.Unmarshal(data, value)
}

// WriteSettings atomically saves a config file.
func WriteSettings(app string, value any) error {
	path, err := settingsPath(app)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	return writeJSON(path, value)
}
