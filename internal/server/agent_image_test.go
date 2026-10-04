package server

import (
	"encoding/base64"
	"fmt"
	"github.com/vessica-labs/vessica-studio/internal/library"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelectedImageProcessesOneOwnedRequestAndRegistersAsset(t *testing.T) {
	st := testStudio(t)
	s := New(st, ModeStudio)
	t.Setenv("VSTD_OPENAI_KEY", "test-capability")
	calls := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/images/generations" || r.Header.Get("Authorization") != "Bearer test-capability" {
			t.Error("unexpected image authority")
		}
		fmt.Fprintf(w, `{"data":[{"b64_json":"%s"}]}`, base64.StdEncoding.EncodeToString([]byte("image fixture")))
	}))
	defer api.Close()
	for name, body := range map[string]string{"owned.yaml": "deck: demo\nslide: 0010-a\nprompt: new background\nslug: colorful-cover\n", "foreign.yaml": "deck: another\nslide: 0010-b\nprompt: never dispatch\n"} {
		if err := os.WriteFile(filepath.Join(st.Root, "requests", name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	generated, err := s.ProcessSelectedImage("demo", "0010-a", api.URL, "gpt-image-1")
	if err != nil || generated == nil || calls != 1 {
		t.Fatalf("generated=%v calls=%d err=%v", generated, calls, err)
	}
	manifest, err := library.Load(filepath.Join(st.Root, "library"))
	if err != nil || len(manifest.Assets) != 1 || manifest.Assets[0].Prompt != "new background" {
		t.Fatalf("registration: %+v %v", manifest, err)
	}
	if _, err := os.Stat(filepath.Join(st.Root, "requests", "foreign.yaml")); err != nil {
		t.Fatal("foreign request altered")
	}
	generated, err = s.ProcessSelectedImage("demo", "0010-a", api.URL, "gpt-image-1")
	if err != nil || generated != nil || calls != 1 {
		t.Fatal("replayed paid generation")
	}
}

func TestImagePlanningAndLandingHaveDistinctOpeningRequests(t *testing.T) {
	st := testStudio(t)
	companion := st.SlidePath("demo", "0010-a", ".md")
	if err := os.WriteFile(companion, []byte("# Cover\n\n## Edit requests\n- create a new background\n\n## Log\n"), 0644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "claude")
	script := `#!/bin/sh
for arg do last="$arg"; done
printf '%s' "$last" > "$VSTD_TEST_OPENING_REQUEST"
if [ "$VSTD_TEST_IMAGE_STAGE" = planning ]; then
cat > decks/demo/slides/0010-a.md <<'EOF'
# Cover

## Edit requests
- awaiting imagery: place the new background

## Log
EOF
cat > requests/cover.yaml <<'EOF'
deck: demo
slide: 0010-a
prompt: colorful cover
slug: cover
EOF
else
printf '<section class="slide" style="background:url(%s)"></section>\n' "$VSTD_TEST_IMAGE_URL" > decks/demo/slides/0010-a.html
cat > decks/demo/slides/0010-a.md <<'EOF'
# Cover

## Edit requests

## Log
- resolved: placed the generated background
EOF
fi
`
	if err := os.WriteFile(bin, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VSTD_AGENT_CMD", bin)
	t.Setenv("VSTD_AGENT_SANDBOX", "")
	t.Setenv("VSTD_OPENAI_KEY", "fixture-capability")
	s := New(st, ModeStudio)
	opening := filepath.Join(t.TempDir(), "opening.txt")
	t.Setenv("VSTD_TEST_OPENING_REQUEST", opening)
	t.Setenv("VSTD_TEST_IMAGE_STAGE", "planning")
	if s.RunAgentSelectedIsolated("demo", "0010-a") != 1 {
		t.Fatal("planning pass did not run")
	}
	planning, err := os.ReadFile(opening)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		fmt.Fprintf(w, `{"data":[{"b64_json":"%s"}]}`, base64.StdEncoding.EncodeToString([]byte("image fixture")))
	}))
	defer api.Close()
	generated, err := s.ProcessSelectedImage("demo", "0010-a", api.URL, "gpt-image-1")
	if err != nil || generated == nil {
		t.Fatalf("image generation: %v %v", generated, err)
	}
	t.Setenv("VSTD_TEST_IMAGE_STAGE", "landing")
	t.Setenv("VSTD_TEST_IMAGE_URL", "/library/"+generated.File)
	if n, err := s.RunAgentSelectedImageLanding("demo", "0010-a", generated, true); n != 1 || err != nil {
		t.Fatalf("landing pass: n=%d err=%v", n, err)
	}
	landing, err := os.ReadFile(opening)
	if err != nil {
		t.Fatal(err)
	}
	if string(planning) == string(landing) {
		t.Fatal("landing opening request repeats the planning request")
	}
	for _, request := range []string{string(planning), string(landing)} {
		if !strings.Contains(request, "Selected companion checkpoint: sha256:") || !strings.Contains(request, `deck "demo", slide "0010-a"`) || !strings.Contains(request, "This job workspace is already isolated") {
			t.Fatal("request lost its companion checkpoint or selected scope")
		}
	}
	if !strings.Contains(string(landing), generated.ID) || !strings.Contains(string(landing), "/library/"+generated.File) || !strings.Contains(string(landing), generated.Hash) || strings.Contains(string(planning), "GENERATED IMAGE RECEIPT") {
		t.Fatal("landing did not receive the exact generated asset identity")
	}
	if calls != 1 || s.RunAgentSelectedIsolated("demo", "0010-a") != 0 {
		t.Fatal("completed image work was replayed")
	}
}

