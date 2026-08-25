package studio

import (
	"context"
	"encoding/json"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/vessica-labs/vessica-studio/internal/chromium"
)

func TestPlayerMarqueeSelectsMovesAndDeletesMultipleObjects(t *testing.T) {
	browser := chromium.Find("")
	if browser == "" {
		t.Skip("Chrome/Chromium unavailable")
	}
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "studio.yaml"), "theme_default: default\n")
	writeFile(t, filepath.Join(root, "themes", "default", "theme.css"), `
.slide{position:relative;width:1280px;height:720px}
.box{position:absolute;width:140px;height:80px;background:#21bf61}
#a{left:180px;top:180px}#b{left:380px;top:220px}
#c{position:absolute;left:580px;top:200px;width:80px;height:80px;border-radius:50%;background:conic-gradient(#21bf61 65%,#213c2a 0)}
`)
	writeFile(t, filepath.Join(root, "decks", "demo", "deck.yaml"), "title: Demo\ntheme: default\n")
	writeFile(t, filepath.Join(root, "decks", "demo", "slides", "0010-group.html"),
		`<section class="slide"><div id="a" class="box" data-edit></div><div id="b" class="box" data-edit></div><div id="c">65%</div></section>`)

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
  if(!document.querySelector('#editbtn')||!document.querySelector('#a'))return '';
  document.querySelector('#editbtn').click();
  const slide=document.querySelector('.slide'),a=document.querySelector('#a'),b=document.querySelector('#b'),c=document.querySelector('#c');
  const ar=a.getBoundingClientRect(),br=b.getBoundingClientRect(),cr=c.getBoundingClientRect();
  const sx=Math.min(ar.left,br.left,cr.left)-10,sy=Math.min(ar.top,br.top,cr.top)-10;
  const ex=Math.max(ar.right,br.right,cr.right)+10,ey=Math.max(ar.bottom,br.bottom,cr.bottom)+10;
  slide.dispatchEvent(new PointerEvent('pointerdown',{bubbles:true,clientX:sx,clientY:sy}));
  window.dispatchEvent(new PointerEvent('pointermove',{bubbles:true,clientX:ex,clientY:ey}));
  window.dispatchEvent(new PointerEvent('pointerup',{bubbles:true,clientX:ex,clientY:ey}));
  const selected=document.querySelector('#editSelection').textContent;
  const selbox=document.querySelector('#selbox');
  const multi=selbox.classList.contains('multi');
  const p={x:ar.left+ar.width/2,y:ar.top+ar.height/2};
  a.dispatchEvent(new PointerEvent('pointerdown',{bubbles:true,clientX:p.x,clientY:p.y}));
  window.dispatchEvent(new PointerEvent('pointermove',{bubbles:true,clientX:p.x+30,clientY:p.y+20}));
  window.dispatchEvent(new PointerEvent('pointerup',{bubbles:true,clientX:p.x+30,clientY:p.y+20}));
  const movedTogether=!!a.dataset.tx&&a.dataset.tx===b.dataset.tx&&a.dataset.tx===c.dataset.tx&&a.dataset.ty===b.dataset.ty&&a.dataset.ty===c.dataset.ty;
  document.querySelector('[data-e="del"]').click();
  return JSON.stringify({
    selected,multi,movedTogether,
    deleted:!document.querySelector('#a')&&!document.querySelector('#b')&&!document.querySelector('#c')
  });
})()`)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Selected      string `json:"selected"`
		Multi         bool   `json:"multi"`
		MovedTogether bool   `json:"movedTogether"`
		Deleted       bool   `json:"deleted"`
	}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	if got.Selected != "3 objects" || !got.Multi {
		t.Fatalf("marquee did not include the CSS shape in the combined selection: %+v", got)
	}
	if !got.MovedTogether {
		t.Fatalf("selected objects did not move together: %+v", got)
	}
	if !got.Deleted {
		t.Fatalf("group delete did not remove every selected object: %+v", got)
	}
}
