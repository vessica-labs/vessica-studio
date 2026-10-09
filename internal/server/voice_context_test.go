package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/vessica-labs/vessica-studio/internal/studio"
)

func TestVoicePresentationContext(t *testing.T) {
	st := testStudio(t)
	files := map[string]string{
		"0010-a.html":        `<section class="slide"><h1>Starting point</h1><script>secretScript()</script></section>`,
		"0010-a.md":          "# Starting point\n\n## Intent\nExplain the workforce baseline.\n\n## Log\nDo not use this as the summary.\n",
		"0020-parked.md":     "",
		"0040-fallback.md":   "",
		"0020-parked.html":   `<section class="slide" data-parked><h1>Unused idea</h1></section>`,
		"0030-topic.html":    `<section class="slide" data-hidden><h1>Operating &amp; governance</h1><p>Visible fallback.</p></section>`,
		"0030-topic.md":      "---\nowner: Matt\n---\n# Operating model\n\n## Intent\nExplain agent accountability and decision rights.\n\n## Talk track\nPRIVATE COMPANION: Delegation needs clear ownership.\n" + strings.Repeat("Narrative beyond the old 600 character limit. ", 50),
		"0040-fallback.html": `<section class="slide"><h2>Costs</h2><style>.x{}</style><p>Token spend and unit economics.</p></section>`,
	}
	for name, body := range files {
		if err := os.WriteFile(st.SlidePath("demo", strings.TrimSuffix(strings.TrimSuffix(name, ".html"), ".md"), name[strings.LastIndex(name, "."):]), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := New(st, ModePublic)
	s.secret = "test-secret"
	s.allowed = map[string]bool{"matt-kropp": true}
	h := s.Routes()
	target := "/api/deck/demo/voice-context?slide=0030-topic"
	denied := httptest.NewRecorder()
	h.ServeHTTP(denied, httptest.NewRequest(http.MethodGet, target, nil))
	if denied.Code != http.StatusForbidden || strings.Contains(denied.Body.String(), "PRIVATE COMPANION") {
		t.Fatalf("anonymous context: %d %s", denied.Code, denied.Body.String())
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, presenterRequest(s, http.MethodGet, target, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("context: %d %s", rr.Code, rr.Body.String())
	}
	var got struct {
		Current struct {
			ID, Companion string
			Page          int
		} `json:"current"`
		Pages []struct {
			ID, Title, Summary string
			Page               int
			Hidden, Parked     bool
		} `json:"pages"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Current.ID != "0030-topic" || got.Current.Page != 2 || got.Current.Companion != files["0030-topic.md"] {
		t.Fatalf("wrong current context: %+v", got.Current)
	}
	if len(got.Pages) != 4 || !got.Pages[1].Parked || got.Pages[1].Page != 0 || !got.Pages[2].Hidden || got.Pages[3].Page != 3 {
		t.Fatalf("wrong page numbering: %+v", got.Pages)
	}
	if got.Pages[2].Title != "Operating & governance" || !strings.Contains(got.Pages[2].Summary, "decision rights") || strings.Contains(got.Pages[0].Summary, "Do not use") || !strings.Contains(got.Pages[3].Summary, "unit economics") {
		t.Fatalf("wrong TOC: %+v", got.Pages)
	}
	if rr.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("presenter notes must not be cached")
	}
	missing := httptest.NewRecorder()
	h.ServeHTTP(missing, presenterRequest(s, http.MethodGet, "/api/deck/demo/voice-context?slide=missing", ""))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing slide: %d", missing.Code)
	}
	// The Cloud transform must expose this read only within its selected deck.
	snapshot, err := studio.CloudContent(st.Root)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{"GET", target, 200}, {"GET", "/api/deck/other/voice-context?slide=0030-topic", 404}, {"POST", target, 404},
	} {
		result, err := TransformEditor(context.Background(), EditorTransformInput{Deck: "demo", Files: snapshot.Files, Method: tc.method, Path: tc.path})
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != tc.want {
			t.Fatalf("transform %s %s: %d %s", tc.method, tc.path, result.Status, result.Body)
		}
	}
	build, err := st.Build("demo")
	if err != nil {
		t.Fatal(err)
	}
	public, err := os.ReadFile(build)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(public), "PRIVATE COMPANION") {
		t.Fatal("private companion leaked into public build")
	}
}
