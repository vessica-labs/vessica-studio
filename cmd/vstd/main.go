// vstd — the Vessica Studio engine.
//
// A local-first presentation engine driven by files: decks are directories of
// HTML slide fragments paired with companion markdown (research, evidence,
// talk track). Agents (Claude) write the files; vstd builds, serves, watches,
// generates imagery, and hosts presentation mode.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"
	"github.com/vessica-labs/vessica-studio/internal/oai"
	"github.com/vessica-labs/vessica-studio/internal/server"
	"github.com/vessica-labs/vessica-studio/internal/studio"
)

const version = "0.3.0"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "init":
		err = cmdInit(args)
	case "new":
		err = cmdNew(args)
	case "list":
		err = cmdList(args)
	case "fork":
		err = cmdFork(args)
	case "diff-upstream":
		err = cmdDiffUpstream(args)
	case "build":
		err = cmdBuild(args)
	case "agent":
		err = cmdAgent(args)
	case "serve":
		err = cmdServe(args)
	case "asset":
		err = cmdAsset(args)
	case "qr":
		err = cmdQR(args)
	case "railway":
		err = cmdRailway(args)
	case "key":
		err = cmdKey(args)
	case "version":
		fmt.Println("vstd", version)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`vstd — Vessica Studio engine

Usage:
  vstd init [dir]                     scaffold a studio content root
  vstd new <deck> [--title T]         create a deck
  vstd list                           list decks
  vstd fork <deck> <client>           fork a deck for a client (deck--client)
  vstd diff-upstream <fork>           slides changed upstream since fork
  vstd build <deck>|--all             assemble build/index.html
  vstd serve [deck] [flags]           serve studio (watch, live reload, edit API)
  vstd asset gen --prompt P [flags]   generate a library image (gpt-image-2)
  vstd asset list|find [--tags a,b --family F]   browse/reuse the library
  vstd key check                      verify OpenAI key resolution
  vstd qr <deck> [--ttl 72] [--host U]  mint a signed audience share link + QR
  vstd railway up                     one-command Railway setup + deploy
  vstd railway status|<args>          linked project info / CLI passthrough
  vstd version

Serve flags:
  --root DIR      studio root (default ".")
  --port N        (default from studio.yaml / PORT env, 4400)
  --mode M        studio | present | public   (default studio)

Asset gen flags:
  --root DIR  --prompt P  --family F  --tags a,b  --size 1024x1024  --slug S  --dry-run

Environment:
  OPENAI_API_KEY        image generation + realtime token minting
  VSTD_PRESENTER_KEY    interim presenter auth for --mode public
  PORT                  overrides port (Railway sets this)
`)
}

func openStudio(root string) (*studio.Studio, error) { return studio.Open(root) }

func rootFlag(fs *flag.FlagSet) *string { return fs.String("root", ".", "studio root") }

func cmdInit(args []string) error {
	dir := "."
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		dir = args[0]
	}
	if err := studio.Init(dir); err != nil {
		return err
	}
	fmt.Println("initialized studio at", dir)
	return nil
}

func cmdNew(args []string) error {
	fs := flag.NewFlagSet("new", flag.ExitOnError)
	root := rootFlag(fs)
	title := fs.String("title", "", "deck title")
	if len(args) < 1 {
		return fmt.Errorf("usage: vstd new <deck> [--title T]")
	}
	name := args[0]
	fs.Parse(args[1:])
	st, err := openStudio(*root)
	if err != nil {
		return err
	}
	if err := st.NewDeck(name, *title); err != nil {
		return err
	}
	fmt.Printf("created deck %s (theme %s)\n", name, st.Config.ThemeDefault)
	return nil
}

func cmdList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	root := rootFlag(fs)
	fs.Parse(args)
	st, err := openStudio(*root)
	if err != nil {
		return err
	}
	decks, err := st.ListDecks()
	if err != nil {
		return err
	}
	for _, d := range decks {
		meta, _ := st.LoadDeckMeta(d)
		ids, _ := st.SlideIDs(d)
		extra := ""
		if meta.ForkedFrom != "" {
			extra = "  (fork of " + meta.ForkedFrom + ")"
		}
		fmt.Printf("%-32s %3d slides  %s%s\n", d, len(ids), meta.Title, extra)
	}
	return nil
}

func cmdFork(args []string) error {
	fs := flag.NewFlagSet("fork", flag.ExitOnError)
	root := rootFlag(fs)
	if len(args) < 2 {
		return fmt.Errorf("usage: vstd fork <deck> <client>")
	}
	src, client := args[0], args[1]
	fs.Parse(args[2:])
	st, err := openStudio(*root)
	if err != nil {
		return err
	}
	dst, err := st.Fork(src, client)
	if err != nil {
		return err
	}
	fmt.Println("forked to", dst)
	return nil
}

