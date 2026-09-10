package studio

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Original source files remain in the studio. Only the disposable release is
// transformed. Derivative names bind the source digest and versioned recipe.
func optimizeReleaseDelivery(root, html string, assets map[string]struct{}) (string, error) {
	paths := make([]string, 0, len(assets))
	for p := range assets {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, path := range paths {
		source := filepath.Join(root, filepath.FromSlash(path))
		ext := strings.ToLower(filepath.Ext(path))
		if ext == ".svg" {
			raw, err := os.ReadFile(source)
			if err != nil {
				return "", err
			}
			clean, err := sanitizeDeliverySVG(raw)
			if err != nil {
				return "", fmt.Errorf("sanitize %s: %w", path, err)
			}
			if err = os.WriteFile(source, clean, 0o644); err != nil {
				return "", err
			}
			continue
		}
		if ext != ".png" && ext != ".jpg" && ext != ".jpeg" && ext != ".webp" && ext != ".mp4" {
			continue
		}
		raw, err := os.ReadFile(source)
		if err != nil {
			return "", err
		}
		hash := sha256.Sum256(raw)
		stem := "assets/variants/" + hex.EncodeToString(hash[:]) + "-v1"
		target := stem + "-1920.webp"
		args := []string{"-nostdin", "-v", "error", "-y", "-i", source, "-map_metadata", "-1", "-threads", "1"}
		if ext == ".mp4" {
			target = stem + "-1080.mp4"
			args = append(args, "-vf", "scale=w='min(1920,iw)':h='min(1080,ih)':force_original_aspect_ratio=decrease:force_divisible_by=2", "-c:v", "libx264", "-preset", "medium", "-crf", "23", "-pix_fmt", "yuv420p", "-c:a", "aac", "-b:a", "128k", "-movflags", "+faststart")
		} else {
			args = append(args, "-vf", "scale='min(1920,iw)':-1", "-frames:v", "1", "-c:v", "libwebp", "-quality", "82")
		}
		dest := filepath.Join(root, filepath.FromSlash(target))
		if err = os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return "", err
		}
		if ext == ".mp4" {
			ffmpeg, lookupErr := exec.LookPath("ffmpeg")
			if lookupErr != nil {
				return "", fmt.Errorf("optimized video requires FFmpeg: %w", lookupErr)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			err = exec.CommandContext(ctx, ffmpeg, append(args, dest)...).Run()
			cancel()
		} else {
			err = encodeReleaseWebP(source, dest, 1920)
		}
		if err != nil {
			return "", fmt.Errorf("optimize %s: %w", path, err)
		}
		info, err := os.Stat(dest)
		if err != nil {
			return "", err
		}
		if info.Size() > maxReleaseFileBytes {
			return "", fmt.Errorf("optimized media exceeds limit")
		}
		// Keep engine video logical paths stable for dynamic video-id resolution.
		if ext == ".mp4" {
			if err = os.Rename(dest, source); err != nil {
				return "", err
			}
			poster := "assets/video-posters/" + strings.TrimSuffix(filepath.Base(path), ext) + ".webp"
			posterPath := filepath.Join(root, filepath.FromSlash(poster))
			if err = os.MkdirAll(filepath.Dir(posterPath), 0o755); err != nil {
				return "", err
			}
			temporary := posterPath + ".png"
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			err = exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-v", "error", "-y", "-i", source, "-frames:v", "1", "-vf", "scale='min(1280,iw)':-1", temporary).Run()
			cancel()
			if err != nil {
				return "", fmt.Errorf("video poster: %w", err)
			}
			err = encodeReleaseWebP(temporary, posterPath, 1280)
			_ = os.Remove(temporary)
			if err != nil {
				return "", err
			}
			assets[poster] = struct{}{}
			html = strings.ReplaceAll(html, `'./assets/video-posters/'+id+'.jpg'`, `'./assets/video-posters/'+id+'.webp'`)

		} else {

			if strings.HasPrefix(path, "assets/video-posters/") {
				stable := strings.TrimSuffix(path, ext) + ".webp"
				if err = os.Rename(dest, filepath.Join(root, filepath.FromSlash(stable))); err != nil {
					return "", err
				}
				target = stable
				html = strings.ReplaceAll(html, `'./assets/video-posters/'+id+'.jpg'`, `'./assets/video-posters/'+id+'.webp'`)
			}
			html = strings.ReplaceAll(html, "./"+path, "./"+target)
			variants := []string{}
			originalWidth, _ := releaseImageDimensions(source)
			if !strings.HasPrefix(path, "assets/video-posters/") {
				for _, width := range []int{640, 1280} {
					if originalWidth <= width {
						continue
					}
					small := fmt.Sprintf("%s-%d.webp", stem, width)
					smallDest := filepath.Join(root, filepath.FromSlash(small))
					err := encodeReleaseWebP(source, smallDest, width)
					if err != nil {
						return "", fmt.Errorf("image variant: %w", err)
					}
					assets[small] = struct{}{}
					variants = append(variants, fmt.Sprintf("./%s %dw", small, width))
				}
			}
			if !strings.HasPrefix(path, "assets/video-posters/") {
				variants = append(variants, fmt.Sprintf("./%s %dw", target, min(1920, originalWidth)))
				sourceAttr := regexp.MustCompile(`(?i)\s(?:data-vstd-)?src=["']` + regexp.QuoteMeta("./"+target) + `["']`)
				html = imageTagRe.ReplaceAllStringFunc(html, func(tag string) string {
					if !sourceAttr.MatchString(tag) || strings.Contains(strings.ToLower(tag), "srcset=") {
						return tag
					}
					attribute := "srcset"
					if strings.Contains(tag, "data-vstd-src=") {
						attribute = "data-vstd-srcset"
					}
					extra := ` ` + attribute + `="` + strings.Join(variants, ", ") + `"`
					if !strings.Contains(strings.ToLower(tag), " sizes=") {
						extra += ` sizes="100vw"`
					}
					return strings.TrimSuffix(tag, ">") + extra + ">"
				})
			}
			assets[target] = struct{}{}
			delete(assets, path)
			if err = os.Remove(source); err != nil {
				return "", err
			}
		}
	}
	return html, nil
}

func encodeReleaseWebP(source, dest string, maxWidth int) error {
	encoder, err := exec.LookPath("cwebp")
	if err != nil {
		return fmt.Errorf("optimized images require cwebp: %w", err)
	}
	width, height := releaseImageDimensions(source)
	if width < 1 || height < 1 {
		return fmt.Errorf("unsupported image dimensions")
	}
	if width > maxWidth {
		height = max(1, height*maxWidth/width)
		width = maxWidth
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	return exec.CommandContext(ctx, encoder, "-quiet", "-q", "82", "-metadata", "none", "-exact", "-resize", fmt.Sprint(width), fmt.Sprint(height), source, "-o", dest).Run()
}
