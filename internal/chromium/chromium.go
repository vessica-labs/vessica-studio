// Package chromium resolves the optional browser binary used by visual
// exports, source critics, and chart migration tools.
package chromium

import (
	"os"
	"os/exec"
	"strings"
)

var appPaths = []string{
	"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
	"/Applications/Chromium.app/Contents/MacOS/Chromium",
	"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
}

// Find returns an explicit binary, VSTD_CHROMIUM, a PATH browser, or a
// standard macOS application install. It returns "" when none is available.
func Find(explicit string) string {
	if configured := strings.TrimSpace(explicit); configured != "" {
		return configured
	}
	if configured := strings.TrimSpace(os.Getenv("VSTD_CHROMIUM")); configured != "" {
		return configured
	}
	for _, candidate := range []string{"chromium", "chromium-browser", "google-chrome"} {
		if path, err := exec.LookPath(candidate); err == nil {
			return path
		}
	}
	for _, candidate := range appPaths {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}
