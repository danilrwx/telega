package session

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mtproto.session")

	s := privateFile{path: path}
	for _, want := range []string{"first", "second"} {
		if err := s.store([]byte(want)); err != nil {
			t.Fatal(err)
		}

		got, err := s.load()
		if err != nil || string(got) != want {
			t.Fatalf("session contents %q, error %v", got, err)
		}
	}

	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := s.load(); err == nil {
		t.Fatal("accepted a session readable by other users")
	}
}

func TestSavedAPIConfig(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_DATA_HOME", base)

	for _, key := range []string{"TELEGA_API_ID", "TELEGA_API_HASH"} {
		t.Setenv(key, "")

		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}

	dir := filepath.Join(base, "telega")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	file := privateFile{filepath.Join(dir, "api")}

	hash := "0123456789abcdef0123456789abcdef"
	if err := file.store([]byte("123\n" + hash + "\n")); err != nil {
		t.Fatal(err)
	}

	got, err := Load()
	if err != nil || got.APIID != 123 || got.APIHash != hash {
		t.Fatal("failed to load saved Telegram API credentials")
	}

	t.Setenv("TELEGA_API_ID", "456")
	t.Setenv("TELEGA_API_HASH", hash)

	got, err = Load()
	if err != nil || got.APIID != 456 {
		t.Fatal("environment must override saved Telegram API credentials")
	}
}

func TestAPICredentialsTrimSurroundingWhitespace(t *testing.T) {
	const hash = "0123456789abcdef0123456789abcdef"

	got, err := parseCredentials(" \t123\r\n", "\t "+hash+" \r\n", t.TempDir())
	if err != nil || got.APIID != 123 || got.APIHash != hash {
		t.Fatal("pasted credentials should accept surrounding whitespace")
	}

	if _, err := parseCredentials("123", hash[:16]+" "+hash[16:], t.TempDir()); err == nil {
		t.Fatal("internal whitespace should be rejected")
	}
}
