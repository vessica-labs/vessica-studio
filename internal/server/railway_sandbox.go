package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/vessica-labs/vessica-studio/internal/studio"
	"gopkg.in/yaml.v3"
)

const (
	defaultRailwaySDKPath = "/opt/vstd-sandbox/node_modules/railway/dist/index.js"
	maxSandboxInputFiles  = 4096
	maxSandboxInputBytes  = int64(512 << 20)
	maxSandboxChanges     = 64
	maxSandboxChangeBytes = int64(128 << 20)
)

//go:embed railway_sandbox_runner.mjs
var railwaySandboxRunnerJS []byte

type sandboxInputFile struct {
	LocalPath  string `json:"localPath"`
	RemotePath string `json:"remotePath"`
	Relative   string `json:"relative,omitempty"`
	SHA256     string `json:"sha256"`
	Mode       uint32 `json:"mode"`
	Size       int64  `json:"size"`
}

type railwaySandboxRequest struct {
	Inputs             []sandboxInputFile `json:"inputs"`
	OutputPrefixes     []string           `json:"outputPrefixes"`
	Prompt             string             `json:"prompt"`
	RemoteImages       []string           `json:"remoteImages,omitempty"`
	ResultDir          string             `json:"resultDir"`
	CodexKeyReference  string             `json:"codexKeyReference"`
	TimeoutSeconds     int                `json:"timeoutSeconds"`
	IdleTimeoutMinutes int                `json:"idleTimeoutMinutes"`
	MaxChanges         int                `json:"maxChanges"`
	MaxChangeBytes     int64              `json:"maxChangeBytes"`
}

type sandboxChange struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type railwaySandboxResult struct {
	SandboxID    string          `json:"sandboxId"`
	Status       string          `json:"status"`
	Network      string          `json:"networkIsolation"`
	ExitCode     *int            `json:"exitCode"`
	TimedOut     bool            `json:"timedOut"`
	Truncated    bool            `json:"truncated"`
	Destroyed    bool            `json:"destroyed"`
	DestroyError string          `json:"destroyError,omitempty"`
	Error        string          `json:"error,omitempty"`
	Changes      []sandboxChange `json:"changes,omitempty"`
}

type agentExecution struct {
	Output    []byte
	Err       error
	SandboxID string
}

func (w *agentWorker) validateExecutionBackend() error {
	switch w.sandbox {
	case "":
		if w.s.Mode == ModePublic && filepath.Base(w.bin) == "codex" {
			return fmt.Errorf("public Codex execution requires VSTD_AGENT_SANDBOX=railway")
		}
		return nil
	case "railway":
		if filepath.Base(w.bin) != "codex" {
			return fmt.Errorf("Railway Sandbox execution currently supports VSTD_AGENT_CMD=codex only")
		}
		return validateRailwaySandboxConfig()
	default:
		return fmt.Errorf("unsupported VSTD_AGENT_SANDBOX %q", w.sandbox)
	}
}

func (w *agentWorker) executeAgent(ctx context.Context, deck, slide, phase, prompt string, images []string) agentExecution {
	if w.sandbox == "railway" && filepath.Base(w.bin) == "codex" {
		return w.executeRailwaySandbox(ctx, deck, slide, phase, prompt, images)
	}
	cmd := agentCommandWithImages(ctx, w.bin, w.s.St.Root, prompt, images)
	out, err := cmd.CombinedOutput()
	return agentExecution{Output: out, Err: err}
}

