package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPPTXCaptureRealBrowserGeometry(t *testing.T) {
	if os.Getenv("VSTD_TEST_BROWSER_RENDER") != "1" {
		t.Skip("opt-in real Chromium capture")
	}
	browser := findChrome()
	if browser == "" {
		t.Fatal("Chromium unavailable")
	}
	html := `<!doctype html><html><head><style>
 body{margin:0}.vstd-page,.slide{width:1280px;height:720px;position:relative}.slide{background:#123;color:white;font:20px Arial}
 .card{position:absolute;left:70px;top:80px;width:260px;padding:15px;border-top:2px solid red;background:linear-gradient(90deg,rgba(0,255,0,.2),transparent)}
 .card::before{content:"";position:absolute;left:3px;top:5px;width:7px;height:7px;background:red;border-radius:50%}
 </style></head><body><div class="vstd-page"><section class="slide" data-vstd="sample"><div class="card">Normal <b>Bold text</b><span style="display:block">Second line</span></div><svg style="position:absolute;left:420px;top:200px" width="100" height="100" viewBox="0 0 50 50"><defs><linearGradient id="fade"><stop offset="0" stop-color="#0f0"/><stop offset="1" stop-color="#0f0" stop-opacity="0"/></linearGradient></defs><rect x="0" y="35" width="50" height="15" fill="url(#fade)"/><path d="M5 10c0 -3 10 -5 15 0z M30 20q5 -5 10 0" fill="#ff0000" stroke="#0f0" stroke-width="2" stroke-dasharray="4 2"/></svg><video style="position:absolute;left:600px;top:100px;width:200px;height:100px" data-vstd-video="test-video" poster="data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="></video><div class="circle" style="position:absolute;left:850px;top:100px;width:100px;height:100px;border:4px solid lime;border-radius:50%;overflow:hidden"><img alt="circle crop" style="width:100%;height:100%" src="data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="/></div></section></div></body></html>`
	st := testStudio(t)
	start := strings.Index(html, "<style>")
	end := strings.Index(html, "</style>") + len("</style>")
	section := html[strings.Index(html, `<section class="slide"`) : strings.Index(html, "</section>")+len("</section>")]
	if err := os.WriteFile(st.SlidePath("demo", "0010-a", ".html"), []byte(html[start:end]+section), 0600); err != nil {
		t.Fatal(err)
	}
	s := New(st, ModeStudio)
	server := httptest.NewServer(s.Routes())
	defer server.Close()
	r := httptest.NewRequest(http.MethodGet, server.URL+"/api/deck/demo/export.pptx", nil)
	ctx, cancel := context.WithTimeout(context.WithValue(r.Context(), http.LocalAddrContextKey, server.Listener.Addr()), 45*time.Second)
	defer cancel()
	model, err := s.capturePPTXDeck(r.WithContext(ctx), "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(model.Slides) != 1 {
		t.Fatal("slide lost")
	}
	var bold, normal, bullet, background, border, path, video, gradient, circle, cropped bool
	for _, e := range model.Slides[0].Elements {
		if len(e.GradientStops) == 2 && e.GradientStops[0].Opacity == 1 && e.GradientStops[1].Opacity == 0 {
			gradient = true
		}
		if e.Kind == "ellipse" && e.Name == "circle" && e.W == 100 && e.H == 100 {
			circle = true
		}
		if e.Kind == "image" && e.Name == "circle crop" {
			data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(e.ImageData, "data:image/png;base64,"))
			if err != nil {
				t.Fatal(err)
			}
			im, err := png.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			_, _, _, corner := im.At(0, 0).RGBA()
			_, _, _, center := im.At(50, 50).RGBA()
			cropped = corner == 0 && center > 0
		}
		switch e.Kind {
		case "text":
			if e.Text == "Bold text" && e.Bold {
				bold = true
			}
			if strings.TrimSpace(e.Text) == "Normal" && !e.Bold && e.X >= 85 {
				normal = true
			}
		case "ellipse", "roundRect":
			if e.W == 7 && e.H == 7 {
				bullet = true
			}
		case "image":
			if e.Name == "CSS background" && len(e.ImageData) > 50 {
				background = true
			}
		case "path":
			if e.Name == "CSS Top border" {
				border = true
			}
			if len(e.Commands) == 5 && len(e.DashArray) == 2 && e.StrokeWidth == 4 {
				path = true
			}
		case "video":
			if e.VideoID == "test-video" && len(e.ImageData) > 50 {
				video = true
			}
		}
	}
	if !bold || !normal || !bullet || !background || !border || !path || !video || !gradient || !circle || !cropped {
		t.Fatalf("captured contracts: bold=%v padding=%v bullet=%v bg=%v border=%v bezier=%v video=%v gradient=%v circle=%v crop=%v", bold, normal, bullet, background, border, path, video, gradient, circle, cropped)
	}
}

func TestPPTXCaptureRealBrowserEditorSession(t *testing.T) {
	if os.Getenv("VSTD_TEST_BROWSER_RENDER") != "1" {
		t.Skip("opt-in real Chromium capture")
	}
	st := testStudio(t)
	if err := os.WriteFile(st.SlidePath("demo", "0010-a", ".html"), []byte(`<section class="slide"><h1>Authorized editable capture</h1><div style="background:linear-gradient(90deg,red,blue);box-shadow:0 4px 12px #888;width:300px;height:200px"></div></section>`), 0600); err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("a", 64)
	handler, err := NewEditorSession(st, EditorSessionOptions{Deck: "demo", Token: token, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	req, _ := http.NewRequest("GET", server.URL+"/api/deck/demo/export.pptx?mode=editable&media=plan", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	response, err := (&http.Client{Timeout: 45 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	bytes, _ := io.ReadAll(response.Body)
	if response.StatusCode != 200 || !strings.Contains(string(bytes), `"videos":[]`) {
		t.Fatalf("isolated capture %d: %s", response.StatusCode, bytes)
	}
	denied, err := http.Get(server.URL + "/api/deck/demo/export.pptx?mode=editable")
	if err != nil {
		t.Fatal(err)
	}
	denied.Body.Close()
	if denied.StatusCode != 401 {
		t.Fatal("missing credential accepted")
	}
}
