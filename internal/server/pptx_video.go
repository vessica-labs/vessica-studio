package server

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vessica-labs/vessica-studio/internal/studio"
	"github.com/vessica-labs/vessica-studio/internal/video"
)

const maxPPTXVideoBytes int64 = 512 << 20

// The measured export model is authoritative for media selection. This plan
// lets an isolated host materialize only referenced video bytes before export.
func pptxVideoIDs(deck studio.PPTXDeck) []string {
	ids := []string{}
	seen := map[string]bool{}
	for _, slide := range deck.Slides {
		for _, element := range slide.Elements {
			if element.Kind == "video" && element.VideoID != "" && !seen[element.VideoID] {
				ids = append(ids, element.VideoID)
				seen[element.VideoID] = true
			}
		}
	}
	return ids
}

// Hydrate only videos captured in the selected slides. Binary media is never
// written into the DOM capture cache, and manifest paths remain content-root scoped.
func (s *Server) hydratePPTXVideos(r *http.Request, deck *studio.PPTXDeck) error {
	loaded := map[string][]byte{}
	var total int64
	for si := range deck.Slides {
		for ei := range deck.Slides[si].Elements {
			e := &deck.Slides[si].Elements[ei]
			if e.Kind != "video" || e.VideoID == "" {
				continue
			}
			if data, ok := loaded[e.VideoID]; ok {
				e.VideoData = data
				continue
			}
			if !videoIDRe.MatchString(e.VideoID) {
				return fmt.Errorf("invalid PowerPoint video ID")
			}
			asset, err := video.Find(s.libDir(), e.VideoID)
			if err != nil || asset == nil {
				return fmt.Errorf("PowerPoint video %s is missing from the manifest", e.VideoID)
			}
			if !strings.HasPrefix(asset.File, "video/") || strings.ToLower(filepath.Ext(asset.File)) != ".mp4" || len(asset.Hash) != 64 || asset.Bytes <= 0 || asset.Bytes > maxPPTXVideoBytes-total {
				return fmt.Errorf("PowerPoint video %s has invalid metadata or exceeds the 512 MiB media budget", e.VideoID)
			}
			if _, err := hex.DecodeString(asset.Hash); err != nil {
				return fmt.Errorf("PowerPoint video %s has an invalid digest", e.VideoID)
			}
			if err := studio.CheckContentPath(s.libDir(), asset.File); err != nil {
				return fmt.Errorf("PowerPoint video %s: %w", e.VideoID, err)
			}
			var source io.ReadCloser
			source, err = os.Open(filepath.Join(s.libDir(), filepath.FromSlash(asset.File)))
			if os.IsNotExist(err) {
				if client := s.S3Client(); client != nil {
					req, reqErr := http.NewRequestWithContext(r.Context(), http.MethodGet, client.PresignGet(objectKey(asset.Hash), presignTTL), nil)
					if reqErr != nil {
						return fmt.Errorf("PowerPoint video %s could not be requested", e.VideoID)
					}
					response, fetchErr := (&http.Client{Timeout: 90 * time.Second}).Do(req)
					if fetchErr != nil {
						return fmt.Errorf("PowerPoint video %s could not be downloaded", e.VideoID)
					}
					if response.StatusCode != http.StatusOK {
						response.Body.Close()
						return fmt.Errorf("PowerPoint video %s is unavailable", e.VideoID)
					}
					source, err = response.Body, nil
				}
			}
			if err != nil {
				return fmt.Errorf("PowerPoint video %s bytes unavailable; materialize the video asset before export", e.VideoID)
			}
			data, readErr := io.ReadAll(io.LimitReader(source, asset.Bytes+1))
			source.Close()
			if readErr != nil || int64(len(data)) != asset.Bytes {
				return fmt.Errorf("PowerPoint video %s has incomplete bytes", e.VideoID)
			}
			digest := sha256.Sum256(data)
			if !strings.EqualFold(hex.EncodeToString(digest[:]), asset.Hash) {
				return fmt.Errorf("PowerPoint video %s failed digest verification", e.VideoID)
			}
			loaded[e.VideoID] = data
			e.VideoData = data
			total += int64(len(data))
		}
	}
	return nil
}
