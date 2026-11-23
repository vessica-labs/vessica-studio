package studio

import (
	"context"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/vessica-labs/vessica-studio/internal/chromium"
)

func TestPlayerTextEditLists(t *testing.T) {
	browser := chromium.Find("")
	if browser == "" {
		t.Skip("Chrome/Chromium unavailable")
	}
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "studio.yaml"), "theme_default: default\n")
	writeFile(t, filepath.Join(root, "themes", "default", "theme.css"), ".slide{position:relative;width:1280px;height:720px}p.s-lead{font-size:30px;font-weight:700}")
	writeFile(t, filepath.Join(root, "decks", "demo", "deck.yaml"), "title: Demo\ntheme: default\n")
	writeFile(t, filepath.Join(root, "decks", "demo", "slides", "0010-one.html"), `<section class="slide"><p class="s-lead" style="position:absolute;left:100px;top:100px">First<br>Second</p><p id="neighbor">Other text</p></section>`)
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
	result, err := chromium.Evaluate(ctx, browser, (&url.URL{Scheme: "file", Path: page}).String(), `(()=>{
  const edit=document.querySelector('#editbtn');
  if(document.readyState!=='complete'||!edit)return '';
  window.__vme={presenter:true,editable:true};window.__vaudience=false;
  edit.click();
  const bullets=document.querySelector('[aria-label="Bulleted list"]');
  const numbers=document.querySelector('[aria-label="Numbered list"]');
  if(!bullets||!numbers)return 'List controls missing';
  if(!bullets.disabled||!numbers.disabled)return 'List controls enabled before text editing';
  let text=document.querySelector('.s-lead');
  text.dispatchEvent(new MouseEvent('dblclick',{bubbles:true}));
  const sel=getSelection();
  sel.selectAllChildren(text);
  if(bullets.disabled||numbers.disabled)return 'List controls disabled in text edit mode';
  bullets.dispatchEvent(new PointerEvent('pointerdown',{bubbles:true}));
  bullets.click();
  text=document.querySelector('.s-lead');
  if(getComputedStyle(text).fontSize!=='30px'||getComputedStyle(text).fontWeight!=='700')return 'List conversion changed typography';
  const items=[...text.querySelectorAll('ul > li')];
  if(items.map(el=>el.textContent).join(',')!=='First,Second')return text.outerHTML;
  if(getComputedStyle(items[0]).listStyleType!=='disc')return 'Bullet marker missing';
  sel.collapse(items[0].firstChild,0);
  numbers.click();
  const numbered=text.querySelector('ol > li');
  if(numbered?.textContent!=='First'||getComputedStyle(numbered).listStyleType!=='decimal')return text.outerHTML;
  const reparsed=new DOMParser().parseFromString(document.querySelector('.slide').outerHTML,'text/html');
  if(!reparsed.querySelector('.s-lead ol > li'))return 'List escaped its text box after HTML reload';
  if(reparsed.querySelector('#neighbor').textContent!=='Other text')return 'List editing changed neighboring text';
  numbers.click();
  if(text.querySelector('ol')||!text.textContent.includes('First'))return 'Toggling numbering lost text or left the list in place';
  document.querySelector('#editRibbon [data-act="edit"]').click();
  if(!bullets.disabled||!numbers.disabled)return 'List controls stayed enabled after leaving edit mode';
  return 'ok';
})()`)
	if err != nil {
		t.Fatal(err)
	}
	if result != "ok" {
		t.Fatal(result)
	}
}
