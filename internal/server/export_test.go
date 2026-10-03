package server

import (
	"strings"
	"testing"
)

func TestPPTXCaptureReportsBrowserScriptErrors(t *testing.T) {
	_, err := parsePPTXCapture([]byte(`<html><body><pre id="vstd-pptx-error">image decode &amp; capture failed</pre></body></html>`))
	if err == nil || !strings.Contains(err.Error(), "image decode & capture failed") {
		t.Fatalf("capture error: %v", err)
	}
}
