package cloudauth

import (
	"context"
	"errors"
	"sync"
)

var ErrNotLoggedIn = errors.New("not logged in to Vessica Studio Cloud")

// Store persists only renewable session material. Implementations must use a
// platform credential service, never a repository or configuration file.
type Store interface {
	Load(context.Context) (string, error)
	Save(context.Context, string) error
	Delete(context.Context) error
}

// MemoryStore is a deterministic credential backend intended for tests.
type MemoryStore struct {
	mu         sync.Mutex
	credential string
	lockOnce   sync.Once
	rotation   chan struct{}
}

func NewMemoryStore() *MemoryStore { return &MemoryStore{} }
func (s *MemoryStore) Lock(ctx context.Context) (func(), error) {
	s.lockOnce.Do(func() { s.rotation = make(chan struct{}, 1) })
	select {
	case s.rotation <- struct{}{}:
		return func() { <-s.rotation }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (s *MemoryStore) Load(context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.credential == "" {
		return "", ErrNotLoggedIn
	}
	return s.credential, nil
}
func (s *MemoryStore) Save(_ context.Context, credential string) error {
	if credential == "" {
		return ErrNotLoggedIn
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.credential = credential
	return nil
}
func (s *MemoryStore) Delete(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.credential = ""
	return nil
}
