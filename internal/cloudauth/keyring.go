package cloudauth

import (
	"context"
	"errors"
	"fmt"

	"github.com/zalando/go-keyring"
)

const (
	CredentialService = "vessica-studio-cloud"
	CredentialAccount = "native-cli-session"
)

// KeyringStore uses the operating system's native credential service.
type KeyringStore struct{ service, account string }

func NewKeyringStore() *KeyringStore {
	return &KeyringStore{service: CredentialService, account: CredentialAccount}
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
