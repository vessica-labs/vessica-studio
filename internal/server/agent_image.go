package server

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/vessica-labs/vessica-studio/internal/library"
	"github.com/vessica-labs/vessica-studio/internal/oai"
	"github.com/vessica-labs/vessica-studio/internal/studio"
	"golang.org/x/net/html"
	"gopkg.in/yaml.v3"
)

// ProcessSelectedImage owns request interpretation and manifest registration.
// The host owns the API's authority and billing. Never process a foreign request.
func (s *Server) ProcessSelectedImage(deck, slide, api, model string) (*library.Asset, error) {
	if !studio.ValidDeckName(deck) || !studio.ValidSlideID(slide) {
		return nil, fmt.Errorf("invalid image selector")
	}
	u, err := url.Parse(api)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !(u.Scheme == "https" || (u.Scheme == "http" && u.Hostname() == "127.0.0.1")) {
		return nil, fmt.Errorf("invalid image API")
	}
	dir := filepath.Join(s.St.Root, "requests")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var selected string
	var request assetRequest
	for _, e := range entries {
		if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		var req assetRequest
		if yaml.Unmarshal(data, &req) != nil {
			continue
		}
		if req.Deck != deck || req.Slide != slide {
			continue
		}
		if selected != "" || (req.Type != "" && req.Type != "image") || strings.TrimSpace(req.Prompt) == "" {
			return nil, fmt.Errorf("selected image request must contain one image")
		}
		selected = e.Name()
		request = req
	}
	if selected == "" {
		return nil, nil
	}
	client := oai.New(api, "")
	if !client.HasKey() {
		return nil, fmt.Errorf("image API credential unavailable")
	}
	// Host transport selects quality and output bounds; no paid retry in this pass.
	asset, err := client.GenerateAsset(filepath.Join(s.St.Root, "library"), model, request.Prompt, request.Family, "1024x1024", request.Slug, request.Tags, false)
	if err != nil {
		return nil, fmt.Errorf("selected image generation failed")
	}
	s.archiveRequest(filepath.Join(dir, selected), selected, "done")
	return asset, nil
}

// RunAgentSelectedImageLanding carries the actual provider result into placement
// and refuses a successful sweep that merely clears the request or reuses an old asset.
func (s *Server) RunAgentSelectedImageLanding(deck, slide string, asset *library.Asset, isolated bool) (int, error) {
	if !studio.ValidDeckName(deck) || !studio.ValidSlideID(slide) || asset == nil || asset.ID == "" || asset.Hash == "" || asset.File == "" || filepath.IsAbs(asset.File) || filepath.Clean(asset.File) != asset.File || strings.HasPrefix(asset.File, "..") {
		return 0, fmt.Errorf("invalid generated image receipt")
	}
	n := s.runAgentSelected(deck, slide, isolated, asset)
	return n, s.validateGeneratedImageLanding(deck, slide, asset)
}

func (s *Server) validateGeneratedImageLanding(deck, slide string, asset *library.Asset) error {
	fragment, err := os.ReadFile(s.St.SlidePath(deck, slide, ".html"))
	if err != nil || !referencesGeneratedImage(string(fragment), asset.File) {
		return fmt.Errorf("generated image was not placed on the selected slide")
	}
	companion, err := os.ReadFile(s.St.SlidePath(deck, slide, ".md"))
	if err != nil {
		return fmt.Errorf("generated image landing companion unavailable")
	}
	section := editReqRe.FindStringSubmatch(string(companion))
	if section == nil || actionable(section[1]) || strings.Contains(section[1], "- awaiting") || strings.Contains(section[1], "(worker error") {
		return fmt.Errorf("generated image landing remains unresolved")
	}
	return nil
}

func generatedImageReceipt(asset *library.Asset) string {
	data, _ := json.Marshal(struct {
		ID   string `json:"id"`
		URL  string `json:"url"`
		Hash string `json:"hash"`
	}{asset.ID, "/library/" + asset.File, asset.Hash})
	return "\n\nGENERATED IMAGE RECEIPT (engine-owned result of this sweep): " + string(data) + `
This is the exact newly generated image. Use this URL for the selected slide;
do not infer its identity from an old asset, a partial manifest, or filename recency.
Inspect the image pixels, execute the companion's landing plan, and update the actual
HTML image/background reference. Render the selected slide and inspect the rendered
PNG before resolving the request. A cleared companion alone is not successful placement.`
}

var imageCSSURL = regexp.MustCompile(`(?i)url\(\s*['"]?([^'")\s]+)`)

func referencesGeneratedImage(fragment, file string) bool {
	matches := func(raw string) bool {
		u, err := url.Parse(raw)
		if err != nil || u.Host != "" || u.Scheme != "" {
			return false
		}
		path := strings.TrimPrefix(u.Path, "/")
		for strings.HasPrefix(path, "./") || strings.HasPrefix(path, "../") {
			path = strings.TrimPrefix(strings.TrimPrefix(path, "../"), "./")
		}
		return path == "library/"+file
	}
	z := html.NewTokenizer(strings.NewReader(fragment))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return false
		}
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			continue
		}
		token := z.Token()
		for _, attr := range token.Attr {
			switch attr.Key {
			case "src", "data-vstd-src", "poster":
				if matches(attr.Val) {
					return true
				}
			case "srcset", "data-vstd-srcset":
				for _, candidate := range strings.Split(attr.Val, ",") {
					if fields := strings.Fields(candidate); len(fields) > 0 && matches(fields[0]) {
						return true
					}
				}
			case "style":
				for _, match := range imageCSSURL.FindAllStringSubmatch(attr.Val, -1) {
					if matches(match[1]) {
						return true
					}
				}
			}
		}
	}
}
