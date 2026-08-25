package studio

import (
	"context"
	"encoding/json"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vessica-labs/vessica-studio/internal/chromium"
)

func TestPlayerEditsCSSBackgroundImageCrop(t *testing.T) {
	browser := chromium.Find("")
	if browser == "" {
		t.Skip("Chrome/Chromium unavailable")
	}
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "studio.yaml"), "theme_default: default\n")
	writeFile(t, filepath.Join(root, "themes", "default", "theme.css"), `
.slide{position:relative;width:1280px;height:720px}
#picture{position:absolute;left:100px;top:100px;width:200px;height:100px;
  background-image:url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='1000' height='100'%3E%3Crect width='1000' height='100' fill='%2321bf61'/%3E%3C/svg%3E");
  background-repeat:no-repeat;background-size:500% auto;background-position:25% 50%}
`)
	writeFile(t, filepath.Join(root, "decks", "demo", "deck.yaml"), "title: Demo\ntheme: default\n")
	writeFile(t, filepath.Join(root, "decks", "demo", "slides", "0010-picture.html"),
		`<section class="slide"><div id="picture" aria-label="Picture panel"></div></section>`)

	st, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	page, err := st.Build("demo")
	if err != nil {
		t.Fatal(err)
	}
	target := (&url.URL{Scheme: "file", Path: page}).String()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	raw, err := chromium.Evaluate(ctx, browser, target, `(()=>{
  if(!document.querySelector('#editbtn')||!document.querySelector('#picture'))return '';
  document.querySelector('#editbtn').click();
  const picture=document.querySelector('#picture');
  const point=()=>{const r=picture.getBoundingClientRect();return {x:r.left+r.width/2,y:r.top+r.height/2}};
  let p=point();
  picture.dispatchEvent(new PointerEvent('pointerdown',{bubbles:true,clientX:p.x,clientY:p.y}));
  window.dispatchEvent(new PointerEvent('pointerup',{bubbles:true,clientX:p.x,clientY:p.y}));
  const tools=document.querySelector('#imageRibbonTools');
  const crop=document.querySelector('[data-img="crop"]');
  crop.click();
  const before=getComputedStyle(picture).backgroundPosition;
  p=point();
  picture.dispatchEvent(new PointerEvent('pointerdown',{bubbles:true,clientX:p.x,clientY:p.y}));
  window.dispatchEvent(new PointerEvent('pointermove',{bubbles:true,clientX:p.x+20,clientY:p.y+10}));
  window.dispatchEvent(new PointerEvent('pointerup',{bubbles:true,clientX:p.x+20,clientY:p.y+10}));
  const after=picture.style.backgroundPosition;
  document.querySelector('[data-img="zoom-in"]').click();
  return JSON.stringify({
    selection:document.querySelector('#editSelection').textContent,
    tools:getComputedStyle(tools).display,
    cropOn:crop.classList.contains('on'),
    before,after,size:picture.style.backgroundSize,
    moved:picture.dataset.tx!==undefined||picture.dataset.ty!==undefined
  });
})()`)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Selection string `json:"selection"`
		Tools     string `json:"tools"`
		CropOn    bool   `json:"cropOn"`
		Before    string `json:"before"`
		After     string `json:"after"`
		Size      string `json:"size"`
		Moved     bool   `json:"moved"`
	}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	if got.Selection != "Picture" || got.Tools != "flex" || !got.CropOn {
		t.Fatalf("background picture crop controls unavailable: %+v", got)
	}
	if got.After == "" || got.After == got.Before {
		t.Fatalf("crop drag did not change background position: %+v", got)
	}
	if !strings.HasPrefix(got.Size, "550%") {
		t.Fatalf("zoom did not change background size: %+v", got)
	}
	if got.Moved {
		t.Fatal("crop drag moved the picture frame instead of the image within it")
	}
}
