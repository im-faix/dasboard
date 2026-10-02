package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/im-faix/sentinel/backend/internal/config"
)

func TestPrometheusEndpointValidation(t *testing.T) {
	tests := []struct {
		raw     string
		wantErr bool
	}{
		{raw: "https://prometheus.example.internal"},
		{raw: "http://prometheus:9090/prometheus"},
		{raw: "https://user:password@prometheus.example.internal", wantErr: true},
		{raw: "https://prometheus.example.internal?token=secret", wantErr: true},
		{raw: "file:///etc/passwd", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.raw, func(t *testing.T) {
			_, err := prometheusEndpoint(test.raw)
			if (err != nil) != test.wantErr {
				t.Fatalf("prometheusEndpoint(%q) error = %v, wantErr %v", test.raw, err, test.wantErr)
			}
		})
	}
}

func TestSecurePrometheusEndpoint(t *testing.T) {
	tests := []struct {
		raw   string
		token string
		ok    bool
	}{
		{raw: "http://prometheus:9090", ok: true},
		{raw: "http://127.0.0.1:9090", ok: true},
		{raw: "http://prometheus.example.internal:9090", ok: false},
		{raw: "http://prometheus:9090", token: strings.Repeat("x", 32), ok: false},
		{raw: "https://prometheus.example.internal", token: strings.Repeat("x", 32), ok: true},
		{raw: "https://prometheus.example.internal", ok: false},
	}
	for _, test := range tests {
		t.Run(test.raw+test.token, func(t *testing.T) {
			endpoint, err := url.Parse(test.raw)
			if err != nil {
				t.Fatal(err)
			}
			if got := securePrometheusEndpoint(endpoint, test.token); got != test.ok {
				t.Fatalf("securePrometheusEndpoint(%q) = %v, want %v", test.raw, got, test.ok)
			}
		})
	}
}

func TestQueryEnvironmentParsesMetricsAndPrometheusRequestsUseBearerToken(t *testing.T) {
	token := strings.Repeat("c", 32)
	bearerSeen := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer "+token {
			bearerSeen = true
		}
		labels := `{"instance":"node-1"}`
		value := "12.5"
		if strings.Contains(r.URL.Query().Get("query"), `job!~"sentinel|node"`) {
			labels = `{"job":"my-app","instance":"my-app:8080"}`
			if strings.HasPrefix(r.URL.Query().Get("query"), "up{") {
				value = "1"
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"result":[{"metric":` + labels + `,"value":[1,"` + value + `"]}]}}`))
	}))
	defer server.Close()

	result := queryEnvironment(context.Background(), config.MetricsEnvironment{
		Name: "lower",
		URL:  server.URL,
	})
	if result.Status != "ok" || len(result.Hosts) != 1 ||
		result.Hosts[0].CPUPercent == nil || *result.Hosts[0].CPUPercent != 12.5 {
		t.Fatalf("expected parsed host metrics, got %#v", result)
	}
	if len(result.Applications) != 1 || result.Applications[0].Job != "my-app" ||
		result.Applications[0].Status != "ok" || result.Applications[0].CPUCoreRate == nil {
		t.Fatalf("expected parsed application target metrics, got %#v", result.Applications)
	}

	samples, err := queryPrometheus(context.Background(), server.Client(), server.URL+"/api/v1/query", token, "up")
	if err != nil {
		t.Fatalf("queryPrometheus returned an error: %v", err)
	}
	if got := sampleValue(samples[0]); got == nil || *got != 12.5 {
		t.Fatalf("expected parsed Prometheus sample 12.5, got %v", got)
	}
	if !bearerSeen {
		t.Fatal("expected configured bearer token on Prometheus request")
	}
}
