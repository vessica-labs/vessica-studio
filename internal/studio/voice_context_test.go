package studio

import (
	"strings"
	"testing"
)

func TestVoicePageMetadata(t *testing.T) {
	for _, tc := range []struct{ name, fragment, title string }{
		{"serif", `<section class="slide"><div class="serif">Scenario planning</div></section>`, "Scenario planning"},
		{"explicit title", `<section class="slide"><h2>Earlier subheading</h2><div data-slide-title>Actual title</div></section>`, "Actual title"},
		{"menu fallback", `<section class="slide" data-menu="Demo navigation"></section>`, "Demo navigation"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := voicePage("0010-example", tc.fragment, ""); got.Title != tc.title {
				t.Fatalf("title = %q, want %q", got.Title, tc.title)
			}
		})
	}
	md := "## Talk track\n" + strings.Repeat("Long narration. ", 100) + "\n## Intent\nFind accountability and decision rights.\n### Details\nOwnership matters.\n## Log\nPRIVATE EDIT HISTORY\n"
	got := voiceCompanionSummary(md)
	if !strings.Contains(got, "decision rights") || !strings.Contains(got, "Ownership") || strings.Contains(got, "PRIVATE EDIT HISTORY") {
		t.Fatalf("summary = %q", got)
	}
}
