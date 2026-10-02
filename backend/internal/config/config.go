package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type Account struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

type Cluster struct {
	Name  string `json:"name"`
	API   string `json:"api"`
	Token string `json:"token"`
	CA    string `json:"caFile"`
}

type MetricsEnvironment struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Token  string `json:"token"`
	CAFile string `json:"caFile"`
}

type Config struct {
	AppName, AppVersion, Host, Port, LogLevel                                                        string
	AdminEmail, AdminPassword                                                                        string
	CookieSecure                                                                                     bool
	KubernetesAPI, KubernetesToken, KubernetesCAFile                                                 string
	KubernetesClusters                                                                               []Cluster
	MetricsToken, MetricsTokenFile, MetricsTokenError                                                string
	MetricsEnvironments                                                                              []MetricsEnvironment
	MetricsEnvironmentsError                                                                         string
	StaticFile                                                                                       string
	Users                                                                                            []Account
	SMTPHost, SMTPPort, SMTPUsername, SMTPPassword, SMTPFrom, SMTPTo, SMTPTLSMode, SMTPTLSServerName string
	AlertTLS, AlertDNS, AlertDomain                                                                  bool
	AlertIntervalMinutes                                                                             int
}

func Load() *Config {
	c := &Config{
		AppName: getEnv("APP_NAME", "Sentinel"), AppVersion: getEnv("APP_VERSION", "0.1.0"),
		Host: getEnv("HOST", "0.0.0.0"), Port: getEnv("PORT", "8080"), LogLevel: getEnv("LOG_LEVEL", "INFO"),
		AdminEmail: getEnv("ADMIN_EMAIL", "admin@sentinel.local"), AdminPassword: getEnv("ADMIN_PASSWORD", "change-me-before-production"),
		CookieSecure: getEnv("COOKIE_SECURE", "false") == "true",
		MetricsToken: os.Getenv("METRICS_TOKEN"), MetricsTokenFile: os.Getenv("METRICS_TOKEN_FILE"),
		KubernetesAPI: getEnv("KUBERNETES_API_URL", "https://kubernetes.default.svc"), KubernetesToken: os.Getenv("KUBERNETES_TOKEN"), KubernetesCAFile: getEnv("KUBERNETES_CA_FILE", "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"),
		StaticFile: getEnv("STATIC_FILE", "internal/api/router/dashboard-reference.html"),
		SMTPHost:   getEnv("SMTP_HOST", ""), SMTPPort: getEnv("SMTP_PORT", "587"),
		SMTPUsername: getEnv("SMTP_USERNAME", ""), SMTPPassword: getEnv("SMTP_PASSWORD", ""),
		SMTPFrom: getEnv("SMTP_FROM", ""), SMTPTo: getEnv("SMTP_TO", ""),
		SMTPTLSMode: getEnv("SMTP_TLS_MODE", "starttls"), SMTPTLSServerName: getEnv("SMTP_TLS_SERVER_NAME", ""),
		AlertTLS: getEnv("ALERT_TLS_ENABLED", "true") == "true", AlertDNS: getEnv("ALERT_DNS_ENABLED", "true") == "true", AlertDomain: getEnv("ALERT_DOMAIN_ENABLED", "true") == "true",
		AlertIntervalMinutes: 15,
	}
	c.MetricsToken = strings.TrimSpace(c.MetricsToken)
	if c.MetricsTokenFile != "" && c.MetricsToken == "" {
		token, err := os.ReadFile(c.MetricsTokenFile)
		if err != nil {
			c.MetricsTokenError = "unable to read metrics token file"
		} else {
			c.MetricsToken = strings.TrimSpace(string(token))
			if c.MetricsToken == "" {
				c.MetricsTokenError = "metrics token file is empty"
			}
		}
	}
	if len(c.MetricsToken) > 0 && len(c.MetricsToken) < 32 {
		c.MetricsTokenError = "metrics token must contain at least 32 characters"
		c.MetricsToken = ""
	}
	if raw := os.Getenv("PROMETHEUS_ENVIRONMENTS_JSON"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &c.MetricsEnvironments); err != nil || c.MetricsEnvironments == nil {
			c.MetricsEnvironmentsError = "PROMETHEUS_ENVIRONMENTS_JSON must be a valid JSON array"
		} else {
			seenNames := make(map[string]struct{}, len(c.MetricsEnvironments))
			for i := range c.MetricsEnvironments {
				c.MetricsEnvironments[i].Name = strings.TrimSpace(c.MetricsEnvironments[i].Name)
				c.MetricsEnvironments[i].URL = strings.TrimSpace(c.MetricsEnvironments[i].URL)
				c.MetricsEnvironments[i].Token = strings.TrimSpace(c.MetricsEnvironments[i].Token)
				nameKey := strings.ToLower(c.MetricsEnvironments[i].Name)
				if nameKey == "" || c.MetricsEnvironments[i].URL == "" ||
					(c.MetricsEnvironments[i].Token != "" && len(c.MetricsEnvironments[i].Token) < 32) {
					c.MetricsEnvironmentsError = "each Prometheus environment needs a name, URL, and a token of at least 32 characters when configured"
				}
				if _, exists := seenNames[nameKey]; exists {
					c.MetricsEnvironmentsError = "Prometheus environment names must be unique"
				}
				seenNames[nameKey] = struct{}{}
			}
		}
	}
	if raw := os.Getenv("KUBERNETES_CLUSTERS_JSON"); raw != "" {
		_ = json.Unmarshal([]byte(raw), &c.KubernetesClusters)
	}
	if len(c.KubernetesClusters) == 0 && c.KubernetesAPI != "" && c.KubernetesToken != "" {
		c.KubernetesClusters = []Cluster{{Name: "default", API: c.KubernetesAPI, Token: c.KubernetesToken, CA: c.KubernetesCAFile}}
	}
	if c.KubernetesToken == "" {
		if token, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/token"); err == nil {
			c.KubernetesToken = strings.TrimSpace(string(token))
		}
	}
	if v := os.Getenv("ALERT_INTERVAL_MINUTES"); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n > 0 {
			c.AlertIntervalMinutes = n
		}
	}
	if raw := os.Getenv("SENTINEL_USERS_JSON"); raw != "" {
		_ = json.Unmarshal([]byte(raw), &c.Users)
	}
	adminEmail := strings.TrimSpace(strings.ToLower(c.AdminEmail))
	adminPresent := false
	valid := c.Users[:0]
	for _, u := range c.Users {
		u.Email = strings.TrimSpace(strings.ToLower(u.Email))
		if u.Role == "admin" || u.Role == "operator" || u.Role == "viewer" {
			if u.Email != "" && u.Password != "" {
				valid = append(valid, u)
				if u.Email == adminEmail {
					adminPresent = true
				}
			}
		}
	}
	if !adminPresent && adminEmail != "" && c.AdminPassword != "" {
		valid = append(valid, Account{Email: adminEmail, Password: c.AdminPassword, Role: "admin"})
	}
	c.Users = valid
	return c
}

func (c *Config) Address() string { return c.Host + ":" + c.Port }

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok && value != "" {
		return value
	}
	return fallback
}
