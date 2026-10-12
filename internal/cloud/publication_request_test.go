package cloud

import (
	"encoding/json"
	"testing"
)

func TestPublicationVisibilityIsExplicitAndOptional(t *testing.T) {
	for _, tc := range []struct {
		request PublicationRequest
		want    string
	}{
		{PublicationRequest{RevisionID: "rev"}, `{"revision_id":"rev"}`},
		{PublicationRequest{RevisionID: "rev", Visibility: "public"}, `{"visibility":"public","revision_id":"rev"}`},
	} {
		b, err := json.Marshal(tc.request)
		if err != nil || string(b) != tc.want {
			t.Fatalf("publication request: %s, %v", b, err)
		}
	}
}
