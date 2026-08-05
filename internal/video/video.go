// Package video implements the video asset pipeline: ingest (normalize with
// ffmpeg when available, extract a poster frame, probe metadata), the library
// manifest entries, and content-addressed storage under library/video/.
//
// Layout (relative to the library dir):
//
//	video/<sha256>.mp4          the bytes — gitignored, synced to the bucket
//	video-posters/<id>.jpg      small poster frames — committed to git
//	manifest.json               gains a "videos" array (see oai.Manifest)
//
// Slides reference videos by asset id only (data-vstd-video="<id>"); the
// engine resolves /assets/video/<id> per serving mode. Bytes never enter git
// history — GitHub blocks >100MB files and every clone would carry every
// revision forever — so hosted serving pulls from S3-compatible storage
// (see internal/s3) instead of the repo image.
package video

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/vessica-labs/vessica-studio/internal/oai"
)

// Dir names under the library root.
const (
	BytesDir  = "video"
	PosterDir = "video-posters"
)

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// Slugify makes a video asset id from a filename or explicit slug.
func Slugify(s string) string {
	s = strings.TrimSuffix(strings.ToLower(filepath.Base(s)), filepath.Ext(s))
	s = strings.Trim(slugRe.ReplaceAllString(s, "-"), "-")
	if len(s) > 48 {
		s = s[:48]
	}
	if s == "" {
		s = "video"
	}
	return s
}

// Probe holds ffprobe results for a media file.
type Probe struct {
	Codec    string
	Width    int
	Height   int
	Duration float64
	Format   string
}

func haveFFmpeg() bool {
	_, err1 := exec.LookPath("ffmpeg")
	_, err2 := exec.LookPath("ffprobe")
	return err1 == nil && err2 == nil
}

func probe(path string) (*Probe, error) {
	out, err := exec.Command("ffprobe", "-v", "error", "-print_format", "json",
		"-show_format", "-show_streams", path).Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe: %w", err)
	}
	var raw struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
			CodecName string `json:"codec_name"`
			Width     int    `json:"width"`
			Height    int    `json:"height"`
		} `json:"streams"`
		Format struct {
			FormatName string `json:"format_name"`
			Duration   string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("ffprobe parse: %w", err)
	}
	p := &Probe{Format: raw.Format.FormatName}
	p.Duration, _ = strconv.ParseFloat(raw.Format.Duration, 64)
	for _, s := range raw.Streams {
		if s.CodecType == "video" {
			p.Codec, p.Width, p.Height = s.CodecName, s.Width, s.Height
			break
		}
	}
	return p, nil
}

// Options for Ingest.
type Options struct {
	Slug        string   // asset id; derived from the filename when empty
	Tags        []string
	Source      string // original filename, recorded in the manifest
	NoTranscode bool   // store bytes as-is even when ffmpeg is present
}

// Result of an ingest.
type Result struct {
	Asset      *oai.VideoAsset
	Transcoded bool     // true when ffmpeg re-encoded (vs remux/copy)
	Warnings   []string // e.g. "ffmpeg not found — stored as-is, no poster"
}

