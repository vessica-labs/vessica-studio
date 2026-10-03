package server

import (
	"fmt"
	"github.com/vessica-labs/vessica-studio/internal/oai"
	"github.com/vessica-labs/vessica-studio/internal/studio"
	"gopkg.in/yaml.v3"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// ProcessSelectedImage owns request interpretation and manifest registration.
// The host owns the API's authority and billing. Never process a foreign request.
func (s *Server) ProcessSelectedImage(deck, slide, api, model string) (bool, error) {
	if !studio.ValidDeckName(deck) || !studio.ValidSlideID(slide) {
		return false, fmt.Errorf("invalid image selector")
	}
	u, err := url.Parse(api)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !(u.Scheme == "https" || (u.Scheme == "http" && u.Hostname() == "127.0.0.1")) {
		return false, fmt.Errorf("invalid image API")
	}
	dir := filepath.Join(s.St.Root, "requests")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var selected string
	var request assetRequest
	for _, e := range entries {
		if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return false, err
		}
		var req assetRequest
		if yaml.Unmarshal(data, &req) != nil {
			continue
		}
		if req.Deck != deck || req.Slide != slide {
			continue
		}
		if selected != "" || (req.Type != "" && req.Type != "image") || strings.TrimSpace(req.Prompt) == "" {
			return false, fmt.Errorf("selected image request must contain one image")
		}
		selected = e.Name()
		request = req
	}
	if selected == "" {
		return false, nil
	}
	client := oai.New(api, "")
	if !client.HasKey() {
		return false, fmt.Errorf("image API credential unavailable")
	}
	// Host transport selects quality and output bounds; no paid retry in this pass.
	_, err = client.GenerateAsset(filepath.Join(s.St.Root, "library"), model, request.Prompt, request.Family, "1024x1024", request.Slug, request.Tags, false)
	if err != nil {
		return false, fmt.Errorf("selected image generation failed")
	}
	s.archiveRequest(filepath.Join(dir, selected), selected, "done")
	return true, nil
}
