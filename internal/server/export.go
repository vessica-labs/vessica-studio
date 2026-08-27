package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vessica-labs/vessica-studio/internal/studio"
)

// PDF export: GET /api/deck/{deck}/export.pdf (any authorized viewer) builds a
// static print page of the deck's active + hidden slides (parked/"unused"
// excluded), renders it through a locally installed Chrome/Chromium
// (--headless --print-to-pdf — no Go dependency), and streams the PDF back
// as a download. Chrome fetches the page from this same server via a
// short-lived one-time key, so /library images resolve over HTTP exactly as
// they do in the player.

type printJob struct {
	html string
	exp  time.Time
}

const pptxCaptureScript = `<script>
(async()=>{
  const sleep=ms=>new Promise(r=>setTimeout(r,ms));
  if(document.fonts&&document.fonts.ready)await document.fonts.ready;
  const images=[...document.images];
  await Promise.race([Promise.all(images.map(i=>i.complete?Promise.resolve():i.decode().catch(()=>{}))),sleep(2500)]);
  await sleep(250);
  const out={title:document.title||'Vessica deck',slides:[]};
  const visible=(el,cs,r)=>cs.display!=='none'&&cs.visibility!=='hidden'&&parseFloat(cs.opacity||1)>0&&r.width>.3&&r.height>.3;
  const colors=s=>(s||'').match(/rgba?\([^)]*\)|#[0-9a-fA-F]{3,8}/g)||[];
  const opaque=c=>c&&c!=='transparent'&&!/rgba\([^)]*,\s*0(?:\.0+)?\s*\)/.test(c);
  const opacity=el=>{let o=1,n=el;while(n&&n.nodeType===1){o*=parseFloat(getComputedStyle(n).opacity||1);n=n.parentElement;}return o;};
  function geom(r,sr){const sx=1280/sr.width,sy=720/sr.height;return{x:(r.left-sr.left)*sx,y:(r.top-sr.top)*sy,w:r.width*sx,h:r.height*sy};}
  function rotation(cs){const m=(cs.transform||'').match(/^matrix\(([^)]+)\)$/);if(!m)return 0;const p=m[1].split(',').map(Number);return Math.atan2(p[1],p[0])*180/Math.PI;}
  function svgRotation(el){const m=el.getCTM&&el.getCTM();return m?Math.atan2(m.b,m.a)*180/Math.PI:0;}
  function svgColor(el,prop){let v=getComputedStyle(el)[prop]||'';const m=v.match(/url\(["']?#([^"')]+)["']?\)/);if(m){const paint=el.ownerSVGElement&&el.ownerSVGElement.querySelector('#'+CSS.escape(m[1]));const stop=paint&&paint.querySelector(prop==='stroke'?'stop:last-child':'stop');if(stop)v=getComputedStyle(stop).stopColor||stop.getAttribute('stop-color')||v;}return v;}
  function styleBase(el,cs,r,sr){return{...geom(r,sr),rotation:rotation(cs),opacity:opacity(el),fill:cs.backgroundColor,gradient:colors(cs.backgroundImage),stroke:cs.borderTopColor,stroke_width:Math.max(parseFloat(cs.borderTopWidth)||0,parseFloat(cs.borderRightWidth)||0,parseFloat(cs.borderBottomWidth)||0,parseFloat(cs.borderLeftWidth)||0)};}
  function point(svg,x,y,sr){const p=svg.ownerSVGElement.createSVGPoint();p.x=x;p.y=y;const q=p.matrixTransform(svg.getScreenCTM());return{x:(q.x-sr.left)*1280/sr.width,y:(q.y-sr.top)*720/sr.height};}
  async function pictureData(img,r,cs){
    try{
      if(img.decode)await Promise.race([img.decode().catch(()=>{}),sleep(1200)]);
      const nw=img.naturalWidth||img.videoWidth,nh=img.naturalHeight||img.videoHeight;if(!nw||!nh)return '';
      const scale=Math.min(2,1800/Math.max(r.width,r.height));const w=Math.max(1,Math.round(r.width*scale)),h=Math.max(1,Math.round(r.height*scale));
      const c=document.createElement('canvas');c.width=w;c.height=h;const x=c.getContext('2d');
      const fit=cs.objectFit||'fill',src=nw/nh,dst=w/h;let sx=0,sy=0,sw=nw,sh=nh,dx=0,dy=0,dw=w,dh=h;
      if(fit==='cover'){if(src>dst){sw=nh*dst;sx=(nw-sw)/2}else{sh=nw/dst;sy=(nh-sh)/2}}
      else if(fit==='contain'){if(src>dst){dh=w/src;dy=(h-dh)/2}else{dw=h*src;dx=(w-dw)/2}}
      x.drawImage(img,sx,sy,sw,sh,dx,dy,dw,dh);return c.toDataURL('image/png');
    }catch(_){return ''}
  }
  async function backgroundPicture(el,r,cs){
    const m=(cs.backgroundImage||'').match(/url\(["']?([^"')]+)["']?\)/);if(!m)return '';
    try{const img=new Image();img.crossOrigin='anonymous';img.src=m[1];await Promise.race([img.decode(),sleep(1200)]);return pictureData(img,r,{objectFit:cs.backgroundSize==='contain'?'contain':'cover'});}catch(_){return ''}
  }
  function textValue(el,cs){let t=(el.innerText||el.textContent||'').replace(/\u00a0/g,' ').trim();if(el.tagName==='LI'&&cs.listStyleType!=='none')t='• '+t;if(cs.textTransform==='uppercase')t=t.toUpperCase();if(cs.textTransform==='lowercase')t=t.toLowerCase();return t;}
  function wantsText(el){
    const tag=el.tagName;const semantic=/^(H[1-6]|P|LI|TD|TH|DT|DD|LABEL|BUTTON|FIGCAPTION)$/.test(tag);
    const role=/(s-title|s-lead|kpi|bh|headband|chev|tiny|eyebrow|quote|caption|label|title|subtitle)/.test(el.className||'');
    const direct=[...el.childNodes].some(n=>n.nodeType===3&&n.nodeValue.trim());
    const block=[...el.children].some(c=>/^(DIV|P|H[1-6]|UL|OL|LI|TABLE|SECTION|ARTICLE|FIGURE)$/.test(c.tagName)&&(c.textContent||'').trim());
    return semantic||role||(direct&&!block)||(!el.children.length&&(el.textContent||'').trim());
  }
  for(const page of document.querySelectorAll('.vstd-page')){
    const slide=page.querySelector('.slide'),sr=slide.getBoundingClientRect();const elements=[];
    async function walk(el,textBlocked=false){
      if(!(el instanceof Element)||el.matches('.notes,script,style'))return;
      const cs=getComputedStyle(el),r=el.getBoundingClientRect();if(!visible(el,cs,r))return;
      if(el!==slide){
        const base=styleBase(el,cs,r,sr);const radius=parseFloat(cs.borderTopLeftRadius)||0;
        if(opaque(cs.backgroundColor)||base.gradient.length>=2||base.stroke_width>0)elements.push({kind:radius>8?'roundRect':'rect',name:(el.className||el.tagName).toString().slice(0,80),...base});
        if(/url\(/.test(cs.backgroundImage)){const data=await backgroundPicture(el,r,cs);if(data)elements.push({kind:'image',name:'Background image',...geom(r,sr),rotation:base.rotation,opacity:base.opacity,image_data:data});}
      }
      if(el.tagName==='IMG'){
        const data=await pictureData(el,r,cs);elements.push({kind:'image',name:el.alt||'Image',...geom(r,sr),rotation:rotation(cs),opacity:opacity(el),image_data:data});return;
      }
      if(el.tagName==='VIDEO'){
        let source=el;if(el.poster){const p=new Image();p.src=el.poster;await Promise.race([p.decode().catch(()=>{}),sleep(1800)]);source=p}const data=await pictureData(source,r,cs);elements.push({kind:'image',name:'Video poster',...geom(r,sr),rotation:rotation(cs),opacity:opacity(el),image_data:data});return;
      }
      if(el.localName==='svg'){
        for(const n of el.querySelectorAll('rect,circle,ellipse,line,polyline,polygon,path,text')){
          const ns=getComputedStyle(n),nr=n.getBoundingClientRect();if(ns.display==='none'||ns.visibility==='hidden'||opacity(n)<=0||(nr.width<=.3&&nr.height<=.3))continue;
          const tag=(n.localName||n.tagName).toLowerCase();
          const common={name:(n.getAttribute('class')||n.tagName).toString().slice(0,80),stroke:svgColor(n,'stroke'),stroke_width:parseFloat(ns.strokeWidth)||1,fill:svgColor(n,'fill'),opacity:opacity(n)};
          if(tag==='text'){let g=geom(nr,sr),rot=svgRotation(n);if(Math.abs(rot%180)>45){const cx=g.x+g.w/2,cy=g.y+g.h/2,t=g.w;g.w=g.h;g.h=t;g.x=cx-g.w/2;g.y=cy-g.h/2;}g.x-=2;g.w+=4;elements.push({kind:'text',...g,...common,rotation:rot,nowrap:true,text:(n.textContent||'').trim(),font_family:ns.fontFamily,font_size:parseFloat(ns.fontSize)||16,bold:parseInt(ns.fontWeight)>=600,italic:ns.fontStyle==='italic',align:ns.textAnchor==='middle'?'center':(ns.textAnchor==='end'?'right':'left'),color:svgColor(n,'fill'),valign:'middle'});continue;}
          if(tag==='rect')elements.push({kind:parseFloat(n.getAttribute('rx')||0)>4?'roundRect':'rect',...geom(nr,sr),...common});
          else if(tag==='circle'||tag==='ellipse')elements.push({kind:'ellipse',...geom(nr,sr),...common});
          else if(tag==='line')elements.push({kind:'line',...common,points:[point(n,+n.getAttribute('x1'),+n.getAttribute('y1'),sr),point(n,+n.getAttribute('x2'),+n.getAttribute('y2'),sr)]});
          else if(tag==='polyline'||tag==='polygon'){const pts=[...n.points].map(p=>point(n,p.x,p.y,sr));if(tag==='polygon'&&pts.length)pts.push(pts[0]);elements.push({kind:'path',...common,points:pts});}
          else if(tag==='path'){try{const len=n.getTotalLength(),count=Math.max(2,Math.min(100,Math.ceil(len/12)));const pts=[];for(let i=0;i<=count;i++){const p=n.getPointAtLength(len*i/count);pts.push(point(n,p.x,p.y,sr));}elements.push({kind:'path',...common,points:pts});}catch(_){}}
        }
        return;
      }
      const takeText=!textBlocked&&wantsText(el),txt=takeText?textValue(el,cs):'';
      if(txt)elements.push({kind:'text',name:(el.className||el.tagName).toString().slice(0,80),...geom(r,sr),rotation:rotation(cs),opacity:opacity(el),nowrap:cs.whiteSpace==='nowrap',text:txt,font_family:cs.fontFamily,font_size:parseFloat(cs.fontSize)||16,bold:parseInt(cs.fontWeight)>=600,italic:cs.fontStyle==='italic',align:cs.textAlign,valign:cs.verticalAlign,color:cs.color});
      for(const child of el.children)await walk(child,textBlocked||!!txt);
    }
    for(const video of slide.querySelectorAll('video[data-vstd-video]'))if(!video.poster)video.poster='/assets/video/'+encodeURIComponent(video.dataset.vstdVideo)+'/poster';
    const scs=getComputedStyle(slide);elements.push({kind:'rect',name:'Slide background',x:0,y:0,w:1280,h:720,fill:scs.backgroundColor,gradient:colors(scs.backgroundImage),stroke_width:0,opacity:1});
    for(const child of slide.children)await walk(child,false);
    out.slides.push({id:slide.dataset.vstd||'',elements});
  }
  const pre=document.createElement('pre');pre.id='vstd-pptx-json';pre.textContent=JSON.stringify(out);document.body.replaceChildren(pre);
})().catch(e=>{const pre=document.createElement('pre');pre.id='vstd-pptx-error';pre.textContent=String(e&&e.stack||e);document.body.replaceChildren(pre)});
</script>`

