package metrics

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type requestKey struct {
	method string
	status int
}

type Registry struct {
	mu       sync.Mutex
	requests map[requestKey]uint64
	duration map[requestKey]time.Duration
	started  time.Time
}

func New() *Registry {
	return &Registry{
		requests: make(map[requestKey]uint64),
		duration: make(map[requestKey]time.Duration),
		started:  time.Now(),
	}
}

func (r *Registry) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		started := time.Now()
		recorder := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(recorder, req)
		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}
		method := req.Method
		switch method {
		case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
			http.MethodPatch, http.MethodDelete, http.MethodOptions:
		default:
			method = "OTHER"
		}
		key := requestKey{method: method, status: status}
		r.mu.Lock()
		r.requests[key]++
		r.duration[key] += time.Since(started)
		r.mu.Unlock()
	})
}

func (r *Registry) ServeHTTP(w http.ResponseWriter, req *http.Request, bearerToken, tokenError string) {
	if tokenError != "" {
		http.Error(w, "metrics endpoint is not configured correctly", http.StatusServiceUnavailable)
		return
	}
	if bearerToken == "" {
		http.NotFound(w, req)
		return
	}
	provided := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
	if provided == req.Header.Get("Authorization") ||
		subtle.ConstantTimeCompare([]byte(provided), []byte(bearerToken)) != 1 {
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	r.mu.Lock()
	requests := make(map[requestKey]uint64, len(r.requests))
	durations := make(map[requestKey]time.Duration, len(r.duration))
	for key, value := range r.requests {
		requests[key] = value
		durations[key] = r.duration[key]
	}
	r.mu.Unlock()

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	fmt.Fprintln(w, "# HELP sentinel_http_requests_total Total HTTP requests handled by Sentinel.")
	fmt.Fprintln(w, "# TYPE sentinel_http_requests_total counter")
	fmt.Fprintln(w, "# HELP sentinel_http_request_duration_seconds_sum Total HTTP request duration in seconds.")
	fmt.Fprintln(w, "# TYPE sentinel_http_request_duration_seconds_sum counter")
	fmt.Fprintln(w, "# HELP sentinel_http_request_duration_seconds_count Number of HTTP request durations recorded.")
	fmt.Fprintln(w, "# TYPE sentinel_http_request_duration_seconds_count counter")
	keys := make([]requestKey, 0, len(requests))
	for key := range requests {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].method == keys[j].method {
			return keys[i].status < keys[j].status
		}
		return keys[i].method < keys[j].method
	})
	for _, key := range keys {
		labels := fmt.Sprintf(`method="%s",status="%s"`, key.method, strconv.Itoa(key.status))
		fmt.Fprintf(w, "sentinel_http_requests_total{%s} %d\n", labels, requests[key])
		fmt.Fprintf(w, "sentinel_http_request_duration_seconds_sum{%s} %s\n", labels, strconv.FormatFloat(durations[key].Seconds(), 'f', 9, 64))
		fmt.Fprintf(w, "sentinel_http_request_duration_seconds_count{%s} %d\n", labels, requests[key])
	}

	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	fmt.Fprintln(w, "# HELP go_goroutines Number of goroutines that currently exist.")
	fmt.Fprintln(w, "# TYPE go_goroutines gauge")
	fmt.Fprintf(w, "go_goroutines %d\n", runtime.NumGoroutine())
	fmt.Fprintln(w, "# HELP go_memstats_heap_alloc_bytes Number of heap bytes allocated and still in use.")
	fmt.Fprintln(w, "# TYPE go_memstats_heap_alloc_bytes gauge")
	fmt.Fprintf(w, "go_memstats_heap_alloc_bytes %d\n", memory.HeapAlloc)
	fmt.Fprintln(w, "# HELP sentinel_process_uptime_seconds Time since the Sentinel process started.")
	fmt.Fprintln(w, "# TYPE sentinel_process_uptime_seconds gauge")
	fmt.Fprintf(w, "sentinel_process_uptime_seconds %.3f\n", time.Since(r.started).Seconds())
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(p)
}
