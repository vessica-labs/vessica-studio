package studio

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vessica-labs/vessica-studio/internal/cloud"
)

const (
	cloudMaxFiles     = 10000
	cloudMaxFileSize  = 16 << 20
	cloudMaxTotalSize = 128 << 20
)

type CloudSnapshot struct {
	Files  []cloud.File
	Digest string
}

// CloudContent returns the canonical, deterministic cloud projection of a studio.
func CloudContent(root string) (CloudSnapshot, error) {
	if _, err := Open(root); err != nil {
		return CloudSnapshot{}, err
	}
	var files []cloud.File
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel != "." && excludedCloudDir(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if !allowedCloudPath(rel) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("cloud content %q is not a regular file", rel)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files = append(files, cloud.File{Path: rel, Content: b, Mode: uint32(info.Mode().Perm())})
		return nil
	})
	if err != nil {
		return CloudSnapshot{}, err
	}
	if err := ValidateCloudContent(files); err != nil {
		return CloudSnapshot{}, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return CloudSnapshot{Files: files, Digest: cloudDigest(files)}, nil
}

func excludedCloudDir(rel string) bool {
	return rel == ".git" || rel == ".vstd" || rel == "requests" || strings.Contains(rel, "/build") || rel == "library/video" || rel == "library/videos"
}
func allowedCloudPath(p string) bool {
	if p == "studio.yaml" || p == "library/manifest.json" {
		return true
	}
	parts := strings.Split(p, "/")
	if len(parts) >= 3 && parts[0] == "themes" {
		return true
	}
	if len(parts) >= 3 && parts[0] == "library" && parts[1] == "img" {
		return true
	}
	if len(parts) >= 3 && parts[0] == "decks" {
		if len(parts) == 3 {
			return parts[2] == "deck.yaml" || parts[2] == "deck.css"
		}
		return len(parts) == 4 && parts[2] == "slides" && (strings.HasSuffix(parts[3], ".html") || strings.HasSuffix(parts[3], ".md"))
	}
	return false
}

func ValidateCloudContent(files []cloud.File) error {
	if len(files) > cloudMaxFiles {
		return fmt.Errorf("cloud snapshot has too many files")
	}
	seen := map[string]bool{}
	var total int64
	hasStudio := false
	for _, f := range files {
		p := filepath.ToSlash(f.Path)
		if p == "" || p != f.Path || filepath.IsAbs(p) || strings.Contains(p, "\\") || filepath.Clean(p) != p || strings.HasPrefix(p, "../") || !allowedCloudPath(p) {
			return fmt.Errorf("invalid cloud content path %q", f.Path)
		}
		fold := strings.ToLower(p)
		if seen[fold] {
			return fmt.Errorf("duplicate or case-colliding cloud path %q", p)
		}
		seen[fold] = true
		if len(f.Content) > cloudMaxFileSize {
			return fmt.Errorf("cloud content %q is too large", p)
		}
		total += int64(len(f.Content))
		if total > cloudMaxTotalSize {
			return fmt.Errorf("cloud snapshot is too large")
		}
		if p == "studio.yaml" {
			hasStudio = true
		}
	}
	if !hasStudio {
		return fmt.Errorf("cloud snapshot is missing studio.yaml")
	}
	return nil
}

// ApplyCloudContent validates all remote input before replacing projected files.
func ApplyCloudContent(root string, files []cloud.File) error {
	if err := ValidateCloudContent(files); err != nil {
		return err
	}
	parent := filepath.Dir(root)
	stage, err := os.MkdirTemp(parent, ".vstd-cloud-stage-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	for _, f := range files {
		p := filepath.Join(stage, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			return err
		}
		mode := os.FileMode(f.Mode) & 0777
		if mode == 0 {
			mode = 0644
		}
		if err := os.WriteFile(p, f.Content, mode); err != nil {
			return err
		}
	}
	if _, err := Open(stage); err != nil {
		return fmt.Errorf("invalid cloud studio: %w", err)
	}
	// Preserve excluded state by updating only the canonical projection. Writes are atomic per file.
	current, _ := CloudContent(root)
	backup := make(map[string]cloud.File, len(current.Files))
	for _, f := range current.Files {
		backup[f.Path] = f
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		for _, f := range files {
			if _, existed := backup[f.Path]; !existed {
				_ = os.Remove(filepath.Join(root, filepath.FromSlash(f.Path)))
			}
		}
		for _, f := range backup {
			p := filepath.Join(root, filepath.FromSlash(f.Path))
			_ = os.MkdirAll(filepath.Dir(p), 0755)
			_ = os.WriteFile(p, f.Content, os.FileMode(f.Mode)&0777)
		}
	}()
	incoming := map[string]bool{}
	for _, f := range files {
		incoming[f.Path] = true
	}
	for _, f := range current.Files {
		if !incoming[f.Path] {
			if err := os.Remove(filepath.Join(root, filepath.FromSlash(f.Path))); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	for _, f := range files {
		src := filepath.Join(stage, filepath.FromSlash(f.Path))
		b, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		dst := filepath.Join(root, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			return err
		}
		tmp := dst + ".vstd-sync"
		mode := os.FileMode(f.Mode) & 0777
		if mode == 0 {
			mode = 0644
		}
		if err := os.WriteFile(tmp, b, mode); err != nil {
			return err
		}
		if err := os.Rename(tmp, dst); err != nil {
			return err
		}
	}
	_, err = Open(root)
	if err == nil {
		committed = true
	}
	return err
}

func cloudDigest(files []cloud.File) string {
	h := sha256.New()
	for _, f := range files {
		fmt.Fprintf(h, "%s\x00%d\x00", f.Path, len(f.Content))
		h.Write(f.Content)
	}
	return hex.EncodeToString(h.Sum(nil))
}