func (w *agentWorker) executeRailwaySandbox(ctx context.Context, deck, slide, phase, prompt string, images []string) agentExecution {
	enginePath, _ := os.Executable()
	inputs, remoteImages, err := collectRailwaySandboxInputs(w.s.St.Root, deck, slide, images, enginePath)
	if err != nil {
		return agentExecution{Err: fmt.Errorf("prepare Railway sandbox: %w", err)}
	}
	for i, image := range images {
		if i < len(remoteImages) {
			prompt = strings.ReplaceAll(prompt, image, remoteImages[i])
		}
	}
	resultDir, err := os.MkdirTemp("", "vstd-railway-sandbox-result-*")
	if err != nil {
		return agentExecution{Err: fmt.Errorf("prepare Railway sandbox result: %w", err)}
	}
	defer os.RemoveAll(resultDir)
	runnerDir, err := os.MkdirTemp("", "vstd-railway-sandbox-runner-*")
	if err != nil {
		return agentExecution{Err: fmt.Errorf("prepare Railway sandbox runner: %w", err)}
	}
	defer os.RemoveAll(runnerDir)
	runnerPath := filepath.Join(runnerDir, "runner.mjs")
	if err := os.WriteFile(runnerPath, railwaySandboxRunnerJS, 0o600); err != nil {
		return agentExecution{Err: fmt.Errorf("write Railway sandbox runner: %w", err)}
	}
	timeout := sandboxRemoteTimeout(time.Until(deadlineOr(ctx, time.Now().Add(agentPassTimeout()))))
	request := railwaySandboxRequest{
		Inputs: inputs, OutputPrefixes: sandboxOutputPrefixes(deck), Prompt: prompt,
		RemoteImages: remoteImages, ResultDir: resultDir, CodexKeyReference: sandboxCodexKeyReference(),
		TimeoutSeconds: int(timeout.Seconds()), IdleTimeoutMinutes: sandboxIdleTimeout(timeout),
		MaxChanges: maxSandboxChanges, MaxChangeBytes: maxSandboxChangeBytes,
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return agentExecution{Err: fmt.Errorf("encode Railway sandbox request: %w", err)}
	}
	cmd := exec.CommandContext(ctx, "node", runnerPath)
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Env = sandboxDispatcherEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	var result railwaySandboxResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if len(detail) > 600 {
			detail = detail[len(detail)-600:]
		}
		if runErr == nil {
			runErr = err
		}
		return agentExecution{Err: fmt.Errorf("Railway sandbox dispatcher: %w — %s", runErr, detail)}
	}
	logPath := filepath.Join(resultDir, "agent.log")
	out, _ := os.ReadFile(logPath)
	if result.SandboxID != "" {
		log.Printf("agent: Railway sandbox %s phase=%s network=%s destroyed=%v", result.SandboxID, phase, result.Network, result.Destroyed)
	}
	if runErr != nil {
		return agentExecution{Output: out, Err: fmt.Errorf("Railway sandbox dispatcher: %w", runErr), SandboxID: result.SandboxID}
	}
	if result.Error != "" {
		return agentExecution{Output: out, Err: fmt.Errorf("Railway sandbox: %s", result.Error), SandboxID: result.SandboxID}
	}
	if !result.Destroyed {
		return agentExecution{Output: out, Err: fmt.Errorf("Railway sandbox %s was not destroyed: %s", result.SandboxID, result.DestroyError), SandboxID: result.SandboxID}
	}
	if result.TimedOut {
		return agentExecution{Output: out, Err: fmt.Errorf("Railway sandbox agent timed out"), SandboxID: result.SandboxID}
	}
	if result.Truncated {
		return agentExecution{Output: out, Err: fmt.Errorf("Railway sandbox agent output was truncated"), SandboxID: result.SandboxID}
	}
	if result.ExitCode == nil || *result.ExitCode != 0 {
		code := -1
		if result.ExitCode != nil {
			code = *result.ExitCode
		}
		return agentExecution{Output: out, Err: fmt.Errorf("Railway sandbox agent exited %d", code), SandboxID: result.SandboxID}
	}
	if err := applyRailwaySandboxChanges(w.s.St.Root, deck, resultDir, inputs, result.Changes); err != nil {
		return agentExecution{Output: out, Err: fmt.Errorf("apply Railway sandbox result: %w", err), SandboxID: result.SandboxID}
	}
	return agentExecution{Output: out, SandboxID: result.SandboxID}
}

func deadlineOr(ctx context.Context, fallback time.Time) time.Time {
	if deadline, ok := ctx.Deadline(); ok {
		return deadline
	}
	return fallback
}

func sandboxRemoteTimeout(remaining time.Duration) time.Duration {
	if remaining <= 0 {
		return time.Second
	}
	reserve := 90 * time.Second
	if remaining < 3*time.Minute {
		reserve = remaining / 3
	}
	remote := remaining - reserve
	if remote < time.Second {
		return time.Second
	}
	return remote
}

