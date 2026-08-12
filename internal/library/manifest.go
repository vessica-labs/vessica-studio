// Package library owns the shared image and video asset catalog stored in a
// studio root's library/manifest.json.
package library

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Manifest struct {
	Version       int                    `json:"version"`
	StyleFamilies map[string]StyleFamily `json:"styleFamilies"`
	Assets        []Asset                `json:"assets"`
	Videos        []VideoAsset           `json:"videos,omitempty"`
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

type VideoAsset struct {
	ID       string   `json:"id"`
	File     string   `json:"file"`
	Poster   string   `json:"poster,omitempty"`
	Hash     string   `json:"hash"`
	Bytes    int64    `json:"bytes"`
	Duration float64  `json:"duration,omitempty"`
	Width    int      `json:"width,omitempty"`
	Height   int      `json:"height,omitempty"`
	Source   string   `json:"source,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	Created  string   `json:"created"`
	Usage    []string `json:"usage,omitempty"`
}

func Load(dir string) (*Manifest, error) {
	manifest := &Manifest{Version: 1, StyleFamilies: map[string]StyleFamily{}}
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return manifest, nil
	}
	if err := json.Unmarshal(b, manifest); err != nil {
		return nil, fmt.Errorf("library manifest: %w", err)
	}
	if manifest.StyleFamilies == nil {
		manifest.StyleFamilies = map[string]StyleFamily{}
	}
	return manifest, nil
}

func (m *Manifest) Save(dir string) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("library manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), b, 0o644); err != nil {
		return fmt.Errorf("library manifest: %w", err)
	}
	return nil
}
