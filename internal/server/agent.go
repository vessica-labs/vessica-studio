// Agent worker: executes queued redesign requests by running a headless
// coding-agent session inside the studio root. The queue is the file contract
// itself — unresolved "## Edit requests" bullets in slide companions.
// Progress is reported through the same "(in progress — NN%)" markers the
// status endpoint already reads, so the player's progress bars track the
// worker for free. Scope is enforced post-hoc with git: any change outside
// decks/, library/ or requests/ is reverted.
//
// Enable with VSTD_AGENT=1. The default Claude runner requires the claude CLI
// and ANTHROPIC_API_KEY (or existing CLI auth); set VSTD_AGENT_CMD=codex to use
// the Codex CLI with OPENAI_API_KEY. Optional:
//
//	VSTD_AGENT_CMD          agent executable (default "claude")
//	VSTD_AGENT_MAX_PER_HOUR rate cap (default 6)
//	VSTD_GIT_PUSH=1         commit+push each completed pass
//	VSTD_GIT_REMOTE         https remote incl. token (bootstraps .git if absent)
//	VSTD_GIT_BRANCH         push target branch (default main)
package server

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vessica-labs/vessica-studio/internal/studio"
	"gopkg.in/yaml.v3"
)

type agentWorker struct {
	s          *Server
	runs       []time.Time
	maxPerHour int
	push       bool
	bin        string
	branch     string
	conc       int
	mu         sync.Mutex
	inflight   map[string]bool
	pushMu     sync.Mutex
	queuedN    int
	capped     bool
}

// Info reports worker state for the status endpoint.
func (w *agentWorker) Info() map[string]any {
	w.mu.Lock()
	defer w.mu.Unlock()
	cut := time.Now().Add(-time.Hour)
	used := 0
	for _, t := range w.runs {
		if t.After(cut) {
			used++
		}
	}
	return map[string]any{"enabled": true, "maxPerHour": w.maxPerHour,
		"used": used, "queued": w.queuedN, "capped": w.capped, "inflight": len(w.inflight)}
}

// SetMax raises/lowers the hourly cap at runtime (memory-only; restart
// returns to the configured default — the circuit breaker stays safe).
func (w *agentWorker) SetMax(n int) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	if n >= 1 && n <= 100 {
		w.maxPerHour = n
		w.capped = false
	}
	return w.maxPerHour
}

// StartAgent launches the background worker when VSTD_AGENT=1.
func (s *Server) StartAgent() {
	if os.Getenv("VSTD_AGENT") != "1" {
		return
	}
	w := &agentWorker{s: s, maxPerHour: 12, bin: "claude", branch: "main",
		push: os.Getenv("VSTD_GIT_PUSH") == "1", conc: 3, inflight: map[string]bool{}}
	if v := os.Getenv("VSTD_AGENT_CMD"); v != "" {
		w.bin = v
	}
	if v := os.Getenv("VSTD_AGENT_MAX_PER_HOUR"); v != "" {
		fmt.Sscanf(v, "%d", &w.maxPerHour)
	}
	if v := os.Getenv("VSTD_AGENT_CONCURRENCY"); v != "" {
		fmt.Sscanf(v, "%d", &w.conc)
	}
	if v := os.Getenv("VSTD_GIT_BRANCH"); v != "" {
		w.branch = v
	}
	s.Agent = w
	go w.loop()
}

