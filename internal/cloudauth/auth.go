// Package cloudauth manages native cloud sessions without exposing renewable
// credentials to callers or persisting short-lived access tokens.
package cloudauth

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/vessica-labs/vessica-studio/internal/cloud"
)

var ErrLoginExpired = errors.New("cloud login expired; start login again")

type LogoutError struct{ Remote bool }

func (e *LogoutError) Error() string {
	if e.Remote {
		return "local cloud session removed but remote revocation failed"
	}
	return "could not remove local cloud session"
}

type API interface {
	StartDeviceAuthorization(context.Context) (cloud.DeviceAuthorization, error)
	ExchangeDeviceCode(context.Context, string) (cloud.Token, error)
	Refresh(context.Context, string) (cloud.Token, error)
	Revoke(context.Context, string) error
	Account(context.Context) (cloud.Account, error)
}

type LoginPrompt struct{ UserCode, VerificationURI string }
type Prompt func(LoginPrompt) error
type Option func(*Manager)

func WithPollInterval(d time.Duration) Option {
	return func(m *Manager) {
		if d > 0 {
			m.pollInterval = d
		}
	}
}
func WithClock(now func() time.Time) Option {
	return func(m *Manager) {
		if now != nil {
			m.now = now
		}
	}
}

type Manager struct {
	api          API
	store        Store
	pollInterval time.Duration
	now          func() time.Time
	mu           sync.Mutex
	access       string
	expires      time.Time
}

func New(api API, store Store, opts ...Option) *Manager {
	m := &Manager{api: api, store: store, pollInterval: 5 * time.Second, now: time.Now}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

func (m *Manager) Login(ctx context.Context, prompt Prompt) (string, error) {
	auth, err := m.api.StartDeviceAuthorization(ctx)
	if err != nil {
		return "", safeError("start cloud login", err)
	}
	u, urlErr := url.Parse(auth.VerificationURI)
	if urlErr != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || auth.DeviceCode == "" || auth.UserCode == "" || strings.ContainsAny(auth.UserCode, "\r\n\x1b") || auth.ExpiresIn <= 0 || auth.ExpiresIn > 3600 || auth.Interval < 0 || auth.Interval > 300 {
		return "", errors.New("cloud returned an invalid device authorization")
	}
	if prompt != nil {
		if err := prompt(LoginPrompt{UserCode: auth.UserCode, VerificationURI: auth.VerificationURI}); err != nil {
			return "", safeError("show cloud login", err)
		}
	}
	deadline := m.now().Add(time.Duration(auth.ExpiresIn) * time.Second)
	interval := m.pollInterval
	if auth.Interval > 0 && interval == 5*time.Second {
		interval = time.Duration(auth.Interval) * time.Second
	}
	for {
		if !m.now().Before(deadline) {
			return "", ErrLoginExpired
		}
		token, pollErr := m.api.ExchangeDeviceCode(ctx, auth.DeviceCode)
		if pollErr == nil {
			return m.accept(token)
		}
		var remote *cloud.Error
		if !errors.As(pollErr, &remote) || (remote.Code != "authorization_pending" && remote.Code != "slow_down") {
			return "", safeError("complete cloud login", pollErr, auth.DeviceCode)
		}
		if remote.Code == "slow_down" {
			interval += 5 * time.Second
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(interval):
		}
	}
}

func (m *Manager) accept(token cloud.Token) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.acceptLocked(token)
}

func (m *Manager) acceptLocked(token cloud.Token) (string, error) {
	if token.AccessToken == "" || token.RefreshToken == "" || token.ExpiresIn <= 0 {
		return "", errors.New("cloud returned an incomplete session")
	}
	if err := m.store.Save(context.Background(), token.RefreshToken); err != nil {
		return "", safeError("save cloud session", err, token.AccessToken, token.RefreshToken)
	}
	m.access = token.AccessToken
	m.expires = m.now().Add(time.Duration(token.ExpiresIn) * time.Second)
	return token.AccessToken, nil
}

// Token implements cloud.TokenSource. Access tokens remain memory-only.
func (m *Manager) Token(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.access != "" && m.now().Before(m.expires) {
		v := m.access
		return v, nil
	}
	refresh, err := m.store.Load(ctx)
	if err != nil {
		return "", safeError("load cloud session", err)
	}
	token, err := m.api.Refresh(ctx, refresh)
	if err != nil {
		return "", safeError("refresh cloud session", err, refresh)
	}
	if token.RefreshToken == "" {
		token.RefreshToken = refresh
	}
	return m.acceptLocked(token)
}

func (m *Manager) Account(ctx context.Context) (cloud.Account, error) {
	account, err := m.api.Account(ctx)
	if err != nil {
		return cloud.Account{}, safeError("inspect cloud account", err)
	}
	return account, nil
}

func (m *Manager) Logout(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	refresh, loadErr := m.store.Load(ctx)
	var revokeErr error
	if loadErr == nil {
		revokeErr = m.api.Revoke(ctx, refresh)
	} else if !errors.Is(loadErr, ErrNotLoggedIn) {
		revokeErr = loadErr
	}
	deleteErr := m.store.Delete(ctx)
	m.access = ""
	m.expires = time.Time{}
	if deleteErr != nil {
		return &LogoutError{}
	}
	if revokeErr != nil {
		return &LogoutError{Remote: true}
	}
	return nil
}

func Redact(message string, secrets ...string) string {
	for _, value := range secrets {
		if value != "" {
			message = strings.ReplaceAll(message, value, "[REDACTED]")
		}
	}
	return message
}
func safeError(action string, err error, secrets ...string) error {
	if errors.Is(err, ErrNotLoggedIn) {
		return fmt.Errorf("%s: %w", action, ErrNotLoggedIn)
	}
	var incompatible *cloud.IncompatibleError
	if errors.As(err, &incompatible) {
		return fmt.Errorf("%s: %w", action, incompatible)
	}
	var remote *cloud.Error
	if errors.As(err, &remote) {
		return fmt.Errorf("%s: %w", action, remote)
	}
	// Unknown adapters may include request or credential material in errors.
	// Keep the operational context and discard their untrusted message.
	return errors.New(action + " failed")
}
