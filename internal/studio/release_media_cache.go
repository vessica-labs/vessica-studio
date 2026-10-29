package studio

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

var releaseEncoderDigests sync.Map

// Only an explicitly supplied host cache is used. Cloud supplies a private
// tenant namespace; the cache is disposable and never authorizes a publication.
// Recipes and encoder bytes bind entries; every hit is integrity checked.
func cachedReleaseDerivative(source, dest, encoder, recipe string, encode func() error) error {
	root := os.Getenv("XDG_CACHE_HOME")
	if !filepath.IsAbs(root) {
		return encode()
	}
	root = filepath.Join(root, "vstd", "release-media-v1")
	key, err := releaseMediaCacheKey(source, encoder, recipe)
	if err != nil || os.MkdirAll(root, 0o700) != nil {
		return encode()
	}
	path := filepath.Join(root, key+".media")
	if restoreReleaseDerivative(path, dest) {
		return nil
	}
	if err := encode(); err != nil {
		return err
	}
	// Cache failures never change an otherwise valid release.
	storeReleaseDerivative(path, dest)
	trimReleaseMediaCache(root, 256<<20)
	return nil
}

func releaseMediaCacheKey(source, encoder, recipe string) (string, error) {
	info, err := os.Stat(encoder)
	if err != nil {
		return "", err
	}
	identity := fmt.Sprintf("%s:%d:%d", encoder, info.Size(), info.ModTime().UnixNano())
	digest, ok := releaseEncoderDigests.Load(identity)
	if !ok {
		file, err := os.Open(encoder)
		if err != nil {
			return "", err
		}
		hash := sha256.New()
		_, err = io.Copy(hash, file)
		_ = file.Close()
		if err != nil {
			return "", err
		}
		digest, _ = releaseEncoderDigests.LoadOrStore(identity, hex.EncodeToString(hash.Sum(nil)))
	}
	hash := sha256.New()
	_, _ = fmt.Fprintf(hash, "%s\x00%s\x00", recipe, digest)
	file, err := os.Open(source)
	if err != nil {
		return "", err
	}
	defer file.Close()
	if _, err = io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func restoreReleaseDerivative(path, dest string) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= sha256.Size || info.Size() > maxReleaseFileBytes+sha256.Size {
		return false
	}
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	expected := make([]byte, sha256.Size)
	if _, err = io.ReadFull(file, expected); err != nil {
		return false
	}
	temporary, err := os.CreateTemp(filepath.Dir(dest), ".release-cache-*")
	if err != nil {
		return false
	}
	defer os.Remove(temporary.Name())
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(temporary, hash), io.LimitReader(file, maxReleaseFileBytes+1))
	closeErr := temporary.Close()
	if err != nil || closeErr != nil || n != info.Size()-sha256.Size || !bytes.Equal(expected, hash.Sum(nil)) {
		return false
	}
	return os.Rename(temporary.Name(), dest) == nil
}

func storeReleaseDerivative(path, source string) {
	file, err := os.Open(source)
	if err != nil {
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > 256<<20 {
		return
	}
	hash := sha256.New()
	if _, err = io.Copy(hash, file); err != nil {
		return
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".release-cache-*")
	if err != nil {
		return
	}
	defer os.Remove(temporary.Name())
	_, headerErr := temporary.Write(hash.Sum(nil))
	_, copyErr := io.Copy(temporary, file)
	closeErr := temporary.Close()
	if headerErr == nil && copyErr == nil && closeErr == nil {
		_ = os.Rename(temporary.Name(), path)
	}
}

func trimReleaseMediaCache(root string, limit int64) {
	entries, _ := os.ReadDir(root)
	files := []os.FileInfo{}
	var total int64
	for _, entry := range entries {
		info, err := entry.Info()
		if err == nil && info.Mode().IsRegular() && strings.HasSuffix(entry.Name(), ".media") {
			files = append(files, info)
			total += info.Size()
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].ModTime().Before(files[j].ModTime()) })
	for _, file := range files {
		if total <= limit {
			break
		}
		if os.Remove(filepath.Join(root, file.Name())) == nil {
			total -= file.Size()
		}
	}
}
