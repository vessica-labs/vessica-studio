package server

import (
	"image/png"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEditorRenderPrintRequiresLiveKey(t *testing.T) {
	s := New(testStudio(t), ModeStudio)
	routes := editorRenderRoutes(s.Routes(), "demo")
	key := s.putPrintJob("private print")
	for _, tc := range []struct {
		key  string
		code int
	}{{"", 403}, {"invalid", 403}, {key, 200}} {
		w := httptest.NewRecorder()
		routes.ServeHTTP(w, httptest.NewRequest("GET", "/api/deck/demo/print.html?key="+tc.key, nil))
		if w.Code != tc.code {
			t.Fatalf("print status %d, want %d", w.Code, tc.code)
		}
	}
	s.dropPrintJob(key)
	w := httptest.NewRecorder()
	routes.ServeHTTP(w, httptest.NewRequest("GET", "/api/deck/demo/print.html?key="+key, nil))
	if w.Code != 403 {
		t.Fatal("expired print key accepted")
	}
}

func TestEditorSessionRealRaster(t *testing.T) {
	if os.Getenv("VSTD_TEST_BROWSER_RENDER") != "1" {
		t.Skip("opt-in real Chromium and Poppler check")
	}
	if findChrome() == "" {
		t.Fatal("Chromium unavailable")
	}
	if _, err := exec.LookPath("pdftoppm"); err != nil {
		t.Fatal(err)
	}
	st := testStudio(t)
	if err := os.WriteFile(filepath.Join(st.Root, "decks/demo/slides/0010-a.html"), []byte(`<section class="slide" style="background:rgb(0,128,0)"><h1>Real cover</h1></section>`), 0600); err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("x", 64)
	h, err := NewEditorSession(st, EditorSessionOptions{Deck: "demo", Token: token, ExpiresAt: time.Now().Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	req, _ := http.NewRequest("GET", server.URL+"/api/app/decks/demo/thumbnail.png", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Timeout: 40 * time.Second}
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("raster HTTP %d", response.StatusCode)
	}
	im, err := png.Decode(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	r, g, b, _ := im.At(im.Bounds().Dx()/2, im.Bounds().Dy()/2).RGBA()
	if g < 20000 || r > 1000 || b > 1000 {
		t.Fatalf("cover not rendered: rgb %d %d %d", r, g, b)
	}
}

func TestEditorRenderSurfaceIsReadOnlyAndDeckScoped(t *testing.T) {
	routes := editorRenderRoutes(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }), "demo")
	for _, p := range []string{"/api/deck/demo/print.html?key=example", "/library/image.png", "/assets/video/demo/poster", "/api/deck/demo/source/evidence.pdf"} {
		w := httptest.NewRecorder()
		routes.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
		if w.Code != 204 {
			t.Fatalf("allowed %s: %d", p, w.Code)
		}
	}
	for _, p := range []string{"/api/editor/snapshot", "/api/deck/foreign/print.html", "/api/deck/demo/slide/a", "/api/app/decks/demo/thumbnail.png", "/library/../studio.yaml", "/api/realtime/token"} {
		w := httptest.NewRecorder()
		routes.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
		if w.Code != 404 {
			t.Fatalf("excluded %s: %d", p, w.Code)
		}
	}
	w := httptest.NewRecorder()
	routes.ServeHTTP(w, httptest.NewRequest("PUT", "/library/image.png", nil))
	if w.Code != 404 {
		t.Fatal("mutation allowed")
	}
}

func TestEditorRenderUsesTemporaryLoopbackWithoutSessionCredential(t *testing.T) {
	var address string
	routes := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/deck/demo/print.html" {
			_, _ = w.Write([]byte("print page"))
			return
		}
		address = r.Context().Value(http.LocalAddrContextKey).(net.Addr).String()
		client := &http.Client{Timeout: time.Second}
		response, err := client.Get("http://" + address + "/api/deck/demo/print.html?key=fixture")
		if err != nil {
			t.Error(err)
			return
		}
		defer response.Body.Close()
		_, _ = io.Copy(w, response.Body)
	})
	w := httptest.NewRecorder()
	serveEditorRender(w, httptest.NewRequest("GET", "/api/app/decks/demo/thumbnail.png", nil), routes, "demo")
	if w.Body.String() != "print page" {
		t.Fatalf("nested render: %q", w.Body.String())
	}
	conn, err := net.DialTimeout("tcp", address, time.Second)
	if err == nil {
		conn.Close()
		t.Fatal("temporary listener remained open")
	}
}