func sandboxIdleTimeout(timeout time.Duration) int {
	minutes := int((timeout + 5*time.Minute + time.Minute - 1) / time.Minute)
	if minutes < 10 {
		minutes = 10
	}
	if minutes > 60 {
		minutes = 60
	}
	return minutes
}

func sandboxCodexKeyReference() string {
	service := strings.TrimSpace(os.Getenv("VSTD_AGENT_SANDBOX_SECRET_SERVICE"))
	if service == "" {
		service = strings.TrimSpace(os.Getenv("RAILWAY_SERVICE_NAME"))
	}
	if service == "" {
		service = "Vessica Studio"
	}
	variable := strings.TrimSpace(os.Getenv("VSTD_AGENT_SANDBOX_SECRET_VARIABLE"))
	if variable == "" {
		variable = "OPENAI_API_KEY"
	}
	return "${{" + service + "." + variable + "}}"
}

func sandboxDispatcherEnv() []string {
	allowed := []string{"PATH", "HOME", "TMPDIR", "SSL_CERT_FILE", "NODE_EXTRA_CA_CERTS",
		"RAILWAY_TOKEN", "RAILWAY_ENVIRONMENT_ID", "RAILWAY_GRAPHQL_ENDPOINT", "VSTD_RAILWAY_SDK"}
	out := make([]string, 0, len(allowed)+1)
	for _, key := range allowed {
		if value, ok := os.LookupEnv(key); ok && value != "" {
			out = append(out, key+"="+value)
		}
	}
	if !hasEnvKey(out, "VSTD_RAILWAY_SDK") {
		out = append(out, "VSTD_RAILWAY_SDK="+defaultRailwaySDKPath)
	}
	return out
}

func hasEnvKey(env []string, key string) bool {
	prefix := key + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return true
		}
	}
	return false
}

func validateRailwaySandboxConfig() error {
	if os.Getenv("RAILWAY_TOKEN") == "" {
		return fmt.Errorf("project-scoped RAILWAY_TOKEN is required for Railway Sandbox execution")
	}
	if os.Getenv("RAILWAY_ENVIRONMENT_ID") == "" {
		return fmt.Errorf("RAILWAY_ENVIRONMENT_ID is required for Railway Sandbox execution")
	}
	if _, err := exec.LookPath("node"); err != nil {
		return fmt.Errorf("Node.js is required for Railway Sandbox execution: %w", err)
	}
	sdkPath := strings.TrimSpace(os.Getenv("VSTD_RAILWAY_SDK"))
	if sdkPath == "" {
		sdkPath = defaultRailwaySDKPath
	}
	if info, err := os.Stat(sdkPath); err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("Railway JavaScript SDK not found at %s", sdkPath)
	}
	return nil
}

var libraryPathPattern = regexp.MustCompile(`(?:\.\./)*library/([A-Za-z0-9][A-Za-z0-9._/-]*)`)

