package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/hermes-apprentice/observer/internal/store"
)

type Config struct {
	Addr   string
	Logger *slog.Logger
	Store  *store.Store // optional during scaffold; /records returns [] when nil
}

type Server struct {
	cfg    Config
	srv    *http.Server
	logger *slog.Logger
	store  *store.Store
}

func New(cfg Config) *Server {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	mux := http.NewServeMux()
	s := &Server{cfg: cfg, logger: cfg.Logger, store: cfg.Store}
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /records", s.handleRecords)
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

// handleRecords serves GET /records?since=<epoch_seconds>&pattern=<id>&limit=<N>.
// Returns a JSON array sorted by created_at DESC, empty array if no matches.
// Without a store wired (early scaffold mode), returns [].
func (s *Server) handleRecords(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if s.store == nil {
		_, _ = w.Write([]byte("[]"))
		return
	}

	opts := store.QueryOpts{
		PatternID: r.URL.Query().Get("pattern"),
	}
	if v := r.URL.Query().Get("since"); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid `since`: must be a number (unix epoch seconds)")
			return
		}
		opts.SincePosix = f
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "invalid `limit`: must be a positive integer")
			return
		}
		if n > 1000 {
			n = 1000 // cap so a wide-open query can't blow up the response
		}
		opts.Limit = n
	}

	records, err := s.store.Query(r.Context(), opts)
	if err != nil {
		s.logger.Error("query records failed", "err", err)
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}

	// Always emit an array, never null, even when there are no rows.
	if records == nil {
		records = []store.Record{}
	}
	if err := json.NewEncoder(w).Encode(records); err != nil {
		s.logger.Error("encode records failed", "err", err)
	}
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
