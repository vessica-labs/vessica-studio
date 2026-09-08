package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vessica-labs/vessica-studio/internal/chromium"
)

func TestLiveRefreshAppliesSlideMarkupAndDeckStyles(t *testing.T) {
	browser := chromium.Find("")
	if browser == "" {
		t.Skip("Chrome/Chromium unavailable")
	}
	st := testStudio(t)
	fragment := filepath.Join(st.Root, "decks", "demo", "slides", "0010-a.html")
	deckCSS := filepath.Join(st.Root, "decks", "demo", "deck.css")
	if err := os.WriteFile(deckCSS, []byte(".s-title{position:absolute;left:10px}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	routes := New(st, ModeStudio).Routes()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/test/live-refresh" {
			left := "320px"
			if r.URL.Query().Get("phase") == "styles" {
				left = "480px"
			} else if err := os.WriteFile(fragment, []byte(`<section class="slide"><div class="s-title">After</div></section>`+"\n"), 0o644); err != nil {
				http.Error(w, "update failed", http.StatusInternalServerError)
				return
			}
			if err := os.WriteFile(deckCSS, []byte(".s-title{position:absolute;left:"+left+"}\n"), 0o644); err != nil {
				http.Error(w, "update failed", http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		routes.ServeHTTP(w, r)
	})
	server := httptest.NewServer(handler)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	raw, err := chromium.Evaluate(ctx, browser, server.URL+"/d/demo/", `(async()=>{
  const title=document.querySelector('#frame .s-title');
  if(!title)return '';
  if(!window.__liveRefreshStarted){
    if(title.textContent!=='Before'||getComputedStyle(title).left!=='10px')return '';
	const edit=document.getElementById('editbtn');
	if(!edit||getComputedStyle(edit).display==='none')return '';
	edit.click();
	const filmTitle=document.querySelector('#filmList .fmini')?.shadowRoot?.querySelector('.s-title');
	if(!filmTitle||getComputedStyle(filmTitle).left!=='10px')return '';
    document.body.dataset.liveRefreshDocument='original';
    const response=await fetch('/test/live-refresh',{method:'POST'});
    if(!response.ok)return JSON.stringify({error:'fixture update failed'});
    window.__liveRefreshStarted=1;
    return '';
  }
  const current=document.querySelector('#frame .s-title');
	const filmCurrent=document.querySelector('#filmList .fmini')?.shadowRoot?.querySelector('.s-title');
	if(window.__liveRefreshStarted===1&&current&&filmCurrent&&current.textContent==='After'&&getComputedStyle(current).left==='320px'&&getComputedStyle(filmCurrent).left==='320px'){
		const response=await fetch('/test/live-refresh?phase=styles',{method:'POST'});
		if(!response.ok)return JSON.stringify({error:'style-only fixture update failed'});
		window.__liveRefreshStarted=2;
		return '';
	}
	if(window.__liveRefreshStarted===2&&current&&filmCurrent&&current.textContent==='After'&&getComputedStyle(current).left==='480px'&&getComputedStyle(filmCurrent).left==='480px'){
		return JSON.stringify({text:current.textContent,left:getComputedStyle(current).left,filmLeft:getComputedStyle(filmCurrent).left,sameDocument:document.body.dataset.liveRefreshDocument==='original'});
  }
  return '';
})()`)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Error        string `json:"error"`
		Text         string `json:"text"`
		Left         string `json:"left"`
		FilmLeft     string `json:"filmLeft"`
		SameDocument bool   `json:"sameDocument"`
	}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	if got.Error != "" || got.Text != "After" || got.Left != "480px" || got.FilmLeft != "480px" || !got.SameDocument {
		t.Fatalf("live refresh did not apply complete revision without reload: %#v", got)
	}
}
