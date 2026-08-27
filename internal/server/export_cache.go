package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/vessica-labs/vessica-studio/internal/library"
	"github.com/vessica-labs/vessica-studio/internal/studio"
)

const powerpointCacheVersion = 1

type PowerPointCacheEntry struct {
	Mode        string    `json:"mode"`
	SlideID     string    `json:"slide_id"`
	Fingerprint string    `json:"fingerprint"`
	File        string    `json:"file"`
	GeneratedAt time.Time `json:"generated_at"`
}

type PowerPointCacheManifest struct {
	Version int                             `json:"version"`
	Entries map[string]PowerPointCacheEntry `json:"entries"`
}

type powerpointCacheStats struct {
	Hits, Misses int
}

type powerpointCacheRenderer interface {
	Visual(context.Context, *http.Request, string, []string) ([][]byte, error)
	Editable(*http.Request, string, []string) (studio.PPTXDeck, error)
}

type browserPowerPointRenderer struct{ server *Server }

func (b browserPowerPointRenderer) Visual(ctx context.Context, r *http.Request, deck string, ids []string) ([][]byte, error) {
	pdf, _, err := b.server.renderDeckPDFForSlides(r, deck, ids)
	if err != nil {
		return nil, err
	}
	return rasterizePDF(ctx, pdf)
}

func (b browserPowerPointRenderer) Editable(r *http.Request, deck string, ids []string) (studio.PPTXDeck, error) {
	return b.server.capturePPTXDeckForSlides(r, deck, ids)
}

func (s *Server) powerpointRenderer() powerpointCacheRenderer {
	if s.PowerPointRenderer != nil {
		return s.PowerPointRenderer
	}
	return browserPowerPointRenderer{server: s}
}

var localAssetURLRe = regexp.MustCompile(`(?i)(?:/|\b)library/([a-zA-Z0-9_./-]+)`)
var videoPosterURLRe = regexp.MustCompile(`(?i)/assets/video/([a-z0-9-]+)/poster`)
var deckSourceURLRe = regexp.MustCompile(`(?i)(?:/api/deck/[a-z0-9-]+/source/|sources/)([a-zA-Z0-9_.-]+)`)
var themeFontURLRe = regexp.MustCompile(`(?i)fonts/([a-zA-Z0-9_./-]+)`)

func (s *Server) powerpointCacheDir(deck string) string {
	return filepath.Join(s.St.DeckDir(deck), "build", "assets", "powerpoint")
}

func (s *Server) deckExportLock(deck string) *sync.Mutex {
	v, _ := s.exportLocks.LoadOrStore(deck, &sync.Mutex{})
	return v.(*sync.Mutex)
}

func readPowerPointManifest(dir string) PowerPointCacheManifest {
	m := PowerPointCacheManifest{Version: powerpointCacheVersion, Entries: map[string]PowerPointCacheEntry{}}
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil || json.Unmarshal(b, &m) != nil || m.Version != powerpointCacheVersion || m.Entries == nil {
		return PowerPointCacheManifest{Version: powerpointCacheVersion, Entries: map[string]PowerPointCacheEntry{}}
	}
	return m
}

func writePowerPointManifest(dir string, manifest PowerPointCacheManifest) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "manifest.json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func cacheKey(mode, slide string) string { return mode + "/" + slide }

