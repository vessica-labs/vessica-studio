package server

// The cloud visual editor uses this bounded data-only adapter. It does not
// start a listener, run browser scripts, resolve credentials, or invoke tools.
import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/vessica-labs/vessica-studio/internal/studio"
)

type EditorTransformInput struct {
	Deck    string               `json:"deck"`
	Files   []studio.ContentFile `json:"files"`
	Method  string               `json:"method"`
	Path    string               `json:"path"`
	Headers map[string]string    `json:"headers"`
	Body    []byte               `json:"body"`
}
type EditorTransformResult struct {
	Status  int                  `json:"status"`
	Headers map[string]string    `json:"headers"`
	Body    []byte               `json:"body"`
	Files   []studio.ContentFile `json:"-"`
}

func TransformEditor(ctx context.Context, in EditorTransformInput) (EditorTransformResult, error) {
	var result EditorTransformResult
	u, err := url.ParseRequestURI(in.Path)
	if err != nil || u.IsAbs() || u.Host != "" || u.RawPath != "" || strings.ContainsAny(u.Path, "\\\x00") || path.Clean(u.Path) != strings.TrimSuffix(u.Path, "/") || !studio.ValidDeckName(in.Deck) || !transformRoute(in.Method, u.Path, in.Deck) {
		return result, errors.New("unsupported visual editor operation")
	}
	if len(in.Body) > 16<<20 {
		return result, errors.New("editor body limit")
	}
	var total int
	for _, f := range in.Files {
		total += len(f.Content)
	}
	if len(in.Files) > 2000 || total > 20<<20 {
		return result, errors.New("editor snapshot limit")
	}
	if err = studio.ValidateCloudContent(in.Files); err != nil {
		return result, err
	}
	root, err := os.MkdirTemp("", "vstd-editor-transform-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(root)
	if err = studio.ApplyCloudContent(root, in.Files); err != nil {
		return result, err
	}
	st, err := studio.Open(root)
	if err != nil {
		return result, err
	}
	decks, err := st.ListDecks()
	if err != nil || len(decks) != 1 || decks[0] != in.Deck {
		return result, errors.New("editor requires selected deck only")
	}
	// Deliberately do not call New: no auth/provider initialization is needed for
	// an already-authorized data transform, and no environment secrets are read.
	s := &Server{St: st, Mode: ModeStudio, subs: map[chan string]bool{}}
	r, err := http.NewRequestWithContext(ctx, in.Method, in.Path, bytes.NewReader(in.Body))
	if err != nil {
		return result, err
	}
	for k, v := range in.Headers {
		if strings.EqualFold(k, "content-type") || strings.EqualFold(k, "range") {
			r.Header.Set(k, v)
		}
	}
	w := httptest.NewRecorder()
	switch {
	case u.Path == "/api/me":
		writeJSON(w, map[string]any{"mode": "studio", "presenter": true, "editable": true, "start_editing": true, "capabilities": map[string]bool{"transfer_slides": false}})
	case u.Path == "/d/"+in.Deck+"/":
		built, e := st.Build(in.Deck)
		if e != nil {
			return result, e
		}
		content, e := os.ReadFile(built)
		if e != nil {
			return result, e
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(content)
	case strings.HasPrefix(u.Path, "/library/"):
		found := false
		for _, f := range in.Files {
			if "/"+f.Path == u.Path {
				http.ServeContent(w, r, path.Base(f.Path), time.Time{}, bytes.NewReader(f.Content))
				found = true
				break
			}
		}
		if !found {
			http.NotFound(w, r)
		}
	default:
		s.Routes().ServeHTTP(w, r)
	}
	if w.Body.Len() > 32<<20 {
		return result, errors.New("editor response limit")
	}
	snapshot, err := studio.CloudContent(root)
	if err != nil {
		return result, err
	}
	result = EditorTransformResult{Status: w.Code, Headers: map[string]string{}, Body: w.Body.Bytes(), Files: snapshot.Files}
	for k, v := range w.Header() {
		result.Headers[strings.ToLower(k)] = strings.Join(v, ", ")
	}
	return result, nil
}

func transformRoute(method, p, deck string) bool {
	if method == "GET" && (p == "/api/me" || p == "/d/"+deck+"/" || strings.HasPrefix(p, "/library/")) {
		return true
	}
	prefix := "/api/deck/" + deck + "/"
	if !strings.HasPrefix(p, prefix) {
		return false
	}
	suffix := strings.TrimPrefix(p, prefix)
	parts := strings.Split(suffix, "/")
	if method == "GET" {
		return suffix == "status" || len(parts) == 2 && (parts[0] == "slide" && studio.ValidSlideID(parts[1]) || parts[0] == "source")
	}
	if method == "POST" && suffix == "slides" {
		return true
	}
	if len(parts) < 3 || parts[0] != "slide" || !studio.ValidSlideID(parts[1]) {
		return false
	}
	if method == "PUT" {
		return len(parts) == 3 && (parts[2] == "fragment" || parts[2] == "title" || parts[2] == "companion") || len(parts) == 4 && parts[2] == "companion"
	}
	return method == "POST" && len(parts) == 3 && (parts[2] == "move" || parts[2] == "attachment")
}

func (r EditorTransformResult) MarshalJSON() ([]byte, error) {
	type file struct {
		Path    string `json:"path"`
		Content []byte `json:"content"`
	}
	files := make([]file, len(r.Files))
	for i, f := range r.Files {
		files[i] = file{f.Path, f.Content}
	}
	return json.Marshal(struct {
		Status  int               `json:"status"`
		Headers map[string]string `json:"headers"`
		Body    []byte            `json:"body"`
		Files   []file            `json:"files"`
	}{r.Status, r.Headers, r.Body, files})
}
