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

type Config struct {
	AppName, AppVersion, Host, Port, LogLevel                        string
	AdminEmail, AdminPassword                                        string
	CookieSecure                                                     bool
	KubernetesAPI, KubernetesToken, KubernetesCAFile                 string
	StaticFile                                                       string
	Users                                                            []Account
	SMTPHost, SMTPPort, SMTPUsername, SMTPPassword, SMTPFrom, SMTPTo string
	AlertIntervalMinutes                                             int
}

func Load() *Config {
	c := &Config{
		AppName: getEnv("APP_NAME", "Sentinel"), AppVersion: getEnv("APP_VERSION", "0.1.0"),
		Host: getEnv("HOST", "0.0.0.0"), Port: getEnv("PORT", "8080"), LogLevel: getEnv("LOG_LEVEL", "INFO"),
		AdminEmail: getEnv("ADMIN_EMAIL", "admin@sentinel.local"), AdminPassword: getEnv("ADMIN_PASSWORD", "change-me-before-production"),
		CookieSecure:  getEnv("COOKIE_SECURE", "false") == "true",
		KubernetesAPI: getEnv("KUBERNETES_API_URL", "https://kubernetes.default.svc"), KubernetesToken: os.Getenv("KUBERNETES_TOKEN"), KubernetesCAFile: getEnv("KUBERNETES_CA_FILE", "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"),
		StaticFile: getEnv("STATIC_FILE", "internal/api/router/index.html"),
		SMTPHost:   getEnv("SMTP_HOST", ""), SMTPPort: getEnv("SMTP_PORT", "587"),
		SMTPUsername: getEnv("SMTP_USERNAME", ""), SMTPPassword: getEnv("SMTP_PASSWORD", ""),
		SMTPFrom: getEnv("SMTP_FROM", ""), SMTPTo: getEnv("SMTP_TO", ""),
		AlertIntervalMinutes: 15,
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
