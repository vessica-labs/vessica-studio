// Package library owns the shared image and video asset catalog stored in a
// studio root's library/manifest.json.
package library

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Manifest struct {
	Version       int                    `json:"version"`
	StyleFamilies map[string]StyleFamily `json:"styleFamilies"`
	Assets        []Asset                `json:"assets"`
	Bundles       []BundleAsset          `json:"bundles,omitempty"`
	Videos        []VideoAsset           `json:"videos,omitempty"`
}

type StyleFamily struct {
	PromptPrefix string `json:"promptPrefix"`
}

type Asset struct {
	// Preserve source attribution and future metadata when registering new images.
	extra          map[string]json.RawMessage
	sourceFields   map[string]json.RawMessage
	baselineFields map[string]json.RawMessage

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

// assetFields excludes unknown metadata from the typed overlay so removing a known
// optional field cannot resurrect its previous wire value.
var assetFields = []string{"id", "file", "prompt", "family", "tags", "model", "size", "created", "hash", "usage"}

type assetWire Asset

func (a *Asset) UnmarshalJSON(data []byte) error {
	var typed assetWire
	if err := json.Unmarshal(data, &typed); err != nil {
		return err
	}
	var extra map[string]json.RawMessage
	if err := json.Unmarshal(data, &extra); err != nil {
		return err
	}
	source := make(map[string]json.RawMessage)
	for _, key := range assetFields {
		if value, ok := extra[key]; ok {
			source[key] = value
		}
		delete(extra, key)
	}
	normalized, err := json.Marshal(typed)
	if err != nil {
		return err
	}
	var baseline map[string]json.RawMessage
	if err := json.Unmarshal(normalized, &baseline); err != nil {
		return err
	}
	*a = Asset(typed)
	a.extra = extra
	a.sourceFields = source
	a.baselineFields = baseline
	return nil
}

func (a Asset) MarshalJSON() ([]byte, error) {
	raw, err := json.Marshal(assetWire(a))
	if err != nil {
		return nil, err
	}
	if a.baselineFields == nil && len(a.extra) == 0 {
		return raw, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	// Keep unchanged wire fields exactly as they were, including absence, null
	// and explicit empty values. Cloud accepts only additive registrations.
	for _, key := range assetFields {
		if a.baselineFields != nil && bytes.Equal(fields[key], a.baselineFields[key]) {
			if original, ok := a.sourceFields[key]; ok {
				fields[key] = original
			} else {
				delete(fields, key)
			}
		}
	}
	for key, value := range a.extra {
		fields[key] = value
	}
	return json.Marshal(fields)
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

// BundleAsset is an immutable, self-contained HTML application plus local data.
type BundleAsset struct {
	ID            string `json:"id"`
	File          string `json:"file"`
	Hash          string `json:"hash"`
	Bytes         int64  `json:"bytes"`
	Entrypoint    string `json:"entrypoint"`
	ExpandedBytes int64  `json:"expandedBytes"`
	FileCount     int    `json:"fileCount"`
	Poster        string `json:"poster,omitempty"`
	Created       string `json:"created,omitempty"`
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