func collectRailwaySandboxInputs(root, deck, slide string, images []string, enginePath string) ([]sandboxInputFile, []string, error) {
	if !studio.ValidDeckName(deck) || !studio.ValidSlideID(slide) {
		return nil, nil, fmt.Errorf("invalid deck or slide")
	}
	files := map[string]sandboxInputFile{}
	addRootFile := func(rel string) error {
		rel = filepath.ToSlash(filepath.Clean(rel))
		local, err := safeSandboxJoin(root, rel)
		if err != nil {
			return err
		}
		if err := rejectSymlinkPath(root, rel); err != nil {
			return err
		}
		info, err := os.Stat(local)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		input, err := newSandboxInput(local, "/workspace/"+rel, rel, uint32(info.Mode().Perm()))
		if err != nil {
			return err
		}
		files[input.RemotePath] = input
		return nil
	}
	addTree := func(rel string) error {
		base, err := safeSandboxJoin(root, rel)
		if err != nil {
			return err
		}
		return filepath.WalkDir(base, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			name := entry.Name()
			if entry.IsDir() && (name == "build" || name == ".git" || name == "_vessica" || name == "_agent-logs" || name == ".codex-tmp") {
				return filepath.SkipDir
			}
			if entry.IsDir() || name == ".DS_Store" {
				return nil
			}
			relPath, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			return addRootFile(relPath)
		})
	}
	for _, rel := range []string{"studio.yaml", "AGENTS.md", "CLAUDE.md", "codex/AGENTS.md"} {
		if err := addRootFile(rel); err != nil {
			return nil, nil, err
		}
	}
	deckRel := filepath.ToSlash(filepath.Join("decks", deck))
	if err := addTree(deckRel); err != nil {
		return nil, nil, err
	}
	var deckConfig struct {
		Theme string `yaml:"theme"`
	}
	if raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(deckRel), "deck.yaml")); err == nil {
		_ = yaml.Unmarshal(raw, &deckConfig)
	}
	if deckConfig.Theme != "" {
		if err := addTree(filepath.ToSlash(filepath.Join("themes", deckConfig.Theme))); err != nil && !os.IsNotExist(err) {
			return nil, nil, err
		}
	}
	if err := addRootFile("library/manifest.json"); err != nil {
		return nil, nil, err
	}
	deckInputs := make([]sandboxInputFile, 0, len(files))
	for _, input := range files {
		deckInputs = append(deckInputs, input)
	}
	for _, input := range deckInputs {
		if !strings.HasPrefix(input.Relative, deckRel+"/") || input.Size > 4<<20 {
			continue
		}
		raw, err := os.ReadFile(input.LocalPath)
		if err != nil {
			return nil, nil, err
		}
		for _, match := range libraryPathPattern.FindAllSubmatch(raw, -1) {
			if len(match) == 2 {
				if err := addRootFile("library/" + string(match[1])); err != nil {
					return nil, nil, err
				}
			}
		}
	}
	requestDir := filepath.Join(root, "requests")
	entries, _ := os.ReadDir(requestDir)
	for _, entry := range entries {
		if entry.IsDir() || !(strings.HasSuffix(entry.Name(), ".yaml") || strings.HasSuffix(entry.Name(), ".yml")) {
			continue
		}
		var request struct {
			Deck  string `yaml:"deck"`
			Slide string `yaml:"slide"`
		}
		raw, err := os.ReadFile(filepath.Join(requestDir, entry.Name()))
		if err == nil && yaml.Unmarshal(raw, &request) == nil && request.Deck == deck && request.Slide == slide {
			if err := addRootFile(filepath.ToSlash(filepath.Join("requests", entry.Name()))); err != nil {
				return nil, nil, err
			}
		}
	}
	if enginePath != "" {
		if info, err := os.Stat(enginePath); err == nil && info.Mode().IsRegular() {
			input, err := newSandboxInput(enginePath, "/workspace/bin/vstd", "", 0o755)
			if err != nil {
				return nil, nil, err
			}
			files[input.RemotePath] = input
		}
	}
	remoteImages := make([]string, 0, len(images))
	for index, image := range images {
		remote := fmt.Sprintf("/workspace/.vstd/images/%02d%s", index, strings.ToLower(filepath.Ext(image)))
		input, err := newSandboxInput(image, remote, "", 0o600)
		if err != nil {
			return nil, nil, err
		}
		files[remote] = input
		remoteImages = append(remoteImages, remote)
	}
	inputs := make([]sandboxInputFile, 0, len(files))
	var total int64
	for _, input := range files {
		inputs = append(inputs, input)
		total += input.Size
	}
	if len(inputs) > maxSandboxInputFiles || total > maxSandboxInputBytes {
		return nil, nil, fmt.Errorf("sandbox input exceeds limit: %d files, %d bytes", len(inputs), total)
	}
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].RemotePath < inputs[j].RemotePath })
	return inputs, remoteImages, nil
}

func newSandboxInput(local, remote, relative string, mode uint32) (sandboxInputFile, error) {
	file, err := os.Open(local)
	if err != nil {
		return sandboxInputFile{}, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return sandboxInputFile{}, err
	}
	return sandboxInputFile{LocalPath: local, RemotePath: remote, Relative: relative,
		SHA256: hex.EncodeToString(hash.Sum(nil)), Mode: mode, Size: size}, nil
}