func TestSelectedImageRefusesMultipleRequestsBeforeProviderDispatch(t *testing.T) {
	st := testStudio(t)
	s := New(st, ModeStudio)
	t.Setenv("VSTD_OPENAI_KEY", "test-capability")
	for _, name := range []string{"a.yaml", "b.yaml"} {
		if err := os.WriteFile(filepath.Join(st.Root, "requests", name), []byte("deck: demo\nslide: 0010-a\nprompt: background\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.ProcessSelectedImage("demo", "0010-a", "http://127.0.0.1:1", "gpt-image-1"); err == nil {
		t.Fatal("accepted multiple images")
	}
}

func TestImageLandingRejectsReusedAssetWithoutRegenerating(t *testing.T) {
	st := testStudio(t)
	s := New(st, ModeStudio)
	t.Setenv("VSTD_OPENAI_KEY", "fixture-capability")
	calls := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		fmt.Fprintf(w, `{"data":[{"b64_json":"%s"}]}`, base64.StdEncoding.EncodeToString([]byte("new image")))
	}))
	defer api.Close()
	writeTestFile(t, filepath.Join(st.Root, "requests/new.yaml"), "deck: demo\nslide: 0010-a\nprompt: new ensemble\n")
	asset, err := s.ProcessSelectedImage("demo", "0010-a", api.URL, "gpt-image-1")
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, st.SlidePath("demo", "0010-a", ".md"), "# Cover\n\n## Edit requests\n- awaiting imagery: place new ensemble\n\n## Log\n")
	bin := filepath.Join(t.TempDir(), "claude")
	writeTestFile(t, bin, `#!/bin/sh
cat > decks/demo/slides/0010-a.md <<'EOF'
# Cover

## Edit requests

## Log
- resolved: claimed new image placement
EOF
`)
	if err := os.Chmod(bin, 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VSTD_AGENT_CMD", bin)
	t.Setenv("VSTD_AGENT_SANDBOX", "")
	// A mention in a comment cannot stand in for a rendered image reference.
	writeTestFile(t, st.SlidePath("demo", "0010-a", ".html"), `<section class="slide" style="background:url(/library/img/old.png)"></section><!-- `+"/library/"+asset.File+` -->`)
	if _, err := s.RunAgentSelectedImageLanding("demo", "0010-a", asset, true); err == nil {
		t.Fatal("accepted a cleared request with the previous image still on the slide")
	}
	if calls != 1 {
		t.Fatal("landing replayed image generation")
	}
}
