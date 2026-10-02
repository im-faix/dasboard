package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMetricsEndpointRequiresBearerToken(t *testing.T) {
	registry := New()
	token := strings.Repeat("a", 32)

	unauthorized := httptest.NewRecorder()
	registry.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/metrics", nil), token, "")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized response, got %d", unauthorized.Code)
	}

	authorizedRequest := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	authorizedRequest.Header.Set("Authorization", "Bearer "+token)
	authorized := httptest.NewRecorder()
	registry.ServeHTTP(authorized, authorizedRequest, token, "")
	if authorized.Code != http.StatusOK {
		t.Fatalf("expected successful scrape, got %d", authorized.Code)
	}
	if !strings.Contains(authorized.Body.String(), "go_goroutines ") {
		t.Fatal("expected Go runtime metrics in scrape response")
	}
}

func TestMetricsEndpointDisabledWithoutToken(t *testing.T) {
	registry := New()
	response := httptest.NewRecorder()
	registry.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil), "", "")
	if response.Code != http.StatusNotFound {
		t.Fatalf("expected hidden endpoint when no token is configured, got %d", response.Code)
	}
}

func TestMiddlewareRecordsHTTPRequestMetrics(t *testing.T) {
	registry := New()
	handler := registry.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("CUSTOM", "/", nil))

	scrapeRequest := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	scrapeRequest.Header.Set("Authorization", "Bearer "+strings.Repeat("b", 32))
	scrape := httptest.NewRecorder()
	registry.ServeHTTP(scrape, scrapeRequest, strings.Repeat("b", 32), "")
	if !strings.Contains(scrape.Body.String(), `method="OTHER",status="201"`) {
		t.Fatal("expected bounded method and response status labels")
	}
}
