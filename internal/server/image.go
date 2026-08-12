package server

// Image upload — the raster twin of the video upload endpoint. Drag-dropping
// a PNG/JPEG/GIF/WebP onto a slide in edit mode POSTs it here; the engine
// stores it in library/img/ (committed to git — images are small, unlike
// video bytes), registers it in the manifest like any generated asset, and
// the player inserts an <img src="/library/img/..."> the user can move and
// resize with the normal edit handles.
//
// Uploads are content-hashed: re-dropping the same file returns the existing
// asset instead of duplicating it. Studio mode only.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/vessica-labs/vessica-studio/internal/library"
)

var imgSlugRe = regexp.MustCompile(`[^a-z0-9]+`)

var imgExts = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true}

// handleImageUpload ingests POST /api/asset/image?filename=F[&slug=S&tags=a,b]
// (raw bytes in the body). Studio mode only (wrapped by editOnly in Routes).
func (s *Server) handleImageUpload(w http.ResponseWriter, r *http.Request) {
	filename := r.URL.Query().Get("filename")
	ext := strings.ToLower(filepath.Ext(filename))
	if !imgExts[ext] {
		jsonErr(w, fmt.Errorf("unsupported image type %q (png, jpg, gif, webp)", ext), 400)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 100<<20)) // 100MB guard
	if err != nil || len(body) == 0 {
		jsonErr(w, fmt.Errorf("empty or unreadable body"), 400)
		return
	}
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:8])

	libDir := s.libDir()
	man, err := library.Load(libDir)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	// content-hash dedupe: same bytes → same asset, no duplicate files
	for i := range man.Assets {
		if man.Assets[i].Hash == hash {
			writeJSON(w, map[string]any{"status": "ok", "asset": man.Assets[i], "reused": true})
			return
		}
	}

	slug := r.URL.Query().Get("slug")
	if slug == "" {
		slug = strings.TrimSuffix(strings.ToLower(filepath.Base(filename)), ext)
	}
	slug = strings.Trim(imgSlugRe.ReplaceAllString(strings.ToLower(slug), "-"), "-")
	if len(slug) > 40 {
		slug = slug[:40]
	}
	if slug == "" {
		slug = "image"
	}
	id := slug + "-" + hash[:6]

	var tags []string
	for _, t := range strings.Split(r.URL.Query().Get("tags"), ",") {
		if t = strings.TrimSpace(t); t != "" {
			tags = append(tags, t)
		}
	}

	size := ""
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(body)); err == nil {
		size = fmt.Sprintf("%dx%d", cfg.Width, cfg.Height)
	}

	file := "img/" + id + ext
	if err := os.MkdirAll(filepath.Join(libDir, "img"), 0o755); err != nil {
		jsonErr(w, err, 500)
		return
	}
	if err := os.WriteFile(filepath.Join(libDir, filepath.FromSlash(file)), body, 0o644); err != nil {
		jsonErr(w, err, 500)
		return
	}
	asset := library.Asset{
		ID: id, File: file, Prompt: "(uploaded: " + filepath.Base(filename) + ")",
		Tags: tags, Model: "upload", Size: size,
		Created: time.Now().Format(time.RFC3339), Hash: hash,
	}
	man.Assets = append(man.Assets, asset)
	if err := man.Save(libDir); err != nil {
		jsonErr(w, err, 500)
		return
	}
	s.Broadcast("reload")
	writeJSON(w, map[string]any{"status": "ok", "asset": asset, "reused": false})
}
