// Package s3 is a minimal S3-compatible client for the video pipeline:
// PUT/HEAD objects (library sync) and presigned GET URLs (hosted serving).
// It speaks AWS Signature V4 directly — no SDK — because the engine needs
// exactly three operations and ships as a small static binary.
//
// Works with any S3-compatible store; the intended target is a Railway
// Storage Bucket (free egress, presigned URLs, standard S3 API). Credentials
// follow the same pattern as the OpenAI key: environment first, then an
// optional shell command (e.g. macOS Keychain) from studio.yaml — the keys
// live only in the engine process.
//
// Env:
//
//	VSTD_S3_ENDPOINT    e.g. https://bucket-production-xxxx.up.railway.app
//	                    or https://s3.region.amazonaws.com (path-style is used)
//	VSTD_S3_BUCKET      bucket name
//	VSTD_S3_ACCESS_KEY  access key id
//	VSTD_S3_SECRET_KEY  secret access key
//	VSTD_S3_REGION      signing region (default "auto" — fine for Railway/R2/MinIO)
package s3

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

type Client struct {
	Endpoint  string // scheme://host[:port]
	Bucket    string
	Region    string
	AccessKey string
	SecretKey string
	HTTP      *http.Client
	// Now allows tests to pin the clock; nil means time.Now.
	Now func() time.Time
}

// KeyCmds are optional shell commands (studio.yaml storage:) whose stdout is
// the credential — same pattern as openai.api_key_cmd.
type KeyCmds struct {
	AccessKeyCmd string
	SecretKeyCmd string
}

// FromEnv builds a client from VSTD_S3_* env vars, filling gaps from cmds.
// Returns nil when not configured (no endpoint or no credentials) — callers
// treat nil as "bucket sync/serving disabled".
func FromEnv(endpoint, bucket, region string, cmds KeyCmds) *Client {
	// Env values are trimmed defensively: a trailing newline or space from a
	// copy-paste into a hosting dashboard silently breaks every signature.
	env := func(k string) string { return strings.TrimSpace(os.Getenv(k)) }
	c := &Client{
		Endpoint:  strings.TrimRight(firstOf(env("VSTD_S3_ENDPOINT"), endpoint), "/"),
		Bucket:    firstOf(env("VSTD_S3_BUCKET"), bucket),
		Region:    firstOf(env("VSTD_S3_REGION"), region, "auto"),
		AccessKey: env("VSTD_S3_ACCESS_KEY"),
		SecretKey: env("VSTD_S3_SECRET_KEY"),
		HTTP:      &http.Client{Timeout: 10 * time.Minute},
	}
	if c.AccessKey == "" && cmds.AccessKeyCmd != "" {
		c.AccessKey = runCmd(cmds.AccessKeyCmd)
	}
	if c.SecretKey == "" && cmds.SecretKeyCmd != "" {
		c.SecretKey = runCmd(cmds.SecretKeyCmd)
	}
	if c.Endpoint == "" || c.Bucket == "" || c.AccessKey == "" || c.SecretKey == "" {
		return nil
	}
	return c
}

func firstOf(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func runCmd(cmd string) string {
	out, err := exec.Command("sh", "-c", cmd).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// objectURL uses path-style addressing (endpoint/bucket/key), which every
// S3-compatible store accepts and avoids virtual-host DNS assumptions.
func (c *Client) objectURL(key string) string {
	return c.Endpoint + "/" + c.Bucket + "/" + uriEncode(key, false)
}

// ---- SigV4 ----

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// uriEncode implements the AWS canonical URI encoding (RFC 3986, with '/'
// preserved when encodeSlash is false).
func uriEncode(s string, encodeSlash bool) string {
	var b strings.Builder
	for _, ch := range []byte(s) {
		switch {
		case ch >= 'A' && ch <= 'Z', ch >= 'a' && ch <= 'z', ch >= '0' && ch <= '9',
			ch == '-', ch == '.', ch == '_', ch == '~':
			b.WriteByte(ch)
		case ch == '/' && !encodeSlash:
			b.WriteByte(ch)
		default:
			fmt.Fprintf(&b, "%%%02X", ch)
		}
	}
	return b.String()
}

func (c *Client) signingKey(date string) []byte {
	k := hmacSHA256([]byte("AWS4"+c.SecretKey), date)
	k = hmacSHA256(k, c.Region)
	k = hmacSHA256(k, "s3")
	return hmacSHA256(k, "aws4_request")
}

// canonicalQuery sorts and encodes query params per SigV4.
func canonicalQuery(q url.Values) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		for _, v := range q[k] {
			parts = append(parts, uriEncode(k, true)+"="+uriEncode(v, true))
		}
	}
	return strings.Join(parts, "&")
}

