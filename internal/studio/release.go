package studio

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/vessica-labs/vessica-studio/internal/library"
)

type ReleaseEngineIdentity struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	Revision string `json:"revision"`
}

type ReleaseArtifact struct {
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	Bytes     int64  `json:"bytes"`
	MediaType string `json:"mediaType"`
}

type ReleaseManifest struct {
	SchemaVersion    int                   `json:"schemaVersion"`
	Engine           ReleaseEngineIdentity `json:"engine"`
	Entrypoint       string                `json:"entrypoint"`
	Theme            string                `json:"theme"`
	Assets           []string              `json:"assets"`
	Artifacts        []ReleaseArtifact     `json:"artifacts"`
	ManifestChecksum string                `json:"manifestChecksum"`
}

type unsignedReleaseManifest struct {
	SchemaVersion int                   `json:"schemaVersion"`
	Engine        ReleaseEngineIdentity `json:"engine"`
	Entrypoint    string                `json:"entrypoint"`
	Theme         string                `json:"theme"`
	Assets        []string              `json:"assets"`
	Artifacts     []ReleaseArtifact     `json:"artifacts"`
}

var (
	releaseRevisionRE    = regexp.MustCompile(`^[a-f0-9]{40}$`)
	releaseLibraryRE     = regexp.MustCompile(`(?:\./|/)library/([A-Za-z0-9][A-Za-z0-9._/-]*)`)
	releaseRootLibraryRE = regexp.MustCompile(`(^|[("'=:\s])\/library\/`)
	releaseVideoRE       = regexp.MustCompile(`data-vstd-video=["']([a-z0-9][a-z0-9-]*)["']`)
)

const (
	maxReleaseArtifacts  = 10_000
	maxReleaseFileBytes  = int64(512 << 20)
	maxReleaseTotalBytes = int64(2 << 30)
)

// BuildRelease emits one deterministic, self-contained presentation release.
// It consumes the engine-owned player and file model through Build and never
// includes authoring source, companion notes, credentials, or generated state.
func (s *Studio) BuildRelease(deck, output string, engine ReleaseEngineIdentity) (*ReleaseManifest, error) {
	if engine.Name != "vstd" || engine.Version == "" || !releaseRevisionRE.MatchString(engine.Revision) {
		return nil, fmt.Errorf("invalid release engine identity")
	}
	meta, err := s.LoadDeckMeta(deck)
	if err != nil {
		return nil, err
	}
	root, err := filepath.Abs(s.Root)
	if err != nil {
		return nil, err
	}
	destination, err := filepath.Abs(output)
	if err != nil {
		return nil, err
	}
	physicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	physicalDestination, err := resolveReleasePath(destination)
	if err != nil {
		return nil, err
	}
	if physicalDestination == physicalRoot || strings.HasPrefix(physicalDestination, physicalRoot+string(filepath.Separator)) {
		return nil, fmt.Errorf("release output must be outside the studio root")
	}
	if err := requireEmptyOrMissing(destination); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return nil, err
	}
	staging, err := os.MkdirTemp(filepath.Dir(destination), ".vstd-release-")
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(staging)
		}
	}()

	built, err := s.Build(deck)
	if err != nil {
		return nil, err
	}
	htmlBytes, err := os.ReadFile(built)
	if err != nil {
		return nil, err
	}
	html := releaseRelativePlayer(string(htmlBytes))
	if err := writeReleaseFile(staging, "index.html", []byte(html)); err != nil {
		return nil, err
	}

	assetPaths := map[string]struct{}{}
	for _, match := range releaseLibraryRE.FindAllStringSubmatch(html, -1) {
		relative := filepath.ToSlash(filepath.Clean(match[1]))
		if !validReleasePath(relative) {
			return nil, fmt.Errorf("unsafe release library path")
		}
		contents, err := readReleaseSource(filepath.Join(s.Root, "library"), relative)
		if err != nil {
			return nil, fmt.Errorf("release library asset %q is unavailable", relative)
		}
		path := "library/" + relative
		if err := writeReleaseFile(staging, path, contents); err != nil {
			return nil, err
		}
		assetPaths[path] = struct{}{}
	}
	if err := s.copyReleaseVideos(staging, html, assetPaths); err != nil {
		return nil, err
	}

	artifacts, err := releaseArtifacts(staging)
	if err != nil {
		return nil, err
	}
	assets := make([]string, 0, len(assetPaths))
	for path := range assetPaths {
		assets = append(assets, path)
	}
	sort.Strings(assets)
	unsigned := unsignedReleaseManifest{
		SchemaVersion: 1,
		Engine:        engine,
		Entrypoint:    "index.html",
		Theme:         meta.Theme,
		Assets:        assets,
		Artifacts:     artifacts,
	}
	checksum, err := canonicalChecksum(unsigned)
	if err != nil {
		return nil, err
	}
	manifest := &ReleaseManifest{
		SchemaVersion:    unsigned.SchemaVersion,
		Engine:           unsigned.Engine,
		Entrypoint:       unsigned.Entrypoint,
		Theme:            unsigned.Theme,
		Assets:           unsigned.Assets,
		Artifacts:        unsigned.Artifacts,
		ManifestChecksum: checksum,
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	encoded = append(encoded, '\n')
	if err := writeReleaseFile(staging, "release-manifest.json", encoded); err != nil {
		return nil, err
	}
	if err := os.Remove(destination); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err := os.Chmod(staging, 0o755); err != nil {
		return nil, err
	}
	if err := os.Rename(staging, destination); err != nil {
		return nil, err
	}
	committed = true
	return manifest, nil
}

