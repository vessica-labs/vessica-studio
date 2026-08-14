// Package chart contains chart-authoring migrations that operate on the
// Studio file contract without introducing a charting runtime dependency.
package chart

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/vessica-labs/vessica-studio/internal/chromium"
	"github.com/vessica-labs/vessica-studio/internal/studio"
)

// Options controls an SVG-text promotion pass.
type Options struct {
	Browser string
	DryRun  bool
}

// Result describes the migration without exposing the rewritten fragment.
type Result struct {
	Promoted int      `json:"promoted"`
	Charts   int      `json:"charts"`
	Skipped  []string `json:"skipped"`
	Fragment string   `json:"fragment"`
	Error    string   `json:"error"`
}

var browserEvaluate = chromium.Evaluate

// PromoteSVGText renders one slide, converts each SVG <text> (or direct
// <tspan>) into an absolutely-positioned editable HTML overlay, and writes the
// fragment atomically through Studio. Unsupported textPath/zero-geometry nodes
// abort the write so a migration can never silently discard chart labels.
func PromoteSVGText(ctx context.Context, st *studio.Studio, deck, id string, opts Options) (Result, error) {
	ids, err := st.SlideIDs(deck)
	if err != nil {
		return Result{}, err
	}
	index := -1
	for i, slideID := range ids {
		if slideID == id {
			index = i
			break
		}
	}
	if index < 0 {
		return Result{}, fmt.Errorf("slide %q not found in deck %q", id, deck)
	}
	browser := chromium.Find(opts.Browser)
	if browser == "" {
		return Result{}, fmt.Errorf("Chrome/Chromium not found (set VSTD_CHROMIUM or pass --chromium)")
	}
	built, err := st.Build(deck)
	if err != nil {
		return Result{}, err
	}
	page, err := os.ReadFile(built)
	if err != nil {
		return Result{}, err
	}
	script := promotionScript(id, index)
	injected := strings.Replace(string(page), "</body>", script+"</body>", 1)
	if injected == string(page) {
		return Result{}, fmt.Errorf("built player has no </body> marker")
	}
	tmp, err := os.MkdirTemp("", "vstd-chart-promote-*")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(tmp)
	pagePath := filepath.Join(tmp, "index.html")
	if err := os.WriteFile(pagePath, []byte(injected), 0o644); err != nil {
		return Result{}, err
	}
	runCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	target := (&url.URL{Scheme: "file", Path: pagePath}).String() + "#/" + strconv.Itoa(index+1)
	raw, err := browserEvaluate(runCtx, browser, target, `(document.querySelector('#vstd-chart-result')||{}).textContent||''`)
	if err != nil {
		return Result{}, fmt.Errorf("chart migration browser: %w", err)
	}
	var result Result
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return Result{}, fmt.Errorf("decode chart migration data: %w", err)
	}
	if result.Error != "" {
		return result, fmt.Errorf("chart migration: %s", result.Error)
	}
	if len(result.Skipped) > 0 {
		return result, fmt.Errorf("chart migration skipped %d unsupported text node(s): %s", len(result.Skipped), strings.Join(result.Skipped, "; "))
	}
	if result.Promoted == 0 || opts.DryRun {
		return result, nil
	}
	if err := st.WriteFragment(deck, id, result.Fragment); err != nil {
		return result, err
	}
	return result, nil
}

func promotionScript(id string, index int) string {
	idJSON, _ := json.Marshal(id)
	script := chartPromotionScript
	script = strings.ReplaceAll(script, "__VSTD_SLIDE_ID__", string(idJSON))
	script = strings.ReplaceAll(script, "__VSTD_SLIDE_INDEX__", strconv.Itoa(index))
	return script
}

