// Agent worker: executes queued redesign requests by running a headless
// Claude session inside the studio root. The queue is the file contract
// itself — unresolved "## Edit requests" bullets in slide companions.
// Progress is reported through the same "(in progress — NN%)" markers the
// status endpoint already reads, so the player's progress bars track the
// worker for free. Scope is enforced post-hoc with git: any change outside
// decks/, library/ or requests/ is reverted.
//
// Enable with VSTD_AGENT=1 (requires the claude CLI on PATH and
// ANTHROPIC_API_KEY or existing CLI auth). Optional:
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
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

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
}

// StartAgent launches the background worker when VSTD_AGENT=1.
func (s *Server) StartAgent() {
	if os.Getenv("VSTD_AGENT") != "1" {
		return
	}
	w := &agentWorker{s: s, maxPerHour: 6, bin: "claude", branch: "main",
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
	go w.loop()
}

func (w *agentWorker) loop() {
	w.bootstrapGit()
	log.Printf("agent: worker enabled (cmd=%s, max %d/hour, concurrency %d, push=%v)", w.bin, w.maxPerHour, w.conc, w.push)
	for {
		time.Sleep(15 * time.Second)
		for _, c := range w.nextAll() {
			key := c[0] + "/" + c[1]
			w.mu.Lock()
			busy := w.inflight[key]
			slots := len(w.inflight)
			if !busy && slots < w.conc {
				w.inflight[key] = true
			}
			w.mu.Unlock()
			if busy || slots >= w.conc || !w.allow() {
				continue
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
(ignore "- resolved:" lines and the "(in progress" marker). Follow the repo
contract in CLAUDE.md strictly: companion-first, update the companion with
every change, meet the formatting standard, reuse library assets
(library/manifest.json) before generating. If new imagery is required, drop
a yaml into requests/ with deck: and slide: fields and rewrite that bullet
prefixed "- awaiting imagery: …" so the queue knows it is on hold; complete
everything else.

Progress: update the "(in progress — cloud agent — NN%%)" marker line in the
companion as you work — 60 while editing, 90 before finishing.

If the section contains "- awaiting imagery:" bullets whose generation
request is NO LONGER in requests/ (the asset now exists — find the newest
matching entry in library/manifest.json), execute the landing plan written
in that bullet, place the asset on the slide, and resolve the bullet.

When done: move each completed request bullet into "## Log" as
"- resolved: ..." and delete the in-progress marker line.

STRICT SCOPE: modify ONLY files under decks/%s/, library/, and requests/.
Never touch themes/, engine files, other decks, or git state.`, deck, id, deck, id, deck)

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, w.bin, "--dangerously-skip-permissions", "-p", prompt)
	cmd.Dir = w.s.St.Root
	cmd.Env = os.Environ()
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
	log.Printf("agent: pass complete — %s/%s", deck, id)
	if w.push {
		w.gitPush(deck, id)
	}
	w.s.Broadcast("reload")
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
