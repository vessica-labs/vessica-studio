package studio

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Fork copies decks/<src> to decks/<src>--<client>, recording provenance:
// the parent deck name and a content hash per slide at fork time. Those
// hashes power DiffUpstream later (agent-assisted rebase, not blind merge).
func (s *Studio) Fork(src, client string) (string, error) {
	if !ValidDeckName(src) || !ValidDeckName(client) {
		return "", fmt.Errorf("invalid deck/client name (lowercase, digits, hyphens)")
	}
	dst := src + "--" + client
	dstDir := s.DeckDir(dst)
	if _, err := os.Stat(dstDir); err == nil {
		return "", fmt.Errorf("deck %q already exists", dst)
	}
	if err := copyDir(s.DeckDir(src), dstDir, "build"); err != nil {
		return "", err
	}
	hashes, err := s.HashSlides(src)
	if err != nil {
		return "", err
	}
	meta, err := s.LoadDeckMeta(dst)
	if err != nil {
		return "", err
	}
	meta.ForkedFrom = src
	meta.ForkDate = time.Now().Format("2006-01-02")
	meta.ParentHashes = hashes
	meta.Title = meta.Title + " (" + client + ")"
	if err := s.SaveDeckMeta(dst, meta); err != nil {
		return "", err
	}
	return dst, nil
}

// ForkAs copies a deck to an explicit storage key and title. It is used by
// the hosted collaboration catalog, where the destination belongs to the
// caller rather than encoding a client name in the source deck's slug.
func (s *Studio) ForkAs(src, dst, title string) (err error) {
	if !ValidDeckName(src) || !ValidDeckName(dst) {
		return fmt.Errorf("invalid source or destination deck name")
	}
	if _, err := os.Stat(s.DeckDir(dst)); err == nil {
		return fmt.Errorf("deck %q already exists", dst)
	}
	if err := copyDir(s.DeckDir(src), s.DeckDir(dst), "build"); err != nil {
		return err
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(s.DeckDir(dst))
		}
	}()
	hashes, err := s.HashSlides(src)
	if err != nil {
		return err
	}
	meta, err := s.LoadDeckMeta(dst)
	if err != nil {
		return err
	}
	meta.ForkedFrom = src
	meta.ForkDate = time.Now().Format("2006-01-02")
	meta.ParentHashes = hashes
	meta.Title = strings.TrimSpace(title)
	meta.Visibility = "private"
	if err := s.SaveDeckMeta(dst, meta); err != nil {
		return err
	}
	complete = true
	return nil
}

// DiffUpstream reports which slides changed in the parent deck since the
// fork, and which were added or removed.
func (s *Studio) DiffUpstream(fork string) (changed, added, removed []string, err error) {
	meta, err := s.LoadDeckMeta(fork)
	if err != nil {
		return nil, nil, nil, err
	}
	if meta.ForkedFrom == "" {
		return nil, nil, nil, fmt.Errorf("deck %q is not a fork", fork)
	}
	now, err := s.HashSlides(meta.ForkedFrom)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("parent deck %q: %w", meta.ForkedFrom, err)
	}
	then := meta.ParentHashes
	for id, h := range now {
		if old, ok := then[id]; !ok {
			added = append(added, id)
		} else if old != h {
			changed = append(changed, id)
		}
	}
	for id := range then {
		if _, ok := now[id]; !ok {
			removed = append(removed, id)
		}
	}
	return changed, added, removed, nil
}

func copyDir(src, dst string, skipNames ...string) error {
	skip := map[string]bool{}
	for _, n := range skipNames {
		skip[n] = true
	}
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		parts := strings.Split(rel, string(filepath.Separator))
		if skip[parts[0]] {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
}
