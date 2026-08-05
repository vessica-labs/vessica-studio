package server

// Video asset serving + ingestion.
//
// Serving is mode-aware behind one stable URL shape, so slides only ever
// reference /assets/video/<id>:
//
//	studio/present — bytes stream from library/video/ on disk
//	                 (http.ServeContent → Range requests, seeking, offline)
//	public (Railway) — bytes are NOT in the image (gitignored), so the
//	                 engine validates deck access, then 302s to a short-lived
//	                 presigned bucket URL. No bucket configured → 404 with a
//	                 hint. Falls back to a local file when one exists (e.g.
//	                 a volume-mounted deployment).
//
// Posters are small JPEGs committed to git, served directly in every mode.
//
// Upload (POST /api/asset/video) is studio-mode only, like every other write.

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/vessica-labs/vessica-studio/internal/s3"
	"github.com/vessica-labs/vessica-studio/internal/video"
)

const presignTTL = 15 * time.Minute

var videoIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// S3Client resolves the bucket client from env + studio.yaml (nil = disabled).
func (s *Server) S3Client() *s3.Client {
	st := s.St.Config.Storage
	return s3.FromEnv(st.Endpoint, st.Bucket, st.Region, s3.KeyCmds{
		AccessKeyCmd: st.AccessKeyCmd, SecretKeyCmd: st.SecretKeyCmd,
	})
}

func (s *Server) libDir() string { return filepath.Join(s.St.Root, "library") }

// handleVideo serves GET /assets/video/{id}.
func (s *Server) handleVideo(w http.ResponseWriter, r *http.Request) {
	if !s.hasAnyAccess(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	id := r.PathValue("id")
	if !videoIDRe.MatchString(id) {
		http.NotFound(w, r)
		return
	}
	a, err := video.Find(s.libDir(), id)
	if err != nil || a == nil {
		http.NotFound(w, r)
		return
	}
	local := filepath.Join(s.libDir(), filepath.FromSlash(a.File))
	if f, err := os.Open(local); err == nil {
		defer f.Close()
		fi, _ := f.Stat()
		w.Header().Set("Content-Type", videoContentType(local))
		w.Header().Set("ETag", `"`+a.Hash[:16]+`"`)
		w.Header().Set("Cache-Control", "private, max-age=86400")
		http.ServeContent(w, r, filepath.Base(local), fi.ModTime(), f)
		return
	}
	// no local bytes (hosted image ships without them) — presign from the bucket
	if c := s.S3Client(); c != nil {
		http.Redirect(w, r, c.PresignGet(objectKey(a.Hash), presignTTL), http.StatusFound)
		return
	}
	http.Error(w, "video bytes unavailable: not on disk and no bucket configured (set VSTD_S3_* — see DEPLOY.md)", http.StatusNotFound)
}

// handleVideoPoster serves GET /assets/video/{id}/poster.
func (s *Server) handleVideoPoster(w http.ResponseWriter, r *http.Request) {
	if !s.hasAnyAccess(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	id := r.PathValue("id")
	if !videoIDRe.MatchString(id) {
		http.NotFound(w, r)
		return
	}
	a, err := video.Find(s.libDir(), id)
	if err != nil || a == nil || a.Poster == "" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeFile(w, r, filepath.Join(s.libDir(), filepath.FromSlash(a.Poster)))
}

// handleVideoUpload ingests POST /api/asset/video?filename=F[&slug=S&tags=a,b]
// (raw bytes in the body — the player's drag-drop uses XHR for upload
// progress). Studio mode only (wrapped by editOnly in Routes).
func (s *Server) handleVideoUpload(w http.ResponseWriter, r *http.Request) {
	filename := r.URL.Query().Get("filename")
	if filename == "" {
		filename = "upload.mp4"
	}
	slug := r.URL.Query().Get("slug")
	var tags []string
	for _, t := range strings.Split(r.URL.Query().Get("tags"), ",") {
		if t = strings.TrimSpace(t); t != "" {
			tags = append(tags, t)
		}
	}

	tmp, err := os.CreateTemp("", "vstd-upload-*"+filepath.Ext(filename))
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	defer os.Remove(tmp.Name())
	n, err := io.Copy(tmp, io.LimitReader(r.Body, 2<<30)) // 2GB guard
	tmp.Close()
	if err != nil {
		jsonErr(w, err, 400)
		return
	}
	if n == 0 {
		jsonErr(w, fmt.Errorf("empty body"), 400)
		return
	}
	res, err := video.Ingest(s.libDir(), tmp.Name(), video.Options{
		Slug: slug, Tags: tags, Source: filename,
	})
	if err != nil {
		jsonErr(w, err, 400)
		return
	}
	// best-effort background sync so the hosted deck works on next deploy
	// without a manual `vstd asset push`
	go s.pushVideo(res.Asset.Hash, res.Asset.File)
	s.Broadcast("reload")
	writeJSON(w, map[string]any{
		"status": "ok", "asset": res.Asset,
		"transcoded": res.Transcoded, "warnings": res.Warnings,
	})
}

func videoContentType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".webm":
		return "video/webm"
	case ".mov":
		return "video/quicktime"
	case ".m4v":
		return "video/x-m4v"
	default:
		return "video/mp4"
	}
}

// objectKey is the bucket key for a video blob: content-addressed, so
// re-pushes are no-ops and nothing is ever overwritten in place.
func objectKey(hash string) string { return "video/" + hash + ".mp4" }

// pushVideo uploads one blob to the bucket if configured and absent.
func (s *Server) pushVideo(hash, file string) {
	c := s.S3Client()
	if c == nil {
		return
	}
	key := objectKey(hash)
	if ok, _, err := c.Head(key); err != nil || ok {
		if err != nil {
			log.Printf("video: bucket check failed for %s: %v", key, err)
		}
		return
	}
	local := filepath.Join(s.libDir(), filepath.FromSlash(file))
	if err := c.Put(key, local, "video/mp4"); err != nil {
		log.Printf("video: bucket sync failed for %s: %v", key, err)
		return
	}
	log.Printf("video: synced %s to bucket", key)
}

// processVideoRequest handles a requests/*.yaml with type: video — the cloud
// Cowork path: the session drops the file (e.g. library/inbox/demo.mp4) plus
// a request yaml via the device bridge; the running engine ingests it.
func (s *Server) processVideoRequest(reqPath, name string, req assetRequest) {
	src := filepath.Join(s.St.Root, filepath.FromSlash(req.Path))
	if rel, err := filepath.Rel(s.St.Root, src); err != nil || strings.HasPrefix(rel, "..") {
		log.Printf("requests: video path escapes studio root in %s", name)
		s.archiveRequest(reqPath, name, "malformed")
		return
	}
	if _, err := os.Stat(src); err != nil {
		log.Printf("requests: video file missing for %s (%s) — leaving queued", name, req.Path)
		return // file may still be uploading through the bridge
	}
	res, err := video.Ingest(s.libDir(), src, video.Options{
		Slug: req.Slug, Tags: req.Tags, Source: filepath.Base(req.Path),
	})
	if err != nil {
		log.Printf("requests: video ingest %s failed: %v", name, err)
		s.archiveRequest(reqPath, name, "failed")
		return
	}
	os.Remove(src) // ingested — drop the inbox original
	log.Printf("requests: ingested video %s (%d bytes)", res.Asset.ID, res.Asset.Bytes)
	go s.pushVideo(res.Asset.Hash, res.Asset.File)
	s.archiveRequest(reqPath, name, "done")
	s.Broadcast("reload")
}
