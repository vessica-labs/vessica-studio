package studio

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ImageDelivery describes an immutable display copy. The source remains editable.
type ImageDelivery struct {
	SourceSHA256 string `json:"sourceSha256"`
	SHA256       string `json:"sha256"`
	Bytes        int64  `json:"bytes"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	MediaType    string `json:"mediaType"`
	Recipe       string `json:"recipe"`
}

// BuildImageDelivery uses the release image recipe without building a deck.
// Output must not exist. Limits bound decoder memory before starting cwebp.
func BuildImageDelivery(source, output string) (ImageDelivery, error) {
	var result ImageDelivery
	switch strings.ToLower(filepath.Ext(source)) {
	case ".png", ".jpg", ".jpeg", ".webp":
	default:
		return result, fmt.Errorf("unsupported image format")
	}
	info, err := os.Stat(source)
	if err != nil {
		return result, err
	}
	if !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > 16*1024*1024 {
		return result, fmt.Errorf("image input exceeds limit")
	}
	width, height := releaseImageDimensions(source)
	if width < 1 || height < 1 || width > 16384 || height > 16384 || int64(width)*int64(height) > 40_000_000 {
		return result, fmt.Errorf("image dimensions exceed limit")
	}
	raw, err := os.ReadFile(source)
	if err != nil {
		return result, err
	}
	// Reserve the output exclusively so aliases and existing files cannot overwrite source.
	file, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return result, err
	}
	if err = file.Close(); err != nil {
		_ = os.Remove(output)
		return result, err
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.Remove(output)
		}
	}()
	if err = encodeReleaseWebP(source, output, 1920); err != nil {
		return result, err
	}
	encoded, err := os.ReadFile(output)
	if err != nil {
		return result, err
	}
	if len(encoded) == 0 || len(encoded) > 16*1024*1024 {
		return result, fmt.Errorf("image output exceeds limit")
	}
	width, height = releaseImageDimensions(output)
	result = ImageDelivery{fmt.Sprintf("%x", sha256.Sum256(raw)), fmt.Sprintf("%x", sha256.Sum256(encoded)), int64(len(encoded)), width, height, "image/webp", "webp-v1-1920"}
	complete = true
	return result, nil
}