func cmdDiffUpstream(args []string) error {
	fs := flag.NewFlagSet("diff-upstream", flag.ExitOnError)
	root := rootFlag(fs)
	if len(args) < 1 {
		return fmt.Errorf("usage: vstd diff-upstream <fork>")
	}
	fork := args[0]
	fs.Parse(args[1:])
	st, err := openStudio(*root)
	if err != nil {
		return err
	}
	changed, added, removed, err := st.DiffUpstream(fork)
	if err != nil {
		return err
	}
	if len(changed)+len(added)+len(removed) == 0 {
		fmt.Println("no upstream changes since fork")
		return nil
	}
	for _, id := range changed {
		fmt.Println("changed upstream:", id)
	}
	for _, id := range added {
		fmt.Println("added upstream:  ", id)
	}
	for _, id := range removed {
		fmt.Println("removed upstream:", id)
	}
	return nil
}

func cmdBuild(args []string) error {
	fs := flag.NewFlagSet("build", flag.ExitOnError)
	root := rootFlag(fs)
	all := fs.Bool("all", false, "build every deck")
	rest := []string{}
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			rest = append(rest, a)
		} else {
			rest = append([]string{a}, rest...)
		}
	}
	var deck string
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		deck = rest[0]
		fs.Parse(rest[1:])
	} else {
		fs.Parse(rest)
	}
	st, err := openStudio(*root)
	if err != nil {
		return err
	}
	targets := []string{}
	if *all || deck == "" {
		targets, err = st.ListDecks()
		if err != nil {
			return err
		}
	} else {
		targets = []string{deck}
	}
	for _, d := range targets {
		out, err := st.Build(d)
		if err != nil {
			return fmt.Errorf("%s: %w", d, err)
		}
		fmt.Println("built", out)
	}
	return nil
}

// cmdAgent sweeps the redesign queue once, synchronously, using the local
// claude CLI (VSTD_AGENT_CMD to override). `vstd serve` with VSTD_AGENT=1
// runs the same worker continuously in the background.
func cmdAgent(args []string) error {
	fs := flag.NewFlagSet("agent", flag.ExitOnError)
	root := rootFlag(fs)
	fs.Parse(args)
	st, err := openStudio(*root)
	if err != nil {
		return err
	}
	srv := server.New(st, server.ModeStudio)
	n := srv.RunAgentOnce()
	log.Printf("agent: sweep complete — %d pass(es) run", n)
	return nil
}

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	root := rootFlag(fs)
	port := fs.Int("port", 0, "port")
	mode := fs.String("mode", envOr("VSTD_MODE", "studio"), "studio|present|public")
	var deck string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		deck = args[0]
		args = args[1:]
	}
	fs.Parse(args)
	st, err := openStudio(*root)
	if err != nil {
		return err
	}
	m := server.Mode(*mode)
	if m != server.ModeStudio && m != server.ModePresent && m != server.ModePublic {
		return fmt.Errorf("invalid mode %q", *mode)
	}
	srv := server.New(st, m)
	p := st.Config.Port
	if *port != 0 {
		p = *port
	}
	go srv.Watch(700 * time.Millisecond)
	srv.StartAgent()
	addr := fmt.Sprintf(":%d", p)
	url := fmt.Sprintf("http://localhost:%d/", p)
	if deck != "" {
		url += "d/" + deck + "/"
	}
	log.Printf("vstd %s serving %s (mode %s) — %s", version, st.Root, m, url)
	return http.ListenAndServe(addr, srv.Routes())
}

func cmdKey(args []string) error {
	fs := flag.NewFlagSet("key", flag.ExitOnError)
	root := rootFlag(fs)
	if len(args) < 1 || args[0] != "check" {
		return fmt.Errorf("usage: vstd key check")
	}
	fs.Parse(args[1:])
	st, err := openStudio(*root)
	if err != nil {
		return err
	}
	c := oai.New(st.Config.OpenAI.BaseURL, st.Config.OpenAI.APIKeyCmd)
	if !c.HasKey() {
		fmt.Println("no OpenAI key found.")
		fmt.Println("  option 1: export OPENAI_API_KEY=sk-...")
		fmt.Println("  option 2 (macOS Keychain):")
		fmt.Println("    security add-generic-password -U -s vessica-openai -a $USER -w 'sk-...'")
		fmt.Println("    then in studio.yaml under openai:")
		fmt.Println("      api_key_cmd: security find-generic-password -s vessica-openai -w")
		return fmt.Errorf("key not configured")
	}
	fmt.Printf("key resolved (%d chars) — source hidden, never logged\n", len(c.Key))
	return nil
}

