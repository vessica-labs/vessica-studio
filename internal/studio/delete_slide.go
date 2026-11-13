package studio

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

var ErrLastSlide = errors.New("a presentation must have at least one slide")
var ErrSlideChanged = errors.New("the slide changed after it was opened; reopen the presentation and try again")

// DeleteSlide removes the slide fragment, companion and optional link together.
// The caller holds the content lock. A repeated deletion is a no-op.
func (s *Studio) DeleteSlide(deck, id, expectedHash string) error {
	if !ValidDeckName(deck) || !ValidSlideID(id) {
		return fmt.Errorf("invalid deck/slide id")
	}
	ids, err := s.SlideIDs(deck)
	if err != nil {
		return err
	}
	if !slices.Contains(ids, id) {
		return nil
	}
	if len(ids) == 1 {
		return ErrLastSlide
	}
	if expectedHash != "" && s.HashSlide(deck, id) != expectedHash {
		return ErrSlideChanged
	}
	meta, err := s.LoadDeckMeta(deck)
	if err != nil {
		return err
	}

	stage, err := os.MkdirTemp(filepath.Dir(s.SlidePath(deck, id, ".html")), ".vstd-delete-")
	if err != nil {
		return err
	}
	var moved []string
	rollback := func(cause error) error {
		for _, ext := range moved {
			cause = errors.Join(cause, os.Rename(filepath.Join(stage, id+ext), s.SlidePath(deck, id, ext)))
		}
		// Keep any staged files that could not be restored.
		_ = os.Remove(stage)
		return cause
	}
	for _, ext := range []string{".md", ".link.yaml", ".html"} {
		src := s.SlidePath(deck, id, ext)
		info, err := os.Lstat(src)
		if errors.Is(err, os.ErrNotExist) && ext != ".html" {
			continue
		}
		if err != nil {
			return rollback(err)
		}
		if !info.Mode().IsRegular() {
			return rollback(fmt.Errorf("slide file %s is not a regular file", id+ext))
		}
		if err := os.Rename(src, filepath.Join(stage, id+ext)); err != nil {
			return rollback(err)
		}
		moved = append(moved, ext)
	}
	if slices.Contains(meta.SlideOrder, id) {
		meta.SlideOrder = slices.DeleteFunc(meta.SlideOrder, func(candidate string) bool { return candidate == id })
		if err := s.SaveDeckMeta(deck, meta); err != nil {
			return rollback(err)
		}
	}
	_ = os.RemoveAll(stage)
	return nil
}
