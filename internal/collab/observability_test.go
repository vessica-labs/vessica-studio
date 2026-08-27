package collab

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestNewObservabilityDashboardSerializesEmptyCollectionsAsArrays(t *testing.T) {
	dashboard := newObservabilityDashboard(30, time.Unix(0, 0).UTC(), 0)
	body, err := json.Marshal(dashboard)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(body)
	for _, field := range []string{"daily", "viewers", "presentations", "team", "errors", "openai"} {
		if !strings.Contains(encoded, `"`+field+`":[]`) {
			t.Fatalf("%s collection is not an empty JSON array: %s", field, encoded)
		}
	}
}
