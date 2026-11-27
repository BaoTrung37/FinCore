package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakePoolStats struct {
	total    int32
	acquired int32
	idle     int32
}

func (f fakePoolStats) TotalConns() int32    { return f.total }
func (f fakePoolStats) AcquiredConns() int32 { return f.acquired }
func (f fakePoolStats) IdleConns() int32     { return f.idle }

func TestRegistry_ObserveAndRender(t *testing.T) {
	reg := NewRegistry()
	reg.SetPoolStatProvider(fakePoolStats{total: 10, acquired: 3, idle: 7})

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reg.ObserveHTTPRequest(http.MethodGet, "/api/v1/accounts", http.StatusOK, 15*time.Millisecond)
		}()
	}
	wg.Wait()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	reg.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Fatalf("unexpected content-type %q", ct)
	}

	body := rec.Body.String()
	for _, want := range []string{
		"fincore_process_uptime_seconds",
		"fincore_db_pool_total_conns 10",
		"fincore_db_pool_acquired_conns 3",
		"fincore_db_pool_idle_conns 7",
		`fincore_http_requests_total{method="GET",route="/api/v1/accounts",status="200"} 20`,
		`fincore_http_request_duration_seconds_count{route="/api/v1/accounts"} 20`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("expected metrics output to contain %q, got:\n%s", want, body)
		}
	}
}
