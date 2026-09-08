package server

import (
	"context"
	"github.com/vessica-labs/vessica-studio/internal/chromium"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHostedAudienceElementsRenderWithoutImageEndpoint(t *testing.T) {
	browser := chromium.Find("")
	if browser == "" {
		t.Skip("Chrome unavailable")
	}
	st := testStudio(t)
	fragment := `<section class="slide"><div data-vstd-audience-share><img data-vstd-audience-qr><span data-vstd-audience-url>old.example/follow</span></div></section>`
	if err := os.WriteFile(filepath.Join(st.Root, "decks", "demo", "slides", "0010-a.html"), []byte(fragment), 0600); err != nil {
		t.Fatal(err)
	}
	link := "https://studio.example/s/ABCDEFG234"
	built, err := st.BuildWithAudienceURL("demo", &link)
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(built)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write(content)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	raw, err := chromium.Evaluate(ctx, browser, server.URL+"/", `(()=>{if(document.readyState!=='complete')return '';const image=document.querySelector('[data-vstd-audience-qr]');if(!image?.complete||image.naturalWidth!==640)return '';return JSON.stringify({url:document.querySelector('[data-vstd-audience-url]').textContent,embedded:image.src.startsWith('data:image/png;base64,')});})()`)
	if err != nil || !strings.Contains(raw, `"embedded":true`) || !strings.Contains(raw, "studio.example/s/ABCDEFG234") {
		t.Fatalf("QR render: %v %s", err, raw)
	}
}
