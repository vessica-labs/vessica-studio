package server

import (
	"bytes"
	"context"
	"crypto/rand"
	_ "embed"
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

	"github.com/vessica-labs/vessica-studio/internal/chromium"
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

//go:embed pptx_capture.js
var pptxCaptureJS string

func injectPPTXCapture(page string) string {
	return strings.Replace(page, "</body>", "<script>"+pptxCaptureJS+"</script></body>", 1)
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
	if r.Context().Value(editorRenderKey{}) == true || !s.isPresenter(r) {
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

	ctx, cancel := context.WithTimeout(r.Context(), 180*time.Second)
	defer cancel()
	// Virtual-time dump-dom can finish while asynchronous image decoding is
	// still pending. Poll the capture's completion marker over DevTools instead.
	dump, err := chromium.EvaluateWithViewport(ctx, chrome, pageURL, `(()=>{
 const result=document.getElementById('vstd-pptx-json')||document.getElementById('vstd-pptx-error');
 return result?document.documentElement.outerHTML:'';
})()`, 1280, 720)
	if err != nil {
		return studio.PPTXDeck{}, fmt.Errorf("chrome object capture: %w", err)
	}
	return parsePPTXCapture([]byte(dump))
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
		if err == nil && r.URL.Query().Get("media") == "plan" {
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, map[string]any{"videos": pptxVideoIDs(model)})
			return
		}
		if err == nil {
			err = s.hydratePPTXVideos(r, &model)
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
