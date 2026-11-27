package httpserver

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/BaoTrung37/FinCore/internal/platform/metrics"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// HealthChecker verifies downstream readiness (e.g., PostgreSQL pool).
type HealthChecker interface {
	Ping(ctx context.Context) error
}

// RouterOptions configures optional HTTP hardening and observability middlewares.
type RouterOptions struct {
	CORSAllowedOrigins []string
	RateLimitRPS       float64
	RateLimitBurst     int
	Metrics            *metrics.Registry
}

// NewRouter builds a chi router with standard platform middlewares, /healthz, /metrics, and Swagger.
func NewRouter(log *slog.Logger, db HealthChecker) *chi.Mux {
	return NewRouterWithOptions(log, db, RouterOptions{
		CORSAllowedOrigins: []string{"*"},
		Metrics:            metrics.NewRegistry(),
	})
}

// NewRouterWithOptions builds a chi router with configurable CORS, rate limiting, and metrics.
func NewRouterWithOptions(log *slog.Logger, db HealthChecker, opts RouterOptions) *chi.Mux {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	if len(opts.CORSAllowedOrigins) > 0 {
		r.Use(CORS(opts.CORSAllowedOrigins))
	}
	r.Use(RequestLogger(log))
	if opts.Metrics != nil {
		r.Use(MetricsMiddleware(opts.Metrics))
	}
	if opts.RateLimitRPS > 0 && opts.RateLimitBurst > 0 {
		r.Use(RateLimit(opts.RateLimitRPS, opts.RateLimitBurst))
	}
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

	if opts.Metrics != nil {
		r.Handle("/metrics", opts.Metrics.Handler())
	}

	RegisterSwagger(r)

	return r
}
