package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/vessica-labs/vessica-studio/internal/studio"
)

// PDF export: GET /api/deck/{deck}/export.pdf (presenter-only) builds a
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

func (s *Server) handleExportPDF(w http.ResponseWriter, r *http.Request) {
	if !s.isPresenter(r) {
		jsonErr(w, fmt.Errorf("presenter auth required"), http.StatusUnauthorized)
		return
	}
	deck := r.PathValue("deck")
	if !studio.ValidDeckName(deck) {
		jsonErr(w, fmt.Errorf("invalid deck"), 400)
		return
	}
	html, pages, err := s.St.BuildPrintHTML(deck)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	chrome := findChrome()
	if chrome == "" {
		jsonErr(w, fmt.Errorf("PDF export needs Chrome or Chromium on this machine — install one, or point VSTD_CHROME at a browser binary"), 500)
		return
	}

	// Chrome loads the print page from this same server so /library and
	// /assets URLs in slides resolve. Reach it via loopback on whatever port
	// this request came in on.
	la, _ := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if la == nil {
		jsonErr(w, fmt.Errorf("cannot determine local server address"), 500)
		return
	}
	_, port, err := net.SplitHostPort(la.String())
	if err != nil {
		jsonErr(w, fmt.Errorf("cannot determine local server port: %v", err), 500)
		return
	}
	key := s.putPrintJob(html)
	defer s.dropPrintJob(key)
	url := fmt.Sprintf("http://127.0.0.1:%s/api/deck/%s/print.html?key=%s", port, deck, key)

	tmp, err := os.MkdirTemp("", "vstd-pdf-*")
	if err != nil {
		jsonErr(w, err, 500)
		return
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
		jsonErr(w, fmt.Errorf("chrome failed to start: %v", err), 500)
		return
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
		case err := <-exited:
			if fi, statErr := os.Stat(out); statErr == nil && fi.Size() > 0 {
				done = true // exited cleanly after writing
			} else {
				msg := strings.TrimSpace(stderr.String())
				if len(msg) > 400 {
					msg = msg[len(msg)-400:]
				}
				jsonErr(w, fmt.Errorf("chrome print failed: %v — %s", err, msg), 500)
				return
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
		jsonErr(w, fmt.Errorf("chrome print timed out"), http.StatusGatewayTimeout)
		return
	}
	pdf, err := os.ReadFile(out)
	if err != nil {
		jsonErr(w, fmt.Errorf("chrome produced no PDF: %v", err), 500)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `attachment; filename="`+deck+`.pdf"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-VSTD-Pages", strconv.Itoa(pages))
	w.Write(pdf)
}