func injectPPTXCapture(page string) string {
	return strings.Replace(page, "</body>", pptxCaptureScript+"</body>", 1)
}

func parsePPTXCapture(dump []byte) (studio.PPTXDeck, error) {
	const marker = `<pre id="vstd-pptx-json">`
	s := string(dump)
	start := strings.Index(s, marker)
	if start < 0 {
		if e := strings.Index(s, `<pre id="vstd-pptx-error">`); e >= 0 {
			e += len(`<pre id="vstd-pptx-error">`)
			if end := strings.Index(s[e:], "</pre>"); end >= 0 {
				return studio.PPTXDeck{}, fmt.Errorf("browser capture: %s", html.UnescapeString(s[e:e+end]))
			}
		}
		return studio.PPTXDeck{}, fmt.Errorf("browser did not produce PPTX object data")
	}
	start += len(marker)
	end := strings.Index(s[start:], "</pre>")
	if end < 0 {
		return studio.PPTXDeck{}, fmt.Errorf("truncated PPTX object data")
	}
	var deck studio.PPTXDeck
	if err := json.Unmarshal([]byte(html.UnescapeString(s[start:start+end])), &deck); err != nil {
		return studio.PPTXDeck{}, fmt.Errorf("decode PPTX object data: %w", err)
	}
	if len(deck.Slides) == 0 {
		return studio.PPTXDeck{}, fmt.Errorf("browser captured no slides")
	}
	return deck, nil
}

