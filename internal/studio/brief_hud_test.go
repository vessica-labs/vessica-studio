package studio

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/vessica-labs/vessica-studio/internal/chromium"
)

func TestMeetingBriefControlsVisibleInPresentationAndEditor(t *testing.T) {
	browser := chromium.Find("")
	if browser == "" {
		t.Skip("Chrome/Chromium unavailable")
	}
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "studio.yaml"), "theme_default: default\n")
	writeFile(t, filepath.Join(root, "themes", "default", "theme.css"), ".slide{width:1280px;height:720px}")
	writeFile(t, filepath.Join(root, "decks", "demo", "deck.yaml"), "title: Demo\ntheme: default\n")
	writeFile(t, filepath.Join(root, "decks", "demo", "slides", "0010-cover.html"), `<section class="slide">Meeting</section>`)
	st, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	page, err := st.Build("demo")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/d/demo/":
			w.Header().Set("Content-Type", "text/html")
			http.ServeFile(w, r, page)
		case "/api/me":
			_, _ = w.Write([]byte(`{"presenter":true,"editable":true,"start_editing":false,"mode":"studio"}`))
		case "/api/vessica/demo/brief":
			_, _ = w.Write([]byte(`{"generation":"test","revision":1,"markdown":"# Client brief","updated_at":""}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	got, err := chromium.Evaluate(ctx, browser, srv.URL+"/d/demo/", `(()=>{
  if(document.readyState!=='complete'||!window.__vme?.presenter||!window.__vbrief)return '';
  const visible=e=>!!e&&e.getClientRects().length>0&&getComputedStyle(e).visibility!=='hidden';
  const hud=document.querySelector('#kbbtn');
  if(!visible(hud))return 'presentation KB hidden';
  hud.click();
  const dialog=document.querySelector('dialog[aria-label="Deck meeting brief"]');
  if(!dialog?.open)return 'presentation drawer unavailable';
  dialog.close();document.querySelector('#editbtn').click();
  const ribbon=document.querySelector('#editRibbon [data-act="kb"]');
  if(!visible(ribbon))return 'editor KB hidden';
  ribbon.click();
  return dialog.open?'ok':'editor drawer unavailable';
})()`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "ok" {
		t.Fatalf("meeting brief UI: %s", got)
	}
}
