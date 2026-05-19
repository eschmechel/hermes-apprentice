package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"
)

type Config struct {
	Addr   string
	Logger *slog.Logger
}

type Server struct {
	cfg    Config
	srv    *http.Server
	logger *slog.Logger
}

func New(cfg Config) *Server {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	mux := http.NewServeMux()
	s := &Server{cfg: cfg, logger: cfg.Logger}
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /records", s.handleRecordsStub)
	s.srv = &http.Server{
		Addr:              cfg.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return s
}

func (s *Server) ListenAndServe(ctx context.Context) error {
	s.logger.Info("http listening", "addr", s.cfg.Addr)
	err := s.srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) Shutdown(ctx context.Context) error {
	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return s.srv.Shutdown(shutdownCtx)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// handleRecordsStub is a placeholder for observer-05. Returns an empty array
// today so downstream consumers can start integrating without crashing.
func (s *Server) handleRecordsStub(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte("[]"))
}
