package router

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	apiMiddleware "github.com/im-faix/sentinel/backend/internal/api/middleware"
	"github.com/im-faix/sentinel/backend/internal/config"
	"github.com/im-faix/sentinel/backend/internal/health"
	"github.com/im-faix/sentinel/backend/internal/metrics"
	"github.com/im-faix/sentinel/backend/internal/version"
)

type user struct {
	Email       string    `json:"email"`
	Role        string    `json:"role"`
	MasterAdmin bool      `json:"masterAdmin"`
	Expires     time.Time `json:"expires"`
}
type monitor struct {
	ID, Kind, Target string
	WarnDays         int `json:"warnDays"`
	Added            time.Time
}
type api struct {
	cfg        *config.Config
	mu         sync.RWMutex
	sessions   map[string]user
	monitors   []monitor
	alertMu    sync.Mutex
	lastAlerts map[string]time.Time
	loginMu    sync.Mutex
	loginFails map[string][]time.Time
	telemetry  *metrics.Registry
}

func New(cfg *config.Config) *chi.Mux {
	a := &api{cfg: cfg, sessions: map[string]user{}, lastAlerts: map[string]time.Time{}, loginFails: map[string][]time.Time{}, telemetry: metrics.New(), monitors: []monitor{{ID: "tls-example", Kind: "tls", Target: "example.com:443", WarnDays: 30, Added: time.Now()}, {ID: "dns-example", Kind: "dns", Target: "example.com", WarnDays: 30, Added: time.Now()}}}
	go a.alertLoop()
	r := chi.NewRouter()
	r.Use(apiMiddleware.Security)
	r.Use(apiMiddleware.RequestID)
	r.Use(a.telemetry.Middleware)
	r.Get("/health", health.Health)
	r.Get("/live", health.Live)
	r.Get("/ready", health.Ready)
	r.Get("/api/v1/version", version.Get)
	r.Get("/metrics", func(w http.ResponseWriter, req *http.Request) {
		a.telemetry.ServeHTTP(w, req, cfg.MetricsToken, cfg.MetricsTokenError)
	})
	r.Post("/api/v1/auth/login", a.login)
	r.Post("/api/v1/auth/logout", a.logout)
	r.Get("/api/v1/auth/me", a.require("viewer", a.me))
	r.Get("/api/v1/users", a.require("admin", a.listUsers))
	r.Post("/api/v1/users", a.require("admin", a.createUser))
	r.Delete("/api/v1/users/{email}", a.require("admin", a.deleteUser))
	r.Get("/api/v1/overview", a.require("viewer", a.overview))
	r.Get("/api/v1/monitors", a.require("viewer", a.listMonitors))
	r.Post("/api/v1/monitors", a.require("admin", a.addMonitor))
	r.Delete("/api/v1/monitors/{id}", a.require("admin", a.deleteMonitor))
	r.Get("/api/v1/kubernetes", a.require("viewer", a.kubernetes))
	r.Get("/api/v1/metrics", a.require("viewer", a.metrics))
	r.Get("/*", func(w http.ResponseWriter, _ *http.Request) {
		b, err := os.ReadFile(cfg.StaticFile)
		if err != nil {
			http.Error(w, "dashboard asset unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(b)
	})
	return r
}
func write(w http.ResponseWriter, s int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(s)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}
func token() string { b := make([]byte, 32); _, _ = rand.Read(b); return hex.EncodeToString(b) }
func (a *api) login(w http.ResponseWriter, r *http.Request) {
	if !a.loginAllowed(r) {
		write(w, http.StatusTooManyRequests, map[string]string{"error": "too many login attempts; try again later"})
		return
	}
	var in struct{ Email, Password string }
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		write(w, 401, map[string]string{"error": "invalid credentials"})
		return
	}
	var account *config.Account
	a.mu.RLock()
	for i := range a.cfg.Users {
		candidate := &a.cfg.Users[i]
		if subtle.ConstantTimeCompare([]byte(strings.ToLower(strings.TrimSpace(in.Email))), []byte(candidate.Email)) == 1 && subtle.ConstantTimeCompare([]byte(in.Password), []byte(candidate.Password)) == 1 {
			account = candidate
			break
		}
	}
	a.mu.RUnlock()
	if account == nil {
		a.recordLoginFailure(r)
		write(w, 401, map[string]string{"error": "invalid credentials"})
		return
	}
	t := token()
	a.mu.Lock()
	a.sessions[t] = user{Email: account.Email, Role: account.Role, MasterAdmin: strings.EqualFold(account.Email, a.cfg.AdminEmail) && account.Role == "admin", Expires: time.Now().Add(8 * time.Hour)}
	a.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "sentinel_session", Value: t, Path: "/", HttpOnly: true, Secure: a.cfg.CookieSecure, SameSite: http.SameSiteStrictMode, MaxAge: 28800})
	write(w, 200, map[string]string{"email": account.Email, "role": account.Role})
}
func (a *api) loginAllowed(r *http.Request) bool {
	now := time.Now()
	key, _, _ := net.SplitHostPort(r.RemoteAddr)
	if key == "" {
		key = r.RemoteAddr
	}
	a.loginMu.Lock()
	defer a.loginMu.Unlock()
	kept := a.loginFails[key][:0]
	for _, at := range a.loginFails[key] {
		if now.Sub(at) < 15*time.Minute {
			kept = append(kept, at)
		}
	}
	a.loginFails[key] = kept
	return len(kept) < 10
}
func (a *api) recordLoginFailure(r *http.Request) {
	key, _, _ := net.SplitHostPort(r.RemoteAddr)
	if key == "" {
		key = r.RemoteAddr
	}
	a.loginMu.Lock()
	a.loginFails[key] = append(a.loginFails[key], time.Now())
	a.loginMu.Unlock()
}
func (a *api) logout(w http.ResponseWriter, r *http.Request) {
	if c, e := r.Cookie("sentinel_session"); e == nil {
		a.mu.Lock()
		delete(a.sessions, c.Value)
		a.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: "sentinel_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: a.cfg.CookieSecure, SameSite: http.SameSiteStrictMode})
	write(w, 204, nil)
}
func (a *api) require(role string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, e := r.Cookie("sentinel_session")
		if e != nil {
			write(w, 401, map[string]string{"error": "authentication required"})
			return
		}
		a.mu.RLock()
		u, ok := a.sessions[c.Value]
		a.mu.RUnlock()
		if !ok || time.Now().After(u.Expires) {
			write(w, 401, map[string]string{"error": "session expired"})
			return
		}
		if role == "admin" && u.Role != "admin" {
			write(w, 403, map[string]string{"error": "insufficient role"})
			return
		}
		if role == "operator" && u.Role == "viewer" {
			write(w, 403, map[string]string{"error": "insufficient role"})
			return
		}
		next(w, r)
	}
}
func (a *api) me(w http.ResponseWriter, r *http.Request) {
	c, _ := r.Cookie("sentinel_session")
	a.mu.RLock()
	u := a.sessions[c.Value]
	a.mu.RUnlock()
	write(w, 200, u)
}
func (a *api) listUsers(w http.ResponseWriter, _ *http.Request) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	users := make([]map[string]any, 0, len(a.cfg.Users))
	for _, account := range a.cfg.Users {
		users = append(users, map[string]any{"email": account.Email, "role": account.Role, "masterAdmin": strings.EqualFold(account.Email, a.cfg.AdminEmail) && account.Role == "admin"})
	}
	write(w, http.StatusOK, users)
}
func (a *api) createUser(w http.ResponseWriter, r *http.Request) {
	var in config.Account
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		write(w, http.StatusBadRequest, map[string]string{"error": "invalid user payload"})
		return
	}
	in.Email = strings.TrimSpace(strings.ToLower(in.Email))
	in.Role = strings.TrimSpace(strings.ToLower(in.Role))
	if in.Email == "" || in.Password == "" || (in.Role != "admin" && in.Role != "viewer") {
		write(w, http.StatusBadRequest, map[string]string{"error": "email, password, and role (admin or viewer) are required"})
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, account := range a.cfg.Users {
		if strings.EqualFold(account.Email, in.Email) {
			write(w, http.StatusConflict, map[string]string{"error": "user already exists"})
			return
		}
	}
	a.cfg.Users = append(a.cfg.Users, in)
	write(w, http.StatusCreated, map[string]any{"email": in.Email, "role": in.Role, "masterAdmin": false})
}
func (a *api) deleteUser(w http.ResponseWriter, r *http.Request) {
	email, _ := url.PathUnescape(chi.URLParam(r, "email"))
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || strings.EqualFold(email, a.cfg.AdminEmail) {
		write(w, http.StatusBadRequest, map[string]string{"error": "the master admin cannot be removed"})
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for i, account := range a.cfg.Users {
		if strings.EqualFold(account.Email, email) {
			a.cfg.Users = append(a.cfg.Users[:i], a.cfg.Users[i+1:]...)
			write(w, http.StatusNoContent, nil)
			return
		}
	}
	write(w, http.StatusNotFound, map[string]string{"error": "user not found"})
}
func (a *api) listMonitors(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	write(w, 200, a.monitors)
}
func (a *api) addMonitor(w http.ResponseWriter, r *http.Request) {
	var m monitor
	if json.NewDecoder(r.Body).Decode(&m) != nil || !(m.Kind == "tls" || m.Kind == "dns" || m.Kind == "domain") || strings.TrimSpace(m.Target) == "" {
		write(w, 400, map[string]string{"error": "kind must be tls, dns, or domain; target is required"})
		return
	}
	m.ID = token()[:12]
	m.Added = time.Now()
	if m.WarnDays == 0 {
		m.WarnDays = 30
	}
	a.mu.Lock()
	a.monitors = append(a.monitors, m)
	a.mu.Unlock()
	write(w, 201, m)
}
func (a *api) deleteMonitor(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	a.mu.Lock()
	defer a.mu.Unlock()
	for i, m := range a.monitors {
		if m.ID == id {
			a.monitors = append(a.monitors[:i], a.monitors[i+1:]...)
			write(w, 204, nil)
			return
		}
	}
	write(w, 404, map[string]string{"error": "monitor not found"})
}
func (a *api) overview(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	list := append([]monitor(nil), a.monitors...)
	a.mu.RUnlock()
	out := make([]any, 0, len(list))
	bad := 0
	for _, m := range list {
		x := checkMonitor(r.Context(), m)
		if x["status"] != "ok" {
			bad++
		}
		out = append(out, x)
	}
	write(w, 200, map[string]any{"checkedAt": time.Now(), "total": len(out), "attention": bad, "results": out})
}
func checkMonitor(ctx context.Context, m monitor) map[string]any {
	if m.Kind == "tls" {
		return tlsCheck(m)
	}
	if m.Kind == "dns" {
		return dnsCheck(m)
	}
	return domainCheck(ctx, m)
}
func tlsCheck(m monitor) map[string]any {
	target := m.Target
	if !strings.Contains(target, ":") {
		target += ":443"
	}
	host, _, _ := net.SplitHostPort(target)
	d := net.Dialer{Timeout: 6 * time.Second}
	c, e := tls.DialWithDialer(&d, "tcp", target, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	if e != nil {
		return map[string]any{"id": m.ID, "kind": m.Kind, "target": m.Target, "status": "critical", "message": e.Error()}
	}
	defer c.Close()
	cert := c.ConnectionState().PeerCertificates[0]
	days := int(time.Until(cert.NotAfter).Hours() / 24)
	s := "ok"
	if days <= m.WarnDays {
		s = "warning"
	}
	if days < 0 {
		s = "critical"
	}
	return map[string]any{"id": m.ID, "kind": m.Kind, "target": m.Target, "status": s, "expiresAt": cert.NotAfter, "daysRemaining": days, "issuer": cert.Issuer.CommonName}
}
func dnsCheck(m monitor) map[string]any {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	x, e := net.DefaultResolver.LookupHost(ctx, m.Target)
	if e != nil {
		return map[string]any{"id": m.ID, "kind": m.Kind, "target": m.Target, "status": "critical", "message": e.Error()}
	}
	return map[string]any{"id": m.ID, "kind": m.Kind, "target": m.Target, "status": "ok", "records": x}
}
func domainCheck(ctx context.Context, m monitor) map[string]any {
	d := strings.TrimPrefix(strings.TrimPrefix(m.Target, "https://"), "http://")
	d = strings.Split(d, "/")[0]
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://rdap.org/domain/"+url.PathEscape(d), nil)
	res, e := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if e != nil {
		return map[string]any{"id": m.ID, "kind": m.Kind, "target": m.Target, "status": "unknown", "message": e.Error()}
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return map[string]any{"id": m.ID, "kind": m.Kind, "target": m.Target, "status": "unknown", "message": fmt.Sprintf("RDAP returned %d", res.StatusCode)}
	}
	var raw struct {
		Events []struct {
			EventAction string `json:"eventAction"`
			EventDate   string `json:"eventDate"`
		} `json:"events"`
	}
	if json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&raw) != nil {
		return map[string]any{"id": m.ID, "kind": m.Kind, "target": m.Target, "status": "unknown"}
	}
	for _, e := range raw.Events {
		if e.EventAction == "expiration" {
			if t, er := time.Parse(time.RFC3339, e.EventDate); er == nil {
				days := int(time.Until(t).Hours() / 24)
				s := "ok"
				if days <= m.WarnDays {
					s = "warning"
				}
				if days < 0 {
					s = "critical"
				}
				return map[string]any{"id": m.ID, "kind": m.Kind, "target": m.Target, "status": s, "expiresAt": t, "daysRemaining": days}
			}
		}
	}
	return map[string]any{"id": m.ID, "kind": m.Kind, "target": m.Target, "status": "unknown", "message": "Registry did not provide an expiry date"}
}
func (a *api) kubernetes(w http.ResponseWriter, r *http.Request) {
	if len(a.cfg.KubernetesClusters) == 0 {
		write(w, 200, map[string]any{"configured": false, "message": "Set KUBERNETES_API_URL and KUBERNETES_TOKEN to enable cluster inventory."})
		return
	}
	clusters := make([]any, 0, len(a.cfg.KubernetesClusters))
	for _, cluster := range a.cfg.KubernetesClusters {
		clusters = append(clusters, inspectCluster(r.Context(), cluster))
	}
	write(w, 200, map[string]any{"configured": true, "clusters": clusters})
}

func inspectCluster(ctx context.Context, cluster config.Cluster) map[string]any {
	rootCAs, _ := x509.SystemCertPool()
	if rootCAs == nil {
		rootCAs = x509.NewCertPool()
	}
	if ca, err := os.ReadFile(cluster.CA); err == nil {
		rootCAs.AppendCertsFromPEM(ca)
	}
	client := http.Client{Timeout: 8 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: rootCAs}}}
	get := func(path string, out any) (int, error) {
		req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimSuffix(cluster.API, "/")+path, nil)
		if err != nil {
			return 0, err
		}
		req.Header.Set("Authorization", "Bearer "+cluster.Token)
		res, err := client.Do(req)
		if err != nil {
			return 0, err
		}
		defer res.Body.Close()
		if res.StatusCode >= 300 {
			return res.StatusCode, fmt.Errorf("Kubernetes API returned %d", res.StatusCode)
		}
		return res.StatusCode, json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(out)
	}
	var nodes struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Status struct {
				Capacity map[string]string `json:"capacity"`
			} `json:"status"`
		} `json:"items"`
	}
	if _, err := get("/api/v1/nodes", &nodes); err != nil {
		return map[string]any{"name": cluster.Name, "status": "error", "message": err.Error()}
	}
	var metrics struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Usage map[string]string `json:"usage"`
		} `json:"items"`
	}
	_, metricsErr := get("/apis/metrics.k8s.io/v1beta1/nodes", &metrics)
	byName := map[string]map[string]string{}
	for _, item := range metrics.Items {
		byName[item.Metadata.Name] = item.Usage
	}
	result := make([]map[string]any, 0, len(nodes.Items))
	for _, node := range nodes.Items {
		usage := byName[node.Metadata.Name]
		var stats struct {
			Node struct {
				FS struct {
					Available uint64 `json:"availableBytes"`
					Capacity  uint64 `json:"capacityBytes"`
				} `json:"fs"`
			} `json:"node"`
		}
		_, _ = get("/api/v1/nodes/"+url.PathEscape(node.Metadata.Name)+"/proxy/stats/summary", &stats)
		diskUsedPercent := float64(0)
		if stats.Node.FS.Capacity > 0 {
			diskUsedPercent = float64(stats.Node.FS.Capacity-stats.Node.FS.Available) / float64(stats.Node.FS.Capacity) * 100
		}
		result = append(result, map[string]any{"name": node.Metadata.Name, "cpu": usage["cpu"], "memory": usage["memory"], "diskCapacity": node.Status.Capacity["ephemeral-storage"], "diskUsedPercent": diskUsedPercent, "metricsAvailable": usage != nil})
	}
	status := "ok"
	if metricsErr != nil {
		status = "degraded"
	}
	return map[string]any{"name": cluster.Name, "status": status, "nodes": result, "nodeCount": len(result), "metricsError": errorText(metricsErr)}
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (a *api) alertLoop() {
	interval := time.Duration(a.cfg.AlertIntervalMinutes) * time.Minute
	if interval <= 0 {
		interval = 15 * time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	a.evaluateAlerts()
	for range ticker.C {
		a.evaluateAlerts()
	}
}

func (a *api) evaluateAlerts() {
	if a.cfg.SMTPHost == "" || a.cfg.SMTPFrom == "" || a.cfg.SMTPTo == "" {
		return
	}
	a.mu.RLock()
	monitors := append([]monitor(nil), a.monitors...)
	a.mu.RUnlock()
	for _, m := range monitors {
		if (m.Kind == "tls" && !a.cfg.AlertTLS) || (m.Kind == "dns" && !a.cfg.AlertDNS) || (m.Kind == "domain" && !a.cfg.AlertDomain) {
			continue
		}
		result := checkMonitor(context.Background(), m)
		status, _ := result["status"].(string)
		if status == "ok" || status == "unknown" {
			continue
		}
		key := m.ID + ":" + status
		a.alertMu.Lock()
		last := a.lastAlerts[key]
		a.alertMu.Unlock()
		if time.Since(last) < 24*time.Hour {
			continue
		}
		subject := fmt.Sprintf("Sentinel alert: %s %s", m.Kind, m.Target)
		body := fmt.Sprintf("Monitor: %s\nTarget: %s\nStatus: %s\n", m.Kind, m.Target, status)
		if days, ok := result["daysRemaining"].(int); ok {
			body += fmt.Sprintf("Days remaining: %d\n", days)
		}
		if expires, ok := result["expiresAt"].(time.Time); ok {
			body += fmt.Sprintf("Expires: %s\n", expires.Format(time.RFC3339))
		}
		if message, ok := result["message"].(string); ok {
			body += "Details: " + message + "\n"
		}
		if err := sendSMTPAlert(a.cfg, subject, body); err == nil {
			a.alertMu.Lock()
			a.lastAlerts[key] = time.Now()
			a.alertMu.Unlock()
		}
	}
}

func sendSMTPAlert(cfg *config.Config, subject, body string) error {
	host := cfg.SMTPHost
	addr := net.JoinHostPort(host, cfg.SMTPPort)
	tlsName := cfg.SMTPTLSServerName
	if tlsName == "" {
		tlsName = host
	}
	message := []byte("From: " + cfg.SMTPFrom + "\r\nTo: " + cfg.SMTPTo + "\r\nSubject: " + subject + "\r\n\r\n" + body)
	var client *smtp.Client
	var err error
	if cfg.SMTPTLSMode == "implicit" || cfg.SMTPPort == "465" {
		conn, dialErr := tls.Dial("tcp", addr, &tls.Config{ServerName: tlsName, MinVersion: tls.VersionTLS12})
		if dialErr != nil {
			return dialErr
		}
		client, err = smtp.NewClient(conn, host)
	} else if cfg.SMTPTLSMode == "none" {
		client, err = smtp.Dial(addr)
	} else {
		client, err = smtp.Dial(addr)
		if err == nil {
			err = client.StartTLS(&tls.Config{ServerName: tlsName, MinVersion: tls.VersionTLS12})
		}
	}
	if err != nil {
		return err
	}
	defer client.Close()
	if cfg.SMTPUsername != "" {
		if err = client.Auth(smtp.PlainAuth("", cfg.SMTPUsername, cfg.SMTPPassword, host)); err != nil {
			return err
		}
	}
	if err = client.Mail(cfg.SMTPFrom); err != nil {
		return err
	}
	for _, recipient := range strings.Split(cfg.SMTPTo, ",") {
		if recipient = strings.TrimSpace(recipient); recipient != "" {
			if err = client.Rcpt(recipient); err != nil {
				return err
			}
		}
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err = writer.Write(message); err != nil {
		_ = writer.Close()
		return err
	}
	return writer.Close()
}