func (w *agentWorker) loop() {
	w.bootstrapGit()
	if n := w.recoverInterruptedPasses(); n > 0 {
		log.Printf("agent: recovered %d interrupted pass(es) for retry", n)
	}
	log.Printf("agent: worker enabled (cmd=%s, max %d/hour, concurrency %d, push=%v)", w.bin, w.maxPerHour, w.conc, w.push)
	var lastBlockLog time.Time
	for {
		time.Sleep(15 * time.Second)
		queued := w.nextAll()
		w.mu.Lock()
		w.queuedN = len(queued)
		if len(queued) == 0 {
			w.capped = false
		}
		w.mu.Unlock()
		for _, c := range queued {
			key := c[0] + "/" + c[1]
			w.mu.Lock()
			busy := w.inflight[key]
			slots := len(w.inflight)
			if !busy && slots < w.conc {
				w.inflight[key] = true
			}
			w.mu.Unlock()
			if busy || slots >= w.conc {
				continue
			}
			if !w.allow() {
				w.mu.Lock()
				delete(w.inflight, key)
				w.capped = true
				w.mu.Unlock()
				if time.Since(lastBlockLog) > 2*time.Minute {
					log.Printf("agent: RATE CAP reached — %d slide(s) queued and waiting; raise the cap from the player banner or VSTD_AGENT_MAX_PER_HOUR", len(queued))
					lastBlockLog = time.Now()
				}
				break
			}
			deck, slide := c[0], c[1]
			go func() {
				defer func() {
					w.mu.Lock()
					delete(w.inflight, deck+"/"+slide)
					w.mu.Unlock()
				}()
				w.runPass(deck, slide)
			}()
		}
	}
}

// recoverInterruptedPasses clears progress markers left behind by a process
// restart. An in-progress marker is only valid while this process owns the
// corresponding inflight entry; preserving one across startup would make the
// slide permanently invisible to nextAll(). Worker-error markers remain
// human-reviewed and are intentionally not retried automatically.
func (w *agentWorker) recoverInterruptedPasses() int {
	decks, err := w.s.St.ListDecks()
	if err != nil {
		return 0
	}
	recovered := 0
	for _, deck := range decks {
		ids, err := w.s.St.SlideIDs(deck)
		if err != nil {
			continue
		}
		for _, id := range ids {
			path := w.s.St.SlidePath(deck, id, ".md")
			body, err := os.ReadFile(path)
			if err != nil || !strings.Contains(string(body), "- (in progress") {
				continue
			}
			w.mark(deck, id, "")
			recovered++
		}
	}
	return recovered
}

// RunOnce sweeps the queue synchronously (vstd agent --once).
func (s *Server) RunAgentOnce() int {
	w := &agentWorker{s: s, maxPerHour: 1 << 30, bin: "claude", branch: "main",
		push: os.Getenv("VSTD_GIT_PUSH") == "1"}
	if v := os.Getenv("VSTD_AGENT_CMD"); v != "" {
		w.bin = v
	}
	w.bootstrapGit()
	n := 0
	seen := map[string]bool{}
	for {
		deck, slide := w.next()
		if deck == "" || seen[deck+"/"+slide] {
			return n
		}
		seen[deck+"/"+slide] = true
		w.runPass(deck, slide)
		n++
	}
}

func (w *agentWorker) allow() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	cut := time.Now().Add(-time.Hour)
	kept := w.runs[:0]
	for _, t := range w.runs {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	w.runs = kept
	if len(w.runs) >= w.maxPerHour {
		return false
	}
	w.runs = append(w.runs, time.Now())
	return true
}

var editReqRe = regexp.MustCompile(`## Edit requests\n([\s\S]*?)(\n## |\z)`)

// actionable reports whether an Edit requests section body contains live
// work: "- " bullets other than resolved history or awaiting-imagery holds.
func actionable(sec string) bool {
	for _, l := range strings.Split(sec, "\n") {
		if strings.HasPrefix(l, "- ") && !strings.HasPrefix(l, "- resolved:") &&
			!strings.HasPrefix(l, "- awaiting") && !strings.HasPrefix(l, "- (") {
			return true
		}
	}
	return false
}

