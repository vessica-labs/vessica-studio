package server

import (
	"slices"
	"testing"
)

func TestPPTXChromeBudgetCoversDeckWideCapture(t *testing.T) {
	args := pptxChromeArgs(t.TempDir(), "http://127.0.0.1:4400/print")
	if !slices.Contains(args, "--virtual-time-budget=180000") {
		t.Fatalf("PPTX Chrome args use a shorter virtual-time budget: %#v", args)
	}
}
