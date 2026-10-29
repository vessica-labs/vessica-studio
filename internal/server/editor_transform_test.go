package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vessica-labs/vessica-studio/internal/library"
	"github.com/vessica-labs/vessica-studio/internal/studio"
)

func TestEditorTransformRenderAndEdit(t *testing.T) {
	st := testStudio(t)
	snapshot, err := studio.CloudContent(st.Root)
	if err != nil {
		t.Fatal(err)
	}
	input := EditorTransformInput{Deck: "demo", Files: snapshot.Files, Method: "GET", Path: "/d/demo/"}
	rendered, err := TransformEditor(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if rendered.Status != 200 || !strings.Contains(string(rendered.Body), `id="editRibbon"`) {
		t.Fatalf("render: %d", rendered.Status)
	}
	input.Method = "PUT"
	input.Path = "/api/deck/demo/slide/0010-a/fragment"
	input.Body = []byte(`<section class="slide"><h1>Direct cloud edit</h1></section>`)
	edited, err := TransformEditor(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if edited.Status != 200 {
		t.Fatalf("edit: %d %s", edited.Status, edited.Body)
	}
	found := false
	for _, f := range edited.Files {
		if f.Path == "decks/demo/slides/0010-a.html" {
			found = strings.Contains(string(f.Content), "Direct cloud edit")
		}
	}
	if !found {
		t.Fatal("result did not preserve edited snapshot")
	}
	encoded, err := json.Marshal(edited)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Files []struct {
			Path    string `json:"path"`
			Content []byte `json:"content"`
		}
	}
	if err := json.Unmarshal(encoded, &wire); err != nil || len(wire.Files) != len(edited.Files) {
		t.Fatal("invalid wire snapshot")
	}
	// A second transform uses only the returned snapshot; no process or worktree
	// state is necessary to render a durable edit after a restart.
	input.Files = edited.Files
	input.Method, input.Path, input.Body = "GET", "/d/demo/", nil
	reopened, err := TransformEditor(context.Background(), input)
	if err != nil || !strings.Contains(string(reopened.Body), "Direct cloud edit") {
		t.Fatal("reopen lost edit", err)
	}
	for _, p := range []string{"/api/deck/demo/export.pdf", "/api/asset/video", "/api/realtime/token", "/api/deck/other/slide/a", "/library/../../etc/passwd", "/api/deck/demo/slide/0010-a/refresh-link"} {
		input.Path = p
		for _, method := range []string{"GET", "POST", "PUT"} {
			input.Method = method
			if got, err := TransformEditor(context.Background(), input); err == nil && got.Status != 404 {
				t.Fatalf("allowed execution or unsafe route %s", p)
			}
		}
	}
}

func TestEditorTransformAllowsReadOnlyVideoPlayback(t *testing.T) {
	if !transformRoute("GET", "/assets/video/vstd-upload-850815525", "demo") {
		t.Fatal("editor transform rejected the engine video route")
	}
	for _, method := range []string{"POST", "PUT", "DELETE"} {
		if transformRoute(method, "/assets/video/vstd-upload-850815525", "demo") {
			t.Fatalf("editor transform allowed %s video route", method)
		}
	}
}

func TestEditorTransformServesMaterializedFileBackedVideo(t *testing.T) {
	st := testStudio(t)
	videoBytes := []byte("materialized cloud video")
	digest := fmt.Sprintf("%x", sha256.Sum256(videoBytes))
	manifest := &library.Manifest{Version: 1, StyleFamilies: map[string]library.StyleFamily{}, Videos: []library.VideoAsset{{
		ID: "cloud-video", File: "video/" + digest + ".mp4", Hash: digest, Bytes: int64(len(videoBytes)), Poster: "img/cloud-video.jpg",
	}}}
	if err := manifest.Save(filepath.Join(st.Root, "library")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(st.Root, "library", "video"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.Root, "library", "video", digest+".mp4"), videoBytes, 0600); err != nil {
		t.Fatal(err)
	}
	in := EditorTransformInput{Root: st.Root, Delta: true, Deck: "demo", Method: "GET", Path: "/assets/video/cloud-video", Headers: map[string]string{"range": "bytes=0-10"}}
	result, err := TransformEditor(context.Background(), in)
	if err != nil || result.Status != 206 || string(result.Body) != string(videoBytes[:11]) || result.Headers["content-range"] != "bytes 0-10/24" {
		t.Fatalf("video response: status=%d headers=%v body=%q err=%v", result.Status, result.Headers, result.Body, err)
	}
}

func TestEditorTransformServesMaterializedFileBackedBundle(t *testing.T) {
	st := testStudio(t)
	content := []byte("materialized cloud bundle")
	digest := fmt.Sprintf("%x", sha256.Sum256(content))
	manifest := &library.Manifest{Version: 1, Bundles: []library.BundleAsset{{ID: "flight", File: "bundle/" + digest + ".zip", Hash: digest, Bytes: int64(len(content)), Entrypoint: "index.html", ExpandedBytes: int64(len(content)), FileCount: 1}}}
	if err := manifest.Save(filepath.Join(st.Root, "library")); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(st.Root, "library", "bundle", digest+".zip")
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, content, 0600); err != nil {
		t.Fatal(err)
	}
	in := EditorTransformInput{Root: st.Root, Delta: true, PresentationOnly: true, Deck: "demo", Method: "HEAD", Path: "/assets/bundle/flight"}
	result, err := TransformEditor(context.Background(), in)
	if err != nil || result.Status != 200 || len(result.Body) != 0 || result.Headers["content-length"] != fmt.Sprint(len(content)) {
		t.Fatalf("HEAD: %+v %v", result, err)
	}
	wire, err := json.Marshal(result)
	if err != nil || !strings.Contains(string(wire), `"body":""`) {
		t.Fatalf("HEAD must retain the base64 body contract: %s %v", wire, err)
	}
	in.Method, in.Headers = "GET", map[string]string{"range": "bytes=0-10"}
	result, err = TransformEditor(context.Background(), in)
	if err != nil || result.Status != 206 || string(result.Body) != string(content[:11]) {
		t.Fatalf("GET: %+v %v", result, err)
	}
	for _, f := range result.Files {
		if strings.HasPrefix(f.Path, "library/bundle/") {
			t.Fatal("bundle leaked into snapshot")
		}
	}
	in.Path = "/assets/bundle/unregistered"
	result, err = TransformEditor(context.Background(), in)
	if err != nil || result.Status != 404 {
		t.Fatalf("unregistered bundle: %+v %v", result, err)
	}
	in.Path = "/assets/bundle/flight"
	if err := os.WriteFile(file, []byte(strings.Repeat("x", len(content))), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := TransformEditor(context.Background(), in); err == nil {
		t.Fatal("corrupt bundle accepted")
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(st.Root, "studio.yaml"), file); err != nil {
		t.Fatal(err)
	}
	if _, err := TransformEditor(context.Background(), in); err == nil {
		t.Fatal("symlink bundle accepted")
	}
}

func TestEditorTransformFileBackedDelta(t *testing.T) {
	st := testStudio(t)
	in := EditorTransformInput{Root: st.Root, Delta: true, Deck: "demo", Method: "GET", Path: "/api/me"}
	result, err := TransformEditor(context.Background(), in)
	if err != nil || result.Status != 200 || !result.Delta || len(result.Files) != 0 {
		t.Fatalf("read delta: %+v %v", result, err)
	}
	in.Method, in.Path = "PUT", "/api/deck/demo/slide/0010-a/fragment"
	in.Body = []byte(`<section class="slide"><h1>Changed delta</h1></section>`)
	result, err = TransformEditor(context.Background(), in)
	if err != nil || result.Status != 200 || !result.Delta || len(result.Files) == 0 {
		t.Fatalf("write delta: %+v %v", result, err)
	}
	for _, file := range result.Files {
		if !strings.HasPrefix(file.Path, "decks/demo/") {
			t.Fatalf("unchanged file in delta: %s", file.Path)
		}
	}
	original, err := studio.CloudContent(st.Root)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range original.Files {
		if strings.Contains(string(f.Content), "Changed delta") {
			t.Fatal("read-only input root mutated")
		}
	}
	in.Files = original.Files
	if _, err = TransformEditor(context.Background(), in); err == nil {
		t.Fatal("ambiguous root accepted")
	}
}

func TestEditorTransformLargeFileBackedDeck(t *testing.T) {
	st := testStudio(t)
	if err := os.MkdirAll(filepath.Join(st.Root, "library", "img"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b", "c", "d"} {
		if err := os.WriteFile(filepath.Join(st.Root, "library", "img", name+".png"), make([]byte, 11<<20), 0600); err != nil {
			t.Fatal(err)
		}
	}
	in := EditorTransformInput{Root: st.Root, Delta: true, Deck: "demo", Method: "GET", Path: "/d/demo/"}
	rendered, err := TransformEditor(context.Background(), in)
	if err != nil || rendered.Status != 200 || len(rendered.Files) != 0 || !strings.Contains(string(rendered.Body), `id="editRibbon"`) {
		t.Fatalf("large render: status=%d files=%d err=%v", rendered.Status, len(rendered.Files), err)
	}
	in.Method, in.Path = "PUT", "/api/deck/demo/slide/0010-a/fragment"
	in.Body = []byte(`<section class="slide"><h1>Large deck edit</h1></section>`)
	edited, err := TransformEditor(context.Background(), in)
	if err != nil || edited.Status != 200 || len(edited.Files) == 0 {
		t.Fatalf("large edit: status=%d files=%d err=%v", edited.Status, len(edited.Files), err)
	}
	for _, f := range edited.Files {
		if strings.HasPrefix(f.Path, "library/") {
			t.Fatalf("unchanged media copied to delta: %s", f.Path)
		}
	}
}

func TestEditorTransformCloudAudience(t *testing.T) {
	st := testStudio(t)
	snapshot, err := studio.CloudContent(st.Root)
	if err != nil {
		t.Fatal(err)
	}
	link := "https://studio.example/s/ABCD2345"
	input := EditorTransformInput{Deck: "demo", Files: snapshot.Files, Method: "GET", Path: "/d/demo/", AudienceURL: &link}
	result, err := TransformEditor(context.Background(), input)
	if err != nil || result.Status != 200 || !strings.Contains(string(result.Body), `"audience_url":"https://studio.example/s/ABCD2345"`) {
		t.Fatalf("render: %v %d", err, result.Status)
	}
	input.Path = "/api/deck/demo/share-qr.png?ttl=168"
	result, err = TransformEditor(context.Background(), input)
	if err != nil || result.Status != 200 || result.Headers["content-type"] != "image/png" {
		t.Fatalf("QR: %v %+v", err, result.Headers)
	}
	link = ""
	result, err = TransformEditor(context.Background(), input)
	if err != nil || result.Status != 404 {
		t.Fatalf("disabled QR: %v %d", err, result.Status)
	}
	link = "javascript:alert(1)"
	if _, err = TransformEditor(context.Background(), input); err == nil {
		t.Fatal("unsafe URL accepted")
	}
}

func TestEditorDeliveryURLsNeverPersistIntoSource(t *testing.T) {
	st := testStudio(t)
	snapshot, err := studio.CloudContent(st.Root)
	if err != nil {
		t.Fatal(err)
	}
	destination := "https://assets.example/protected/scope/digest/photo.png"
	input := EditorTransformInput{Deck: "demo", Files: snapshot.Files, Method: "PUT", Path: "/api/deck/demo/slide/0010-a/fragment", AssetURLs: map[string]string{"/library/img/photo.png": destination}, AssetSessionURL: "https://assets.example/protected/scope/_session.gif", Body: []byte(`<section class="slide"><img src="` + destination + `"></section>`)}
	edited, err := TransformEditor(context.Background(), input)
	if err != nil || edited.Status != 200 {
		t.Fatalf("save: %v %d", err, edited.Status)
	}
	for _, file := range edited.Files {
		if file.Path == "decks/demo/slides/0010-a.html" {
			if strings.Contains(string(file.Content), "assets.example") || !strings.Contains(string(file.Content), "/library/img/photo.png") {
				t.Fatal("delivery identity persisted in source")
			}
		}
	}
	input.Files = edited.Files
	input.Method = "GET"
	input.Path = "/d/demo/"
	input.Body = nil
	rendered, err := TransformEditor(context.Background(), input)
	if err != nil || !strings.Contains(string(rendered.Body), `src="`+destination+`"`) {
		t.Fatal("missing direct URL", err)
	}
	if !strings.Contains(string(rendered.Body), "_session.gif") {
		t.Fatal("missing session renewal")
	}
	input.AssetURLs["/library/img/photo.png"] = "javascript:alert(1)"
	if _, err = TransformEditor(context.Background(), input); err == nil {
		t.Fatal("unsafe delivery URL accepted")
	}
}

func TestPresentationTransformRejectsWrites(t *testing.T) {
	st := testStudio(t)
	snapshot, err := studio.CloudContent(st.Root)
	if err != nil {
		t.Fatal(err)
	}
	in := EditorTransformInput{Deck: "demo", Files: snapshot.Files, PresentationOnly: true, Method: "GET", Path: "/api/me"}
	result, err := TransformEditor(context.Background(), in)
	if err != nil || result.Status != 200 {
		t.Fatal(result.Status, err)
	}
	var me map[string]any
	if err = json.Unmarshal(result.Body, &me); err != nil {
		t.Fatal(err)
	}
	if me["editable"] != false || me["presenter"] != true || me["mode"] != "present" {
		t.Fatal(me)
	}
	for _, route := range []struct{ method, path string }{
		{"PUT", "/api/deck/demo/slide/0010-a/fragment"},
		{"PUT", "/api/deck/demo/slide/0010-a/companion"},
		{"POST", "/api/deck/demo/slides"},
		{"POST", "/api/deck/demo/slide/0010-a/move"},
	} {
		in.Method, in.Path = route.method, route.path
		got, err := TransformEditor(context.Background(), in)
		if err != nil || got.Status != 404 {
			t.Fatalf("write allowed %s: %d %v", route.path, got.Status, err)
		}
	}
	in.Method, in.Path = "GET", "/api/deck/demo/voice-context?slide=0010-a"
	got, err := TransformEditor(context.Background(), in)
	if err != nil || got.Status != 200 {
		t.Fatalf("voice context: %d %v", got.Status, err)
	}
}