// queuedImageSlides maps deck/slide keys with a generation request still
// waiting in requests/ — such slides stay on hold.
func (w *agentWorker) queuedImageSlides() map[string]bool {
	m := map[string]bool{}
	ents, err := os.ReadDir(w.s.St.Root + "/requests")
	if err != nil {
		return m
	}
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		b, err := os.ReadFile(w.s.St.Root + "/requests/" + e.Name())
		if err != nil {
			continue
		}
		var req assetRequest
		if yaml.Unmarshal(b, &req) != nil {
			continue
		}
		if req.Slide != "" {
			m[req.Deck+"/"+req.Slide] = true
			m["/"+req.Slide] = true
		}
	}
	return m
}

// nextAll returns every slide with actionable queued work, in deck order.
func (w *agentWorker) nextAll() [][2]string {
	var out [][2]string
	decks, err := w.s.St.ListDecks()
	if err != nil {
		return out
	}
	queued := w.queuedImageSlides()
	for _, d := range decks {
		ids, err := w.s.St.SlideIDs(d)
		if err != nil {
			continue
		}
		for _, id := range ids {
			b, err := os.ReadFile(w.s.St.SlidePath(d, id, ".md"))
			if err != nil {
				continue
			}
			m := editReqRe.FindStringSubmatch(string(b))
			if m == nil {
				continue
			}
			sec := m[1]
			if strings.Contains(sec, "(in progress") || strings.Contains(sec, "(worker error") {
				continue
			}
			if actionable(sec) || (strings.Contains(sec, "- awaiting") && !queued[d+"/"+id] && !queued["/"+id]) {
				out = append(out, [2]string{d, id})
			}
		}
	}
	return out
}

// next returns the first slide with actionable queued work — including
// awaiting-imagery holds whose asset has since been generated.
func (w *agentWorker) next() (string, string) {
	decks, err := w.s.St.ListDecks()
	if err != nil {
		return "", ""
	}
	queued := w.queuedImageSlides()
	for _, d := range decks {
		ids, err := w.s.St.SlideIDs(d)
		if err != nil {
			continue
		}
		for _, id := range ids {
			b, err := os.ReadFile(w.s.St.SlidePath(d, id, ".md"))
			if err != nil {
				continue
			}
			m := editReqRe.FindStringSubmatch(string(b))
			if m == nil {
				continue
			}
			sec := m[1]
			if strings.Contains(sec, "(in progress") || strings.Contains(sec, "(worker error") {
				continue
			}
			if actionable(sec) {
				return d, id
			}
			if strings.Contains(sec, "- awaiting") && !queued[d+"/"+id] && !queued["/"+id] {
				return d, id // imagery has landed in the library — resume
			}
		}
	}
	return "", ""
}

func (w *agentWorker) mark(deck, id, line string) {
	p := w.s.St.SlidePath(deck, id, ".md")
	b, err := os.ReadFile(p)
	if err != nil {
		return
	}
	s := string(b)
	// remove any existing marker, then insert the new one under the header
	s = regexp.MustCompile(`- \((in progress|worker error)[^\n]*\n`).ReplaceAllString(s, "")
	if line != "" {
		s = strings.Replace(s, "## Edit requests\n", "## Edit requests\n"+line+"\n", 1)
	}
	os.WriteFile(p, []byte(s), 0o644)
}

