package main

import (
	"errors"
	"flag"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/vessica-labs/vessica-studio/internal/server"
)

// This transport never accepts gateway credentials on the command line, in URLs,
// or in the content workspace. The sandbox supervisor supplies its environment.
func cmdEditorSession(args []string) error {
	fs := flag.NewFlagSet("editor-session", flag.ContinueOnError)
	root := rootFlag(fs)
	deck := fs.String("deck", "", "the single materialized deck")
	port := fs.Int("port", 8080, "loopback listening port")
	lifetime := fs.Duration("lifetime", 30*time.Minute, "session lifetime (maximum 1h)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *port < 1 || *port > 65535 || *lifetime <= 0 || *lifetime > time.Hour {
		return errors.New("invalid editor-session arguments")
	}
	st, err := openStudio(*root)
	if err != nil {
		return err
	}
	handler, err := server.NewEditorSession(st, server.EditorSessionOptions{Deck: *deck, Token: os.Getenv("VSTD_EDITOR_TOKEN"), ExpiresAt: time.Now().Add(*lifetime)})
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(*port)))
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 16 << 10}
	timer := time.AfterFunc(*lifetime, func() { _ = srv.Close() })
	defer timer.Stop()
	err = srv.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
