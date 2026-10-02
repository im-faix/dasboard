package config

import (
	"strings"
	"testing"
)

func TestLoadPrometheusEnvironments(t *testing.T) {
	token := strings.Repeat("x", 32)
	t.Setenv("PROMETHEUS_ENVIRONMENTS_JSON", `[{"name":" lower ","url":"http://prometheus:9090"},{"name":"production","url":"https://prometheus.example.internal","token":"`+token+`"}]`)

	cfg := Load()
	if cfg.MetricsEnvironmentsError != "" {
		t.Fatalf("unexpected configuration error: %s", cfg.MetricsEnvironmentsError)
	}
	if len(cfg.MetricsEnvironments) != 2 || cfg.MetricsEnvironments[0].Name != "lower" {
		t.Fatalf("expected normalized lower and production environments, got %#v", cfg.MetricsEnvironments)
	}
	if cfg.MetricsEnvironments[1].Token != token {
		t.Fatal("expected production bearer token to be retained for server-side use")
	}
}

func TestLoadRejectsInvalidPrometheusEnvironmentConfiguration(t *testing.T) {
	t.Setenv("PROMETHEUS_ENVIRONMENTS_JSON", `null`)
	if cfg := Load(); cfg.MetricsEnvironmentsError == "" {
		t.Fatal("expected null to be rejected instead of treated as an empty environment list")
	}

	t.Setenv("PROMETHEUS_ENVIRONMENTS_JSON", `[{"name":"production","url":"https://prometheus.example.internal","token":"short"}]`)
	if cfg := Load(); cfg.MetricsEnvironmentsError == "" {
		t.Fatal("expected short bearer tokens to be rejected")
	}
}

func TestLoadAcceptsAdminOperatorAndViewerUsers(t *testing.T) {
	t.Setenv("ADMIN_EMAIL", "admin@example.com")
	t.Setenv("ADMIN_PASSWORD", "admin-password")
	t.Setenv("SENTINEL_USERS_JSON", `[{"email":"admin@example.com","password":"admin-password","role":"admin"},{"email":"operator@example.com","password":"operator-password","role":"operator"},{"email":"viewer@example.com","password":"viewer-password","role":"viewer"}]`)

	cfg := Load()
	if len(cfg.Users) != 3 {
		t.Fatalf("expected all three supported roles, got %#v", cfg.Users)
	}
	if cfg.Users[1].Role != "operator" {
		t.Fatalf("expected operator role to be retained, got %q", cfg.Users[1].Role)
	}
}