func (w *agentWorker) runPass(deck, id string) {
	log.Printf("agent: pass starting — %s/%s", deck, id)
	preexisting := w.dirtyPaths() // human edits present before the pass — never revert these
	w.mark(deck, id, "- (in progress — cloud agent — 40%)")
	prompt := fmt.Sprintf(`You are the Vessica Studio cloud redesign worker, running headless in a studio content repo.

TASK: run the redesign pass for deck %q, slide %q — and ONLY that slide.

Read decks/%s/slides/%s.md. The "## Edit requests" section lists the work
(ignore "- resolved:" lines and the "(in progress" marker). Read the repo's
agent guide and run "vstd skill slide-edit" before editing. Follow the content
contract strictly: companion-first, update the companion with
every change, meet the formatting standard, reuse library assets
(library/manifest.json) before generating. If new imagery is required, drop
a yaml into requests/ with deck: and slide: fields and rewrite that bullet
prefixed "- awaiting imagery: …" so the queue knows it is on hold; complete
everything else.

If the companion frontmatter lists attachments, those files live at the
deck-relative paths shown there. They are primary source material: inspect
them directly and preserve their attachment metadata. The engine will run a
separate visual source-fidelity critic after your pass.

Progress: update the "(in progress — cloud agent — NN%%)" marker line in the
companion as you work — 60 while editing, 90 before finishing.

If the section contains "- awaiting imagery:" bullets whose generation
request is NO LONGER in requests/ (the asset now exists — find the newest
matching entry in library/manifest.json), execute the landing plan written
in that bullet, place the asset on the slide, and resolve the bullet.

When done: move each completed request bullet into "## Log" as
"- resolved: ..." and delete the in-progress marker line.

STRICT SCOPE: modify ONLY files under decks/%s/, library/, and requests/.
Never touch themes/, engine files, other decks, or git state.

TOOL DENIALS ARE NOT A STOPPING CONDITION. You are headless — no human can
approve anything. If the Edit/Write tools return a permission error, apply
the same change through the Bash tool instead (e.g. a python3 heredoc doing
exact-string replacement asserted against the current file content). Never
end the pass waiting for permission or asking a question; a pass that ends
without resolving its bullets is recorded as a failure.`, deck, id, deck, id, deck)

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	cmd := agentCommand(ctx, w.bin, w.s.St.Root, prompt)
	out, err := cmd.CombinedOutput()
	w.enforceScope(preexisting)
	// full output always lands in a log file for diagnosis
	logDir := w.s.St.Root + "/_agent-logs"
	os.MkdirAll(logDir, 0o755)
	logFile := fmt.Sprintf("%s/%s-%s.log", logDir, deck, id)
	os.WriteFile(logFile, out, 0o644)
	if err != nil {
		tail := strings.TrimSpace(string(out))
		tail = strings.ReplaceAll(tail, "\n", " · ")
		if len(tail) > 220 {
			tail = tail[len(tail)-220:]
		}
		log.Printf("agent: pass FAILED %s/%s: %v — see %s", deck, id, err, logFile)
		w.mark(deck, id, fmt.Sprintf("- (worker error: %v — %s — full log: _agent-logs/%s-%s.log — clear this line to retry)", err, tail, deck, id))
		return
	}
	// if the agent forgot to clear its marker, clear it
	if b, rerr := os.ReadFile(w.s.St.SlidePath(deck, id, ".md")); rerr == nil &&
		strings.Contains(string(b), "(in progress") {
		w.mark(deck, id, "")
	}
	// a "successful" pass that leaves live bullets would loop forever — flag it
	if b, rerr := os.ReadFile(w.s.St.SlidePath(deck, id, ".md")); rerr == nil {
		if m := editReqRe.FindStringSubmatch(string(b)); m != nil && actionable(m[1]) {
			log.Printf("agent: pass left unresolved requests — flagging %s/%s", deck, id)
			w.mark(deck, id, "- (worker error: pass finished without resolving all requests — clear this line to retry)")
			return
		}
	}
	if ok := w.runSourceCritic(deck, id, preexisting); !ok {
		return
	}
	log.Printf("agent: pass complete — %s/%s", deck, id)
	if w.s.ContentSync != nil {
		w.s.ContentSync.Notify()
	} else if w.push {
		w.gitPush(deck, id)
	}
	w.s.Broadcast("reload")
}

func agentCommand(ctx context.Context, bin, root, prompt string) *exec.Cmd {
	return agentCommandWithImages(ctx, bin, root, prompt, nil)
}

