package updater

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func mockDownloads(t *testing.T, files map[string]string) {
	t.Helper()

	previous := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		body, ok := files[r.URL.Path]

		status := http.StatusOK
		if !ok {
			status = http.StatusNotFound
		}

		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}

	t.Cleanup(func() { http.DefaultClient = previous })
}

func TestReleaseComparison(t *testing.T) {
	mockDownloads(t, map[string]string{"/repos/test/tool/releases/latest": `{"tag_name":"v0.10.0","draft":false,"prerelease":false}`})

	app := App{Name: "tool", Repository: "test/tool", Version: "v0.9.0"}

	release, err := app.Check(t.Context())
	if err != nil || !app.Newer(release) {
		t.Fatalf("numeric version comparison failed: %+v, %v", release, err)
	}

	app.Version = "v0.10.0"
	if app.Newer(release) {
		t.Fatal("current release must not update itself")
	}

	app.Version = "v0.11.0"
	if app.Newer(release) {
		t.Fatal("updates must not downgrade")
	}

	for _, version := range []string{"dev", "v0.2.0-rc.1", "not-a-version"} {
		if IsRelease(version) {
			t.Fatalf("unexpected stable version: %q", version)
		}
	}
}

func releaseArchive(t *testing.T, name, version string) []byte {
	t.Helper()

	body := []byte("#!/bin/sh\nprintf '%s\\n' 'tool " + version + "'\n")

	var out bytes.Buffer

	zip := gzip.NewWriter(&out)

	archive := tar.NewWriter(zip)
	if err := archive.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}

	if _, err := archive.Write(body); err != nil {
		t.Fatal(err)
	}

	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}

	if err := zip.Close(); err != nil {
		t.Fatal(err)
	}

	return out.Bytes()
}

func TestInstallPreservesOldBinaryUntilVerified(t *testing.T) {
	for _, scenario := range []struct {
		name, entry, version string
		badSum               bool
		ok                   bool
	}{
		{"valid", "tool", "v0.2.0", false, true},
		{"checksum mismatch", "tool", "v0.2.0", true, false},
		{"wrong version", "tool", "v0.1.0", false, false},
		{"unsafe archive path", "../tool", "v0.2.0", false, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			data := releaseArchive(t, scenario.entry, scenario.version)

			sum := fmt.Sprintf("%x", sha256.Sum256(data))
			if scenario.badSum {
				sum = strings.Repeat("0", 64)
			}

			archive, err := archiveName("tool", runtime.GOOS, runtime.GOARCH)
			if err != nil {
				t.Fatal(err)
			}

			base := "/test/tool/releases/download/v0.2.0/"
			mockDownloads(t, map[string]string{base + archive: string(data), base + "SHA256SUMS": sum + "  " + archive + "\n"})

			target := filepath.Join(t.TempDir(), "tool")
			if err := os.WriteFile(target, []byte("old binary"), 0o755); err != nil {
				t.Fatal(err)
			}

			link := target + "-link"
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}

			err = (App{Name: "tool", Repository: "test/tool"}).Install(t.Context(), Release{Version: "v0.2.0"}, link)
			if (err == nil) != scenario.ok {
				t.Fatalf("installation success=%t, want %t: %v", err == nil, scenario.ok, err)
			}

			body, readErr := os.ReadFile(target)
			if readErr != nil {
				t.Fatal(readErr)
			}

			if !scenario.ok && string(body) != "old binary" {
				t.Fatal("failed update replaced the existing binary")
			}

			if scenario.ok && !strings.Contains(string(body), "v0.2.0") {
				t.Fatal("new binary was not installed")
			}

			info, err := os.Lstat(link)
			if err != nil || info.Mode()&os.ModeSymlink == 0 {
				t.Fatal("update replaced the symlink instead of its target")
			}

			leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(target), ".tool-update-*"))
			if err != nil || len(leftovers) != 0 {
				t.Fatalf("staged files left behind: %v %v", leftovers, err)
			}
		})
	}
}

func TestArchivePlatforms(t *testing.T) {
	for _, target := range []struct{ system, arch, want string }{
		{"openbsd", "arm", "tool_openbsd_armv7.tar.gz"}, {"darwin", "arm64", "tool_darwin_arm64.tar.gz"}, {"linux", "amd64", "tool_linux_amd64.tar.gz"},
	} {
		name, err := archiveName("tool", target.system, target.arch)
		if err != nil || name != target.want {
			t.Fatalf("archive: %q, %v", name, err)
		}
	}

	if _, err := archiveName("tool", "windows", "amd64"); err == nil {
		t.Fatal("unsupported platform accepted")
	}
}
