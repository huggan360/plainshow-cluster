package controller

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"
)

// Server is intentionally small: controller-only collaboration and overview
// features are added here, never by turning this process into a device.
type Server struct {
	config      *Config
	fingerprint string
}

// NewServer builds an unattached or attached controller service.
func NewServer(config *Config, fingerprint string) *Server {
	return &Server{config: config, fingerprint: fingerprint}
}

// Handler exposes the controller identity without any node APIs.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": s.config.ID, "name": s.config.Name, "fingerprint": s.fingerprint,
			"networks": s.config.Networks,
		})
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("Plainshow Controller Server\n"))
	})
	return mux
}

// ListenAndServe runs the controller over TLS until context cancellation.
func (s *Server) ListenAndServe(ctx context.Context, certificate tls.Certificate, onReady func(string)) error {
	address := net.JoinHostPort(s.config.Listen.Bind, strconv.Itoa(s.config.Listen.Port))
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("controller address %s is not available: %w", address, err)
	}
	if onReady != nil {
		onReady("https://" + address)
	}
	server := &http.Server{
		Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	err = server.Serve(tls.NewListener(listener, &tls.Config{
		Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS13,
	}))
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
