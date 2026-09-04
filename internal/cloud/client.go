package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const defaultResponseLimit int64 = 192 << 20 // 128 MiB content plus base64 and JSON framing.

type TokenSource interface {
	Token(context.Context) (string, error)
}
type TokenSourceFunc func(context.Context) (string, error)

func (f TokenSourceFunc) Token(ctx context.Context) (string, error) { return f(ctx) }

type Option func(*Client) error
type Client struct {
	endpoint      *url.URL
	http          *http.Client
	token         TokenSource
	clientVersion string
	responseLimit int64
	clock         func() time.Time
	operationID   func() string
}

func NewClient(opts ...Option) (*Client, error) {
	c := &Client{http: http.DefaultClient, responseLimit: defaultResponseLimit, clock: time.Now}
	for _, opt := range opts {
		if err := opt(c); err != nil {
			return nil, err
		}
	}
	if c.endpoint == nil {
		return nil, fmt.Errorf("cloud endpoint is required")
	}
	if c.clientVersion == "" {
		return nil, fmt.Errorf("client version is required")
	}
	c.http = secureHTTPClient(c.http, c.endpoint)
	return c, nil
}
func WithEndpoint(raw string) Option {
	return func(c *Client) error {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
			return fmt.Errorf("invalid cloud endpoint")
		}
		host := u.Hostname()
		if u.Scheme != "https" && !(u.Scheme == "http" && (host == "localhost" || net.ParseIP(host).IsLoopback())) {
			return fmt.Errorf("cloud endpoint must use HTTPS")
		}
		u.Host = strings.ToLower(u.Host)
		u.Path = strings.TrimRight(u.Path, "/")
		c.endpoint = u
		return nil
	}
}

// Endpoint returns the validated, credential-free endpoint identity.
func (c *Client) Endpoint() string { return c.endpoint.String() }
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) error {
		if h == nil {
			return fmt.Errorf("HTTP client is required")
		}
		c.http = h
		return nil
	}
}
func WithTokenSource(t TokenSource) Option { return func(c *Client) error { c.token = t; return nil } }
func WithClientVersion(v string) Option {
	return func(c *Client) error { c.clientVersion = v; return nil }
}
func WithResponseLimit(n int64) Option {
	return func(c *Client) error {
		if n < 1 {
			return fmt.Errorf("response limit must be positive")
		}
		c.responseLimit = n
		return nil
	}
}
func WithClock(f func() time.Time) Option {
	return func(c *Client) error {
		if f == nil {
			return fmt.Errorf("clock is required")
		}
		c.clock = f
		return nil
	}
}
func WithOperationIDs(f func() string) Option {
	return func(c *Client) error {
		if f == nil {
			return fmt.Errorf("operation ID source is required")
		}
		c.operationID = f
		return nil
	}
}

func secureHTTPClient(base *http.Client, origin *url.URL) *http.Client {
	clone := *base
	previous := base.CheckRedirect
	clone.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != origin.Scheme || !strings.EqualFold(req.URL.Host, origin.Host) {
			return http.ErrUseLastResponse
		}
		if previous != nil {
			return previous(req, via)
		}
		if len(via) >= 10 {
			return http.ErrUseLastResponse
		}
		return nil
	}
	return &clone
}
func (c *Client) Capabilities(ctx context.Context) (Capabilities, error) {
	var out Capabilities
	err := c.do(ctx, http.MethodGet, "/v1/capabilities", nil, &out, false)
	return out, err
}
func (c *Client) negotiate(ctx context.Context, capability string) error {
	caps, err := c.Capabilities(ctx)
	if err != nil {
		return err
	}
	if caps.Protocol != ProtocolVersion || versionLess(c.clientVersion, caps.MinimumClientVersion) {
		return &IncompatibleError{Protocol: caps.Protocol, MinimumClientVersion: caps.MinimumClientVersion}
	}
	if capability == "" {
		return nil
	}
	for _, got := range caps.Capabilities {
		if got == capability {
			return nil
		}
	}
	return &IncompatibleError{Protocol: caps.Protocol, MissingCapability: capability}
}
func (c *Client) StartDeviceAuthorization(ctx context.Context) (DeviceAuthorization, error) {
	var out DeviceAuthorization
	if err := c.negotiate(ctx, ""); err != nil {
		return out, err
	}
	err := c.do(ctx, http.MethodPost, "/v1/auth/device", DeviceAuthorizationRequest{ClientVersion: c.clientVersion}, &out, false)
	return out, err
}
func (c *Client) ExchangeDeviceCode(ctx context.Context, code string) (Token, error) {
	var out Token
	err := c.do(ctx, http.MethodPost, "/v1/auth/token", TokenRequest{DeviceCode: code}, &out, false)
	return out, err
}
func (c *Client) Refresh(ctx context.Context, refresh string) (Token, error) {
	var out Token
	err := c.do(ctx, http.MethodPost, "/v1/auth/token", TokenRequest{RefreshToken: refresh}, &out, false)
	return out, err
}
func (c *Client) Revoke(ctx context.Context, refresh string) error {
	return c.do(ctx, http.MethodPost, "/v1/auth/revoke", RevokeRequest{RefreshToken: refresh}, nil, false)
}
func (c *Client) Account(ctx context.Context) (Account, error) {
	var out Account
	err := c.do(ctx, http.MethodGet, "/v1/account", nil, &out, true)
	return out, err
}
func (c *Client) Workspaces(ctx context.Context, cursor string) (WorkspaceList, error) {
	var out WorkspaceList
	if err := c.negotiate(ctx, CapabilityWorkspaceRead); err != nil {
		return out, err
	}
	path := "/v1/workspaces"
	if cursor != "" {
		path += "?cursor=" + url.QueryEscape(cursor)
	}
	err := c.do(ctx, http.MethodGet, path, nil, &out, true)
	return out, err
}
func (c *Client) Workspace(ctx context.Context, id string) (Workspace, error) {
	var out Workspace
	if err := c.negotiate(ctx, CapabilityWorkspaceRead); err != nil {
		return out, err
	}
	err := c.do(ctx, http.MethodGet, "/v1/workspaces/"+url.PathEscape(id), nil, &out, true)
	return out, err
}
func (c *Client) Revision(ctx context.Context, workspace, id string) (Revision, error) {
	var out Revision
	if err := c.negotiate(ctx, CapabilityWorkspaceRead); err != nil {
		return out, err
	}
	err := c.do(ctx, http.MethodGet, "/v1/workspaces/"+url.PathEscape(workspace)+"/revisions/"+url.PathEscape(id), nil, &out, true)
	return out, err
}
func (c *Client) Sync(ctx context.Context, workspace string, in SyncRequest) (Revision, error) {
	var out Revision
	if err := c.negotiate(ctx, CapabilityWorkspaceSync); err != nil {
		return out, err
	}
	if in.OperationID == "" && c.operationID != nil {
		in.OperationID = c.operationID()
	}
	err := c.do(ctx, http.MethodPost, "/v1/workspaces/"+url.PathEscape(workspace)+"/revisions", in, &out, true)
	return out, err
}
func (c *Client) Publish(ctx context.Context, workspace string, in PublicationRequest) (Publication, error) {
	var out Publication
	if err := c.negotiate(ctx, CapabilityPublicationWrite); err != nil {
		return out, err
	}
	if in.OperationID == "" && c.operationID != nil {
		in.OperationID = c.operationID()
	}
	err := c.do(ctx, http.MethodPost, "/v1/workspaces/"+url.PathEscape(workspace)+"/publications", in, &out, true)
	return out, err
}
func (c *Client) Publication(ctx context.Context, workspace, id string) (Publication, error) {
	var out Publication
	err := c.do(ctx, http.MethodGet, "/v1/workspaces/"+url.PathEscape(workspace)+"/publications/"+url.PathEscape(id), nil, &out, true)
	return out, err
}