// findChrome locates a Chrome-family binary for headless PDF rendering.
// VSTD_CHROME overrides; otherwise PATH names, then macOS app bundles.
func findChrome() string {
	if p := os.Getenv("VSTD_CHROME"); p != "" {
		return p
	}
	for _, name := range []string{"google-chrome-stable", "google-chrome", "chromium", "chromium-browser", "chrome"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	for _, p := range []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
		"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// isLoopback reports whether the request arrived over the loopback
// interface — i.e. from a process on this same machine/container, like the
// headless Chrome we spawn for PDF export. External traffic (Railway edge
// included) reaches the listener over a real interface, never loopback.
func isLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (s *Server) putPrintJob(html string) string {
	b := make([]byte, 16)
	rand.Read(b)
	key := hex.EncodeToString(b)
	s.mu.Lock()
	if s.printJobs == nil {
		s.printJobs = map[string]printJob{}
	}
	for k, j := range s.printJobs { // sweep expired
		if time.Now().After(j.exp) {
			delete(s.printJobs, k)
		}
	}
	s.printJobs[key] = printJob{html: html, exp: time.Now().Add(2 * time.Minute)}
	s.mu.Unlock()
	return key
}

func (s *Server) dropPrintJob(key string) {
	s.mu.Lock()
	delete(s.printJobs, key)
	s.mu.Unlock()
}

// handlePrintHTML serves the static print page. Reachable with a live
// one-time key (how the spawned Chrome loads it), or directly by the
// presenter (handy for eyeballing print layout in a normal browser tab).
func (s *Server) handlePrintHTML(w http.ResponseWriter, r *http.Request) {
	deck := r.PathValue("deck")
	if !studio.ValidDeckName(deck) {
		http.NotFound(w, r)
		return
	}
	if key := r.URL.Query().Get("key"); key != "" {
		s.mu.Lock()
		job, ok := s.printJobs[key]
		s.mu.Unlock()
		if ok && time.Now().Before(job.exp) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			io.WriteString(w, job.html)
			return
		}
	}
	if !s.isPresenter(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	html, _, err := s.St.BuildPrintHTML(deck)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	io.WriteString(w, html)
}

func (s *Server) renderDeckPDF(r *http.Request, deck string) ([]byte, int, error) {
	return s.renderDeckPDFForSlides(r, deck, nil)
}

func (s *Server) renderDeckPDFForSlides(r *http.Request, deck string, selected []string) ([]byte, int, error) {
	html, ids, err := s.St.BuildPrintHTMLForSlides(deck, selected)
	if err != nil {
		return nil, 0, err
	}
	pages := len(ids)
	chrome := findChrome()
	if chrome == "" {
		return nil, 0, fmt.Errorf("PDF export needs Chrome or Chromium on this machine — install one, or point VSTD_CHROME at a browser binary")
	}

	// Chrome loads the print page from this same server so /library and
	// /assets URLs in slides resolve. Reach it via loopback on whatever port
	// this request came in on.
	la, _ := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if la == nil {
		return nil, 0, fmt.Errorf("cannot determine local server address")
	}
	_, port, err := net.SplitHostPort(la.String())
	if err != nil {
		return nil, 0, fmt.Errorf("cannot determine local server port: %v", err)
	}
	key := s.putPrintJob(html)
	defer s.dropPrintJob(key)
	url := fmt.Sprintf("http://127.0.0.1:%s/api/deck/%s/print.html?key=%s", port, deck, key)

	tmp, err := os.MkdirTemp("", "vstd-pdf-*")
	if err != nil {
		return nil, 0, err
	}
	defer os.RemoveAll(tmp)
	out := filepath.Join(tmp, deck+".pdf")

	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, chrome,
		"--headless",
		"--disable-gpu",
		"--no-sandbox",            // containers (Railway) lack the privileges Chrome's sandbox needs
		"--disable-dev-shm-usage", // container /dev/shm is tiny; render via /tmp instead
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-component-update",
		"--disable-background-networking",
		"--disable-sync",
		"--hide-scrollbars",
		"--no-pdf-header-footer",
		"--user-data-dir="+filepath.Join(tmp, "profile"),
		"--print-to-pdf="+out,
		url)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, 0, fmt.Errorf("chrome failed to start: %v", err)
	}
	// Chrome (macOS especially) can linger after the PDF is fully written —
	// background updater children keep the process alive. So don't wait for
	// exit: watch for the output file to appear and stop growing, then kill.
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	var lastSize int64 = -1
	done, timedOut := false, false
	for !done && !timedOut {
		select {
		case runErr := <-exited:
			if fi, statErr := os.Stat(out); statErr == nil && fi.Size() > 0 {
				done = true // exited cleanly after writing
			} else {
				msg := strings.TrimSpace(stderr.String())
				if len(msg) > 400 {
					msg = msg[len(msg)-400:]
				}
				return nil, 0, fmt.Errorf("chrome print failed: %v — %s", runErr, msg)
			}
		case <-ctx.Done():
			timedOut = true
		case <-time.After(300 * time.Millisecond):
			if fi, err := os.Stat(out); err == nil && fi.Size() > 0 {
				if fi.Size() == lastSize {
					done = true // written and stable across two polls
				}
				lastSize = fi.Size()
			}
		}
	}
	cmd.Process.Kill()
	if timedOut {
		return nil, 0, fmt.Errorf("chrome print timed out")
	}
	pdf, err := os.ReadFile(out)
	if err != nil {
		return nil, 0, fmt.Errorf("chrome produced no PDF: %v", err)
	}
	return pdf, pages, nil
}

