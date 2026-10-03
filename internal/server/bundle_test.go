package server

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/vessica-labs/vessica-studio/internal/bundle"
	"github.com/vessica-labs/vessica-studio/internal/chromium"
	"github.com/vessica-labs/vessica-studio/internal/studio"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestBundleLaunchIsolationAndLifecycle(t *testing.T) {
	for _, published := range []bool{false, true} {
		t.Run(fmt.Sprint(published), func(t *testing.T) { testBundleLifecycle(t, published) })
	}
}
func testBundleLifecycle(t *testing.T, published bool) {
	browser := chromium.Find("")
	if browser == "" {
		t.Skip("Chrome unavailable")
	}
	st := testStudio(t)
	var packed bytes.Buffer
	w := zip.NewWriter(&packed)
	files := map[string]string{
		"index.html":       `<script>(async()=>{const text=await(await fetch('data/message.txt')).text();let isolated=false;try{parent.document.body}catch(e){isolated=true};let blocked=false;try{await fetch('/api/me')}catch(e){blocked=true};parent.postMessage({type:'bundle-fixture',text,isolated,blocked},'*')})()</script>`,
		"data/message.txt": "real bundled data",
	}
	for name, value := range files {
		f, e := w.Create(name)
		if e != nil {
			t.Fatal(e)
		}
		f.Write([]byte(value))
	}
	w.Close()
	zipPath := filepath.Join(t.TempDir(), "demo.zip")
	os.WriteFile(zipPath, packed.Bytes(), 0644)
	if _, e := bundle.Ingest(filepath.Join(st.Root, "library"), zipPath, "demo", "index.html", ""); e != nil {
		t.Fatal(e)
	}
	slideDir := filepath.Join(st.Root, "decks", "demo", "slides")
	os.WriteFile(filepath.Join(slideDir, "0010-a.html"), []byte(`<section class="slide"><div data-vstd-bundle="demo"><button data-bundle-launch>Launch</button><span data-bundle-status></span></div></section>`), 0644)
	os.WriteFile(filepath.Join(slideDir, "0020-b.html"), []byte(`<section class="slide">Next page</section>`), 0644)
	os.WriteFile(filepath.Join(slideDir, "0020-b.md"), []byte("## Intent\nNext"), 0644)
	routes := New(st, ModeStudio).Routes()
	releaseRoot := t.TempDir()
	if published {
		manifest, e := st.BuildRelease("demo", releaseRoot, studio.ReleaseEngineIdentity{Name: "vstd", Version: "test", Revision: strings.Repeat("a", 40)})
		if e != nil {
			t.Fatal(e)
		}
		marked := false
		for _, a := range manifest.Artifacts {
			if a.Path == "index.html" && a.Variant == "bundle-relay-v1" {
				marked = true
			}
		}
		if !marked {
			t.Fatal("unmarked relay shell")
		}
		p := filepath.Join(releaseRoot, "presentation.html")
		html, _ := os.ReadFile(p)
		driver := bundleCacheDriver
		os.WriteFile(p, []byte(strings.Replace(string(html), "</body>", driver+"</body>", 1)), 0644)
	}

	var downloads, checks atomic.Int32
	var revoked atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__revoke" {
			revoked.Store(true)
			w.WriteHeader(204)
			return
		}
		if r.URL.Path == "/assets/bundle/demo" || r.URL.Path == "/assets/bundle/demo.zip" {
			if r.Method == "GET" {
				downloads.Add(1)
			} else if r.Method == "HEAD" {
				checks.Add(1)
			}
			if revoked.Load() {
				http.NotFound(w, r)
				return
			}
		}
		w.Header().Set("Content-Security-Policy", "default-src 'self' blob: data:; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; connect-src 'self' blob: data:; frame-src 'self' blob:")
		if published {
			if r.URL.Path == "/presentation.html" {
				if r.URL.Query().Get("share") != "fixture-viewer" {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Security-Policy", "default-src 'self' blob: data:; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; connect-src blob: data:; frame-src 'self' blob:; sandbox allow-scripts")
			}
			http.FileServer(http.Dir(releaseRoot)).ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/d/demo/" {
			recorded := httptest.NewRecorder()
			routes.ServeHTTP(recorded, r)
			for k, values := range recorded.Header() {
				if k != "Content-Length" {
					w.Header()[k] = values
				}
			}
			w.WriteHeader(recorded.Code)
			w.Write([]byte(strings.Replace(recorded.Body.String(), "</body>", bundleCacheDriver+"</body>", 1)))
			return
		}
		routes.ServeHTTP(w, r)
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	target := srv.URL + "/d/demo/"
	expression := `window.__bundleLifecycle?JSON.stringify(window.__bundleLifecycle):''`
	if published {
		target = srv.URL + "/?share=fixture-viewer#/1"
		expression = `(()=>{if(!window.__fixtureListening){window.__fixtureListening=true;addEventListener('message',async e=>{if(e.data?.type==='fixture-revoke'){await fetch('/__revoke',{method:'POST'});e.source.postMessage({type:'fixture-revoked'},'*');}if(e.data?.type==='bundle-fixture'&&e.data.relaunched)window.__fixture=e.data})}return window.__fixture?JSON.stringify(window.__fixture):''})()`
	}
	raw, e := chromium.Evaluate(ctx, browser, target, expression)
	if e != nil {
		t.Fatal(e)
	}
	var got struct {
		Text       string
		Isolated   bool
		Blocked    bool
		Sandbox    string
		Unloaded   bool
		Relaunched bool
		Revoked    bool
	}
	if e = json.Unmarshal([]byte(raw), &got); e != nil {
		t.Fatal(e)
	}
	if got.Text != "real bundled data" || !got.Isolated || !got.Blocked || got.Sandbox != "allow-scripts" || !got.Unloaded || !got.Relaunched || !got.Revoked || downloads.Load() != 1 || checks.Load() != 2 {
		t.Fatalf("bundle behavior: %+v downloads=%d checks=%d", got, downloads.Load(), checks.Load())
	}
}

