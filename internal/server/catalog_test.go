package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vessica-labs/vessica-studio/internal/studio"
)

func TestLocalSlideTransferIntentIsBoundSingleUseAndOrdered(t *testing.T) {
	t.Setenv("VSTD_USER_CONFIG_DIR", t.TempDir())
	st := testStudio(t)
	if err := st.NewDeck("target", "Target"); err != nil {
		t.Fatal(err)
	}
	s := New(st, ModeStudio)
	h := s.Routes()
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/app/decks", nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"storage_key":"demo"`) {
		t.Fatalf("compatible deck list status=%d body=%s", rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/player/deck/demo/transfer-intent", strings.NewReader(`{"slide_ids":["0010-a"]}`)))
	if rr.Code != http.StatusOK {
		t.Fatalf("intent status=%d body=%s", rr.Code, rr.Body.String())
	}
	var opened struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &opened); err != nil {
		t.Fatal(err)
	}
	token := strings.TrimPrefix(opened.URL, "/presentations#transfer=")
	if token == opened.URL || token == "" {
		t.Fatalf("intent URL = %q", opened.URL)
	}
	body := `{"token":"` + token + `"}`
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/app/slide-transfer-intents/exchange", strings.NewReader(body)))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"source_deck_id":"demo"`) {
		t.Fatalf("exchange status=%d body=%s", rr.Code, rr.Body.String())
	}
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/app/slide-transfer-intents/exchange", strings.NewReader(body)))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("replay status=%d body=%s", rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/app/slide-transfers", strings.NewReader(`{"source_deck_id":"demo","target_deck_id":"target","slide_ids":["0010-a"],"mode":"copy"}`)))
	if rr.Code != http.StatusCreated {
		t.Fatalf("transfer status=%d body=%s", rr.Code, rr.Body.String())
	}
	ids, err := st.SlideIDs("target")
	if err != nil || len(ids) != 2 || ids[1] != "0020-a" {
		t.Fatalf("target ids=%v err=%v", ids, err)
	}
}

func TestLinkedSlideRejectsEveryContentWriteButAllowsReorder(t *testing.T) {
	st := testStudio(t)
	if err := st.NewDeck("target", "Target"); err != nil {
		t.Fatal(err)
	}
	result, err := st.TransferSlides(studio.SlideTransferRequest{SourceDeck: "demo", TargetDeck: "target", SlideIDs: []string{"0010-a"}, Mode: "link"})
	if err != nil {
		t.Fatal(err)
	}
	id := result.SlideIDs[0]
	s := New(st, ModeStudio)
	h := s.Routes()
	writes := []struct {
		method, path, body string
	}{
		{http.MethodPut, "/api/deck/target/slide/" + id + "/fragment", `<section class="slide">changed</section>`},
		{http.MethodPut, "/api/deck/target/slide/" + id + "/companion", "changed"},
		{http.MethodPut, "/api/deck/target/slide/" + id + "/companion/Intent", "changed"},
		{http.MethodPut, "/api/deck/target/slide/" + id + "/title", "changed"},
		{http.MethodPost, "/api/deck/target/slide/" + id + "/attachment?filename=evidence.pdf", "pdf"},
	}
	for _, tc := range writes {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)))
		if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "detach") {
			t.Fatalf("%s %s status=%d body=%s", tc.method, tc.path, rr.Code, rr.Body.String())
		}
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/deck/target/slide/"+id+"/move", strings.NewReader(`{"After":""}`)))
	if rr.Code != http.StatusOK {
		t.Fatalf("reorder status=%d body=%s", rr.Code, rr.Body.String())
	}
}
