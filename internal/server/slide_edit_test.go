package server

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/vessica-labs/vessica-studio/internal/library"
)

func TestDeleteSlideUpdatesSnapshotAndRejectsStaleDelete(t *testing.T) {
	st := testStudio(t)
	if err := st.NewSlide("demo", "0020-b", "Second", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.MoveSlide("demo", "0020-b", ""); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(st.LinkPath("demo", "0010-a"), []byte("source_slide: 0020-b\n"), 0644); err != nil {
		t.Fatal(err)
	}
	path := "/api/deck/demo/slide/0010-a"
	in := EditorTransformInput{Root: st.Root, Delta: true, Deck: "demo", Method: "DELETE", Path: path, Headers: map[string]string{"if-match": "\"old\""}}
	got, err := TransformEditor(context.Background(), in)
	if err != nil || got.Status != 422 || len(got.Deleted) != 0 {
		t.Fatalf("stale delete: %+v %v", got, err)
	}
	in.Headers["if-match"] = "\"" + st.HashSlide("demo", "0010-a") + "\""
	got, err = TransformEditor(context.Background(), in)
	want := []string{"decks/demo/slides/0010-a.html", "decks/demo/slides/0010-a.md"}
	if err != nil || got.Status != 200 || !slices.Equal(got.Deleted, want) || len(got.Files) != 1 ||
		got.Files[0].Path != "decks/demo/deck.yaml" || strings.Contains(string(got.Files[0].Content), "0010-a") {
		t.Fatalf("delete delta: %+v %v", got, err)
	}
	request := func(mode Mode, method, path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		New(st, mode).Routes().ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		return w
	}
	if w := request(ModePresent, "DELETE", path, ""); w.Code != http.StatusForbidden {
		t.Fatalf("present-mode delete: %d", w.Code)
	}
	for range 2 {
		if w := request(ModeStudio, "DELETE", path, ""); w.Code != 200 {
			t.Fatalf("delete or replay: %d %s", w.Code, w.Body)
		}
	}
	if _, err := os.Stat(st.LinkPath("demo", "0010-a")); !os.IsNotExist(err) {
		t.Fatalf("linked slide metadata remains: %v", err)
	}
	if w := request(ModeStudio, "DELETE", "/api/deck/demo/slide/0020-b", ""); w.Code != 422 {
		t.Fatalf("last slide deletion: %d", w.Code)
	}
	if w := request(ModeStudio, "PUT", path+"/fragment", `<section class="slide">Old edit</section>`); w.Code != http.StatusGone {
		t.Fatalf("stale fragment write: %d", w.Code)
	}
}

func TestEditorImageUploadReturnsAssetAndSnapshotDelta(t *testing.T) {
	st := testStudio(t)
	var imageBytes bytes.Buffer
	if err := png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 30, 20))); err != nil {
		t.Fatal(err)
	}
	in := EditorTransformInput{Root: st.Root, Delta: true, Deck: "demo", Method: "POST",
		Path: "/api/asset/image?deck=demo&filename=paste.png", Body: imageBytes.Bytes()}
	got, err := TransformEditor(context.Background(), in)
	if err != nil || got.Status != 200 || len(got.Files) != 2 {
		t.Fatalf("image upload: %+v %v", got, err)
	}
	var payload struct{ Asset library.Asset }
	if err := json.Unmarshal(got.Body, &payload); err != nil || payload.Asset.Size != "30x20" {
		t.Fatalf("image metadata: %+v %v", payload, err)
	}
	if got.Files[0].Path != "library/"+payload.Asset.File || !bytes.Equal(got.Files[0].Content, imageBytes.Bytes()) || got.Files[1].Path != "library/manifest.json" {
		t.Fatal("upload delta did not contain the image and manifest")
	}
	in.Path = "/api/asset/image?deck=foreign&filename=paste.png"
	if got, err := TransformEditor(context.Background(), in); err != nil || got.Status != 404 {
		t.Fatalf("foreign deck upload: %d %v", got.Status, err)
	}
	in.Path = "/api/asset/image?deck=demo&filename=paste.jpg"
	if got, err := TransformEditor(context.Background(), in); err != nil || got.Status != 400 || len(got.Files) != 0 {
		t.Fatalf("invalid image accepted: %d %v", got.Status, err)
	}
}
