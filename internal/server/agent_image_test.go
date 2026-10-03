package server

import (
	"encoding/base64"
	"fmt"
	"github.com/vessica-labs/vessica-studio/internal/library"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	if err != nil || !generated || calls != 1 {
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
	if err != nil || generated || calls != 1 {
		t.Fatal("replayed paid generation")
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
