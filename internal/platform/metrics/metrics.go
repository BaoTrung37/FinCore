package metrics

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// PoolStatProvider exposes PostgreSQL connection pool metrics without coupling to pgxpool directly.
type PoolStatProvider interface {
	TotalConns() int32
	AcquiredConns() int32
	IdleConns() int32
}

var defaultBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0}

type reqKey struct {
	method string
	route  string
	status int
}

type histData struct {
	buckets []uint64
	sum     float64
	count   uint64
}

// Registry collects thread-safe application metrics and renders Prometheus exposition format (0.0.4).
type Registry struct {
	mu        sync.RWMutex
	startedAt time.Time
	requests  map[reqKey]uint64
	durations map[string]*histData // keyed by route
	poolStats PoolStatProvider
}

// NewRegistry creates an empty metrics Registry.
func NewRegistry() *Registry {
	return &Registry{
		startedAt: time.Now(),
		requests:  make(map[reqKey]uint64),
		durations: make(map[string]*histData),
	}
}

// SetPoolStatProvider attaches an optional DB pool stat provider.
func (r *Registry) SetPoolStatProvider(p PoolStatProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.poolStats = p
}

// ObserveHTTPRequest records one completed HTTP request.
func (r *Registry) ObserveHTTPRequest(method, route string, status int, duration time.Duration) {
	if route == "" {
		route = "unknown"
	}
	secs := duration.Seconds()

	r.mu.Lock()
	defer r.mu.Unlock()

	r.requests[reqKey{method: method, route: route, status: status}]++

	h, ok := r.durations[route]
	if !ok {
		h = &histData{buckets: make([]uint64, len(defaultBuckets))}
		r.durations[route] = h
	}
	h.count++
	h.sum += secs
	for i, bound := range defaultBuckets {
		if secs <= bound {
			h.buckets[i]++
		}
	}
}

// Handler returns an http.Handler serving metrics in Prometheus text exposition format.
func (r *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(r.Render()))
	})
}

// Render formats all collected metrics as Prometheus 0.0.4 text.
func (r *Registry) Render() string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var b strings.Builder

	b.WriteString("# HELP fincore_process_uptime_seconds Seconds since process start.\n")
	b.WriteString("# TYPE fincore_process_uptime_seconds gauge\n")
	fmt.Fprintf(&b, "fincore_process_uptime_seconds %.3f\n", time.Since(r.startedAt).Seconds())

	if r.poolStats != nil {
		b.WriteString("# HELP fincore_db_pool_total_conns Total connections in PostgreSQL pool.\n")
		b.WriteString("# TYPE fincore_db_pool_total_conns gauge\n")
		fmt.Fprintf(&b, "fincore_db_pool_total_conns %d\n", r.poolStats.TotalConns())

		b.WriteString("# HELP fincore_db_pool_acquired_conns Currently acquired connections in PostgreSQL pool.\n")
		b.WriteString("# TYPE fincore_db_pool_acquired_conns gauge\n")
		fmt.Fprintf(&b, "fincore_db_pool_acquired_conns %d\n", r.poolStats.AcquiredConns())

		b.WriteString("# HELP fincore_db_pool_idle_conns Currently idle connections in PostgreSQL pool.\n")
		b.WriteString("# TYPE fincore_db_pool_idle_conns gauge\n")
		fmt.Fprintf(&b, "fincore_db_pool_idle_conns %d\n", r.poolStats.IdleConns())
	}

	if len(r.requests) > 0 {
		b.WriteString("# HELP fincore_http_requests_total Total HTTP requests handled.\n")
		b.WriteString("# TYPE fincore_http_requests_total counter\n")

		keys := make([]reqKey, 0, len(r.requests))
		for k := range r.requests {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			if keys[i].route != keys[j].route {
				return keys[i].route < keys[j].route
			}
			if keys[i].method != keys[j].method {
				return keys[i].method < keys[j].method
			}
			return keys[i].status < keys[j].status
		})
		for _, k := range keys {
			fmt.Fprintf(&b, "fincore_http_requests_total{method=%q,route=%q,status=%q} %d\n",
				k.method, k.route, strconv.Itoa(k.status), r.requests[k])
		}
	}

	if len(r.durations) > 0 {
		b.WriteString("# HELP fincore_http_request_duration_seconds HTTP request latency distribution.\n")
		b.WriteString("# TYPE fincore_http_request_duration_seconds histogram\n")

		routes := make([]string, 0, len(r.durations))
		for route := range r.durations {
			routes = append(routes, route)
		}
		sort.Strings(routes)
		for _, route := range routes {
			h := r.durations[route]
			for i, bound := range defaultBuckets {
				fmt.Fprintf(&b, "fincore_http_request_duration_seconds_bucket{route=%q,le=%q} %d\n",
					route, formatFloat(bound), h.buckets[i])
			}
			fmt.Fprintf(&b, "fincore_http_request_duration_seconds_bucket{route=%q,le=\"+Inf\"} %d\n", route, h.count)
			fmt.Fprintf(&b, "fincore_http_request_duration_seconds_sum{route=%q} %s\n", route, formatFloat(h.sum))
			fmt.Fprintf(&b, "fincore_http_request_duration_seconds_count{route=%q} %d\n", route, h.count)
		}
	}

	return b.String()
}

func formatFloat(v float64) string {
	if math.IsInf(v, 1) {
		return "+Inf"
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}
