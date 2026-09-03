package studio

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"gopkg.in/yaml.v3"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	cloudMaxFiles     = 10000
	cloudMaxFileSize  = 16 << 20
	cloudMaxTotalSize = 128 << 20
)

// ContentFile belongs to the studio file model, not a remote transport.
type ContentFile struct {
	Path    string
	Content []byte
	Mode    uint32
}

type CloudSnapshot struct {
	Files  []ContentFile
	Digest string
}

// CloudContent returns the canonical, deterministic cloud projection of a studio.
func CloudContent(root string) (CloudSnapshot, error) {
	if _, err := Open(root); err != nil {
		return CloudSnapshot{}, err
	}
	var files []ContentFile
	var total int64
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
		total += info.Size()
		if info.Size() > cloudMaxFileSize || total > cloudMaxTotalSize || len(files) >= cloudMaxFiles {
			return fmt.Errorf("cloud snapshot exceeds size or file count limits")
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files = append(files, ContentFile{Path: rel, Content: b, Mode: uint32(info.Mode().Perm())})
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
		switch strings.ToLower(filepath.Ext(p)) {
		case ".css", ".html", ".js", ".json", ".md", ".svg", ".png", ".jpg", ".jpeg", ".webp", ".gif", ".avif", ".woff", ".woff2", ".ttf":
			return true
		default:
			return false
		}
	}
	if len(parts) >= 3 && parts[0] == "library" && parts[1] == "img" {
		switch strings.ToLower(filepath.Ext(p)) {
		case ".svg", ".png", ".jpg", ".jpeg", ".webp", ".gif", ".avif":
			return true
		default:
			return false
		}
	}
	if len(parts) >= 3 && parts[0] == "decks" {
		if len(parts) == 3 {
			return parts[2] == "deck.yaml" || parts[2] == "deck.css"
		}
		return len(parts) == 4 && parts[2] == "slides" && (strings.HasSuffix(parts[3], ".html") || strings.HasSuffix(parts[3], ".md"))
	}
	return false
}

