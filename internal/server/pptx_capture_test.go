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
			for _, p := range e.Paragraphs {
				for _, run := range p.Runs {
					if run.Text == "Bold text" && run.Bold {
						bold = true
					}
					if strings.TrimSpace(run.Text) == "Normal" && !run.Bold && e.X >= 85 {
						normal = true
					}
				}
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

func TestPPTXCaptureRealBrowserEditableTextAndClippedCover(t *testing.T) {
	if os.Getenv("VSTD_TEST_BROWSER_RENDER") != "1" {
		t.Skip("opt-in real Chromium capture")
	}
	st := testStudio(t)
	const fragment = `<section class="slide">
<style>
.wave { position:absolute; left:200px; top:0; width:1080px; height:720px; background:#fff;
  object-fit:cover; clip-path:polygon(0 0,100% 0,100% 100%,70% 100%) }
.accent { position:absolute; left:400px; top:440px; width:500px; height:280px; background:#28b16a;
  clip-path:polygon(calc(50% - 10px) 0,100% 100%,0 100%) }
.copy { position:absolute; left:70px; top:200px; width:190px; padding:0 10px; font:20px/24px Arial; text-align:center }
.copy .line { display:block }
.preserved { position:absolute; left:70px; top:450px; white-space:pre-wrap; font:20px/24px Arial }
</style>
<img class="wave" alt="Cover art" src="data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=">
<div class="accent"></div>
<div class="copy" data-edit="text">This sentence wraps across <strong>several lines</strong> with editable text.<br>Another line<span class="line"><u><em>Styled ending</em></u></span></div>
<p style="position:absolute;left:70px;top:400px">Separate caption</p>
<p class="preserved" data-edit="text">First line

Third line</p>
</section>`
	if err := os.WriteFile(st.SlidePath("demo", "0010-a", ".html"), []byte(fragment), 0600); err != nil {
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
	var layers []string
	for _, e := range model.Slides[0].Elements {
		switch {
		case e.Kind == "image" && e.Name == "Cover art":
			data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(e.ImageData, "data:image/png;base64,"))
			if err != nil {
				t.Fatal(err)
			}
			im, err := png.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			_, _, _, outside := im.At(10, 680).RGBA()
			_, _, _, inside := im.At(1000, 680).RGBA()
			if outside != 0 || inside == 0 {
				t.Fatal("cover image lost its diagonal crop")
			}
			layers = append(layers, "art")
		case e.Name == "accent":
			if e.Kind != "path" || !e.Closed || len(e.Points) != 3 || e.Points[0].X != 640 || e.Points[0].Y != 440 {
				t.Fatalf("accent lost editable polygon: %+v", e)
			}
			layers = append(layers, "accent")
		case e.Kind == "text" && e.Name == "copy":
			if e.NoWrap || e.Align != "center" || e.X != 80 || e.W != 170 || e.H <= 100 || len(e.Paragraphs) != 3 {
				t.Fatalf("text box lost layout: %+v", e)
			}
			first := e.Paragraphs[0]
			if first.LineHeight != 24 || len(first.Runs) != 3 ||
				first.Runs[0].Text != "This sentence wraps across " ||
				first.Runs[1].Text != "several lines" || !first.Runs[1].Bold ||
				first.Runs[2].Text != " with editable text." ||
				e.Paragraphs[1].Runs[0].Text != "Another line" ||
				!e.Paragraphs[2].Runs[0].Italic || !e.Paragraphs[2].Runs[0].Underline {
				t.Fatalf("paragraphs lost text or formatting: %+v", e.Paragraphs)
			}
			layers = append(layers, "copy")
		case e.Kind == "text" && e.Name == "preserved":
			if len(e.Paragraphs) != 3 || len(e.Paragraphs[1].Runs) != 0 ||
				e.Paragraphs[0].Runs[0].Text != "First line" || e.Paragraphs[2].Runs[0].Text != "Third line" {
				t.Fatalf("text box lost its preserved newline or blank line: %+v", e)
			}
			layers = append(layers, "preserved")
		case e.Kind == "text":
			layers = append(layers, e.Name)
		}
	}
	if strings.Join(layers, ",") != "art,accent,copy,P,preserved" {
		t.Fatalf("unexpected layers or additional text boxes: %v", layers)
	}
}
