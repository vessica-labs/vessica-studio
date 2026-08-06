package server

import "testing"

func TestNormalizeE164(t *testing.T) {
	cases := map[string]string{
		"+15551234567":     "+15551234567",
		"(415) 555-2671":   "+14155552671",
		"415.555.2671":     "+14155552671",
		"1-415-555-2671":   "+14155552671",
		"+1 415 555 2671":  "+14155552671",
		"+44 20 7946 0958": "+442079460958",
		"YOURCELL":         "",
		"":                 "",
		"12345":            "",
	}
	for in, want := range cases {
		if got := normalizeE164(in); got != want {
			t.Errorf("normalizeE164(%q) = %q, want %q", in, got, want)
		}
	}
}