func ValidateCloudContent(files []ContentFile) error {
	if len(files) > cloudMaxFiles {
		return fmt.Errorf("cloud snapshot has too many files")
	}
	seen := map[string]bool{}
	var total int64
	hasStudio := false
	for _, f := range files {
		p := filepath.ToSlash(f.Path)
		if p == "" || p != f.Path || filepath.IsAbs(p) || strings.ContainsAny(p, "\\:\x00") || filepath.Clean(p) != p || strings.HasPrefix(p, "../") || !allowedCloudPath(p) {
			return fmt.Errorf("invalid cloud content path %q", f.Path)
		}
		fold := strings.ToLower(p)
		if seen[fold] {
			return fmt.Errorf("duplicate or case-colliding cloud path %q", p)
		}
		seen[fold] = true
		for _, part := range strings.Split(p, "/") {
			if strings.HasPrefix(part, ".") || strings.TrimRight(part, " .") != part {
				return fmt.Errorf("unsafe cloud path %q", p)
			}
		}
		if f.Mode != 0 && f.Mode != 0600 && f.Mode != 0644 {
			return fmt.Errorf("unsafe cloud file mode")
		}
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
	for _, f := range files {
		parts := strings.Split(f.Path, "/")
		if parts[0] == "decks" {
			if !ValidDeckName(parts[1]) || !seen[strings.ToLower("decks/"+parts[1]+"/deck.yaml")] {
				return fmt.Errorf("cloud snapshot has an invalid or incomplete deck")
			}
			if len(parts) == 4 && parts[2] == "slides" {
				ext := filepath.Ext(parts[3])
				id := strings.TrimSuffix(parts[3], ext)
				other := ".html"
				if ext == ".html" {
					other = ".md"
				}
				if !ValidSlideID(id) || !seen[strings.ToLower("decks/"+parts[1]+"/slides/"+id+other)] {
					return fmt.Errorf("cloud snapshot has an unpaired slide")
				}
			}
		}
		if f.Path == "studio.yaml" {
			var config Config
			if err := yaml.Unmarshal(f.Content, &config); err != nil {
				return fmt.Errorf("invalid cloud studio configuration")
			}
			if config.ShareSecretCmd != "" || config.OpenAI.APIKeyCmd != "" || config.Storage.AccessKeyCmd != "" || config.Storage.SecretKeyCmd != "" {
				return fmt.Errorf("cloud snapshots cannot contain credential commands")
			}
			if (config.OpenAI.BaseURL != "" && config.OpenAI.BaseURL != "https://api.openai.com/v1") || config.Storage.Endpoint != "" {
				return fmt.Errorf("cloud snapshots cannot configure credential-bearing provider endpoints; configure providers locally through environment variables")
			}
		}
	}
	return nil
}

// ApplyCloudContent validates all remote input before replacing projected files.
func ApplyCloudContent(root string, files []ContentFile) error {
	if err := ValidateCloudContent(files); err != nil {
		return err
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	current, err := CloudContent(root)
	if err != nil {
		entries, readErr := os.ReadDir(root)
		if readErr != nil || len(entries) != 0 {
			return fmt.Errorf("cannot read existing studio safely")
		}
	}
	// Reject every unsafe destination before deleting or replacing any content.
	for _, f := range append(append([]ContentFile{}, current.Files...), files...) {
		if err := CheckContentPath(root, f.Path); err != nil {
			return err
		}
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
	if err := beginCloudTransaction(root, current.Files, files); err != nil {
		return err
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		_ = RecoverCloudContent(root) // Failed recovery leaves the durable journal intact.
	}()
	incoming := map[string]bool{}
	for _, f := range files {
		incoming[f.Path] = true
	}
	for _, f := range current.Files {
		if !incoming[f.Path] {
			if err := CheckContentPath(root, f.Path); err != nil {
				return err
			}
			if err := os.Remove(filepath.Join(root, filepath.FromSlash(f.Path))); err != nil && !os.IsNotExist(err) {
				return err
			}
			if err := syncCloudDirectory(filepath.Dir(filepath.Join(root, f.Path))); err != nil {
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
		if err := CheckContentPath(root, f.Path); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			return err
		}
		mode := os.FileMode(f.Mode) & 0777
		if mode == 0 {
			mode = 0644
		}
		if err := writeCloudFile(dst, b, mode); err != nil {
			return err
		}
	}
	if err := finishCloudTransaction(root); err != nil {
		return err
	}
	committed = true
	return nil
}

func cloudDigest(files []ContentFile) string {
	h := sha256.New()
	for _, f := range files {
		fmt.Fprintf(h, "%s\x00%d\x00", f.Path, len(f.Content))
		h.Write(f.Content)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ContentDigest validates and hashes an unordered remote projection.
func ContentDigest(files []ContentFile) (string, error) {
	if err := ValidateCloudContent(files); err != nil {
		return "", err
	}
	ordered := append([]ContentFile{}, files...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Path < ordered[j].Path })
	return cloudDigest(ordered), nil
}

// CheckContentPath rejects existing symlinks and non-directory ancestors.
// The resolved studio root is user-selected; remote paths must stay inside it.
func CheckContentPath(root, rel string) error {
	if !filepath.IsLocal(rel) {
		return fmt.Errorf("unsafe local state path")
	}
	path := root
	parts := strings.Split(filepath.FromSlash(rel), string(filepath.Separator))
	for i, part := range parts {
		path = filepath.Join(path, part)
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || (i < len(parts)-1 && !info.IsDir()) || (i == len(parts)-1 && !info.Mode().IsRegular()) {
			return fmt.Errorf("unsafe local state path %q", rel)
		}
	}
	return nil
}

func writeCloudFile(dst string, content []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(dst), ".vstd-sync-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(content); err != nil {
		f.Close()
		return err
	}
	if err = f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), dst); err != nil {
		return err
	}
	return syncCloudDirectory(filepath.Dir(dst))
}
