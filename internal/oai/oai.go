// Package oai wraps the OpenAI HTTP APIs the engine needs: image generation
// (gpt-image-2) for the visual library, and ephemeral client-secret minting
// for gpt-realtime-2 presentation sessions. The API key lives ONLY here —
// read from OPENAI_API_KEY (or VSTD_OPENAI_KEY) — and is never exposed to
// the browser or to agent sessions.
package oai

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type Client struct {
	BaseURL string
	Key     string
	HTTP    *http.Client
}

// New resolves the API key in order: OPENAI_API_KEY env, VSTD_OPENAI_KEY
// env, then keyCmd (a shell command whose stdout is the key — e.g. macOS
// Keychain: `security find-generic-password -s vessica-openai -w`). The key
// stays inside the engine process; it is never logged or served.
func New(baseURL, keyCmd string) *Client {
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		key = os.Getenv("VSTD_OPENAI_KEY")
	}
	if key == "" && keyCmd != "" {
		if out, err := exec.Command("sh", "-c", keyCmd).Output(); err == nil {
			key = strings.TrimSpace(string(out))
		}
	}
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Key: key,
		HTTP: &http.Client{Timeout: 180 * time.Second}}
}

func (c *Client) HasKey() bool { return c.Key != "" }

func (c *Client) post(path string, payload any) ([]byte, int, error) {
	b, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", c.BaseURL+path, bytes.NewReader(b))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Key)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return body, res.StatusCode, nil
}

// ---- image generation + library ----

type Manifest struct {
	Version       int                    `json:"version"`
	StyleFamilies map[string]StyleFamily `json:"styleFamilies"`
	Assets        []Asset                `json:"assets"`
}

type StyleFamily struct {
	PromptPrefix string `json:"promptPrefix"`
}

type Asset struct {
	ID      string   `json:"id"`
	File    string   `json:"file"`
	Prompt  string   `json:"prompt"`
	Family  string   `json:"family,omitempty"`
	Tags    []string `json:"tags,omitempty"`
	Model   string   `json:"model"`
	Size    string   `json:"size"`
	Created string   `json:"created"`
	Hash    string   `json:"hash"`
	Usage   []string `json:"usage,omitempty"`
}

func LoadManifest(libDir string) (*Manifest, error) {
	m := &Manifest{Version: 1, StyleFamilies: map[string]StyleFamily{}}
	b, err := os.ReadFile(filepath.Join(libDir, "manifest.json"))
	if err == nil {
		if err := json.Unmarshal(b, m); err != nil {
			return nil, fmt.Errorf("library manifest: %w", err)
		}
	}
	return m, nil
}

func (m *Manifest) Save(libDir string) error {
	b, _ := json.MarshalIndent(m, "", "  ")
	return os.WriteFile(filepath.Join(libDir, "manifest.json"), b, 0o644)
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// GenerateAsset calls the images API and stores the result in the library,
// applying the style family's prompt prefix for cross-asset consistency.
func (c *Client) GenerateAsset(libDir, model, prompt, family, size, slug string, tags []string, dryRun bool) (*Asset, error) {
	man, err := LoadManifest(libDir)
	if err != nil {
		return nil, err
	}
	full := prompt
	if family != "" {
		if fam, ok := man.StyleFamilies[family]; ok && fam.PromptPrefix != "" {
			full = fam.PromptPrefix + ", " + prompt
		}
	}
	if slug == "" {
		slug = strings.Trim(slugRe.ReplaceAllString(strings.ToLower(prompt), "-"), "-")
		if len(slug) > 40 {
			slug = slug[:40]
		}
	}
	id := fmt.Sprintf("%s-%s", slug, time.Now().Format("0102-1504"))
	if dryRun {
		return &Asset{ID: id, Prompt: full, Family: family, Tags: tags, Model: model, Size: size}, nil
	}
	if !c.HasKey() {
		return nil, fmt.Errorf("no OpenAI key: set OPENAI_API_KEY")
	}
	if size == "" {
		size = "1024x1024"
	}
	body, code, err := c.post("/images/generations", map[string]any{
		"model": model, "prompt": full, "size": size, "n": 1,
	})
	if err != nil {
		return nil, err
	}
	if code != 200 {
		return nil, fmt.Errorf("images API %d: %s", code, truncate(string(body), 400))
	}
	var out struct {
		Data []struct {
			B64 string `json:"b64_json"`
			URL string `json:"url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil || len(out.Data) == 0 {
		return nil, fmt.Errorf("images API: unexpected response")
	}
	var img []byte
	if out.Data[0].B64 != "" {
		img, err = base64.StdEncoding.DecodeString(out.Data[0].B64)
		if err != nil {
			return nil, err
		}
	} else if out.Data[0].URL != "" {
		res, err := c.HTTP.Get(out.Data[0].URL)
		if err != nil {
			return nil, err
		}
		defer res.Body.Close()
		img, err = io.ReadAll(res.Body)
		if err != nil {
			return nil, err
		}
	}
	file := "img/" + id + ".png"
	if err := os.MkdirAll(filepath.Join(libDir, "img"), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(libDir, file), img, 0o644); err != nil {
		return nil, err
	}
	h := sha256.Sum256(img)
	asset := &Asset{ID: id, File: file, Prompt: full, Family: family, Tags: tags,
		Model: model, Size: size, Created: time.Now().Format(time.RFC3339),
		Hash: hex.EncodeToString(h[:8])}
	man.Assets = append(man.Assets, *asset)
	if err := man.Save(libDir); err != nil {
		return nil, err
	}
	return asset, nil
}

// ---- realtime ephemeral token ----

// MintRealtimeToken asks OpenAI for an ephemeral client secret the browser
// can use to open a WebRTC realtime session. Response is passed through.
func (c *Client) MintRealtimeToken(tokenPath, model string) ([]byte, int, error) {
	if !c.HasKey() {
		return nil, 0, fmt.Errorf("no OpenAI key: set OPENAI_API_KEY")
	}
	payload := map[string]any{
		"session": map[string]any{"type": "realtime", "model": model},
	}
	return c.post(tokenPath, payload)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