// Real browser lifecycle: transfer copies must not detach the retained file cache;
// returning to the slide uses HEAD only, and revoked access must block cached data.
const bundleCacheDriver = `<script>addEventListener('DOMContentLoaded',()=>{
 let launches=0;const wait=ms=>new Promise(r=>setTimeout(r,ms));
 addEventListener('message',async event=>{
  if(event.data?.type!=='bundle-fixture'||event.data.relaunched)return;
  launches++;const sandbox=document.querySelector('[data-vstd-bundle] iframe')?.getAttribute('sandbox');
  window.VSTDP.step(1);await wait(30);const unloaded=!document.querySelector('[data-vstd-bundle] iframe');
  if(launches===1){window.VSTDP.step(-1);document.querySelector('[data-bundle-launch]').click();return;}
  // The opaque published deck cannot call the origin. Ask the trusted fixture
  // shell to revoke access; the editor fixture can do so itself.
  if(parent===window)await fetch('/__revoke',{method:'POST'});
  else{
   await new Promise(resolve=>{const receive=e=>{if(e.source===parent&&e.data?.type==='fixture-revoked'){removeEventListener('message',receive);resolve();}};addEventListener('message',receive);parent.postMessage({type:'fixture-revoke'},'*');});
  }
  window.VSTDP.step(-1);document.querySelector('[data-bundle-launch]').click();
  const until=Date.now()+3000;while(document.querySelector('[data-bundle-launch]').disabled&&Date.now()<until)await wait(25);
  const result={...event.data,sandbox,unloaded,relaunched:launches===2,revoked:!document.querySelector('[data-vstd-bundle] iframe')&&document.querySelector('[data-bundle-status]').textContent.includes('try again')};
  window.__bundleLifecycle=result;parent.postMessage(result,'*');
 });document.querySelector('[data-bundle-launch]').click();
})</script>`
