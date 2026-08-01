package main

// vstd railway — one-command hosted deployment, following the vessica-cli
// Railway integration pattern: ensure/auto-install the Railway CLI, reuse its
// login session, link the studio directory to a project, configure service
// variables, deploy, and mint a domain.
//
//	vstd railway up       full setup + deploy (idempotent; re-run to redeploy)
//	vstd railway status   linked project + URL
//	vstd railway <args>   passthrough to the railway CLI from the studio root

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/vessica-labs/vessica-studio/internal/oai"
	"github.com/vessica-labs/vessica-studio/internal/studio"
)

func railwayPath() string {
	if path, err := exec.LookPath("railway"); err == nil {
		return path
	}
	if home, err := os.UserHomeDir(); err == nil {
		p := filepath.Join(home, ".railway", "bin", "railway")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "railway"
}

func ensureRailwayCLI(interactive bool) error {
	if _, err := exec.LookPath(railwayPath()); err == nil {
		return nil
	}
	if _, err := os.Stat(railwayPath()); err == nil {
		return nil
	}
	fmt.Println("The Railway CLI is not installed.")
	if !interactive || !confirm("Install it now from railway.com/install.sh?", true) {
		return fmt.Errorf("install it manually (https://docs.railway.com/guides/cli) and re-run")
	}
	tmp, err := os.MkdirTemp("", "vstd-railway-bootstrap-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	installer := filepath.Join(tmp, "install.sh")
	if out, err := exec.Command("curl", "-fsSL", "https://railway.com/install.sh", "-o", installer).CombinedOutput(); err != nil {
		return fmt.Errorf("download Railway CLI installer: %w: %s", err, strings.TrimSpace(string(out)))
	}
	install := exec.Command("sh", installer)
	install.Stdout, install.Stderr = os.Stdout, os.Stderr
	if err := install.Run(); err != nil {
		return fmt.Errorf("install Railway CLI: %w", err)
	}
	return nil
}

// runRailway captures output; runRailwayStreaming inherits the terminal for
// interactive commands (login, init, up). Both tag the caller like
// vessica-cli does.
func runRailway(dir string, stdin string, args ...string) ([]byte, error) {
	cmd := exec.Command(railwayPath(), args...)
	cmd.Dir = dir
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	cmd.Env = append(os.Environ(), "RAILWAY_CALLER=vstd", "RAILWAY_AGENT_SESSION=vstd-railway-up")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("railway %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func runRailwayStreaming(dir string, args ...string) error {
	cmd := exec.Command(railwayPath(), args...)
	cmd.Dir = dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = append(os.Environ(), "RAILWAY_CALLER=vstd", "RAILWAY_AGENT_SESSION=vstd-railway-up")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("railway %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

// ---- prompts ----

var stdinReader = bufio.NewReader(os.Stdin)

func prompt(label, def string) string {
	if def != "" {
		fmt.Printf("%s [%s]: ", label, def)
	} else {
		fmt.Printf("%s: ", label)
	}
	line, _ := stdinReader.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}

func confirm(label string, def bool) bool {
	hint := "y/N"
	if def {
		hint = "Y/n"
	}
	line := prompt(label+" ("+hint+")", "")
	if line == "" {
		return def
	}
	return strings.HasPrefix(strings.ToLower(line), "y")
}

// ---- secret handling ----

func resolveShareSecret(st *studio.Studio) string {
	if v := os.Getenv("VSTD_SECRET"); v != "" {
		return v
	}
	if st.Config.ShareSecretCmd != "" {
		if out, err := exec.Command("sh", "-c", st.Config.ShareSecretCmd).Output(); err == nil {
			return strings.TrimSpace(string(out))
		}
	}
	return ""
}

func persistShareSecret(st *studio.Studio, secret string) {
	if runtime.GOOS != "darwin" {
		fmt.Println("Store this secret somewhere safe (needed by `vstd qr`):")
		fmt.Println("  export VSTD_SECRET=" + secret)
		return
	}
	if !confirm("Store the share-link secret in macOS Keychain (service vessica-studio-secret)?", true) {
		fmt.Println("  export VSTD_SECRET=" + secret)
		return
	}
	user := os.Getenv("USER")
	if out, err := exec.Command("security", "add-generic-password", "-U",
		"-s", "vessica-studio-secret", "-a", user, "-w", secret).CombinedOutput(); err != nil {
		fmt.Printf("Keychain store failed (%s); export it instead:\n  export VSTD_SECRET=%s\n",
			strings.TrimSpace(string(out)), secret)
		return
	}
	appendConfigKey(st, "share_secret_cmd",
		"security find-generic-password -s vessica-studio-secret -w")
	fmt.Println("Secret stored in Keychain; studio.yaml now resolves it via share_secret_cmd.")
}

// appendConfigKey adds a top-level key to studio.yaml if it is not present.
func appendConfigKey(st *studio.Studio, key, value string) {
	p := filepath.Join(st.Root, "studio.yaml")
	b, err := os.ReadFile(p)
	if err != nil {
		return
	}
	if regexp.MustCompile(`(?m)^` + key + `:`).Match(b) {
		return
	}
	out := strings.TrimRight(string(b), "\n") + fmt.Sprintf("\n%s: %s\n", key, value)
	os.WriteFile(p, []byte(out), 0o644)
}

// ---- the command ----

func cmdRailway(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: vstd railway up|status|<railway args>")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "up":
		return railwayUp(rest)
	case "status":
		fs := flag.NewFlagSet("railway status", flag.ExitOnError)
		root := rootFlag(fs)
		fs.Parse(rest)
		st, err := openStudio(*root)
		if err != nil {
			return err
		}
		if st.Config.PublicHost != "" {
			fmt.Println("public host:", st.Config.PublicHost)
		}
		return runRailwayStreaming(st.Root, "status")
	default:
		// passthrough (logs, open, variables, …) from the studio root
		st, err := openStudio(".")
		if err != nil {
			return err
		}
		return runRailwayStreaming(st.Root, args...)
	}
}

func railwayUp(args []string) error {
	fs := flag.NewFlagSet("railway up", flag.ExitOnError)
	root := rootFlag(fs)
	clientID := fs.String("client-id", "", "GitHub OAuth app client ID (device flow)")
	allowed := fs.String("allowed", "", "comma-separated presenter GitHub logins")
	ghToken := fs.String("github-token", "", "PAT with read access to the engine repo (needed while it is private)")
	copyKey := fs.Bool("with-openai-key", false, "copy the locally-resolved OpenAI key to Railway without prompting")
	dryRun := fs.Bool("dry-run", false, "print the plan without calling Railway")
	fs.Parse(args)

	st, err := openStudio(*root)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(st.Root, "Dockerfile")); err != nil {
		return fmt.Errorf("no Dockerfile in %s — the content repo needs the deployment files (see DEPLOY.md)", st.Root)
	}

	// 1. secret (reuse > generate)
	secret := resolveShareSecret(st)
	newSecret := false
	if secret == "" {
		b := make([]byte, 32)
		rand.Read(b)
		secret = hex.EncodeToString(b)
		newSecret = true
	}

	// 2. gather remaining config
	interactive := !*dryRun
	cid := *clientID
	if cid == "" && interactive {
		cid = prompt("GitHub OAuth client ID for presenter sign-in (Enter to skip for now)", "")
	}
	allow := *allowed
	if allow == "" && cid != "" && interactive {
		allow = prompt("Presenter GitHub login(s), comma-separated", "")
	}
	openAIKey := ""
	client := oai.New(st.Config.OpenAI.BaseURL, st.Config.OpenAI.APIKeyCmd)
	if client.HasKey() {
		if *copyKey || (interactive && confirm("Copy your OpenAI key to Railway (enables hosted Vessica voice)?", true)) {
			openAIKey = client.Key
		}
	}
	gh := *ghToken
	if gh == "" && interactive {
		gh = prompt("GitHub token for the private engine repo build (Enter if public)", "")
	}

	vars := map[string]string{
		"VSTD_MODE":   "public",
		"VSTD_SECRET": secret,
	}
	if cid != "" {
		vars["VSTD_GITHUB_CLIENT_ID"] = cid
	}
	if allow != "" {
		vars["VSTD_ALLOWED_GITHUB"] = allow
	}
	if openAIKey != "" {
		vars["OPENAI_API_KEY"] = openAIKey
	}
	if gh != "" {
		vars["GITHUB_TOKEN"] = gh
	}

	if *dryRun {
		fmt.Println("dry run — would do:")
		fmt.Println("  1. ensure Railway CLI (auto-install with consent)")
		fmt.Println("  2. railway login (if no session)")
		fmt.Println("  3. railway init (if directory not linked)")
		keys := []string{}
		for k := range vars {
			keys = append(keys, k)
		}
		fmt.Printf("  4. set variables: %s\n", strings.Join(keys, ", "))
		fmt.Println("  5. railway up --detach")
		fmt.Println("  6. railway domain → save public_host to studio.yaml")
		if newSecret {
			fmt.Println("  7. store new VSTD_SECRET (Keychain on macOS)")
		}
		return nil
	}

	// 3. CLI + login + link
	if err := ensureRailwayCLI(true); err != nil {
		return err
	}
	if _, err := runRailway(st.Root, "", "whoami"); err != nil {
		fmt.Println("Signing in to Railway…")
		if err := runRailwayStreaming(st.Root, "login"); err != nil {
			return err
		}
	}
	if _, err := runRailway(st.Root, "", "status"); err != nil {
		fmt.Println("Linking this studio to a Railway project…")
		if err := runRailwayStreaming(st.Root, "init"); err != nil {
			return err
		}
	}

	// 4. variables. A fresh `railway init` project has NO service yet — the
	// first `railway up` creates one from this directory — so recover from
	// "Project has no services" by deploying once, then retrying.
	setVar := func(k, v string) error {
		_, err := runRailway(st.Root, v, "variable", "set", k, "--stdin", "--skip-deploys", "--json")
		if err == nil {
			return nil
		}
		if _, err2 := runRailway(st.Root, "", "variables", "--set", k+"="+v, "--skip-deploys"); err2 == nil {
			return nil
		}
		return err
	}
	fmt.Println("Configuring service variables…")
	deployedForService := false
	for k, v := range vars {
		err := setVar(k, v)
		if err != nil && strings.Contains(strings.ToLower(err.Error()), "no services") && !deployedForService {
			fmt.Println("  no service yet — creating it with an initial deploy first…")
			if err := runRailwayStreaming(st.Root, "up", "--detach"); err != nil {
				return err
			}
			deployedForService = true
			err = setVar(k, v)
		}
		if err != nil {
			return fmt.Errorf("set %s: %w", k, err)
		}
		fmt.Println("  set", k)
	}

	// 5. deploy (always — variables were set with --skip-deploys, so this is
	// what makes them take effect; on re-runs it is simply the redeploy)
	fmt.Println("Deploying (railway up)…")
	if err := runRailwayStreaming(st.Root, "up", "--detach"); err != nil {
		return err
	}

	// 6. domain
	host := ""
	if out, err := runRailway(st.Root, "", "domain"); err == nil {
		if m := regexp.MustCompile(`https://\S+`).FindString(string(out)); m != "" {
			host = strings.TrimRight(m, "/")
		}
	}
	if host != "" {
		appendConfigKey(st, "public_host", host)
		fmt.Println("\nLive at:", host)
	} else {
		fmt.Println("\nDeployed. Run `vstd railway domain` to mint a public URL.")
	}

	// 7. persist secret
	if newSecret {
		persistShareSecret(st, secret)
	}

	fmt.Println("\nNext:")
	if cid == "" {
		fmt.Println("  · Presenter sign-in is OFF until you create a GitHub OAuth app (enable")
		fmt.Println("    Device Flow), then: vstd railway up --client-id <id> --allowed <login>")
	}
	if host != "" {
		fmt.Printf("  · Audience QR:  vstd qr <deck> --ttl 72   (host defaults to %s)\n", host)
	}
	fmt.Println("  · Redeploy after content changes: git push (if repo-connected) or vstd railway up")
	return nil
}
