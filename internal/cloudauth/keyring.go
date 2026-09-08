package cloudauth

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zalando/go-keyring"
)

const (
	CredentialService = "vessica-studio-cloud"
	CredentialAccount = "native-cli-session"
)

// KeyringStore uses the operating system's native credential service.
type KeyringStore struct{ service, account string }

func NewKeyringStore(endpoint string) *KeyringStore {
	// Never reuse a renewable credential for a different cloud deployment.
	id := sha256.Sum256([]byte(endpoint))
	return &KeyringStore{service: CredentialService, account: fmt.Sprintf("%s-%x", CredentialAccount, id)}
}

// Lock files contain no credentials; the OS releases their advisory lock even
// if a CLI process crashes. Every endpoint has its own credential namespace.
func (s *KeyringStore) Lock(ctx context.Context) (func(), error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(cache, "vessica-studio", "cloud-session-locks")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	return acquireCredentialFileLock(ctx, filepath.Join(dir, s.account+".lock"))
}
func (s *KeyringStore) Load(context.Context) (string, error) {
	v, err := keyring.Get(s.service, s.account)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNotLoggedIn
	}
	if err != nil {
		return "", fmt.Errorf("secure credential storage is unavailable")
	}
	return v, nil
}
func (s *KeyringStore) Save(_ context.Context, value string) error {
	if value == "" {
		return ErrNotLoggedIn
	}
	if err := keyring.Set(s.service, s.account, value); err != nil {
		return fmt.Errorf("secure credential storage is unavailable")
	}
	return nil
}
func (s *KeyringStore) Delete(context.Context) error {
	err := keyring.Delete(s.service, s.account)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("secure credential storage is unavailable")
	}
	return nil
}