const chartPromotionScript = `<script>
(()=>{
  const result={promoted:0,charts:0,skipped:[],fragment:'',error:''};
  const finish=()=>{const pre=document.createElement('pre');pre.id='vstd-chart-result';pre.textContent=JSON.stringify(result);document.body.replaceChildren(pre);};
  try{
    if(window.VSTDP&&window.VSTDP.goto)window.VSTDP.goto(__VSTD_SLIDE_INDEX__);
    const slide=document.querySelector('.slide[data-vstd='+CSS.escape(__VSTD_SLIDE_ID__)+']');
    if(!slide)throw new Error('target slide not found in built player');
    const slideRect=slide.getBoundingClientRect(),slideScale=slideRect.width/1280||1;
    const pct=n=>(Math.round(n*10000)/10000)+'%';
    const angleOf=el=>{const m=el.getCTM&&el.getCTM();return m?Math.atan2(m.b,m.a)*180/Math.PI:0;};
    for(const svg of slide.querySelectorAll('svg')){
      const textNodes=[...svg.querySelectorAll('text')];if(!textNodes.length)continue;
      const container=svg.parentElement;if(!container){result.skipped.push('SVG without parent container');continue;}
      const cr=container.getBoundingClientRect();if(cr.width<1||cr.height<1){result.skipped.push('hidden or zero-size chart container');continue;}
      result.charts++;container.setAttribute('data-chart-group','');container.setAttribute('data-edit','');
      if(getComputedStyle(container).position==='static')container.style.position='relative';
      svg.classList.add('chart-art');svg.style.pointerEvents='none';
      for(const text of textNodes){
        if(text.querySelector('textPath')){result.skipped.push('textPath: '+(text.textContent||'').trim().slice(0,50));continue;}
		const direct=[...text.children].filter(n=>n.localName==='tspan');
		const mixed=[...text.childNodes].some(n=>n.nodeType===3&&n.nodeValue.trim());
		const sources=direct.length&&!mixed?direct:[text];let failed=false;
        for(const source of sources){
          const value=(source.textContent||'').replace(/\s+/g,' ').trim();if(!value)continue;
          const r=source.getBoundingClientRect();
          if(r.width<.3||r.height<.3){result.skipped.push('zero geometry: '+value.slice(0,50));failed=true;continue;}
          const cs=getComputedStyle(source),rot=angleOf(source),vertical=Math.abs(rot%180)>45;
          let w=r.width,h=r.height;if(vertical){const swap=w;w=h;h=swap;}
          const cx=r.left+r.width/2,cy=r.top+r.height/2,left=cx-w/2,top=cy-h/2;
          const label=document.createElement('div');label.className='chart-label';
          label.setAttribute('data-chart-label','');label.setAttribute('data-edit','');label.textContent=value;
          const anchor=cs.textAnchor||source.getAttribute('text-anchor')||'start';
          const justify=anchor==='middle'?'center':(anchor==='end'?'flex-end':'flex-start');
          const color=cs.fill&&cs.fill!=='none'?cs.fill:cs.color;
          const fs=Math.max(8,h/slideScale);
          label.style.cssText='position:absolute;box-sizing:border-box;display:flex;align-items:center;z-index:2;'+
            'left:'+pct((left-cr.left)/cr.width*100)+';top:'+pct((top-cr.top)/cr.height*100)+';'+
            'width:'+pct(w/cr.width*100)+';height:'+pct(h/cr.height*100)+';'+
            'font-family:'+cs.fontFamily+';font-size:'+fs.toFixed(2)+'px;font-weight:'+cs.fontWeight+';font-style:'+cs.fontStyle+';'+
            'letter-spacing:'+cs.letterSpacing+';line-height:1;color:'+color+';white-space:nowrap;justify-content:'+justify+';text-align:'+({middle:'center',end:'right'}[anchor]||'left')+';'+
            (Math.abs(rot)>.1?'transform:rotate('+rot.toFixed(3)+'deg);transform-origin:center;':'');
          container.appendChild(label);result.promoted++;
        }
        if(!failed)text.remove();
      }
    }
    const clone=slide.cloneNode(true);clone.classList.remove('active');clone.removeAttribute('data-vstd');
    clone.querySelectorAll('[data-vstd-generated]').forEach(el=>el.remove());
    if(window.__vclean)window.__vclean(clone);
    clone.querySelectorAll('[contenteditable]').forEach(el=>el.removeAttribute('contenteditable'));
    result.fragment=clone.outerHTML;
  }catch(e){result.error=String(e&&e.message||e);}
  finish();
})();
</script>`
