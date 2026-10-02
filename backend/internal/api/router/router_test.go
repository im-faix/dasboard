package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/im-faix/sentinel/backend/internal/config"
)

func TestRolePermissions(t *testing.T) {
	tests := []struct {
		name       string
		role       string
		wantStatus int
	}{
		{name: "admin can manage monitors", role: "admin", wantStatus: http.StatusNoContent},
		{name: "operator can manage monitors", role: "operator", wantStatus: http.StatusNoContent},
		{name: "viewer cannot manage monitors", role: "viewer", wantStatus: http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &api{
				sessions: map[string]user{
					"session": {Email: tt.role + "@example.com", Role: tt.role, Expires: time.Now().Add(time.Hour)},
				},
			}
			handler := a.require("operator", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			})
			req := httptest.NewRequest(http.MethodPost, "/api/v1/monitors", nil)
			req.AddCookie(&http.Cookie{Name: "sentinel_session", Value: "session"})
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("expected status %d, got %d: %s", tt.wantStatus, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestRolePermissionsAcrossRoutes(t *testing.T) {
	cfg := &config.Config{
		AdminEmail:           "admin@example.com",
		AlertIntervalMinutes: 15,
		Users: []config.Account{
			{Email: "admin@example.com", Password: "admin-password", Role: "admin"},
			{Email: "operator@example.com", Password: "operator-password", Role: "operator"},
			{Email: "viewer@example.com", Password: "viewer-password", Role: "viewer"},
		},
	}
	handler := New(cfg)
	tests := []struct {
		email           string
		password        string
		monitorStatus   int
		usersListStatus int
	}{
		{email: "admin@example.com", password: "admin-password", monitorStatus: http.StatusCreated, usersListStatus: http.StatusOK},
		{email: "operator@example.com", password: "operator-password", monitorStatus: http.StatusCreated, usersListStatus: http.StatusForbidden},
		{email: "viewer@example.com", password: "viewer-password", monitorStatus: http.StatusForbidden, usersListStatus: http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.email, func(t *testing.T) {
			loginBody := `{"email":"` + tt.email + `","password":"` + tt.password + `"}`
			loginRequest := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(loginBody))
			loginResponse := httptest.NewRecorder()
			handler.ServeHTTP(loginResponse, loginRequest)
			if loginResponse.Code != http.StatusOK {
				t.Fatalf("login status = %d, want %d", loginResponse.Code, http.StatusOK)
			}
			cookies := loginResponse.Result().Cookies()

			monitorRequest := httptest.NewRequest(http.MethodPost, "/api/v1/monitors", strings.NewReader(`{"kind":"tls","target":"example.test:443"}`))
			monitorRequest.AddCookie(cookies[0])
			monitorResponse := httptest.NewRecorder()
			handler.ServeHTTP(monitorResponse, monitorRequest)
			if monitorResponse.Code != tt.monitorStatus {
				t.Fatalf("monitor creation status = %d, want %d: %s", monitorResponse.Code, tt.monitorStatus, monitorResponse.Body.String())
			}

			usersRequest := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
			usersRequest.AddCookie(cookies[0])
			usersResponse := httptest.NewRecorder()
			handler.ServeHTTP(usersResponse, usersRequest)
			if usersResponse.Code != tt.usersListStatus {
				t.Fatalf("user list status = %d, want %d: %s", usersResponse.Code, tt.usersListStatus, usersResponse.Body.String())
			}
		})
	}
}

func TestCreateOperatorUser(t *testing.T) {
	a := &api{cfg: &config.Config{}}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users", strings.NewReader(`{"email":"operator@example.com","password":"secret","role":"operator"}`))
	rec := httptest.NewRecorder()

	a.createUser(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d: %s", http.StatusCreated, rec.Code, rec.Body.String())
	}
	if len(a.cfg.Users) != 1 || a.cfg.Users[0].Role != "operator" {
		t.Fatalf("expected operator account to be created, got %#v", a.cfg.Users)
	}
}

func TestDeleteUserRejectsCurrentAccount(t *testing.T) {
	a := &api{
		cfg: &config.Config{Users: []config.Account{{Email: "operator@example.com", Role: "operator"}}},
		sessions: map[string]user{
			"session": {Email: "operator@example.com", Role: "operator", Expires: time.Now().Add(time.Hour)},
		},
	}
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/users/operator%40example.com", nil)
	req.AddCookie(&http.Cookie{Name: "sentinel_session", Value: "session"})
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("email", "operator@example.com")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeContext))
	rec := httptest.NewRecorder()

	a.deleteUser(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}
}

func TestDeleteUserRevokesSessions(t *testing.T) {
	a := &api{
		cfg: &config.Config{Users: []config.Account{{Email: "operator@example.com", Role: "operator"}}},
		sessions: map[string]user{
			"other-session": {Email: "operator@example.com", Role: "operator", Expires: time.Now().Add(time.Hour)},
		},
	}
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/users/operator%40example.com", nil)
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("email", "operator@example.com")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeContext))
	rec := httptest.NewRecorder()

	a.deleteUser(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected status %d, got %d: %s", http.StatusNoContent, rec.Code, rec.Body.String())
	}
	if len(a.sessions) != 0 {
		t.Fatalf("expected deleted user's sessions to be revoked, got %#v", a.sessions)
	}
}
