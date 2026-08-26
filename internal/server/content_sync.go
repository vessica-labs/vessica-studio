package server

// Content sync gives a single hosted Vessica Studio instance a writable,
// Git-backed content tree without coupling ordinary player saves to the
// redesign agent. The running filesystem changes immediately; this worker
// batches those changes into small commits and periodically reconciles them
// with the configured GitHub branch.

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	syncStateStarting = "starting"
	syncStateReady    = "ready"
	syncStatePending  = "pending"
	syncStateSynced   = "synced"
	syncStateError    = "error"
)

var contentSyncPaths = []string{"decks", "themes", "library", "requests", "site"}

type contentSyncConfig struct {
	Root     string
	Remote   string
	Branch   string
	Token    string
	Debounce time.Duration
	Poll     time.Duration
}

// ContentSync is safe to notify from concurrent HTTP handlers. Git operations
// are serialized, while a generation counter ensures a save arriving during
// a push remains queued for the next pass.
type ContentSync struct {
	server   *Server
	root     string
	remote   string
	branch   string
	token    string
	debounce time.Duration
	poll     time.Duration
	wake     chan struct{}

	opMu sync.Mutex
	mu   sync.Mutex

	ready        bool
	dirty        bool
	generation   uint64
	state        string
	lastError    string
	lastSyncedAt time.Time
}

func newContentSync(s *Server, cfg contentSyncConfig) *ContentSync {
	if cfg.Branch == "" {
		cfg.Branch = "main"
	}
	if cfg.Debounce <= 0 {
		cfg.Debounce = 10 * time.Second
	}
	if cfg.Poll <= 0 {
		cfg.Poll = 45 * time.Second
	}
	return &ContentSync{
		server: s, root: cfg.Root, remote: cfg.Remote, branch: cfg.Branch,
		token: cfg.Token, debounce: cfg.Debounce, poll: cfg.Poll,
		wake: make(chan struct{}, 1), state: syncStateStarting,
	}
}

// StartContentSync enables hosted content writes when VSTD_CONTENT_SYNC=1.
// It bootstraps synchronously so the server never accepts an edit it cannot
// durably sync.
func (s *Server) StartContentSync() error {
	if os.Getenv("VSTD_CONTENT_SYNC") != "1" {
		return nil
	}
	repo := strings.TrimSpace(os.Getenv("VSTD_GIT_REPO"))
	token := strings.TrimSpace(os.Getenv("VSTD_GIT_TOKEN"))
	if repo == "" {
		return fmt.Errorf("VSTD_CONTENT_SYNC=1 requires VSTD_GIT_REPO")
	}
	if token == "" {
		return fmt.Errorf("VSTD_CONTENT_SYNC=1 requires VSTD_GIT_TOKEN")
	}
	remote, err := contentGitRemote(repo)
	if err != nil {
		return err
	}
	cfg := contentSyncConfig{
		Root: s.St.Root, Remote: remote, Branch: strings.TrimSpace(os.Getenv("VSTD_GIT_BRANCH")),
		Token: token, Debounce: envDurationSeconds("VSTD_GIT_DEBOUNCE_SECONDS", 10*time.Second),
		Poll: envDurationSeconds("VSTD_GIT_POLL_SECONDS", 45*time.Second),
	}
	cs := newContentSync(s, cfg)
	if err := cs.bootstrap(); err != nil {
		return fmt.Errorf("content sync bootstrap: %w", err)
	}
	cs.mu.Lock()
	cs.ready = true
	cs.state = syncStateReady
	cs.mu.Unlock()
	s.ContentSync = cs
	go cs.loop()
	log.Printf("content sync: ready (repo=%s branch=%s debounce=%s poll=%s)", repo, cs.branch, cs.debounce, cs.poll)
	return nil
}

func contentGitRemote(repo string) (string, error) {
	if strings.Contains(repo, "://") || filepath.IsAbs(repo) || strings.HasPrefix(repo, "git@") {
		return repo, nil
	}
	parts := strings.Split(repo, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.Contains(repo, "..") {
		return "", fmt.Errorf("VSTD_GIT_REPO must be owner/repository")
	}
	return "https://github.com/" + repo + ".git", nil
}

func envDurationSeconds(name string, fallback time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return fallback
	}
	return time.Duration(n) * time.Second
}

func (c *ContentSync) Editable() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ready
}

