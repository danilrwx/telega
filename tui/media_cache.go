package tui

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const mediaCacheLimit = 64 << 20

// ReadImageData reads an encoded raster within the terminal decoder's input limit.
func ReadImageData(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxKittyInput+1))
	if err != nil {
		return nil, err
	}

	if len(data) > maxKittyInput {
		return nil, fmt.Errorf("image exceeds the %d-byte input limit", maxKittyInput)
	}

	return data, nil
}

// CachedImage reuses validated source bytes across terminal backends and sessions.
func CachedImage[T any](app, key string, maxAge time.Duration, decode func(io.Reader) (T, error), fetch func() ([]byte, error)) (T, error) {
	var zero T

	path := mediaCachePath(app, key)
	if path != "" {
		info, err := os.Lstat(path)
		if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o077 == 0 &&
			info.Size() > 0 && info.Size() <= maxKittyInput && (maxAge == 0 || time.Since(info.ModTime()) < maxAge) {
			file, err := os.Open(path)
			if err == nil {
				value, decodeErr := decode(file)
				_ = file.Close()

				if decodeErr == nil {
					return value, nil
				}
			}
		}
	}

	data, err := fetch()
	if err != nil {
		return zero, err
	}

	value, err := decode(bytes.NewReader(data))
	if err == nil && path != "" && len(data) <= maxKittyInput {
		saveCachedMedia(path, data)
	}

	return value, err
}

func mediaCachePath(app, key string) string {
	base, err := os.UserCacheDir()
	if err != nil || key == "" {
		return ""
	}

	sum := sha256.Sum256([]byte(key))

	return filepath.Join(base, app, "media", hex.EncodeToString(sum[:])+".image")
}

func saveCachedMedia(path string, data []byte) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}

	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return
	}

	file, err := os.CreateTemp(dir, ".media-*")
	if err != nil {
		return
	}

	defer func() { _ = os.Remove(file.Name()) }()

	_, writeErr := file.Write(data)
	closeErr := file.Close()

	if writeErr == nil && closeErr == nil && os.Rename(file.Name(), path) == nil {
		pruneCachedMedia(dir)
	}
}

func pruneCachedMedia(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	var (
		total int64
		files []os.FileInfo
	)

	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".image") {
			continue
		}

		info, err := entry.Info()
		if err == nil && info.Mode().IsRegular() {
			total += info.Size()
			files = append(files, info)
		}
	}

	if total <= mediaCacheLimit && len(files) <= 512 {
		return
	}

	slices.SortFunc(files, func(a, b os.FileInfo) int { return a.ModTime().Compare(b.ModTime()) })

	for i, info := range files {
		if total <= mediaCacheLimit && len(files)-i <= 512 {
			return
		}

		if err := os.Remove(filepath.Join(dir, info.Name())); err == nil {
			total -= info.Size()
		}
	}
}
