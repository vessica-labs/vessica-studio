package cloudauth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/vessica-labs/vessica-studio/internal/cloud"
	"github.com/zalando/go-keyring"
)

const secret = "sentinel-refresh-secret"

type fakeAPI struct {
	start     cloud.DeviceAuthorization
	tokens    []cloud.Token
	poll      int
	revoked   string
	revokeErr error
	account   cloud.Account
}

func (f *fakeAPI) StartDeviceAuthorization(context.Context) (cloud.DeviceAuthorization, error) {
	return f.start, nil
}
func (f *fakeAPI) ExchangeDeviceCode(context.Context, string) (cloud.Token, error) {
	if f.poll >= len(f.tokens) {
		return cloud.Token{}, &cloud.Error{Kind: cloud.ErrorAuth, Code: "authorization_pending"}
	}
	v := f.tokens[f.poll]
	f.poll++
	return v, nil
}
func (f *fakeAPI) Refresh(_ context.Context, refresh string) (cloud.Token, error) {
	if refresh != secret {
		return cloud.Token{}, errors.New("wrong credential")
	}
	return cloud.Token{AccessToken: "access-two", RefreshToken: "refresh-two", ExpiresIn: 60}, nil
}
func (f *fakeAPI) Revoke(_ context.Context, refresh string) error {
	f.revoked = refresh
	return f.revokeErr
}
func (f *fakeAPI) Account(context.Context) (cloud.Account, error) { return f.account, nil }

func TestCredentialMemoryStore(t *testing.T) {
	s := NewMemoryStore()
	if _, err := s.Load(context.Background()); !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("Load error = %v", err)
	}
	if err := s.Save(context.Background(), secret); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load(context.Background())
	if err != nil || got != secret {
		t.Fatalf("Load = %q, %v", got, err)
	}
	if err := s.Delete(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestCredentialKeyringStore(t *testing.T) {
	keyring.MockInit()
	s := NewKeyringStore("https://cloud.example")
	if err := s.Save(context.Background(), secret); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load(context.Background())
	if err != nil || got != secret {
		t.Fatalf("Load = %q, %v", got, err)
	}
	if err := s.Delete(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestCredentialKeyringEndpointIsolation(t *testing.T) {
	keyring.MockInit()
	a, b := NewKeyringStore("https://a.example"), NewKeyringStore("https://b.example")
	if err := a.Save(context.Background(), secret); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Load(context.Background()); !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("cross-endpoint credential access: %v", err)
	}
}

func TestLoginStoresOnlyRefreshCredential(t *testing.T) {
	api := &fakeAPI{start: cloud.DeviceAuthorization{DeviceCode: "device-secret", UserCode: "ABCD", VerificationURI: "https://login.example", ExpiresIn: 60}, tokens: []cloud.Token{{AccessToken: "access-secret", RefreshToken: secret, ExpiresIn: 60}}}
	store := NewMemoryStore()
	m := New(api, store, WithPollInterval(time.Millisecond))
	var prompt LoginPrompt
	token, err := m.Login(context.Background(), func(p LoginPrompt) error { prompt = p; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if token != "access-secret" {
		t.Fatalf("token = %q", token)
	}
	if prompt.UserCode != "ABCD" || prompt.VerificationURI != "https://login.example" {
		t.Fatalf("prompt = %#v", prompt)
	}
	got, _ := store.Load(context.Background())
	if got != secret {
		t.Fatalf("stored = %q", got)
	}
}

func TestRefreshReplacesCredential(t *testing.T) {
	store := NewMemoryStore()
	_ = store.Save(context.Background(), secret)
	m := New(&fakeAPI{}, store)
	got, err := m.Token(context.Background())
	if err != nil || got != "access-two" {
		t.Fatalf("Token = %q, %v", got, err)
	}
	refresh, _ := store.Load(context.Background())
	if refresh != "refresh-two" {
		t.Fatalf("refresh = %q", refresh)
	}
}

func TestLogoutDeletesCredentialAfterRevokeFailure(t *testing.T) {
	store := NewMemoryStore()
	_ = store.Save(context.Background(), secret)
	api := &fakeAPI{revokeErr: errors.New("remote failure containing " + secret)}
	err := New(api, store).Logout(context.Background())
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("error = %v", err)
	}
	if api.revoked != secret {
		t.Fatal("revocation not attempted")
	}
	if _, err := store.Load(context.Background()); !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("credential remains: %v", err)
	}
}

func TestRedactSecrets(t *testing.T) {
	got := Redact("access=abc refresh="+secret, "abc", secret)
	if strings.Contains(got, "abc") || strings.Contains(got, secret) {
		t.Fatalf("not redacted: %s", got)
	}
}
