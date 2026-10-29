package studio

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestReleaseMediaCacheConcurrentWritersAreAtomic(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	source, encoder := filepath.Join(root, "source"), filepath.Join(root, "encoder")
	_ = os.WriteFile(source, []byte("source"), 0o600)
	_ = os.WriteFile(encoder, []byte("encoder"), 0o600)
	errors := make(chan error, 16)
	var group sync.WaitGroup
	for i := 0; i < 16; i++ {
		dest := filepath.Join(t.TempDir(), "out")
		group.Add(1)
		go func() {
			defer group.Done()
			err := cachedReleaseDerivative(source, dest, encoder, "v1", func() error { return os.WriteFile(dest, []byte("complete media"), 0o600) })
			if err == nil {
				data, readErr := os.ReadFile(dest)
				err = readErr
				if err == nil && string(data) != "complete media" {
					err = fmt.Errorf("partial cached bytes")
				}
			}
			errors <- err
		}()
	}
	group.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestReleaseMediaCacheReusesOnlyMatchingInputs(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	source, encoder := filepath.Join(root, "source"), filepath.Join(root, "encoder")
	_ = os.WriteFile(source, []byte("source one"), 0o600)
	_ = os.WriteFile(encoder, []byte("encoder one"), 0o600)
	calls := 0
	build := func(recipe string) []byte {
		dest := filepath.Join(t.TempDir(), "output")
		err := cachedReleaseDerivative(source, dest, encoder, recipe, func() error {
			calls++
			return os.WriteFile(dest, []byte("encoded media"), 0o600)
		})
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(dest)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	if !bytes.Equal(build("recipe-v1"), build("recipe-v1")) || calls != 1 {
		t.Fatalf("cache missed: %d encodes", calls)
	}
	build("recipe-v2")
	_ = os.WriteFile(source, []byte("source two"), 0o600)
	build("recipe-v1")
	_ = os.WriteFile(encoder, []byte("encoder version two"), 0o600)
	build("recipe-v1")
	if calls != 4 {
		t.Fatalf("changed inputs reused stale media: %d", calls)
	}
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "other-tenant"))
	build("recipe-v1")
	if calls != 5 {
		t.Fatal("separate cache namespace reused media")
	}
}

func TestReleaseMediaCacheRejectsCorruptionAndRecoversFailures(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	source, encoder, dest := filepath.Join(root, "source"), filepath.Join(root, "encoder"), filepath.Join(root, "out")
	_ = os.WriteFile(source, []byte("source"), 0o600)
	_ = os.WriteFile(encoder, []byte("encoder"), 0o600)
	fail := errors.New("encoder failed")
	if err := cachedReleaseDerivative(source, dest, encoder, "v1", func() error { return fail }); !errors.Is(err, fail) {
		t.Fatal("encoder failure was hidden")
	}
	calls := 0
	encode := func() error { calls++; return os.WriteFile(dest, []byte("valid bytes"), 0o600) }
	if err := cachedReleaseDerivative(source, dest, encoder, "v1", encode); err != nil {
		t.Fatal(err)
	}
	key, _ := releaseMediaCacheKey(source, encoder, "v1")
	entry := filepath.Join(root, "cache", "vstd", "release-media-v1", key+".media")
	data, _ := os.ReadFile(entry)
	data[len(data)-1] ^= 1
	_ = os.WriteFile(entry, data, 0o600)
	if err := cachedReleaseDerivative(source, dest, encoder, "v1", encode); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("corrupted derivative was reused")
	}
	if err := cachedReleaseDerivative(source, dest, encoder, "v1", encode); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("repaired cache was not reused")
	}
}

func TestReleaseMediaCacheIsOptionalAndBounded(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "")
	calls := 0
	if err := cachedReleaseDerivative("unused", "unused", "unused", "v1", func() error { calls++; return nil }); err != nil || calls != 1 {
		t.Fatal("uncached release failed")
	}
	root := t.TempDir()
	for _, name := range []string{"one.media", "two.media", "three.media"} {
		_ = os.WriteFile(filepath.Join(root, name), bytes.Repeat([]byte{1}, 16), 0o600)
	}
	trimReleaseMediaCache(root, 32)
	entries, _ := os.ReadDir(root)
	if len(entries) != 2 {
		t.Fatalf("cache was not bounded: %d", len(entries))
	}
}