// Ingest brings a video file into the library: normalize to a web-friendly
// H.264 MP4 with +faststart (playback can begin before the file finishes
// downloading), cap at 1080p, extract a poster frame, hash, store under
// video/<sha256>.mp4, and update manifest.json. Re-ingesting under an
// existing id replaces that asset's bytes and metadata (the id is the
// stable reference; the hash names the bytes).
func Ingest(libDir, srcPath string, opts Options) (*Result, error) {
	if _, err := os.Stat(srcPath); err != nil {
		return nil, err
	}
	id := opts.Slug
	if id == "" {
		id = Slugify(srcPath)
	}
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`).MatchString(id) {
		return nil, fmt.Errorf("invalid video id %q (lowercase letters, digits, hyphens)", id)
	}
	source := opts.Source
	if source == "" {
		source = filepath.Base(srcPath)
	}
	res := &Result{}

	work := srcPath
	ext := ".mp4"
	var pr *Probe
	if haveFFmpeg() {
		var err error
		pr, err = probe(srcPath)
		if err != nil {
			return nil, err
		}
		if !opts.NoTranscode {
			tmp, transcoded, err := normalize(srcPath, pr)
			if err != nil {
				return nil, err
			}
			defer os.Remove(tmp)
			work = tmp
			res.Transcoded = transcoded
			if pr2, err := probe(tmp); err == nil {
				pr = pr2
			}
		} else if e := strings.ToLower(filepath.Ext(srcPath)); e != "" {
			ext = e // stored as-is — keep the real container extension
		}
	} else {
		res.Warnings = append(res.Warnings,
			"ffmpeg not found — stored as-is (no normalization, no poster); install ffmpeg for the full pipeline")
		ext = filepath.Ext(srcPath)
		if ext == "" {
			ext = ".mp4"
		}
	}

	// hash + store bytes
	hash, size, err := hashFile(work)
	if err != nil {
		return nil, err
	}
	bytesDir := filepath.Join(libDir, BytesDir)
	if err := os.MkdirAll(bytesDir, 0o755); err != nil {
		return nil, err
	}
	file := BytesDir + "/" + hash + ext
	dst := filepath.Join(libDir, filepath.FromSlash(file))
	if err := copyFile(work, dst); err != nil {
		return nil, err
	}

	// poster frame (~10% in, capped at 3s so long videos don't get a mid-scene frame)
	posterRel := ""
	if pr != nil {
		at := pr.Duration * 0.10
		if at > 3 {
			at = 3
		}
		posterDir := filepath.Join(libDir, PosterDir)
		os.MkdirAll(posterDir, 0o755)
		posterRel = PosterDir + "/" + id + ".jpg"
		posterAbs := filepath.Join(libDir, filepath.FromSlash(posterRel))
		cmd := exec.Command("ffmpeg", "-y", "-v", "error",
			"-ss", fmt.Sprintf("%.2f", at), "-i", dst,
			"-frames:v", "1", "-q:v", "4", "-vf", "scale='min(1280,iw)':-2", posterAbs)
		if out, err := cmd.CombinedOutput(); err != nil {
			res.Warnings = append(res.Warnings, "poster extraction failed: "+strings.TrimSpace(string(out)))
			posterRel = ""
		}
	}

	asset := &oai.VideoAsset{
		ID: id, File: file, Poster: posterRel, Hash: hash, Bytes: size,
		Source: source, Tags: opts.Tags, Created: time.Now().Format(time.RFC3339),
	}
	if pr != nil {
		asset.Duration = round1(pr.Duration)
		asset.Width, asset.Height = pr.Width, pr.Height
	}

	man, err := oai.LoadManifest(libDir)
	if err != nil {
		return nil, err
	}
	replaced := false
	for i := range man.Videos {
		if man.Videos[i].ID == id {
			old := man.Videos[i]
			if old.File != file && old.File != "" {
				// bytes superseded — drop the stale blob so the folder and
				// bucket don't accumulate orphans
				os.Remove(filepath.Join(libDir, filepath.FromSlash(old.File)))
			}
			asset.Usage = old.Usage
			man.Videos[i] = *asset
			replaced = true
			break
		}
	}
	if !replaced {
		man.Videos = append(man.Videos, *asset)
	}
	if err := man.Save(libDir); err != nil {
		return nil, err
	}
	res.Asset = asset
	return res, nil
}

// normalize returns a temp file holding web-ready bytes. H.264 in an MP4/MOV
// container is remuxed (fast, lossless) with +faststart; anything else is
// transcoded to H.264/AAC capped at 1080p. Returns (path, transcoded, err).
func normalize(src string, pr *Probe) (string, bool, error) {
	tmp, err := os.CreateTemp("", "vstd-video-*.mp4")
	if err != nil {
		return "", false, err
	}
	tmp.Close()
	isMP4 := strings.Contains(pr.Format, "mp4") || strings.Contains(pr.Format, "mov")
	if pr.Codec == "h264" && isMP4 {
		cmd := exec.Command("ffmpeg", "-y", "-v", "error", "-i", src,
			"-c", "copy", "-movflags", "+faststart", tmp.Name())
		if out, err := cmd.CombinedOutput(); err != nil {
			os.Remove(tmp.Name())
			return "", false, fmt.Errorf("ffmpeg remux: %s", strings.TrimSpace(string(out)))
		}
		return tmp.Name(), false, nil
	}
	cmd := exec.Command("ffmpeg", "-y", "-v", "error", "-i", src,
		"-c:v", "libx264", "-preset", "fast", "-crf", "23",
		"-vf", "scale='min(1920,iw)':-2", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-b:a", "128k", "-movflags", "+faststart", tmp.Name())
	if out, err := cmd.CombinedOutput(); err != nil {
		os.Remove(tmp.Name())
		return "", false, fmt.Errorf("ffmpeg transcode: %s", strings.TrimSpace(string(out)))
	}
	return tmp.Name(), true, nil
}

// Find returns the manifest entry for a video id, or nil.
func Find(libDir, id string) (*oai.VideoAsset, error) {
	man, err := oai.LoadManifest(libDir)
	if err != nil {
		return nil, err
	}
	for i := range man.Videos {
		if man.Videos[i].ID == id {
			return &man.Videos[i], nil
		}
	}
	return nil, nil
}

func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func round1(f float64) float64 { return float64(int(f*10+0.5)) / 10 }
