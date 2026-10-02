package server

import (
	"encoding/hex"
	"github.com/vessica-labs/vessica-studio/internal/bundle"
	"github.com/vessica-labs/vessica-studio/internal/library"
	"net/http"
	"os"
	"path/filepath"
)

// Archives are data responses only; no ZIP member is ever served as executable
// HTML on the authenticated engine origin.
func (s *Server) handleBundle(w http.ResponseWriter, r *http.Request) {
	if !s.hasAnyAccess(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	id := r.PathValue("id")
	if !bundle.ID.MatchString(id) {
		http.NotFound(w, r)
		return
	}
	m, err := library.Load(s.libDir())
	if err != nil {
		http.NotFound(w, r)
		return
	}
	for _, a := range m.Bundles {
		if a.ID != id {
			continue
		}
		_, hashErr := hex.DecodeString(a.Hash)
		if a.File != "bundle/"+a.Hash+".zip" || len(a.Hash) != 64 || hashErr != nil {
			http.NotFound(w, r)
			return
		}
		file, err := os.Open(filepath.Join(s.libDir(), filepath.FromSlash(a.File)))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || info.Size() != a.Bytes {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Disposition", "attachment")
		w.Header().Set("Cache-Control", "private, no-store")
		http.ServeContent(w, r, id+".zip", info.ModTime(), file)
		return
	}
	http.NotFound(w, r)
}