// sign adds SigV4 headers to an already-built request whose body hash is
// payloadHash ("UNSIGNED-PAYLOAD" allowed).
func (c *Client) sign(req *http.Request, payloadHash string) {
	t := c.now().UTC()
	amzDate := t.Format("20060102T150405Z")
	date := t.Format("20060102")
	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", payloadHash)

	headers := []string{"host", "x-amz-content-sha256", "x-amz-date"}
	var canonHeaders strings.Builder
	for _, h := range headers {
		v := req.Header.Get(h)
		if h == "host" {
			v = req.Host
			if v == "" {
				v = req.URL.Host
			}
		}
		canonHeaders.WriteString(h + ":" + strings.TrimSpace(v) + "\n")
	}
	signedHeaders := strings.Join(headers, ";")

	canonReq := strings.Join([]string{
		req.Method,
		uriEncode(req.URL.Path, false),
		canonicalQuery(req.URL.Query()),
		canonHeaders.String(),
		signedHeaders,
		payloadHash,
	}, "\n")

	scope := date + "/" + c.Region + "/s3/aws4_request"
	toSign := strings.Join([]string{"AWS4-HMAC-SHA256", amzDate, scope, sha256Hex([]byte(canonReq))}, "\n")
	sig := hex.EncodeToString(hmacSHA256(c.signingKey(date), toSign))
	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		c.AccessKey, scope, signedHeaders, sig))
}

// ---- operations ----

// Head reports whether the object exists (and its size).
func (c *Client) Head(key string) (bool, int64, error) {
	req, err := http.NewRequest(http.MethodHead, c.objectURL(key), nil)
	if err != nil {
		return false, 0, err
	}
	c.sign(req, sha256Hex(nil))
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return false, 0, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case 200:
		return true, resp.ContentLength, nil
	case 404:
		return false, 0, nil
	default:
		return false, 0, fmt.Errorf("HEAD %s: HTTP %d", key, resp.StatusCode)
	}
}

// Put uploads a local file to key. Streams from disk; the payload is
// signed as UNSIGNED-PAYLOAD (standard for streaming PUTs over HTTPS).
func (c *Client) Put(key, path, contentType string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPut, c.objectURL(key), f)
	if err != nil {
		return err
	}
	req.ContentLength = st.Size()
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	c.sign(req, "UNSIGNED-PAYLOAD")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 400))
		return fmt.Errorf("PUT %s: HTTP %d: %s", key, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// Get downloads an object to a local file (used by `vstd asset pull`).
func (c *Client) Get(key, path string) error {
	req, err := http.NewRequest(http.MethodGet, c.objectURL(key), nil)
	if err != nil {
		return err
	}
	c.sign(req, sha256Hex(nil))
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("GET %s: HTTP %d", key, resp.StatusCode)
	}
	tmp := path + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// PresignGet returns a time-limited GET URL for key — how hosted (public
// mode) serving hands video bytes to the browser without exposing the
// bucket: the engine validates the deck share token, then 302s here.
// Presigned GETs honor Range headers, so seeking works.
func (c *Client) PresignGet(key string, ttl time.Duration) string {
	t := c.now().UTC()
	amzDate := t.Format("20060102T150405Z")
	date := t.Format("20060102")
	scope := date + "/" + c.Region + "/s3/aws4_request"

	u, _ := url.Parse(c.objectURL(key))
	host := u.Host

	q := url.Values{}
	q.Set("X-Amz-Algorithm", "AWS4-HMAC-SHA256")
	q.Set("X-Amz-Credential", c.AccessKey+"/"+scope)
	q.Set("X-Amz-Date", amzDate)
	q.Set("X-Amz-Expires", fmt.Sprintf("%d", int(ttl.Seconds())))
	q.Set("X-Amz-SignedHeaders", "host")

	canonReq := strings.Join([]string{
		"GET",
		uriEncode(u.Path, false),
		canonicalQuery(q),
		"host:" + host + "\n",
		"host",
		"UNSIGNED-PAYLOAD",
	}, "\n")
	toSign := strings.Join([]string{"AWS4-HMAC-SHA256", amzDate, scope, sha256Hex([]byte(canonReq))}, "\n")
	sig := hex.EncodeToString(hmacSHA256(c.signingKey(date), toSign))
	q.Set("X-Amz-Signature", sig)
	return u.Scheme + "://" + host + u.Path + "?" + q.Encode()
}
