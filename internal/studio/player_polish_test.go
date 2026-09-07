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

func TestPlayerPolishAgendaInsertGridAndChromePlacement(t *testing.T) {
	browser := chromium.Find("")
	if browser == "" {
		t.Skip("Chrome/Chromium unavailable")
	}
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "studio.yaml"), "theme_default: default\n")
	writeFile(t, filepath.Join(root, "themes", "default", "theme.css"), ".slide{position:relative;width:1280px;height:720px;background:#fff}")
	writeFile(t, filepath.Join(root, "decks", "demo", "deck.yaml"), "title: Demo\ntheme: default\n")
	writeFile(t, filepath.Join(root, "decks", "demo", "slides", "0010-one.html"), `<section class="slide"><h1>First page</h1></section>`)
	writeFile(t, filepath.Join(root, "decks", "demo", "slides", "0020-two.html"), `<section class="slide" data-sec="Evidence"><p>Second</p></section>`)
	writeFile(t, filepath.Join(root, "decks", "demo", "slides", "0030-three.html"), `<section class="slide"><p>Third</p></section>`)
	st, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	page, err := st.Build("demo")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	raw, err := chromium.Evaluate(ctx, browser, (&url.URL{Scheme: "file", Path: page}).String(), `(()=>{
  if(!document.querySelector('#filmNew'))return '';
  window.__vme={presenter:true,editable:true};window.__vaudience=false;
  document.querySelector('#editbtn').click();
  const agendaLinks=[...document.querySelectorAll('#menulist a')];
  const agenda=agendaLinks.map(a=>a.textContent.trim());
  agendaLinks[1].click();
  const agendaJumped=document.querySelector('[data-vstd="0020-two"]').classList.contains('active');
  document.querySelector('[data-act="addtext"]').click();
  const text=document.querySelector('[data-edit="text"]');
  document.querySelector('[data-act="shapes"]').click();
  document.querySelector('[data-shape="circle"]').click();
  const circle=document.querySelector('[data-vstd-shape="circle"]');
  document.querySelector('[data-act="grid"]').click();
  const filmHidden=getComputedStyle(document.querySelector('#filmstrip')).display==='none';
  document.querySelector('#overview').classList.remove('open');
  document.querySelector('#filmNew').focus();
  const tip=document.querySelector('#vtip').getBoundingClientRect(),button=document.querySelector('#filmNew').getBoundingClientRect();
  const vstatusTop=getComputedStyle(document.querySelector('#vstatus')).top;
  return JSON.stringify({agenda,agendaJumped,text:!!text,circle:!!circle,filmHidden,tipAbove:tip.bottom<=button.top,vstatusTop});
})()`)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Agenda     []string `json:"agenda"`
		AgendaJump bool     `json:"agendaJumped"`
		Text       bool     `json:"text"`
		Circle     bool     `json:"circle"`
		FilmHidden bool     `json:"filmHidden"`
		TipAbove   bool     `json:"tipAbove"`
		VstatusTop string   `json:"vstatusTop"`
	}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Agenda) != 3 || got.Agenda[0] != "01First page" || got.Agenda[1] != "02Evidence" || got.Agenda[2] != "03Slide 3" {
		t.Fatalf("agenda did not expose every page: %#v", got.Agenda)
	}
	if !got.AgendaJump {
		t.Fatal("Agenda page did not navigate to its slide")
	}
	if !got.Text || !got.Circle {
		t.Fatalf("ribbon insertion unavailable: text=%v circle=%v", got.Text, got.Circle)
	}
	if !got.FilmHidden {
		t.Fatal("Grid left the Slides panel visible")
	}
	if !got.TipAbove {
		t.Fatal("bottom New slide tooltip was placed outside the viewport")
	}
	if got.VstatusTop != "60px" {
		t.Fatalf("Vessica status top=%q want 60px", got.VstatusTop)
	}
}