func cmdAssetList(args []string, find bool) error {
	fs := flag.NewFlagSet("asset list", flag.ExitOnError)
	root := rootFlag(fs)
	tags := fs.String("tags", "", "match any of these comma-separated tags")
	family := fs.String("family", "", "filter by style family")
	fs.Parse(args)
	st, err := openStudio(*root)
	if err != nil {
		return err
	}
	man, err := oai.LoadManifest(st.Root + "/library")
	if err != nil {
		return err
	}
	want := map[string]bool{}
	for _, t := range strings.Split(*tags, ",") {
		if t != "" {
			want[strings.TrimSpace(t)] = true
		}
	}
	n := 0
	for _, a := range man.Assets {
		if *family != "" && a.Family != *family {
			continue
		}
		if len(want) > 0 {
			hit := false
			for _, t := range a.Tags {
				if want[t] {
					hit = true
				}
			}
			if !hit {
				continue
			}
		}
		n++
		fmt.Printf("%-40s family=%-18s tags=%s\n  /library/%s\n", a.ID, a.Family, strings.Join(a.Tags, ","), a.File)
	}
	if n == 0 && find {
		fmt.Println("no matching assets — generate one with `vstd asset gen` (reuse-before-generate: consider widening tags first)")
	}
	if len(man.StyleFamilies) > 0 && !find {
		fmt.Println("\nstyle families:")
		for name, f := range man.StyleFamilies {
			fmt.Printf("  %-20s %s\n", name, f.PromptPrefix)
		}
	}
	return nil
}

func cmdAsset(args []string) error {
	if len(args) >= 1 && args[0] == "list" {
		return cmdAssetList(args[1:], false)
	}
	if len(args) >= 1 && args[0] == "find" {
		return cmdAssetList(args[1:], true)
	}
	if len(args) < 1 || args[0] != "gen" {
		return fmt.Errorf("usage: vstd asset gen|list|find ...")
	}
	fs := flag.NewFlagSet("asset gen", flag.ExitOnError)
	root := rootFlag(fs)
	prompt := fs.String("prompt", "", "image prompt (required)")
	family := fs.String("family", "", "style family from library manifest")
	tags := fs.String("tags", "", "comma-separated tags")
	size := fs.String("size", "1024x1024", "image size")
	slug := fs.String("slug", "", "asset id slug")
	dry := fs.Bool("dry-run", false, "resolve prompt without calling the API")
	fs.Parse(args[1:])
	if *prompt == "" {
		return fmt.Errorf("--prompt is required")
	}
	st, err := openStudio(*root)
	if err != nil {
		return err
	}
	client := oai.New(st.Config.OpenAI.BaseURL, st.Config.OpenAI.APIKeyCmd)
	var tagList []string
	if *tags != "" {
		tagList = strings.Split(*tags, ",")
	}
	asset, err := client.GenerateAsset(st.Root+"/library", st.Config.OpenAI.ImageModel,
		*prompt, *family, *size, *slug, tagList, *dry)
	if err != nil {
		return err
	}
	if *dry {
		fmt.Printf("dry-run — resolved prompt:\n  %s\n  id: %s\n", asset.Prompt, asset.ID)
		return nil
	}
	fmt.Printf("created asset %s (%s)\n  use as: /library/%s\n", asset.ID, asset.File, asset.File)
	return nil
}

func cmdQR(args []string) error {
	fs := flag.NewFlagSet("qr", flag.ExitOnError)
	root := rootFlag(fs)
	ttl := fs.Int("ttl", 72, "link lifetime in hours")
	host := fs.String("host", "", "public base URL (e.g. https://decks.example.com); defaults to localhost")
	if len(args) < 1 {
		return fmt.Errorf("usage: vstd qr <deck> [--ttl hours] [--host url]")
	}
	deck := args[0]
	fs.Parse(args[1:])
	st, err := openStudio(*root)
	if err != nil {
		return err
	}
	secret := resolveShareSecret(st)
	if secret == "" {
		return fmt.Errorf("no share secret: set VSTD_SECRET, or run `vstd railway up` (stores it in Keychain via share_secret_cmd)")
	}
	tok := server.MintShareToken(secret, deck, time.Duration(*ttl)*time.Hour)
	base := *host
	if base == "" {
		base = st.Config.PublicHost
	}
	if base == "" {
		base = fmt.Sprintf("http://localhost:%d", st.Config.Port)
	}
	link := strings.TrimRight(base, "/") + "/v/" + deck + "/" + tok
	fmt.Println(link)
	png, err := qrcode.Encode(link, qrcode.Medium, 512)
	if err != nil {
		return err
	}
	out := st.DeckDir(deck) + "/build/share-qr.png"
	os.MkdirAll(st.DeckDir(deck)+"/build", 0o755)
	if err := os.WriteFile(out, png, 0o644); err != nil {
		return err
	}
	q, _ := qrcode.New(link, qrcode.Low)
	if q != nil {
		fmt.Println(q.ToSmallString(false))
	}
	fmt.Println("QR saved:", out, "· expires in", *ttl, "hours")
	return nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
