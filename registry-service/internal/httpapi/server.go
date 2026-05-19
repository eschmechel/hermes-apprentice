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
	Addr        string
	Logger      *slog.Logger
	RegistryDir string
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
	rh := NewRegistryHandler(cfg.RegistryDir, cfg.Logger)
	rh.Register(mux)

	s.srv = &http.Server{
		Addr:              cfg.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	return s
}

func (s *Server) ListenAndServe(ctx context.Context) error {
	s.logger.Info("http listening", "addr", s.cfg.Addr)

	// Trigger graceful shutdown when the caller's context is cancelled
	// (typically via SIGINT/SIGTERM from signal.NotifyContext).
	go func() {
		<-ctx.Done()
		s.logger.Info("shutting down http server")
		_ = s.Shutdown(context.Background())
	}()

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

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
