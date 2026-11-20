package session

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	gotdsession "github.com/gotd/td/session"
	"golang.org/x/term"
)

// Account provides the credentials and storage for the Telegram connection.
type Account struct {
	APIID   int
	APIHash string
	Storage gotdsession.Storage
}

func parseCredentials(idText, hash, dir string) (Account, error) {
	id, err := strconv.ParseInt(idText, 10, 32)

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
	fmt.Print("API ID: ")

	if _, err := fmt.Fscanln(os.Stdin, &id); err != nil {
		return Account{}, fmt.Errorf("read API ID: %w", err)
	}

	fmt.Print("API hash: ")

	secret, err := term.ReadPassword(int(os.Stdin.Fd()))

	fmt.Println()

	if err != nil {
		return Account{}, err
	}

	cfg, err := parseCredentials(id, strings.TrimSpace(string(secret)), dir)
	clear(secret)

	if err != nil {
		return Account{}, err
	}

	if err := file.store(fmt.Appendf(nil, "%d\n%s\n", cfg.APIID, cfg.APIHash)); err != nil {
		return Account{}, err
	}

	return cfg, nil
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
