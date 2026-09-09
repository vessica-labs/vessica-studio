package server

// The cloud visual editor uses this bounded data-only adapter. It does not
// start a listener, run browser scripts, resolve credentials, or invoke tools.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/skip2/go-qrcode"
	"github.com/vessica-labs/vessica-studio/internal/library"
	"github.com/vessica-labs/vessica-studio/internal/studio"
)

type EditorTransformInput struct {
	AudienceURL *string              `json:"audience_url,omitempty"`
	Root        string               `json:"root,omitempty"`
	Delta       bool                 `json:"delta,omitempty"`
	Deck        string               `json:"deck"`
	Files       []studio.ContentFile `json:"files"`
	Method      string               `json:"method"`
	Path        string               `json:"path"`
	Headers     map[string]string    `json:"headers"`
	Body        []byte               `json:"body"`
}
type EditorTransformResult struct {
	Delta   bool                 `json:"delta,omitempty"`
	Deleted []string             `json:"deleted,omitempty"`
	Status  int                  `json:"status"`
	Headers map[string]string    `json:"headers"`
	Body    []byte               `json:"body"`
	Files   []studio.ContentFile `json:"-"`
}

func TransformEditor(ctx context.Context, in EditorTransformInput) (EditorTransformResult, error) {
	var result EditorTransformResult
	sourceRoot := in.Root
	if in.AudienceURL != nil && *in.AudienceURL != "" {
		audience, e := url.Parse(*in.AudienceURL)
		if e != nil || audience.Scheme != "https" || audience.Host == "" || audience.User != nil || audience.Fragment != "" || len(*in.AudienceURL) > 2048 {
			return result, errors.New("invalid audience URL")
		}
	}
	if in.Root != "" {
		if len(in.Files) != 0 {
			return result, errors.New("root and files are mutually exclusive")
		}
		snapshot, err := studio.CloudContent(in.Root)
		if err != nil {
			return result, err
		}
		in.Files = snapshot.Files
	}
	u, err := url.ParseRequestURI(in.Path)
	if err != nil || u.IsAbs() || u.Host != "" || u.RawPath != "" || strings.ContainsAny(u.Path, "\\\x00") || path.Clean(u.Path) != strings.TrimSuffix(u.Path, "/") || !studio.ValidDeckName(in.Deck) {
		return result, errors.New("unsupported visual editor operation")
	}
	if !transformRoute(in.Method, u.Path, in.Deck) {
		return EditorTransformResult{Status: http.StatusNotFound, Headers: map[string]string{"content-type": "application/json"}, Body: []byte(`{"error":"operation unavailable in visual editor"}`), Files: in.Files}, nil
	}
	if len(in.Body) > 16<<20 {
		return result, errors.New("editor body limit")
	}
	var total int
	for _, f := range in.Files {
		total += len(f.Content)
	}
	// File-backed delta transforms do not send unchanged media through JSON.
	// Keep the inline wire budget while matching the canonical snapshot bound
	// for the file-backed Cloud editor.
	maxTotal := 20 << 20
	if in.Root != "" && in.Delta {
		maxTotal = 128 << 20
	}
	if len(in.Files) > 2000 || total > maxTotal {
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
	// CloudContent deliberately omits large video blobs. A file-backed transform
	// may still serve a video materialized by the trusted Cloud runtime, so copy
	// only regular, manifest-addressed, integrity-checked video files into the
	// disposable transform root.
	if sourceRoot != "" {
		if err = copyEditorTransformVideos(sourceRoot, root); err != nil {
			return result, err
		}
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
		built, e := st.BuildWithAudienceURL(in.Deck, in.AudienceURL)
		if e != nil {
			return result, e
		}
		content, e := os.ReadFile(built)
		if e != nil {
			return result, e
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(content)
	case u.Path == "/api/deck/"+in.Deck+"/share-qr.png":
		if in.AudienceURL == nil || *in.AudienceURL == "" {
			w.WriteHeader(http.StatusNotFound)
		} else {
			png, e := qrcode.Encode(*in.AudienceURL, qrcode.Medium, 640)
			if e != nil {
				return result, e
			}
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("Cache-Control", "no-store")
			_, _ = w.Write(png)
		}
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
	if in.Delta {
		result.Delta = true
		result.Files = nil
		before := make(map[string][]byte, len(in.Files))
		for _, f := range in.Files {
			before[f.Path] = f.Content
		}
		for _, f := range snapshot.Files {
			old, exists := before[f.Path]
			if !exists || !bytes.Equal(old, f.Content) {
				result.Files = append(result.Files, f)
			}
			delete(before, f.Path)
		}
		for _, f := range in.Files {
			if _, exists := before[f.Path]; exists {
				result.Deleted = append(result.Deleted, f.Path)
			}
		}
	}
	return result, nil
}

var editorTransformVideoHash = regexp.MustCompile(`^[a-f0-9]{64}$`)

func copyEditorTransformVideos(sourceRoot, targetRoot string) error {
	manifest, err := library.Load(filepath.Join(sourceRoot, "library"))
	if err != nil {
		return err
	}
	for _, asset := range manifest.Videos {
		if !editorTransformVideoHash.MatchString(asset.Hash) || asset.File != "video/"+asset.Hash+".mp4" || asset.Bytes <= 0 || asset.Bytes > 512<<20 {
			return errors.New("invalid editor video reference")
		}
		source := filepath.Join(sourceRoot, "library", filepath.FromSlash(asset.File))
		info, err := os.Lstat(source)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() || info.Size() != asset.Bytes {
			return errors.New("invalid editor video file")
		}
		target := filepath.Join(targetRoot, "library", filepath.FromSlash(asset.File))
		if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		input, err := os.Open(source)
		if err != nil {
			return err
		}
		output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			input.Close()
			return err
		}
		digest := sha256.New()
		written, copyErr := io.Copy(io.MultiWriter(output, digest), input)
		closeErr := output.Close()
		input.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if written != asset.Bytes || fmt.Sprintf("%x", digest.Sum(nil)) != asset.Hash {
			return errors.New("editor video integrity failure")
		}
	}
	return nil
}

func transformRoute(method, p, deck string) bool {
	if method == "GET" && (p == "/api/me" || p == "/d/"+deck+"/" || strings.HasPrefix(p, "/library/") || strings.HasPrefix(p, "/assets/video/")) {
		return true
	}
	prefix := "/api/deck/" + deck + "/"
	if !strings.HasPrefix(p, prefix) {
		return false
	}
	suffix := strings.TrimPrefix(p, prefix)
	parts := strings.Split(suffix, "/")
	if method == "GET" {
		return suffix == "share-qr.png" || suffix == "status" || len(parts) == 2 && (parts[0] == "slide" && studio.ValidSlideID(parts[1]) || parts[0] == "source")
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
		Delta   bool              `json:"delta,omitempty"`
		Deleted []string          `json:"deleted,omitempty"`
		Status  int               `json:"status"`
		Headers map[string]string `json:"headers"`
		Body    []byte            `json:"body"`
		Files   []file            `json:"files"`
	}{r.Delta, r.Deleted, r.Status, r.Headers, r.Body, files})
}
