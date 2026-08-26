package server

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/vessica-labs/vessica-studio/internal/studio"
)

var sourceNameRe = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

var sourceExts = map[string]bool{
	".pdf": true, ".ppt": true, ".pptx": true,
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true,
	".doc": true, ".docx": true, ".xls": true, ".xlsx": true, ".csv": true,
}

func (s *Server) handleSourceAttachmentUpload(w http.ResponseWriter, r *http.Request) {
	deck, id := r.PathValue("deck"), r.PathValue("id")
	filename := filepath.Base(strings.TrimSpace(r.URL.Query().Get("filename")))
	ext := strings.ToLower(filepath.Ext(filename))
	if filename == "." || filename == "" || !sourceExts[ext] {
		jsonErr(w, fmt.Errorf("unsupported source attachment type %q", ext), http.StatusBadRequest)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, (50<<20)+1))
	if err != nil || len(body) == 0 {
		jsonErr(w, fmt.Errorf("empty or unreadable source attachment"), http.StatusBadRequest)
		return
	}
	if len(body) > 50<<20 {
		jsonErr(w, fmt.Errorf("source attachment exceeds 50MB Git-safe limit"), http.StatusRequestEntityTooLarge)
		return
	}
	h := sha256.Sum256(body)
	hash := hex.EncodeToString(h[:4])
	stem := strings.TrimSuffix(filename, filepath.Ext(filename))
	stem = strings.Trim(sourceNameRe.ReplaceAllString(stem, "-"), "-._")
	if stem == "" {
		stem = "source"
	}
	if len(stem) > 50 {
		stem = stem[:50]
	}
	stored := id + "-" + stem + "-" + hash + ext
	dir := filepath.Join(s.St.DeckDir(deck), "sources")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		jsonErr(w, err, http.StatusInternalServerError)
		return
	}
	if err := os.WriteFile(filepath.Join(dir, stored), body, 0o644); err != nil {
		jsonErr(w, err, http.StatusInternalServerError)
		return
	}
	mediaType := mime.TypeByExtension(ext)
	if mediaType == "" {
		mediaType = http.DetectContentType(body)
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	attachment := studio.CompanionAttachment{
		Name: filename, Path: "sources/" + stored, MediaType: mediaType, Page: page,
		URL: "/api/deck/" + deck + "/source/" + stored,
	}
	if err := s.St.AddCompanionAttachment(deck, id, attachment); err != nil {
		os.Remove(filepath.Join(dir, stored))
		jsonErr(w, err, http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"status": "ok", "attachment": attachment})
}

func (s *Server) handleSourceAttachment(w http.ResponseWriter, r *http.Request) {
	deck, name := r.PathValue("deck"), r.PathValue("name")
	allowed := s.canView(r, deck)
	if s.Collab != nil && !allowed {
		ps, ok := s.nativePlayerSessionForDeck(r, deck)
		allowed = ok && s.Collab.Can(r.Context(), ps.User.ID, ps.Deck, "view")
	}
	if !allowed {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !studio.ValidDeckName(deck) || filepath.Base(name) != name || sourceNameRe.MatchString(name) {
		http.Error(w, "invalid source attachment", http.StatusBadRequest)
		return
	}
	path := filepath.Join(s.St.DeckDir(deck), "sources", name)
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", name))
	http.ServeFile(w, r, path)
}