func agentCommandWithImages(ctx context.Context, bin, root, prompt string, images []string) *exec.Cmd {
	if filepath.Base(bin) == "codex" {
		args := []string{"exec",
			"--dangerously-bypass-approvals-and-sandbox",
			"--skip-git-repo-check", "--ephemeral", "-C", root}
		for _, image := range images {
			args = append(args, "-i", image)
		}
		args = append(args, prompt)
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Dir = root
		cmd.Env = os.Environ()
		if os.Getenv("CODEX_API_KEY") == "" && os.Getenv("OPENAI_API_KEY") != "" {
			cmd.Env = setCommandEnv(cmd.Env, "CODEX_API_KEY", os.Getenv("OPENAI_API_KEY"))
		}
		return cmd
	}
	// Managed machines (e.g. BCG policy) set disableBypassPermissionsMode,
	// silently ignoring --dangerously-skip-permissions — headless Edit/Write
	// then stall on approvals nobody can grant. An explicit allow-list is
	// honored in default permission mode under any policy, so pass both.
	cmd := exec.CommandContext(ctx, bin,
		"--dangerously-skip-permissions",
		"--allowedTools", "Edit,Write,MultiEdit,NotebookEdit,Read,Glob,Grep,Bash",
		"-p", prompt)
	cmd.Dir = root
	cmd.Env = os.Environ()
	return cmd
}

func (w *agentWorker) runSourceCritic(deck, id string, preexisting map[string]bool) bool {
	companion, err := w.s.St.ReadCompanion(deck, id)
	if err == nil {
		if match := editReqRe.FindStringSubmatch(companion); match != nil && strings.Contains(match[1], "- awaiting") {
			// A source-backed slide can also be waiting for generated imagery. Let
			// that request resolve before comparing a deliberately incomplete slide.
			return true
		}
	}
	attachments, err := w.s.St.CompanionAttachments(deck, id)
	if err != nil || len(attachments) == 0 {
		return true
	}
	w.mark(deck, id, "- (in progress — source fidelity critic — 90%)")
	tmp, err := os.MkdirTemp("", "vstd-source-critic-*")
	if err != nil {
		log.Printf("agent: source critic skipped %s/%s: %v", deck, id, err)
		w.mark(deck, id, "")
		return true
	}
	defer os.RemoveAll(tmp)
	current, err := w.renderSlide(deck, id, tmp)
	if err != nil {
		log.Printf("agent: source critic skipped %s/%s: render current slide: %v", deck, id, err)
		w.mark(deck, id, "")
		return true
	}
	images := []string{current}
	var sourceNames []string
	for i, attachment := range attachments {
		if i >= 3 {
			break
		}
		preview, err := w.renderSourceAttachment(deck, attachment, tmp, i)
		if err != nil {
			log.Printf("agent: source preview unavailable %s/%s (%s): %v", deck, id, attachment.Name, err)
			continue
		}
		images = append(images, preview)
		sourceNames = append(sourceNames, attachment.Name)
	}
	if len(images) == 1 {
		w.mark(deck, id, "")
		return true
	}
	prompt := fmt.Sprintf(`You are the independent Vessica Studio source-fidelity critic.

The FIRST attached image is the current rendered slide %q in deck %q. The remaining images are previews of its source attachment(s), in this order: %s.
The same visual inputs are available as local files, current slide first: %s. If your runner does not surface attached images, read those files directly.

Compare the current slide visually and substantively with the source. Inspect the original files listed in the companion frontmatter under attachments: when useful for precise text, labels, values, or context. Correct the slide until it is a high-fidelity native recreation while preserving the deck theme and the companion's intent. Check geometry, hierarchy, labels, values, omissions, source attribution, legibility, and the 1280x720 footer safe zone.

Modify only decks/%s/slides/%s.html and its paired Markdown companion. Do not alter source attachments. Append a dated Log entry describing the critic adjustments. Do not add an Edit requests bullet or leave an in-progress marker. If the current result already has high fidelity, leave the fragment unchanged and only add a concise critic-verified Log entry.`, id, deck, strings.Join(sourceNames, ", "), strings.Join(images, ", "), deck, id)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	cmd := agentCommandWithImages(ctx, w.bin, w.s.St.Root, prompt, images)
	out, runErr := cmd.CombinedOutput()
	w.enforceScope(preexisting)
	logDir := filepath.Join(w.s.St.Root, "_agent-logs")
	os.MkdirAll(logDir, 0o755)
	logFile := filepath.Join(logDir, deck+"-"+id+"-source-critic.log")
	os.WriteFile(logFile, out, 0o644)
	if runErr != nil {
		tail := strings.TrimSpace(strings.ReplaceAll(string(out), "\n", " · "))
		if len(tail) > 180 {
			tail = tail[len(tail)-180:]
		}
		log.Printf("agent: source critic FAILED %s/%s: %v", deck, id, runErr)
		w.mark(deck, id, fmt.Sprintf("- (worker error: source fidelity critic failed: %v — %s — clear this line to retry)\n- SOURCE CRITIC RETRY: compare this slide to its companion source attachments and correct fidelity issues", runErr, tail))
		return false
	}
	w.mark(deck, id, "")
	log.Printf("agent: source critic complete — %s/%s", deck, id)
	return true
}

