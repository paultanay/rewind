// Package server implements the local HTTP server that serves the embedded
// web UI and a single JSON API endpoint.
//
// Design constraints (spec §13):
//   - Bound to 127.0.0.1 only — never exposed to external network.
//   - Stateless: one incident per server instance; no collection, no DB.
//   - /api/incident returns the full model.Incident as JSON (read-only).
//   - SPA routing: any path not matching a real file falls back to index.html.
//   - Graceful shutdown on context cancellation (5s timeout).
package server

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"time"

	"github.com/paultanay/rewind/internal/model"
)

//go:embed ui/dist
var embeddedUI embed.FS

// Server is a local HTTP server bound to 127.0.0.1. It exposes:
//
//	GET /api/incident  → JSON-encoded model.Incident
//	GET /api/health    → {"status":"ok"}
//	GET /*             → embedded SPA (index.html fallback for client-side routing)
type Server struct {
	incident model.Incident
	listener net.Listener
	httpSrv  *http.Server
}

// New creates a Server for the given incident. If port is 0, a random
// available port is chosen.
func New(inc model.Incident, port int) (*Server, error) {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("server: listen %s: %w", addr, err)
	}
	s := &Server{incident: inc, listener: ln}
	s.httpSrv = &http.Server{
		Handler:      s.routes(),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	return s, nil
}

// Port returns the actual port the server is listening on.
func (s *Server) Port() int {
	return s.listener.Addr().(*net.TCPAddr).Port
}

// Addr returns the full listen address, e.g. "127.0.0.1:7750".
func (s *Server) Addr() string {
	return s.listener.Addr().String()
}

// Serve starts the HTTP server in the foreground. It returns when the
// context is cancelled or the server encounters a fatal error.
func (s *Server) Serve(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		if err := s.httpSrv.Serve(s.listener); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.httpSrv.Shutdown(shutCtx)
	case err := <-errCh:
		return err
	}
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/incident", s.handleIncident)
	mux.HandleFunc("/api/health", s.handleHealth)

	// Embed the SPA from ui/dist, stripping the directory prefix.
	distFS, err := fs.Sub(embeddedUI, "ui/dist")
	if err != nil {
		// Should never happen — ui/dist is always present via go:embed.
		panic("embedded UI directory missing: " + err.Error())
	}
	fileServer := http.FileServer(http.FS(distFS))

	// SPA fallback: serve index.html for any route that isn't a real file.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Try to open the file directly.
		path := r.URL.Path
		if path == "/" || path == "" {
			path = "index.html"
		} else {
			path = path[1:] // strip leading /
		}

		if f, err := distFS.Open(path); err == nil {
			f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}

		// Fall back to index.html for client-side routes (SPA).
		idx, err := distFS.Open("index.html")
		if err != nil {
			http.Error(w, "UI not available — run 'make build-ui' first", http.StatusServiceUnavailable)
			return
		}
		defer idx.Close()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.Copy(w, idx) //nolint:errcheck
	})

	return mux
}

func (s *Server) handleIncident(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(s.incident); err != nil {
		http.Error(w, "encode error", http.StatusInternalServerError)
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{"status":"ok"}`)
}
