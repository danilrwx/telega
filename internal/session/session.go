package session

import (
	"bufio"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	gotdsession "github.com/gotd/td/session"
)

// Account provides the credentials and storage for the Telegram connection.
type Account struct {
	APIID   int
	APIHash string
	Storage gotdsession.Storage
}

func parseCredentials(idText, hash, dir string) (Account, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(idText), 10, 32)
	hash = strings.TrimSpace(hash)

	_, invalidHash := hex.DecodeString(hash)
	if err != nil || id < 1 || len(hash) != 32 || invalidHash != nil {
		return Account{}, errors.New("enter a valid api_id and api_hash from https://my.telegram.org")
	}

	return Account{APIID: int(id), APIHash: hash, Storage: privateFile{filepath.Join(dir, "gotd.session")}}, nil
}

// Load reads local credentials or prompts for them on the first run.
func Load() (Account, error) {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Account{}, err
		}

		base = filepath.Join(home, ".local", "share")
	}

	dir := filepath.Join(base, "telega")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Account{}, err
	}

	info, err := os.Lstat(dir)
	if err != nil {
		return Account{}, err
	}

	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return Account{}, fmt.Errorf("%s must be a private directory (chmod 700)", dir)
	}

	id, idSet := os.LookupEnv("TELEGA_API_ID")

	hash, hashSet := os.LookupEnv("TELEGA_API_HASH")
	if idSet || hashSet {
		return parseCredentials(id, hash, dir)
	}

	file := privateFile{filepath.Join(dir, "api")}

	saved, err := file.load()
	if err != nil {
		return Account{}, err
	}

	if saved != nil {
		id, hash, ok := strings.Cut(strings.TrimSpace(string(saved)), "\n")
		clear(saved)

		if !ok {
			return Account{}, fmt.Errorf("invalid Telegram API credentials in %s", file.path)
		}

		return parseCredentials(id, hash, dir)
	}

	fmt.Println("Get your api_id and api_hash at https://my.telegram.org (API development tools).")

	input := bufio.NewScanner(os.Stdin)

	id, err = readCredential(input, "API ID")
	if err != nil {
		return Account{}, err
	}

	hash, err = readCredential(input, "API hash")
	if err != nil {
		return Account{}, err
	}

	cfg, err := parseCredentials(id, hash, dir)
	if err != nil {
		return Account{}, err
	}

	if err := file.store(fmt.Appendf(nil, "%d\n%s\n", cfg.APIID, cfg.APIHash)); err != nil {
		return Account{}, err
	}

	return cfg, nil
}

func readCredential(input *bufio.Scanner, label string) (string, error) {
	fmt.Printf("%s: ", label)

	if input.Scan() {
		return input.Text(), nil
	}

	err := input.Err()
	if err == nil {
		err = io.EOF
	}

	return "", fmt.Errorf("read %s: %w", label, err)
}

type privateFile struct{ path string }

func (s privateFile) LoadSession(context.Context) ([]byte, error) {
	data, err := s.load()
	if data == nil && err == nil {
		return nil, gotdsession.ErrNotFound
	}

	return data, err
}

func (s privateFile) StoreSession(_ context.Context, data []byte) error {
	return s.store(data)
}

func (s privateFile) load() ([]byte, error) {
	info, err := os.Lstat(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%s must be a private regular file", s.path)
	}

	return os.ReadFile(s.path)
}

func (s privateFile) store(data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(s.path), ".telega-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()

	_, writeErr := f.Write(data)

	syncErr := f.Sync()
	if err := errors.Join(writeErr, syncErr, f.Close()); err != nil {
		return err
	}

	return os.Rename(f.Name(), s.path)
}