func (s *Server) handleExportPDF(w http.ResponseWriter, r *http.Request) {
	deck := r.PathValue("deck")
	if !studio.ValidDeckName(deck) {
		jsonErr(w, fmt.Errorf("invalid deck"), http.StatusBadRequest)
		return
	}
	if !s.canView(r, deck) {
		jsonErr(w, fmt.Errorf("deck share access or presenter auth required"), http.StatusUnauthorized)
		return
	}
	s.refreshDeckLinks(r, deck)
	selected, err := exportSlideSelection(r, deck)
	if err != nil {
		jsonErr(w, err, http.StatusBadRequest)
		return
	}
	pdf, pages, err := s.renderDeckPDFForSlides(r, deck, selected)
	if err != nil {
		jsonErr(w, err, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	filename := deck + ".pdf"
	if len(selected) == 1 {
		filename = deck + "-" + selected[0] + ".pdf"
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-VSTD-Pages", strconv.Itoa(pages))
	w.Write(pdf)
}

func rasterizePDF(ctx context.Context, pdf []byte) ([][]byte, error) {
	pdftoppm, err := exec.LookPath("pdftoppm")
	if err != nil {
		return nil, fmt.Errorf("visual-exact PPTX export needs pdftoppm (Poppler) on this machine")
	}
	tmp, err := os.MkdirTemp("", "vstd-pptx-raster-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	in := filepath.Join(tmp, "deck.pdf")
	if err := os.WriteFile(in, pdf, 0o600); err != nil {
		return nil, err
	}
	prefix := filepath.Join(tmp, "slide")
	cmd := exec.CommandContext(ctx, pdftoppm, "-png", "-r", "96", in, prefix)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 500 {
			msg = msg[len(msg)-500:]
		}
		return nil, fmt.Errorf("rasterize PDF for PPTX: %v — %s", err, msg)
	}
	paths, err := filepath.Glob(prefix + "-*.png")
	if err != nil {
		return nil, err
	}
	sort.Slice(paths, func(i, j int) bool {
		page := func(path string) int {
			base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
			n, _ := strconv.Atoi(base[strings.LastIndex(base, "-")+1:])
			return n
		}
		return page(paths[i]) < page(paths[j])
	})
	if len(paths) == 0 {
		return nil, fmt.Errorf("rasterize PDF for PPTX produced no slides")
	}
	images := make([][]byte, 0, len(paths))
	for _, path := range paths {
		image, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		images = append(images, image)
	}
	return images, nil
}

func (s *Server) capturePPTXDeck(r *http.Request, deck string) (studio.PPTXDeck, error) {
	return s.capturePPTXDeckForSlides(r, deck, nil)
}

func (s *Server) capturePPTXDeckForSlides(r *http.Request, deck string, selected []string) (studio.PPTXDeck, error) {
	page, _, err := s.St.BuildPrintHTMLForSlides(deck, selected)
	if err != nil {
		return studio.PPTXDeck{}, err
	}
	chrome := findChrome()
	if chrome == "" {
		return studio.PPTXDeck{}, fmt.Errorf("PPTX export needs Chrome or Chromium on this machine — install one, or point VSTD_CHROME at a browser binary")
	}
	la, _ := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if la == nil {
		return studio.PPTXDeck{}, fmt.Errorf("cannot determine local server address")
	}
	_, port, err := net.SplitHostPort(la.String())
	if err != nil {
		return studio.PPTXDeck{}, fmt.Errorf("cannot determine local server port: %v", err)
	}
	key := s.putPrintJob(injectPPTXCapture(page))
	defer s.dropPrintJob(key)
	pageURL := fmt.Sprintf("http://127.0.0.1:%s/api/deck/%s/print.html?key=%s", port, deck, key)

	tmp, err := os.MkdirTemp("", "vstd-pptx-capture-*")
	if err != nil {
		return studio.PPTXDeck{}, err
	}
	defer os.RemoveAll(tmp)
	ctx, cancel := context.WithTimeout(r.Context(), 180*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, chrome, pptxChromeArgs(tmp, pageURL)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	dumpPath := filepath.Join(tmp, "capture.html")
	dumpFile, err := os.Create(dumpPath)
	if err != nil {
		return studio.PPTXDeck{}, err
	}
	defer dumpFile.Close()
	cmd.Stdout = dumpFile
	if err := cmd.Start(); err != nil {
		return studio.PPTXDeck{}, fmt.Errorf("chrome object capture failed to start: %v", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	var dump []byte
	for dump == nil {
		select {
		case runErr := <-exited:
			dumpFile.Sync()
			dump, _ = os.ReadFile(dumpPath)
			if runErr != nil && len(dump) == 0 {
				err = runErr
			}
		case <-ctx.Done():
			cmd.Process.Kill()
			err = ctx.Err()
			dump = []byte{}
		case <-time.After(250 * time.Millisecond):
			dumpFile.Sync()
			candidate, _ := os.ReadFile(dumpPath)
			if bytes.Contains(candidate, []byte(`<pre id="vstd-pptx-json">`)) && bytes.Contains(candidate, []byte(`</pre>`)) && bytes.Contains(candidate, []byte(`</html>`)) {
				dump = candidate
				cmd.Process.Kill() // Chrome can linger after --dump-dom on macOS.
			}
		}
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 500 {
			msg = msg[len(msg)-500:]
		}
		return studio.PPTXDeck{}, fmt.Errorf("chrome object capture failed: %v — %s", err, msg)
	}
	return parsePPTXCapture(dump)
}

func pptxChromeArgs(tmp, pageURL string) []string {
	return []string{
		"--headless", "--disable-gpu", "--no-sandbox", "--disable-dev-shm-usage",
		"--no-first-run", "--no-default-browser-check", "--disable-component-update",
		"--disable-background-networking", "--disable-sync", "--hide-scrollbars",
		// Object capture awaits image decoding and canvas encoding across the
		// entire deck. A 20s virtual-time budget can expire mid-script and leave
		// Chrome alive with an unresolved promise until the HTTP timeout. Give
		// the capture script the same budget as the request-level guard.
		"--window-size=1280,720", "--virtual-time-budget=180000",
		"--run-all-compositor-stages-before-draw", "--user-data-dir=" + filepath.Join(tmp, "profile"),
		"--dump-dom", pageURL,
	}
}

// handleExportPPTX defaults to the visual-exact path: the browser renders the
// same print HTML used by PDF export and each page becomes one full-bleed PNG
// in PowerPoint. mode=editable retains the older best-effort native-object
// conversion for users who prefer editability over pixel fidelity.
func (s *Server) handleExportPPTX(w http.ResponseWriter, r *http.Request) {
	deck := r.PathValue("deck")
	if !studio.ValidDeckName(deck) {
		jsonErr(w, fmt.Errorf("invalid deck"), http.StatusBadRequest)
		return
	}
	if s.Collab != nil {
		ps, ok := s.playerSessionForDeck(r, deck)
		if !ok || (ps.Mode != "present" && ps.Mode != "edit") || !s.Collab.Can(r.Context(), ps.User.ID, ps.Deck, "present") {
			jsonErr(w, fmt.Errorf("presenter access required"), http.StatusUnauthorized)
			return
		}
	} else if !s.isPresenter(r) {
		jsonErr(w, fmt.Errorf("presenter auth required"), http.StatusUnauthorized)
		return
	}
	s.refreshDeckLinks(r, deck)
	selected, err := exportSlideSelection(r, deck)
	if err != nil {
		jsonErr(w, err, http.StatusBadRequest)
		return
	}
	_, exportIDs, err := s.St.BuildPrintHTMLForSlides(deck, selected)
	if err != nil {
		jsonErr(w, err, http.StatusBadRequest)
		return
	}
	editable := r.URL.Query().Get("mode") == "editable"
	var pptx []byte
	var pages, total, paths int
	var cacheStats powerpointCacheStats
	if editable {
		meta, metaErr := s.St.LoadDeckMeta(deck)
		if metaErr != nil {
			err = metaErr
		}
		var slides []studio.PPTXSlide
		if err == nil {
			slides, cacheStats, err = s.cachedEditablePowerPointSlides(r, deck, exportIDs)
		}
		model := studio.PPTXDeck{Slides: slides}
		if metaErr == nil {
			model.Title = meta.Title
		}
		if err == nil {
			pptx, err = studio.BuildPPTX(model)
		}
		pages = len(slides)
		for _, slide := range slides {
			total += len(slide.Elements)
			for _, element := range slide.Elements {
				if element.Kind == "path" || element.Kind == "line" {
					paths++
				}
			}
		}
	} else {
		ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
		images, stats, cacheErr := s.cachedVisualPowerPointSlides(ctx, r, deck, exportIDs)
		cancel()
		cacheStats = stats
		err = cacheErr
		pages = len(images)
		if err == nil {
			meta, metaErr := s.St.LoadDeckMeta(deck)
			if metaErr != nil {
				err = metaErr
			} else {
				pptx, err = studio.BuildRasterPPTX(meta.Title, images)
				total = len(images)
			}
		}
	}
	if err != nil {
		jsonErr(w, err, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.presentationml.presentation")
	filename := deck + ".pptx"
	mode := "visual-exact"
	if editable {
		filename = deck + "-editable.pptx"
		mode = "editable"
	}
	if len(selected) == 1 {
		filename = deck + "-" + exportIDs[0] + map[bool]string{true: "-editable", false: ""}[editable] + ".pptx"
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-VSTD-Pages", strconv.Itoa(pages))
	w.Header().Set("X-VSTD-PPTX-Mode", mode)
	w.Header().Set("X-VSTD-Objects", strconv.Itoa(total))
	w.Header().Set("X-VSTD-Vector-Paths", strconv.Itoa(paths))
	w.Header().Set("X-VSTD-Cache-Hits", strconv.Itoa(cacheStats.Hits))
	w.Header().Set("X-VSTD-Cache-Misses", strconv.Itoa(cacheStats.Misses))
	w.Write(pptx)
}

func exportSlideSelection(r *http.Request, deck string) ([]string, error) {
	id := strings.TrimSpace(r.URL.Query().Get("slide"))
	if id == "" {
		return nil, nil
	}
	if !studio.ValidSlideID(id) {
		return nil, fmt.Errorf("invalid slide")
	}
	return []string{id}, nil
}
