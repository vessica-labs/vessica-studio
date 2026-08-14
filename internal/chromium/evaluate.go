package chromium

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// Evaluate launches an isolated headless browser, navigates to target, and
// polls expression until it returns a non-empty string. DevTools is used
// instead of --dump-dom because some macOS Chrome builds keep dump-dom alive
// indefinitely after the DOM is ready.
func Evaluate(ctx context.Context, binary, target, expression string) (string, error) {
	profile, err := os.MkdirTemp("", "vstd-chrome-profile-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(profile)
	var diagnostics bytes.Buffer
	cmd := exec.CommandContext(ctx, binary,
		"--headless=new", "--no-sandbox", "--disable-gpu", "--disable-background-networking",
		"--no-first-run", "--no-default-browser-check", "--remote-debugging-port=0",
		"--user-data-dir="+profile, target)
	cmd.Stdout = &diagnostics
	cmd.Stderr = &diagnostics
	if err := cmd.Start(); err != nil {
		return "", err
	}
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()

	port, err := waitForDevTools(ctx, profile, cmd)
	if err != nil {
		return "", fmt.Errorf("start Chrome DevTools: %w: %s", err, strings.TrimSpace(diagnostics.String()))
	}
	wsURL, err := waitForPage(ctx, port, target)
	if err != nil {
		return "", err
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return "", fmt.Errorf("connect Chrome DevTools: %w", err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetReadDeadline(deadline)
		_ = conn.SetWriteDeadline(deadline)
	}

	for id := 1; ; id++ {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}
		msg := map[string]any{"id": id, "method": "Runtime.evaluate", "params": map[string]any{
			"expression": expression, "returnByValue": true, "awaitPromise": true,
		}}
		if err := conn.WriteJSON(msg); err != nil {
			return "", err
		}
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return "", err
			}
			var response struct {
				ID     int `json:"id"`
				Result struct {
					Result struct {
						Type        string `json:"type"`
						Value       any    `json:"value"`
						Description string `json:"description"`
					} `json:"result"`
					Exception any `json:"exceptionDetails"`
				} `json:"result"`
			}
			if json.Unmarshal(data, &response) != nil || response.ID != id {
				continue
			}
			if response.Result.Exception != nil {
				return "", fmt.Errorf("Chrome evaluation failed: %s", response.Result.Result.Description)
			}
			if value, ok := response.Result.Result.Value.(string); ok && value != "" {
				return value, nil
			}
			break
		}
		timer := time.NewTimer(75 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", ctx.Err()
		case <-timer.C:
		}
	}
}

func waitForDevTools(ctx context.Context, profile string, cmd *exec.Cmd) (int, error) {
	path := filepath.Join(profile, "DevToolsActivePort")
	for {
		if data, err := os.ReadFile(path); err == nil {
			line := strings.SplitN(string(data), "\n", 2)[0]
			port, err := strconv.Atoi(strings.TrimSpace(line))
			if err == nil && port > 0 {
				return port, nil
			}
		}
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			return 0, fmt.Errorf("Chrome exited before DevTools became ready")
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func waitForPage(ctx context.Context, port int, target string) (string, error) {
	endpoint := fmt.Sprintf("http://127.0.0.1:%d/json/list", port)
	client := &http.Client{Timeout: time.Second}
	for {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		resp, err := client.Do(req)
		if err == nil {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			resp.Body.Close()
			var pages []struct {
				Type string `json:"type"`
				URL  string `json:"url"`
				WS   string `json:"webSocketDebuggerUrl"`
			}
			if json.Unmarshal(body, &pages) == nil {
				for _, page := range pages {
					if page.Type == "page" && page.WS != "" && (page.URL == target || len(pages) == 1) {
						return page.WS, nil
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}