func (c *Client) do(ctx context.Context, method, path string, input, output any, auth bool) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint.String()+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Vstd-Protocol", ProtocolVersion)
	req.Header.Set("X-Vstd-Version", c.clientVersion)
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth && c.token != nil {
		token, tokenErr := c.token.Token(ctx)
		if tokenErr != nil {
			return tokenErr
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return &Error{Kind: ErrorOffline, Cause: err}
	}
	defer resp.Body.Close()
	limited := io.LimitReader(resp.Body, c.responseLimit+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return &Error{Kind: ErrorMalformedResponse, Cause: err}
	}
	if int64(len(data)) > c.responseLimit {
		return &Error{Kind: ErrorMalformedResponse, Code: "response_too_large"}
	}
	contentType := resp.Header.Get("Content-Type")
	if len(data) > 0 && !strings.HasPrefix(strings.ToLower(contentType), "application/json") {
		return &Error{Kind: ErrorMalformedResponse, Code: "unexpected_content_type"}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return c.remoteError(resp, data)
	}
	if output != nil {
		if len(data) == 0 || json.Unmarshal(data, output) != nil {
			return &Error{Kind: ErrorMalformedResponse, Code: "invalid_json"}
		}
	}
	return nil
}
func (c *Client) remoteError(resp *http.Response, data []byte) error {
	var wire wireError
	_ = json.Unmarshal(data, &wire)
	// Never echo arbitrary error fields from a remote body: it may reflect credentials.
	switch wire.Code {
	case "authorization_pending", "slow_down", "access_denied", "expired_token", "invalid_grant", "incompatible_protocol", "stale_base":
	default:
		wire.Code = "request_failed"
	}
	if resp.StatusCode == http.StatusConflict {
		return &ConflictError{Code: wire.Code, CloudHeadRevisionID: wire.CloudHeadRevisionID}
	}
	if wire.Code == "incompatible_protocol" {
		return &IncompatibleError{Protocol: wire.Protocol, MinimumClientVersion: wire.MinimumClientVersion}
	}
	kind := statusKind(resp.StatusCode)
	e := &Error{Kind: kind, StatusCode: resp.StatusCode, Code: wire.Code}
	if seconds, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && seconds > 0 {
		e.RetryAfter = time.Duration(seconds) * time.Second
	}
	return e
}
func versionLess(current, minimum string) bool {
	if minimum == "" {
		return false
	}
	a := strings.Split(strings.TrimPrefix(current, "v"), ".")
	b := strings.Split(strings.TrimPrefix(minimum, "v"), ".")
	for i := 0; i < 3; i++ {
		var av, bv int
		if i < len(a) {
			av, _ = strconv.Atoi(a[i])
		}
		if i < len(b) {
			bv, _ = strconv.Atoi(b[i])
		}
		if av != bv {
			return av < bv
		}
	}
	return false
}
