package studio

import (
	"context"
	"encoding/json"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vessica-labs/vessica-studio/internal/chromium"
)

func TestPlayerRuntimeHighlightsAndCurrentMonthYear(t *testing.T) {
	browser := chromium.Find("")
	if browser == "" {
		t.Skip("Chrome/Chromium unavailable")
	}
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "studio.yaml"), "theme_default: default\n")
	writeFile(t, filepath.Join(root, "themes", "default", "theme.css"),
		":root{--sans:sans-serif}.slide{position:relative;width:1280px;height:720px}")
	writeFile(t, filepath.Join(root, "decks", "demo", "deck.yaml"),
		"title: Demo\ntheme: default\n")
	writeFile(t, filepath.Join(root, "decks", "demo", "slides", "0010-chart.html"), `<section class="slide">
  <div class="s-title">Never glow <b>this title</b></div>
  <div data-chart-group>
    <svg class="chart-art" role="img" aria-label="Quarterly revenue chart" viewBox="0 0 100 100">
      <title>Revenue rises across four quarters</title><path d="M0 90 L100 10"></path>
    </svg>
    <div class="chart-label" data-chart-label>Enterprise segment</div>
  </div>
  <div data-current-month-year>Fallback date</div>
</section>`)

	st, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	page, err := st.Build("demo")
	if err != nil {
		t.Fatal(err)
	}
	target := (&url.URL{Scheme: "file", Path: page}).String()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	raw, err := chromium.Evaluate(ctx, browser, target, `(()=>{
  if(!window.__vpres)return '';
  const listed=window.__vpres.highlightables();
  const titleResult=window.__vpres.run('highlight',{phrase:'this title'});
  const titleGlow=!!document.querySelector('.vglow');
  const chartResult=window.__vpres.run('highlight',{phrase:'Enterprise segment'});
  const glow=document.querySelector('.vglow');
  const expectedDate=new Intl.DateTimeFormat(undefined,{month:'long',year:'numeric'}).format(new Date());
  const dateText=document.querySelector('[data-current-month-year]').textContent;
  return JSON.stringify({listed,titleResult,titleGlow,chartResult,chartGroup:!!(glow&&glow.hasAttribute('data-chart-group')),dateText,expectedDate});
})()`)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Listed       string `json:"listed"`
		TitleResult  string `json:"titleResult"`
		TitleGlow    bool   `json:"titleGlow"`
		ChartResult  string `json:"chartResult"`
		ChartGroup   bool   `json:"chartGroup"`
		DateText     string `json:"dateText"`
		ExpectedDate string `json:"expectedDate"`
	}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got.Listed, "Never glow") || strings.Contains(got.Listed, "this title") {
		t.Fatalf("title leaked into highlightable phrases: %s", got.Listed)
	}
	for _, want := range []string{"Quarterly revenue chart", "Revenue rises across four quarters", "Enterprise segment"} {
		if !strings.Contains(got.Listed, want) {
			t.Errorf("chart highlightable phrases missing %q: %s", want, got.Listed)
		}
	}
	if got.TitleResult != "no matching element" || got.TitleGlow {
		t.Error("title was highlightable")
	}
	if got.ChartResult != "highlighted" || !got.ChartGroup {
		t.Error("chart label did not highlight the chart group")
	}
	if got.DateText != got.ExpectedDate || got.DateText == "Fallback date" {
		t.Errorf("dynamic month/year = %q, want %q", got.DateText, got.ExpectedDate)
	}
}