func sandboxOutputPrefixes(deck string) []string {
	return []string{"decks/" + deck + "/", "library/", "requests/"}
}

func applyRailwaySandboxChanges(root, deck, resultDir string, inputs []sandboxInputFile, changes []sandboxChange) error {
	if len(changes) > maxSandboxChanges {
		return fmt.Errorf("too many sandbox changes: %d", len(changes))
	}
	original := map[string]string{}
	for _, input := range inputs {
		if input.Relative != "" {
			original[input.Relative] = input.SHA256
		}
	}
	var total int64
	for _, change := range changes {
		if !sandboxOutputAllowed(deck, change.Path) {
			return fmt.Errorf("output path is outside scope: %s", change.Path)
		}
		total += change.Size
		if change.Size < 0 || total > maxSandboxChangeBytes {
			return fmt.Errorf("sandbox output exceeds size limit")
		}
		target, err := safeSandboxJoin(root, change.Path)
		if err != nil {
			return err
		}
		if err := rejectSymlinkPath(root, change.Path); err != nil {
			return err
		}
		currentHash, exists, err := hashFileIfExists(target)
		if err != nil {
			return err
		}
		if before, wasUploaded := original[change.Path]; wasUploaded {
			if !exists || currentHash != before {
				return fmt.Errorf("content changed during sandbox run: %s", change.Path)
			}
		} else if exists {
			return fmt.Errorf("sandbox output conflicts with a new local file: %s", change.Path)
		}
		source, err := safeSandboxJoin(filepath.Join(resultDir, "files"), change.Path)
		if err != nil {
			return err
		}
		gotHash, exists, err := hashFileIfExists(source)
		if err != nil || !exists || gotHash != change.SHA256 {
			return fmt.Errorf("invalid sandbox result for %s", change.Path)
		}
	}
	for _, change := range changes {
		target, _ := safeSandboxJoin(root, change.Path)
		source, _ := safeSandboxJoin(filepath.Join(resultDir, "files"), change.Path)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		input, err := os.Open(source)
		if err != nil {
			return err
		}
		tmp, err := os.CreateTemp(filepath.Dir(target), ".vstd-sandbox-*")
		if err != nil {
			input.Close()
			return err
		}
		_, copyErr := io.Copy(tmp, input)
		input.Close()
		closeErr := tmp.Close()
		if copyErr != nil || closeErr != nil {
			os.Remove(tmp.Name())
			if copyErr != nil {
				return copyErr
			}
			return closeErr
		}
		if err := os.Chmod(tmp.Name(), 0o644); err != nil {
			os.Remove(tmp.Name())
			return err
		}
		if err := os.Rename(tmp.Name(), target); err != nil {
			os.Remove(tmp.Name())
			return err
		}
	}
	return nil
}

func sandboxOutputAllowed(deck, rel string) bool {
	clean := filepath.ToSlash(filepath.Clean(rel))
	if clean != rel || strings.HasPrefix(clean, "/") || clean == "." || strings.Contains(clean, "\x00") {
		return false
	}
	for _, prefix := range sandboxOutputPrefixes(deck) {
		if strings.HasPrefix(clean, prefix) && len(clean) > len(prefix) {
			return true
		}
	}
	return false
}

func safeSandboxJoin(root, rel string) (string, error) {
	if filepath.IsAbs(rel) || strings.Contains(rel, "\x00") {
		return "", fmt.Errorf("unsafe path: %s", rel)
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe path: %s", rel)
	}
	target := filepath.Join(root, clean)
	check, err := filepath.Rel(root, target)
	if err != nil || check == ".." || strings.HasPrefix(check, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe path: %s", rel)
	}
	return target, nil
}

func rejectSymlinkPath(root, rel string) error {
	current := root
	parts := strings.Split(filepath.Clean(filepath.FromSlash(rel)), string(filepath.Separator))
	for _, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("sandbox output traverses symlink: %s", rel)
		}
	}
	return nil
}

func hashFileIfExists(path string) (string, bool, error) {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", false, err
	}
	return hex.EncodeToString(hash.Sum(nil)), true, nil
}