func releaseRelativePlayer(html string) string {
	html = releaseRootLibraryRE.ReplaceAllString(html, `${1}./library/`)
	html = strings.ReplaceAll(html,
		`const srcFor=id=>httpMode?'/assets/video/'+id:'assets/video/'+id+'.mp4';`,
		`const srcFor=id=>'./assets/video/'+id+'.mp4';`)
	html = strings.ReplaceAll(html,
		`const posterFor=id=>httpMode?'/assets/video/'+id+'/poster':'assets/video-posters/'+id+'.jpg';`,
		`const posterFor=id=>'./assets/video-posters/'+id+'.jpg';`)
	return html
}

func (s *Studio) copyReleaseVideos(staging, html string, assets map[string]struct{}) error {
	ids := map[string]struct{}{}
	for _, match := range releaseVideoRE.FindAllStringSubmatch(html, -1) {
		ids[match[1]] = struct{}{}
	}
	if len(ids) == 0 {
		return nil
	}
	manifest, err := library.Load(filepath.Join(s.Root, "library"))
	if err != nil {
		return err
	}
	byID := map[string]library.VideoAsset{}
	for _, video := range manifest.Videos {
		byID[video.ID] = video
	}
	for id := range ids {
		video, ok := byID[id]
		if !ok {
			return fmt.Errorf("release video %q is absent from the library manifest", id)
		}
		if !validReleasePath(video.File) {
			return fmt.Errorf("release video %q has an unsafe library path", id)
		}
		contents, err := readReleaseSource(filepath.Join(s.Root, "library"), video.File)
		if err != nil {
			return fmt.Errorf("release video %q bytes are unavailable", id)
		}
		if !strings.EqualFold(filepath.Ext(video.File), ".mp4") {
			return fmt.Errorf("release video %q uses unsupported non-MP4 bytes", id)
		}
		digest := sha256.Sum256(contents)
		if int64(len(contents)) != video.Bytes || hex.EncodeToString(digest[:]) != video.Hash {
			return fmt.Errorf("release video %q does not match its library manifest", id)
		}
		path := "assets/video/" + id + ".mp4"
		if err := writeReleaseFile(staging, path, contents); err != nil {
			return err
		}
		assets[path] = struct{}{}
		if video.Poster == "" {
			continue
		}
		if !validReleasePath(video.Poster) {
			return fmt.Errorf("release video %q has an unsafe poster path", id)
		}
		poster, err := readReleaseSource(filepath.Join(s.Root, "library"), video.Poster)
		if err != nil {
			return fmt.Errorf("release video %q poster is unavailable", id)
		}
		posterPath := "assets/video-posters/" + id + ".jpg"
		if err := writeReleaseFile(staging, posterPath, poster); err != nil {
			return err
		}
		assets[posterPath] = struct{}{}
	}
	return nil
}

