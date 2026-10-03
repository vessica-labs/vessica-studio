package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/vessica-labs/vessica-studio/internal/chromium"
)

func TestGridDropRefreshesWhileCompanionIsOpen(t *testing.T) {
	browser := chromium.Find("")
	if browser == "" {
		t.Skip("Chrome/Chromium unavailable")
	}
	st := testStudio(t)
	for id, title := range map[string]string{"0010-a": "First", "0020-b": "Second", "0030-c": "Third"} {
		if err := os.WriteFile(st.SlidePath("demo", id, ".html"), []byte(`<section class="slide"><h1>`+title+`</h1></section>`), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(st.SlidePath("demo", id, ".md"), []byte("## Intent\n"+title+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.Build("demo"); err != nil {
		t.Fatal(err)
	}
	token := "grid-render-regression-test-token-32"
	h, err := NewEditorSession(st, EditorSessionOptions{Deck: "demo", Token: token, ExpiresAt: time.Now().Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+token)
		h.ServeHTTP(w, r)
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	raw, err := chromium.Evaluate(ctx, browser, srv.URL+"/d/demo/", `(async()=>{
  if(document.readyState!=='complete'||!window.__vme?.editable)return '';
  document.querySelector('[data-act="grid"]').click();
  await window.__companionDrawer.open();
  const drawer=document.getElementById('companionDrawer');
  const notes=drawer.querySelector('textarea');notes.value='Unsaved speaker note';notes.dispatchEvent(new Event('input',{bubbles:true}));
  const titles=()=>[...document.querySelectorAll('#ovgrid .thumb h1')].map(el=>el.textContent);
  const before=titles();
  const thumbs=[...document.querySelectorAll('#ovgrid .thumb')],rect=thumbs[0].getBoundingClientRect();
  thumbs[2].dispatchEvent(new DragEvent('dragstart',{bubbles:true,dataTransfer:new DataTransfer()}));
  thumbs[0].dispatchEvent(new DragEvent('drop',{bubbles:true,cancelable:true,clientX:rect.right-1,dataTransfer:new DataTransfer()}));
  const until=Date.now()+2500;
  while(Date.now()<until&&titles().join(',')!=='First,Third,Second')await new Promise(resolve=>setTimeout(resolve,25));
  return JSON.stringify({before,after:titles(),gridOpen:document.getElementById('overview').classList.contains('open'),drawerOpen:drawer.classList.contains('open'),notes:notes.value,hash:location.hash});
})()`)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Before, After        []string
		GridOpen, DrawerOpen bool
		Hash                 string
		Notes                string
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.After) != 3 || result.After[1] != "Third" || !result.GridOpen || !result.DrawerOpen || result.Notes != "Unsaved speaker note" || len(result.Hash) < 6 || result.Hash[:6] != "#/grid" {
		t.Fatalf("Grid did not show its saved reorder in place: %+v", result)
	}
}
