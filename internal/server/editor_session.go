package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/vessica-labs/vessica-studio/internal/studio"
)

// EditorSessionOptions describes a single disposable editing process. Token is
// a gateway-to-engine credential, never a browser credential. The caller owns
// tenant authorization, isolation, revision commits and process destruction.
type EditorSessionOptions struct {
	Deck      string
	Token     string
	ExpiresAt time.Time
}

// NewEditorSession exposes the engine-owned HUD and structured editing routes
// for exactly one deck. It deliberately does not start Git sync, collaboration,
// agent workers, or account/provider administration. Run only in an isolated
// content sandbox, behind a gateway that commits snapshots before acknowledging
// saves to the browser. Local mutation alone is not a cloud commit.
func NewEditorSession(st *studio.Studio, options EditorSessionOptions) (http.Handler, error) {
	if !studio.ValidDeckName(options.Deck) || len(options.Token) < 32 || len(options.Token) > 256 || strings.ContainsAny(options.Token, " \r\n\t") || !options.ExpiresAt.After(time.Now()) || options.ExpiresAt.After(time.Now().Add(time.Hour+time.Second)) {
		return nil, errors.New("invalid editor session configuration")
	}
	decks, err := st.ListDecks()
	if err != nil || len(decks) != 1 || decks[0] != options.Deck {
		return nil, errors.New("editor session requires exactly its selected deck")
	}
	if _, err := studio.CloudContent(st.Root); err != nil {
		return nil, errors.New("invalid editor session content")
	}
	server := New(st, ModeStudio)
	routes := server.Routes()
	var mu sync.Mutex
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !time.Now().Before(options.ExpiresAt) || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+options.Token)) != 1 {
			http.Error(w, "editor session unavailable", http.StatusUnauthorized)
			return
		}
		// Reject ambiguous paths before ServeMux can canonicalize or redirect them.
		p := r.URL.Path
		if strings.Contains(p, "\\") || strings.Contains(r.URL.RawPath, "%") || path.Clean(p) != strings.TrimSuffix(p, "/") {
			http.NotFound(w, r)
			return
		}
		if !editorSessionRoute(r.Method, p, options.Deck) {
			http.NotFound(w, r)
			return
		}
		ctx, cancel := context.WithDeadline(r.Context(), options.ExpiresAt)
		defer cancel()
		r = r.WithContext(ctx)
		// SSE observes mutations and must never hold the snapshot/write lock.
		if p == "/api/events" {
			routes.ServeHTTP(w, r)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if p == "/api/app/decks/"+options.Deck+"/thumbnail.png" || p == "/api/deck/"+options.Deck+"/export.pdf" || p == "/api/deck/"+options.Deck+"/export.pptx" {
			serveEditorRender(w, r, routes, options.Deck)
			return
		}
		switch p {
		case "/api/me":
			writeJSON(w, map[string]any{"mode": "studio", "presenter": true, "editable": true, "start_editing": true, "capabilities": map[string]bool{"transfer_slides": false}})
		case "/api/editor/snapshot":
			if r.Method == "PUT" {
				var payload struct {
					Files []struct {
						Path    string `json:"path"`
						Content []byte `json:"content"`
					} `json:"files"`
				}
				if json.NewDecoder(io.LimitReader(r.Body, 180<<20)).Decode(&payload) != nil {
					http.Error(w, "invalid checkpoint", 400)
					return
				}
				files := make([]studio.ContentFile, len(payload.Files))
				for i, f := range payload.Files {
					files[i] = studio.ContentFile{Path: f.Path, Content: f.Content, Mode: 0644}
				}
				if err := studio.ApplyCloudContent(st.Root, files); err != nil {
					http.Error(w, "checkpoint restore failed", 500)
					return
				}
				if _, err := st.Build(options.Deck); err != nil {
					http.Error(w, "checkpoint build failed", 500)
					return
				}
				writeJSON(w, map[string]bool{"ok": true})
				return
			}
			snapshot, err := studio.CloudContent(st.Root)
			if err != nil {
				http.Error(w, "editor snapshot unavailable", 500)
				return
			}
			type file struct {
				Path    string `json:"path"`
				Content []byte `json:"content"`
				Mode    uint32 `json:"mode"`
			}
			files := make([]file, len(snapshot.Files))
			for i, f := range snapshot.Files {
				files[i] = file{f.Path, f.Content, f.Mode}
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(struct {
				Protocol int    `json:"protocol"`
				Digest   string `json:"digest"`
				Files    []file `json:"files"`
			}{1, snapshot.Digest, files})
		default:
			r.Body = http.MaxBytesReader(w, r.Body, 16<<20)
			routes.ServeHTTP(w, r)
		}
	}), nil
}

func editorSessionRoute(method, p, deck string) bool {
	if method == "PUT" && p == "/api/editor/snapshot" {
		return true
	}
	if method == "GET" {
		// Raster-only catalog access is scoped to this session's single deck.
		// Cloud thumbnail workers use the existing engine renderer in isolation.
		if p == "/api/app/decks/"+deck+"/thumbnail.png" {
			return true
		}
		if p == "/api/me" || p == "/api/events" || p == "/api/editor/snapshot" || p == "/d/"+deck+"/" {
			return true
		}
		if strings.HasPrefix(p, "/library/") || strings.HasPrefix(p, "/assets/video/") {
			return true
		}
	}
	prefix := "/api/deck/" + deck + "/"
	if !strings.HasPrefix(p, prefix) {
		return method == "POST" && (p == "/api/asset/image" || p == "/api/asset/video")
	}
	suffix := strings.TrimPrefix(p, prefix)
	if method == "GET" {
		return suffix == "status" || suffix == "export.pdf" || suffix == "export.pptx" || suffix == "print.html" || strings.HasPrefix(suffix, "slide/") || strings.HasPrefix(suffix, "source/")
	}
	if method == "PUT" {
		return strings.HasPrefix(suffix, "slide/") && (strings.HasSuffix(suffix, "/fragment") || strings.Contains(suffix, "/companion") || strings.HasSuffix(suffix, "/title"))
	}
	if method == "POST" {
		return suffix == "slides" || strings.HasPrefix(suffix, "slide/") && (strings.HasSuffix(suffix, "/move") || strings.HasSuffix(suffix, "/attachment") || strings.HasSuffix(suffix, "/detach-link") || strings.HasSuffix(suffix, "/refresh-link"))
	}
	return false
}