func (c *ContentSync) Notify() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.dirty = true
	c.generation++
	c.state = syncStatePending
	c.lastError = ""
	c.mu.Unlock()
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func (c *ContentSync) Status() map[string]any {
	if c == nil {
		return map[string]any{"enabled": false, "editable": false}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[string]any{
		"enabled": true, "editable": c.ready, "state": c.state,
	}
	if c.lastError != "" {
		out["error"] = c.lastError
	}
	if !c.lastSyncedAt.IsZero() {
		out["last_synced_at"] = c.lastSyncedAt.UTC().Format(time.RFC3339)
	}
	return out
}

func (c *ContentSync) loop() {
	ticker := time.NewTicker(c.poll)
	defer ticker.Stop()
	var timer *time.Timer
	var timerC <-chan time.Time
	for {
		select {
		case <-c.wake:
			if timer == nil {
				timer = time.NewTimer(c.debounce)
			} else if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(c.debounce)
			timerC = timer.C
		case <-timerC:
			timerC = nil
			if err := c.syncOnce(); err != nil {
				c.recordError(err)
			}
		case <-ticker.C:
			if err := c.syncOnce(); err != nil {
				c.recordError(err)
			}
		}
	}
}

func (c *ContentSync) bootstrap() error {
	if c.root == "" || c.remote == "" {
		return fmt.Errorf("content sync root and remote are required")
	}
	if _, err := os.Stat(filepath.Join(c.root, ".git")); err == nil {
		status, gerr := c.git("status", "--porcelain")
		if gerr != nil {
			return gerr
		}
		if strings.TrimSpace(status) != "" {
			return fmt.Errorf("existing Git worktree is dirty; refusing content sync bootstrap")
		}
		if _, err := c.git("remote", "get-url", "origin"); err != nil {
			if _, err := c.git("remote", "add", "origin", c.remote); err != nil {
				return err
			}
		} else if _, err := c.git("remote", "set-url", "origin", c.remote); err != nil {
			return err
		}
	} else {
		if _, err := c.git("init"); err != nil {
			return err
		}
		if _, err := c.git("remote", "add", "origin", c.remote); err != nil {
			return err
		}
	}
	if _, err := c.git("fetch", "--depth", "1", "origin", c.branch); err != nil {
		return err
	}
	if _, err := c.git("reset", "--hard", "FETCH_HEAD"); err != nil {
		return err
	}
	return nil
}

func (c *ContentSync) syncOnce() error {
	c.opMu.Lock()
	defer c.opMu.Unlock()

	c.mu.Lock()
	startGeneration := c.generation
	c.mu.Unlock()

	paths, err := c.availableContentPaths()
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return fmt.Errorf("content sync found none of the allowed content paths")
	}
	status, err := c.git(append([]string{"status", "--porcelain", "--"}, paths...)...)
	if err != nil {
		return err
	}
	if strings.TrimSpace(status) != "" {
		if _, err := c.git(append([]string{"add", "--"}, paths...)...); err != nil {
			return err
		}
		if _, err := c.git("-c", "user.name=Vessica Studio", "-c", "user.email=studio@vessica.dev",
			"commit", "-m", "vessica studio: sync hosted edits"); err != nil {
			return err
		}
	}

	if _, err := c.git("fetch", "origin", c.branch); err != nil {
		return err
	}
	head, err := c.git("rev-parse", "HEAD")
	if err != nil {
		return err
	}
	remote, err := c.git("rev-parse", "FETCH_HEAD")
	if err != nil {
		return err
	}
	if head != remote {
		if c.gitOK("merge-base", "--is-ancestor", "HEAD", "FETCH_HEAD") {
			if _, err := c.git("merge", "--ff-only", "FETCH_HEAD"); err != nil {
				return err
			}
		} else if !c.gitOK("merge-base", "--is-ancestor", "FETCH_HEAD", "HEAD") {
			if _, err := c.git("rebase", "FETCH_HEAD"); err != nil {
				_, _ = c.git("rebase", "--abort")
				return fmt.Errorf("content conflict with GitHub; hosted commit retained for retry: %w", err)
			}
		}
	}

	head, err = c.git("rev-parse", "HEAD")
	if err != nil {
		return err
	}
	remote, err = c.git("rev-parse", "FETCH_HEAD")
	if err != nil {
		return err
	}
	if head != remote {
		if _, err := c.git("push", "origin", "HEAD:"+c.branch); err != nil {
			return err
		}
	}

	c.mu.Lock()
	if c.generation == startGeneration {
		c.dirty = false
		c.state = syncStateSynced
	} else {
		c.dirty = true
		c.state = syncStatePending
	}
	c.lastError = ""
	c.lastSyncedAt = time.Now()
	c.mu.Unlock()
	if c.server != nil && c.server.Collab != nil {
		if err := c.server.Collab.ReconcileDecks(context.Background(), c.server.filesystemDecks()); err != nil {
			return fmt.Errorf("reconcile Git-synced presentations: %w", err)
		}
	}
	if c.server != nil && head != remote {
		c.server.Broadcast("reload")
	}
	log.Printf("content sync: synchronized branch %s", c.branch)
	return nil
}

// A content root may legitimately omit an optional directory such as
// requests/. Include paths that exist now or have tracked deletions waiting to
// be staged; never hand Git a pathspec that matches nothing.
func (c *ContentSync) availableContentPaths() ([]string, error) {
	paths := make([]string, 0, len(contentSyncPaths))
	for _, path := range contentSyncPaths {
		if _, err := os.Stat(filepath.Join(c.root, path)); err == nil {
			paths = append(paths, path)
			continue
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		tracked, err := c.git("ls-files", "--", path)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(tracked) != "" {
			paths = append(paths, path)
		}
	}
	return paths, nil
}

func (c *ContentSync) recordError(err error) {
	c.mu.Lock()
	c.state = syncStateError
	c.lastError = err.Error()
	c.mu.Unlock()
	log.Printf("content sync: %v", err)
}

func (c *ContentSync) gitOK(args ...string) bool {
	cmd := exec.Command("git", args...)
	cmd.Dir = c.root
	cmd.Env = c.gitEnv()
	return cmd.Run() == nil
}

func (c *ContentSync) git(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = c.root
	cmd.Env = c.gitEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %v — %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// Git receives the credential through an ephemeral environment-backed config
// header. The token is never placed in argv, the remote URL, .git/config, or
// formatted Git errors.
func (c *ContentSync) gitEnv() []string {
	env := append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if c.token == "" {
		return env
	}
	auth := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + c.token))
	return append(env,
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=http.https://github.com/.extraheader",
		"GIT_CONFIG_VALUE_0=AUTHORIZATION: basic "+auth,
	)
}