func resolveReleasePath(path string) (string, error) {
	remaining := []string{}
	candidate := path
	for {
		resolved, err := filepath.EvalSymlinks(candidate)
		if err == nil {
			parts := append([]string{resolved}, reverseStrings(remaining)...)
			return filepath.Clean(filepath.Join(parts...)), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			return "", err
		}
		remaining = append(remaining, filepath.Base(candidate))
		candidate = parent
	}
}

func reverseStrings(values []string) []string {
	reversed := make([]string, len(values))
	for index := range values {
		reversed[len(values)-1-index] = values[index]
	}
	return reversed
}

func requireEmptyOrMissing(path string) error {
	info, lstatErr := os.Lstat(path)
	if lstatErr == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("release output cannot be a symbolic link")
	}
	if lstatErr != nil && !os.IsNotExist(lstatErr) {
		return lstatErr
	}
	entries, err := os.ReadDir(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("release output must be empty")
	}
	return nil
}

func readReleaseSource(root, relative string) ([]byte, error) {
	if !validReleasePath(relative) {
		return nil, fmt.Errorf("unsafe release source path")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(root, filepath.FromSlash(relative))
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}
	if resolved == root || !strings.HasPrefix(resolved, root+string(filepath.Separator)) {
		return nil, fmt.Errorf("release source escapes the library")
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxReleaseFileBytes {
		return nil, fmt.Errorf("release source is not a bounded regular file")
	}
	return os.ReadFile(resolved)
}

func validReleasePath(path string) bool {
	return path != "" && !strings.HasPrefix(path, "/") && path == filepath.ToSlash(filepath.Clean(path)) &&
		path != ".." && !strings.HasPrefix(path, "../") && !strings.Contains(path, `\`)
}

func writeReleaseFile(root, path string, contents []byte) error {
	if !validReleasePath(path) {
		return fmt.Errorf("unsafe release artifact path")
	}
	destination := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	return os.WriteFile(destination, contents, 0o644)
}

func releaseArtifacts(root string) ([]ReleaseArtifact, error) {
	var artifacts []ReleaseArtifact
	var totalBytes int64
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return fmt.Errorf("release output contains unsupported file type")
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if int64(len(contents)) > maxReleaseFileBytes {
			return fmt.Errorf("release artifact exceeds the per-file limit")
		}
		totalBytes += int64(len(contents))
		if totalBytes > maxReleaseTotalBytes {
			return fmt.Errorf("release artifacts exceed the total byte limit")
		}
		if len(artifacts) >= maxReleaseArtifacts {
			return fmt.Errorf("release artifact count exceeds the limit")
		}
		digest := sha256.Sum256(contents)
		mediaType := mime.TypeByExtension(filepath.Ext(relative))
		if mediaType == "" {
			mediaType = "application/octet-stream"
		}
		artifacts = append(artifacts, ReleaseArtifact{
			Path:      relative,
			SHA256:    hex.EncodeToString(digest[:]),
			Bytes:     int64(len(contents)),
			MediaType: mediaType,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Path < artifacts[j].Path })
	return artifacts, nil
}

func canonicalChecksum(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return "", err
	}
	var canonical bytes.Buffer
	if err := writeCanonicalJSON(&canonical, decoded); err != nil {
		return "", err
	}
	digest := sha256.Sum256(canonical.Bytes())
	return hex.EncodeToString(digest[:]), nil
}

func writeCanonicalJSON(output *bytes.Buffer, value any) error {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		output.WriteByte('{')
		for index, key := range keys {
			if index > 0 {
				output.WriteByte(',')
			}
			encodedKey, _ := json.Marshal(key)
			output.Write(encodedKey)
			output.WriteByte(':')
			if err := writeCanonicalJSON(output, typed[key]); err != nil {
				return err
			}
		}
		output.WriteByte('}')
	case []any:
		output.WriteByte('[')
		for index, item := range typed {
			if index > 0 {
				output.WriteByte(',')
			}
			if err := writeCanonicalJSON(output, item); err != nil {
				return err
			}
		}
		output.WriteByte(']')
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return err
		}
		output.Write(encoded)
	}
	return nil
}