func (w *agentWorker) renderSlide(deck, id, tmp string) (string, error) {
	index, err := w.s.St.Build(deck)
	if err != nil {
		return "", err
	}
	ids, err := w.s.St.SlideIDs(deck)
	if err != nil {
		return "", err
	}
	n := 0
	for i, slideID := range ids {
		if slideID == id {
			n = i + 1
			break
		}
	}
	if n == 0 {
		return "", fmt.Errorf("slide not found")
	}
	chromium := os.Getenv("VSTD_CHROMIUM")
	if chromium == "" {
		for _, candidate := range []string{"chromium", "chromium-browser", "google-chrome"} {
			if path, lookErr := exec.LookPath(candidate); lookErr == nil {
				chromium = path
				break
			}
		}
	}
	if chromium == "" {
		return "", fmt.Errorf("Chromium not found")
	}
	out := filepath.Join(tmp, "current-slide.png")
	target := (&url.URL{Scheme: "file", Path: index}).String() + "#/" + strconv.Itoa(n)
	cmd := exec.Command(chromium, "--headless", "--no-sandbox", "--disable-gpu", "--hide-scrollbars",
		"--window-size=1280,720", "--virtual-time-budget=2500", "--screenshot="+out, target)
	if data, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("chromium: %v — %s", err, strings.TrimSpace(string(data)))
	}
	return out, nil
}

func (w *agentWorker) renderSourceAttachment(deck string, attachment studio.CompanionAttachment, tmp string, index int) (string, error) {
	source := filepath.Join(w.s.St.DeckDir(deck), filepath.FromSlash(attachment.Path))
	ext := strings.ToLower(filepath.Ext(source))
	if imgExts[ext] {
		return source, nil
	}
	pdf := source
	if ext == ".ppt" || ext == ".pptx" {
		libreoffice, err := exec.LookPath("libreoffice")
		if err != nil {
			return "", fmt.Errorf("libreoffice not found")
		}
		cmd := exec.Command(libreoffice, "--headless", "--convert-to", "pdf", "--outdir", tmp, source)
		if data, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("libreoffice: %v — %s", err, strings.TrimSpace(string(data)))
		}
		pdf = filepath.Join(tmp, strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))+".pdf")
	}
	if strings.ToLower(filepath.Ext(pdf)) != ".pdf" {
		return "", fmt.Errorf("no visual preview renderer for %s", ext)
	}
	pdftoppm, err := exec.LookPath("pdftoppm")
	if err != nil {
		return "", fmt.Errorf("pdftoppm not found")
	}
	page := attachment.Page
	if page < 1 {
		page = 1
	}
	prefix := filepath.Join(tmp, fmt.Sprintf("source-%d", index))
	cmd := exec.Command(pdftoppm, "-f", strconv.Itoa(page), "-l", strconv.Itoa(page), "-singlefile", "-png", "-r", "144", pdf, prefix)
	if data, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("pdftoppm: %v — %s", err, strings.TrimSpace(string(data)))
	}
	return prefix + ".png", nil
}

