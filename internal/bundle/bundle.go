// Package bundle validates and registers bounded, immutable application ZIPs.
// Applications execute only in the player's opaque-origin sandbox, never here.
package bundle

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/vessica-labs/vessica-studio/internal/library"
	"golang.org/x/net/html"
)

const MaxArchiveBytes = 128 << 20
const MaxExpandedBytes = 256 << 20
const MaxFiles = 512

var ID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,127}$`)

func SafePath(p string) bool {
	if p == "" || len(p) > 512 || path.Clean(p) != p || strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\\:\x00?#") {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." || strings.HasPrefix(part, ".") || strings.TrimRight(part, " .") != part {
			return false
		}
	}
	return true
}

func Inspect(data []byte, entrypoint string) (library.BundleAsset, error) {
	var out library.BundleAsset
	if len(data) == 0 || len(data) > MaxArchiveBytes || !SafePath(entrypoint) || !strings.HasSuffix(entrypoint, ".html") {
		return out, fmt.Errorf("invalid bundle size or entrypoint")
	}
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return out, fmt.Errorf("invalid bundle ZIP")
	}
	if len(z.File) == 0 || len(z.File) > MaxFiles {
		return out, fmt.Errorf("bundle file count exceeds limit")
	}
	seen := map[string]bool{}
	found := false
	for _, f := range z.File {
		if f.FileInfo().IsDir() {
			continue
		}
		folded := strings.ToLower(f.Name)
		if !SafePath(f.Name) || !f.Mode().IsRegular() || seen[folded] || (f.Method != zip.Store && f.Method != zip.Deflate) || f.Flags&1 != 0 {
			return out, fmt.Errorf("unsafe bundle entry")
		}
		seen[folded] = true
		if f.UncompressedSize64 > MaxExpandedBytes || out.ExpandedBytes+int64(f.UncompressedSize64) > MaxExpandedBytes {
			return out, fmt.Errorf("expanded bundle exceeds limit")
		}
		reader, e := f.Open()
		if e != nil {
			return out, e
		}
		b, e := io.ReadAll(io.LimitReader(reader, int64(f.UncompressedSize64)+1))
		reader.Close()
		if e != nil || uint64(len(b)) != f.UncompressedSize64 {
			return out, fmt.Errorf("bundle entry integrity failed")
		}
		out.ExpandedBytes += int64(len(b))
		out.FileCount++
		if f.Name == entrypoint {
			found = true
			if e = validateHTML(b); e != nil {
				return out, e
			}
		}
	}
	if !found {
		return out, fmt.Errorf("bundle entrypoint is absent")
	}
	hash := sha256.Sum256(data)
	out.Hash = hex.EncodeToString(hash[:])
	out.Bytes = int64(len(data))
	out.File = "bundle/" + out.Hash + ".zip"
	out.Entrypoint = entrypoint
	return out, nil
}

// v1 applications inline their code. Remote scripts/import maps and nested
// documents are unsupported; the sandbox CSP forbids network and navigation.
func validateHTML(b []byte) error {
	if len(b) > 8<<20 {
		return fmt.Errorf("bundle HTML exceeds limit")
	}
	node, err := html.Parse(bytes.NewReader(b))
	if err != nil {
		return err
	}
	var walk func(*html.Node) error
	walk = func(n *html.Node) error {
		if n.Type == html.ElementNode {
			if n.Data == "base" || n.Data == "iframe" || n.Data == "object" || n.Data == "embed" {
				return fmt.Errorf("bundle cannot contain nested documents")
			}
			for _, a := range n.Attr {
				if n.Data == "script" && (a.Key == "src" || (a.Key == "type" && a.Val == "importmap")) {
					return fmt.Errorf("bundle v1 requires inline self-contained scripts")
				}
				if n.Data == "meta" && a.Key == "http-equiv" && strings.EqualFold(a.Val, "refresh") {
					return fmt.Errorf("bundle navigation is forbidden")
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if e := walk(c); e != nil {
				return e
			}
		}
		return nil
	}
	return walk(node)
}

func Ingest(lib, file, id, entrypoint, poster string) (library.BundleAsset, error) {
	var empty library.BundleAsset
	if !ID.MatchString(id) || (poster != "" && (!SafePath(poster) || !strings.HasPrefix(poster, "img/"))) {
		return empty, fmt.Errorf("invalid bundle id or poster")
	}
	f, err := os.Open(file)
	if err != nil {
		return empty, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxArchiveBytes+1))
	if err != nil {
		return empty, err
	}
	a, err := Inspect(data, entrypoint)
	if err != nil {
		return empty, err
	}
	a.ID = id
	a.Poster = poster
	a.Created = time.Now().UTC().Format("2006-01-02")
	m, err := library.Load(lib)
	if err != nil {
		return empty, err
	}
	if err = os.MkdirAll(filepath.Join(lib, "bundle"), 0755); err != nil {
		return empty, err
	}
	target := filepath.Join(lib, filepath.FromSlash(a.File))
	if err = os.WriteFile(target, data, 0644); err != nil {
		return empty, err
	}
	replaced := false
	for i, v := range m.Bundles {
		if v.ID == id {
			m.Bundles[i] = a
			replaced = true
		}
	}
	if !replaced {
		m.Bundles = append(m.Bundles, a)
	}
	return a, m.Save(lib)
}

func Verify(data []byte, a library.BundleAsset) error {
	got, err := Inspect(data, a.Entrypoint)
	if err != nil {
		return err
	}
	if !ID.MatchString(a.ID) || got.Hash != a.Hash || got.Bytes != a.Bytes || got.File != a.File || got.ExpandedBytes != a.ExpandedBytes || got.FileCount != a.FileCount {
		return fmt.Errorf("bundle does not match manifest")
	}
	return nil
}
