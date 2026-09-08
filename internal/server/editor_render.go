package server

import (
	"context"
	"net"
	"net/http"
	"path"
	"strings"
	"time"
)

type editorRenderKey struct{}

// A headless renderer cannot carry the gateway's bearer credential. Give it a
// temporary loopback-only read surface, not the session's locked editing API.
// Print HTML still requires the engine's short-lived, unguessable print-job key.
func editorRenderRoutes(routes http.Handler, deck string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		allowed := p == "/api/deck/"+deck+"/print.html" || strings.HasPrefix(p, "/library/") || strings.HasPrefix(p, "/assets/video/") || strings.HasPrefix(p, "/api/deck/"+deck+"/source/")
		if r.Method != http.MethodGet || !allowed || strings.Contains(p, "\\") || strings.Contains(r.URL.RawPath, "%") || path.Clean(p) != strings.TrimSuffix(p, "/") {
			http.NotFound(w, r)
			return
		}
		routes.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), editorRenderKey{}, true)))
	})
}

func serveEditorRender(w http.ResponseWriter, r *http.Request, routes http.Handler, deck string) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		http.Error(w, "render unavailable", http.StatusServiceUnavailable)
		return
	}
	render := &http.Server{Handler: editorRenderRoutes(routes, deck), ReadHeaderTimeout: 5 * time.Second}
	defer render.Close()
	go func() { _ = render.Serve(listener) }()
	ctx := context.WithValue(r.Context(), http.LocalAddrContextKey, listener.Addr())
	routes.ServeHTTP(w, r.WithContext(ctx))
}