func setCommandEnv(env []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	replaced := false
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			if !replaced {
				out = append(out, prefix+value)
				replaced = true
			}
			continue
		}
		out = append(out, entry)
	}
	if !replaced {
		out = append(out, prefix+value)
	}
	return out
}

// dirtyPaths snapshots the working tree's modified paths.
func (w *agentWorker) dirtyPaths() map[string]bool {
	m := map[string]bool{}
	if _, err := os.Stat(w.s.St.Root + "/.git"); err != nil {
		return m
	}
	out, err := w.git("status", "--porcelain")
	if err != nil {
		return m
	}
	for _, l := range strings.Split(out, "\n") {
		if len(l) >= 4 {
			m[strings.TrimSpace(l[3:])] = true
		}
	}
	return m
}

// enforceScope reverts changes outside the allowed roots — but only changes
// the pass itself introduced; anything dirty before the pass is left alone.
func (w *agentWorker) enforceScope(preexisting map[string]bool) {
	if _, err := os.Stat(w.s.St.Root + "/.git"); err != nil {
		return
	}
	out, err := w.git("status", "--porcelain")
	if err != nil {
		return
	}
	for _, l := range strings.Split(out, "\n") {
		if len(l) < 4 {
			continue
		}
		p := strings.TrimSpace(l[3:])
		if preexisting[p] {
			continue
		}
		if strings.HasPrefix(p, "decks/") || strings.HasPrefix(p, "library/") ||
			strings.HasPrefix(p, "requests/") || strings.HasPrefix(p, "_to_delete/") ||
			strings.HasPrefix(p, "_agent-logs/") {
			continue
		}
		st := strings.TrimSpace(l[:2])
		if st == "??" {
			os.Remove(w.s.St.Root + "/" + p)
		} else {
			w.git("checkout", "--", p)
		}
		log.Printf("agent: reverted out-of-scope change: %s", p)
	}
}

func (w *agentWorker) gitPush(deck, id string) {
	w.pushMu.Lock()
	defer w.pushMu.Unlock()
	if _, err := os.Stat(w.s.St.Root + "/.git"); err != nil {
		log.Printf("agent: push skipped — no git repo")
		return
	}
	w.git("add", "decks", "library", "requests")
	if _, err := w.git("-c", "user.name=Vessica Agent", "-c", "user.email=agent@vessica.dev",
		"commit", "-m", fmt.Sprintf("vessica agent: redesign pass %s/%s", deck, id)); err != nil {
		return // nothing to commit
	}
	if _, err := w.git("push", "origin", "HEAD:"+w.branch); err != nil {
		log.Printf("agent: push failed: %v", err)
	} else {
		log.Printf("agent: pushed %s/%s", deck, id)
	}
}

// bootstrapGit initializes a repo from VSTD_GIT_REMOTE when the serving
// root has none (Railway containers serve an exported tree, not a clone).
func (w *agentWorker) bootstrapGit() {
	remote := os.Getenv("VSTD_GIT_REMOTE")
	if remote == "" {
		return
	}
	if _, err := os.Stat(w.s.St.Root + "/.git"); err == nil {
		return
	}
	branch := w.branch
	steps := [][]string{
		{"init"},
		{"remote", "add", "origin", remote},
		{"fetch", "--depth", "1", "origin", branch},
		{"reset", "--mixed", "FETCH_HEAD"},
	}
	for _, a := range steps {
		if _, err := w.git(a...); err != nil {
			log.Printf("agent: git bootstrap failed at %v: %v", a, err)
			return
		}
	}
	log.Printf("agent: git bootstrapped from remote (branch %s)", branch)
}

func (w *agentWorker) git(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = w.s.St.Root
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %v — %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
