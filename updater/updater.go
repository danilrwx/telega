// Package updater checks and installs published release binaries.
package updater

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

const maxDownload = 128 << 20

// App identifies a release repository and the currently running program.
type App struct{ Name, Repository, Version string }

// Release identifies a published stable version.
type Release struct {
	Version string `json:"tag_name"`
}

// IsRelease excludes development builds from automatic update prompts.
func IsRelease(version string) bool {
	return semver.IsValid(version) && semver.Prerelease(version) == ""
}

// Newer reports whether a release is newer than the running binary.
func (a App) Newer(release Release) bool { return semver.Compare(release.Version, a.Version) > 0 }

// Check finds the latest published stable release without requiring authentication.
func (a App) Check(ctx context.Context) (Release, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	body, err := download(ctx, "https://api.github.com/repos/"+a.Repository+"/releases/latest", 1<<20)
	if err != nil {
		return Release{}, err
	}

	var release struct {
		Release
		Draft, Prerelease bool
	}
	if err := json.Unmarshal(body, &release); err != nil {
		return Release{}, err
	}

	if release.Draft || release.Prerelease || !IsRelease(release.Version) {
		return Release{}, errors.New("no stable release is available")
	}

	return release.Release, nil
}

func download(ctx context.Context, url string, limit int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	request.Header.Set("User-Agent", "terminal-client-updater")

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}

	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download failed: HTTP %d", response.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err == nil && int64(len(data)) > limit {
		err = errors.New("release download exceeds the size limit")
	}

	return data, err
}

func archiveName(name, system, arch string) (string, error) {
	if arch == "arm" {
		arch = "armv7"
	}

	switch system + "/" + arch {
	case "linux/amd64", "linux/arm64", "linux/armv7", "openbsd/amd64", "openbsd/arm64", "openbsd/armv7", "darwin/amd64", "darwin/arm64":
		return name + "_" + system + "_" + arch + ".tar.gz", nil
	default:
		return "", errors.New("no release binary is available for this platform")
	}
}

// Install verifies the archive and binary before atomically replacing target.
func (a App) Install(ctx context.Context, release Release, target string) error {
	if !IsRelease(release.Version) {
		return errors.New("invalid release version")
	}

	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	archive, err := archiveName(a.Name, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}

	base := "https://github.com/" + a.Repository + "/releases/download/" + release.Version + "/"

	sums, err := download(ctx, base+"SHA256SUMS", 64<<10)
	if err != nil {
		return err
	}

	var expected []byte

	for line := range strings.SplitSeq(string(sums), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == archive {
			if expected != nil {
				return errors.New("duplicate archive checksum")
			}

			expected, err = hex.DecodeString(fields[0])
			if err != nil || len(expected) != sha256.Size {
				return errors.New("invalid archive checksum")
			}
		}
	}

	if expected == nil {
		return errors.New("release checksum is missing")
	}

	data, err := download(ctx, base+archive, maxDownload)
	if err != nil {
		return err
	}

	actual := sha256.Sum256(data)
	if !bytes.Equal(actual[:], expected) {
		return errors.New("release checksum verification failed")
	}

	target, err = filepath.EvalSymlinks(target)
	if err != nil {
		return err
	}

	staged, err := os.CreateTemp(filepath.Dir(target), "."+a.Name+"-update-*")
	if err != nil {
		return fmt.Errorf("cannot update %s: %w", target, err)
	}

	defer func() { _ = staged.Close(); _ = os.Remove(staged.Name()) }()

	if err := extractBinary(staged, data, a.Name); err != nil {
		return err
	}

	if err := staged.Chmod(0o755); err != nil {
		return err
	}

	if err := errors.Join(staged.Sync(), staged.Close()); err != nil {
		return err
	}

	output, err := exec.CommandContext(ctx, staged.Name(), "--version").Output()
	if err != nil || strings.TrimSpace(string(output)) != a.Name+" "+release.Version {
		return errors.New("downloaded binary failed its version check")
	}

	return os.Rename(staged.Name(), target)
}

func extractBinary(out io.Writer, data []byte, name string) error {
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer func() { _ = reader.Close() }()

	archive := tar.NewReader(reader)
	found := false

	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return err
		}

		if header.Name != name {
			continue
		}

		if found || header.Typeflag != tar.TypeReg || header.Size < 1 || header.Size > maxDownload {
			return errors.New("invalid release binary")
		}

		if _, err := io.Copy(out, archive); err != nil {
			return err
		}

		found = true
	}

	if !found {
		return errors.New("release binary is missing from the archive")
	}

	return nil
}

// RunCLI checks for updates, optionally replacing the running executable.
func (a App) RunCLI(ctx context.Context, out io.Writer, install bool) error {
	release, err := a.Check(ctx)
	if err != nil {
		return err
	}

	if !a.Newer(release) {
		_, err := fmt.Fprintf(out, "%s %s is up to date.\n", a.Name, a.Version)
		return err
	}

	if !install {
		_, err := fmt.Fprintf(out, "%s %s is available. Run %s --update to install it.\n", a.Name, release.Version, a.Name)
		return err
	}

	target, err := os.Executable()
	if err != nil {
		return err
	}

	if _, err := fmt.Fprintf(out, "Installing %s %s…\n", a.Name, release.Version); err != nil {
		return err
	}

	if err := a.Install(ctx, release, target); err != nil {
		return err
	}

	_, err = fmt.Fprintf(out, "Installed %s %s. Restart the client to use it.\n", a.Name, release.Version)

	return err
}
