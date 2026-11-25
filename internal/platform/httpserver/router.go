package httpserver

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// HealthChecker verifies downstream readiness (e.g., PostgreSQL pool).
type HealthChecker interface {
	Ping(ctx context.Context) error
}

// NewRouter builds a chi router with standard platform middlewares and /healthz.
func NewRouter(log *slog.Logger, db HealthChecker) *chi.Mux {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(RequestLogger(log))
	r.Use(middleware.Recoverer)

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		if db == nil {
			WriteError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "database not configured")
			return
		}
		if err := db.Ping(ctx); err != nil {
			WriteError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "database unreachable")
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	return r
}
