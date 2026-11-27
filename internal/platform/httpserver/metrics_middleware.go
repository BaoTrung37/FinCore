package httpserver

import (
	"net/http"
	"time"

	"github.com/BaoTrung37/FinCore/internal/platform/metrics"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// MetricsMiddleware records HTTP request count and duration into reg using chi route patterns.
func MetricsMiddleware(reg *metrics.Registry) func(http.Handler) http.Handler {
	if reg == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/metrics" {
				next.ServeHTTP(w, r)
				return
			}
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)

			route := chi.RouteContext(r.Context()).RoutePattern()
			if route == "" {
				route = r.URL.Path
			}
			status := ww.Status()
			if status == 0 {
				status = http.StatusOK
			}
			reg.ObserveHTTPRequest(r.Method, route, status, time.Since(start))
		})
	}
}