func (s *Server) slidePowerPointFingerprint(deck, id, mode string) (string, error) {
	meta, err := s.St.LoadDeckMeta(deck)
	if err != nil {
		return "", err
	}
	fragment, _, err := s.St.ReadSlide(deck, id)
	if err != nil {
		return "", err
	}
	themeCSS, err := os.ReadFile(filepath.Join(s.St.ThemeDir(meta.Theme), "theme.css"))
	if err != nil {
		return "", err
	}
	deckCSS, _ := os.ReadFile(filepath.Join(s.St.DeckDir(deck), "deck.css"))
	h := sha256.New()
	fmt.Fprintf(h, "vstd-powerpoint-cache:%d:%s:%s\n", powerpointCacheVersion, mode, id)
	h.Write([]byte(fragment))
	h.Write(themeCSS)
	h.Write(deckCSS)
	combined := fragment + "\n" + string(themeCSS) + "\n" + string(deckCSS)
	paths := map[string]bool{}
	for _, match := range localAssetURLRe.FindAllStringSubmatch(combined, -1) {
		rel := filepath.Clean(filepath.FromSlash(match[1]))
		if rel == "." || strings.HasPrefix(rel, "..") {
			continue
		}
		paths[filepath.Join(s.St.Root, "library", rel)] = true
	}
	for _, match := range deckSourceURLRe.FindAllStringSubmatch(combined, -1) {
		paths[filepath.Join(s.St.DeckDir(deck), "sources", filepath.FromSlash(match[1]))] = true
	}
	for _, match := range themeFontURLRe.FindAllStringSubmatch(combined, -1) {
		paths[filepath.Join(s.St.ThemeDir(meta.Theme), "fonts", filepath.FromSlash(match[1]))] = true
	}
	if videoPosterURLRe.MatchString(combined) {
		manifest, _ := library.Load(filepath.Join(s.St.Root, "library"))
		for _, match := range videoPosterURLRe.FindAllStringSubmatch(combined, -1) {
			found := false
			if manifest != nil {
				for _, video := range manifest.Videos {
					if video.ID != match[1] {
						continue
					}
					asset := video.Poster
					if asset == "" {
						asset = video.File
					}
					paths[filepath.Join(s.St.Root, "library", filepath.FromSlash(asset))] = true
					found = true
					break
				}
			}
			if !found {
				paths[filepath.Join(s.St.Root, "library", "video-posters", match[1]+".jpg")] = true
			}
		}
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	for _, path := range ordered {
		h.Write([]byte(path))
		if body, err := os.ReadFile(path); err == nil {
			h.Write(body)
		} else {
			h.Write([]byte("missing"))
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func atomicCacheFile(path string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *Server) cachedVisualPowerPointSlides(ctx context.Context, r *http.Request, deck string, ids []string) ([][]byte, powerpointCacheStats, error) {
	lock := s.deckExportLock(deck)
	lock.Lock()
	defer lock.Unlock()
	base := s.powerpointCacheDir(deck)
	manifest := readPowerPointManifest(base)
	fingerprints := map[string]string{}
	var misses []string
	stats := powerpointCacheStats{}
	for _, id := range ids {
		fp, err := s.slidePowerPointFingerprint(deck, id, "visual")
		if err != nil {
			return nil, stats, err
		}
		fingerprints[id] = fp
		entry, ok := manifest.Entries[cacheKey("visual", id)]
		if !ok || entry.Fingerprint != fp {
			misses = append(misses, id)
			continue
		}
		body, err := os.ReadFile(filepath.Join(base, filepath.FromSlash(entry.File)))
		if err != nil {
			misses = append(misses, id)
			continue
		}
		if _, err := png.DecodeConfig(bytes.NewReader(body)); err != nil {
			misses = append(misses, id)
			continue
		}
		stats.Hits++
	}
	if len(misses) > 0 {
		images, err := s.powerpointRenderer().Visual(ctx, r, deck, misses)
		if err != nil {
			return nil, stats, err
		}
		if len(images) != len(misses) {
			return nil, stats, fmt.Errorf("visual-exact PowerPoint rendered %d images for %d slides", len(images), len(misses))
		}
		for i, id := range misses {
			fp := fingerprints[id]
			rel := filepath.ToSlash(filepath.Join("visual", id+"-"+fp[:16]+".png"))
			key := cacheKey("visual", id)
			old := manifest.Entries[key]
			if err := atomicCacheFile(filepath.Join(base, filepath.FromSlash(rel)), images[i]); err != nil {
				return nil, stats, err
			}
			manifest.Entries[key] = PowerPointCacheEntry{Mode: "visual", SlideID: id, Fingerprint: fp, File: rel, GeneratedAt: time.Now().UTC()}
			if old.File != "" && old.File != rel {
				_ = os.Remove(filepath.Join(base, filepath.FromSlash(old.File)))
			}
			stats.Misses++
		}
	}
	if err := s.prunePowerPointManifest(deck, base, &manifest); err != nil {
		return nil, stats, err
	}
	if err := writePowerPointManifest(base, manifest); err != nil {
		return nil, stats, err
	}
	out := make([][]byte, 0, len(ids))
	for _, id := range ids {
		entry := manifest.Entries[cacheKey("visual", id)]
		body, err := os.ReadFile(filepath.Join(base, filepath.FromSlash(entry.File)))
		if err != nil {
			return nil, stats, err
		}
		out = append(out, body)
	}
	return out, stats, nil
}

func (s *Server) cachedEditablePowerPointSlides(r *http.Request, deck string, ids []string) ([]studio.PPTXSlide, powerpointCacheStats, error) {
	lock := s.deckExportLock(deck)
	lock.Lock()
	defer lock.Unlock()
	base := s.powerpointCacheDir(deck)
	manifest := readPowerPointManifest(base)
	fingerprints := map[string]string{}
	var misses []string
	stats := powerpointCacheStats{}
	for _, id := range ids {
		fp, err := s.slidePowerPointFingerprint(deck, id, "editable")
		if err != nil {
			return nil, stats, err
		}
		fingerprints[id] = fp
		entry, ok := manifest.Entries[cacheKey("editable", id)]
		if !ok || entry.Fingerprint != fp {
			misses = append(misses, id)
			continue
		}
		var slide studio.PPTXSlide
		body, err := os.ReadFile(filepath.Join(base, filepath.FromSlash(entry.File)))
		if err != nil || json.Unmarshal(body, &slide) != nil || slide.ID == "" {
			misses = append(misses, id)
			continue
		}
		stats.Hits++
	}
	if len(misses) > 0 {
		captured, err := s.powerpointRenderer().Editable(r, deck, misses)
		if err != nil {
			return nil, stats, err
		}
		if len(captured.Slides) != len(misses) {
			return nil, stats, fmt.Errorf("editable PowerPoint captured %d models for %d slides", len(captured.Slides), len(misses))
		}
		for i, id := range misses {
			slide := captured.Slides[i]
			slide.ID = id
			body, err := json.Marshal(slide)
			if err != nil {
				return nil, stats, err
			}
			fp := fingerprints[id]
			rel := filepath.ToSlash(filepath.Join("editable", id+"-"+fp[:16]+".json"))
			key := cacheKey("editable", id)
			old := manifest.Entries[key]
			if err := atomicCacheFile(filepath.Join(base, filepath.FromSlash(rel)), body); err != nil {
				return nil, stats, err
			}
			manifest.Entries[key] = PowerPointCacheEntry{Mode: "editable", SlideID: id, Fingerprint: fp, File: rel, GeneratedAt: time.Now().UTC()}
			if old.File != "" && old.File != rel {
				_ = os.Remove(filepath.Join(base, filepath.FromSlash(old.File)))
			}
			stats.Misses++
		}
	}
	if err := s.prunePowerPointManifest(deck, base, &manifest); err != nil {
		return nil, stats, err
	}
	if err := writePowerPointManifest(base, manifest); err != nil {
		return nil, stats, err
	}
	out := make([]studio.PPTXSlide, 0, len(ids))
	for _, id := range ids {
		entry := manifest.Entries[cacheKey("editable", id)]
		body, err := os.ReadFile(filepath.Join(base, filepath.FromSlash(entry.File)))
		if err != nil {
			return nil, stats, err
		}
		var slide studio.PPTXSlide
		if err := json.Unmarshal(body, &slide); err != nil {
			return nil, stats, err
		}
		out = append(out, slide)
	}
	return out, stats, nil
}

func (s *Server) prunePowerPointManifest(deck, base string, manifest *PowerPointCacheManifest) error {
	ids, err := s.St.SlideIDs(deck)
	if err != nil {
		return err
	}
	valid := map[string]bool{}
	for _, id := range ids {
		fragment, _, err := s.St.ReadSlide(deck, id)
		if err == nil && !studio.SlideParked(fragment) {
			valid[id] = true
		}
	}
	for key, entry := range manifest.Entries {
		if !valid[entry.SlideID] {
			_ = os.Remove(filepath.Join(base, filepath.FromSlash(entry.File)))
			delete(manifest.Entries, key)
		}
	}
	return nil
}
