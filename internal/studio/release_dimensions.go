package studio

import (
	_ "golang.org/x/image/webp"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
)

func releaseImageDimensions(path string) (int, int) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	config, _, err := image.DecodeConfig(f)
	if err != nil {
		return 0, 0
	}
	return config.Width, config.Height
}
