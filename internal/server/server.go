// Package server implements the local HTTP server that serves the embedded
// web UI and a single JSON API endpoint. Implemented fully in Phase 6.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/rewind-io/rewind/internal/model"
)

// Server is a local HTTP server bound to 127.0.0.1. It exposes:
//
//	GET /api/incident  → JSON-encoded model.Incident
//	GET /              → embedded SPA (Phase 6)
type Server struct {
	incident model.Incident
	port     int
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
	// Phase 6: mux.Handle("/", http.FileServer(http.FS(embeddedUI)))
	return mux
}

func (s *Server) handleIncident(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "http://localhost:*")
	if err := json.NewEncoder(w).Encode(s.incident); err != nil {
		http.Error(w, "encode error", http.StatusInternalServerError)
	}
}
