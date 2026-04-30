package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"insighta_backend/internal/auth"
	"insighta_backend/internal/database"
	"insighta_backend/internal/middleware"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

var testSecret = []byte("test-jwt-secret-for-unit-tests")

func mintToken(t *testing.T, userID uuid.UUID, username, role string) string {
	t.Helper()
	tok, err := auth.MintAccessToken(userID, username, role, true, testSecret)
	if err != nil {
		t.Fatalf("mintToken: %v", err)
	}
	return tok
}

// newTestRouter assembles the chi router for tests.
// Profile handlers are 200 stubs so tests that don't cover profiles never touch the DB.
func newTestRouter(cfg *apiConfig, limiter *ipRateLimiter) http.Handler {
	r := chi.NewRouter()
	r.Use(corsMiddleware())

	r.With(rateLimitMiddleware(limiter, 10, time.Minute)).
		Get("/auth/github", cfg.handlerGitHubLogin)
	r.Post("/auth/refresh", cfg.handlerRefresh)

	registerRoutes := func(r chi.Router) {
		r.Use(middleware.Authenticate(cfg.jwtSecret))
		r.Get("/users/me", cfg.handlerGetCurrentUser)
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireRole("admin", "analyst"))
			r.Get("/profiles", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			})
		})
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireRole("admin"))
			r.Post("/profiles", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			})
		})
	}
	r.Route("/api", registerRoutes)
	r.Route("/api/v1", registerRoutes)
	return r
}

// TestRateLimiting_AuthGithub verifies that the /auth/github endpoint enforces
// a sliding-window limit of 10 requests per minute, returning 429 on the 11th.
func TestRateLimiting_AuthGithub(t *testing.T) {
	limiter := newIPRateLimiter()
	cfg := &apiConfig{
		jwtSecret:      testSecret,
		githubClientID: "test-client",
		baseURL:        "http://localhost:8080",
	}
	handler := newTestRouter(cfg, limiter)

	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodGet, "/auth/github", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code == http.StatusTooManyRequests {
			t.Fatalf("request %d was rate-limited early; expected at most 10 free requests", i+1)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/auth/github", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429 on request 11, got %d", rr.Code)
	}
}

// TestMultiInterface_TokenRequired confirms that every protected API endpoint
// returns 401 when no token is supplied — covering both /api and /api/v1 interfaces.
func TestMultiInterface_TokenRequired(t *testing.T) {
	cfg := &apiConfig{jwtSecret: testSecret}
	handler := newTestRouter(cfg, newIPRateLimiter())

	endpoints := []struct{ method, path string }{
		{http.MethodGet, "/api/users/me"},
		{http.MethodGet, "/api/v1/users/me"},
		{http.MethodGet, "/api/profiles"},
		{http.MethodGet, "/api/v1/profiles"},
	}
	for _, ep := range endpoints {
		t.Run(ep.method+" "+ep.path, func(t *testing.T) {
			req := httptest.NewRequest(ep.method, ep.path, nil)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if rr.Code != http.StatusUnauthorized {
				t.Errorf("expected 401, got %d", rr.Code)
			}
		})
	}
}

// TestTokenLifecycle_RefreshProvided exercises the /auth/refresh endpoint end-to-end:
// a valid opaque refresh token is exchanged for a new access + refresh token pair.
func TestTokenLifecycle_RefreshProvided(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	userID := uuid.Must(uuid.NewV7())
	rawToken := "test_raw_refresh_token_abcdef1234567890abcdef"
	tokenHash := hashToken(rawToken)
	now := time.Now()

	// GetRefreshToken — look up the incoming token hash.
	mock.ExpectQuery("refresh_tokens").
		WithArgs(tokenHash).
		WillReturnRows(
			sqlmock.NewRows([]string{"id", "user_id", "token_hash", "expires_at", "revoked", "created_at"}).
				AddRow(uuid.Must(uuid.NewV7()), userID, tokenHash, now.Add(5*time.Minute), false, now),
		)

	// RevokeRefreshToken — single-use rotation.
	mock.ExpectExec("refresh_tokens").
		WithArgs(tokenHash).
		WillReturnResult(sqlmock.NewResult(1, 1))

	// GetAuthUserByID — load the owner.
	mock.ExpectQuery("auth_users").
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(
			sqlmock.NewRows([]string{
				"id", "github_id", "username", "email", "avatar_url",
				"role", "is_active", "last_login_at", "created_at",
			}).AddRow(userID, "gh_test", "testuser", "test@example.local", "",
				"analyst", true, nil, now),
		)

	// CreateRefreshToken — store the newly issued token hash.
	mock.ExpectQuery("refresh_tokens").
		WillReturnRows(
			sqlmock.NewRows([]string{"id", "user_id", "token_hash", "expires_at", "revoked", "created_at"}).
				AddRow(uuid.Must(uuid.NewV7()), userID, "new_token_hash", now.Add(5*time.Minute), false, now),
		)

	cfg := &apiConfig{
		db:        database.New(db),
		rawDB:     db,
		jwtSecret: testSecret,
	}
	handler := newTestRouter(cfg, newIPRateLimiter())

	body, _ := json.Marshal(map[string]string{"refresh_token": rawToken})
	req := httptest.NewRequest(http.MethodPost, "/auth/refresh", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["access_token"] == "" {
		t.Error("refresh response missing access_token")
	}
	if resp["refresh_token"] == "" {
		t.Error("refresh response missing refresh_token")
	}
	if resp["status"] != "success" {
		t.Errorf("expected status=success, got %q", resp["status"])
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet DB expectations: %v", err)
	}
}

// TestUserManagement_NoTokenOnUsersMe confirms that /api/users/me returns 401
// when no access token is available (the caller has not authenticated).
func TestUserManagement_NoTokenOnUsersMe(t *testing.T) {
	cfg := &apiConfig{jwtSecret: testSecret}
	handler := newTestRouter(cfg, newIPRateLimiter())

	req := httptest.NewRequest(http.MethodGet, "/api/users/me", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 without token, got %d", rr.Code)
	}

	var body map[string]string
	json.NewDecoder(rr.Body).Decode(&body) //nolint:errcheck
	if body["status"] != "error" {
		t.Errorf("expected status=error in body, got %q", body["status"])
	}
}

// TestAPIVersioningAndStructure_ValidToken verifies that both the legacy /api
// and the versioned /api/v1 prefixes honour a valid JWT and return the correct user,
// including the email and avatar_url fields added to /users/me.
func TestAPIVersioningAndStructure_ValidToken(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	userID := uuid.Must(uuid.NewV7())
	token := mintToken(t, userID, "versiontest", "analyst")
	now := time.Now()

	makeAuthRow := func() *sqlmock.Rows {
		return sqlmock.NewRows([]string{
			"id", "github_id", "username", "email", "avatar_url",
			"role", "is_active", "last_login_at", "created_at",
		}).AddRow(userID, "gh_versiontest", "versiontest", "versiontest@example.local", "",
			"analyst", true, nil, now)
	}
	// One DB call per path tested (/api/users/me and /api/v1/users/me).
	mock.ExpectQuery("auth_users").WithArgs(sqlmock.AnyArg()).WillReturnRows(makeAuthRow())
	mock.ExpectQuery("auth_users").WithArgs(sqlmock.AnyArg()).WillReturnRows(makeAuthRow())

	cfg := &apiConfig{db: database.New(db), jwtSecret: testSecret}
	handler := newTestRouter(cfg, newIPRateLimiter())

	for _, path := range []string{"/api/users/me", "/api/v1/users/me"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", rr.Code)
			}
			var resp map[string]interface{}
			if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if resp["username"] != "versiontest" {
				t.Errorf("username mismatch: got %v", resp["username"])
			}
			if resp["email"] == nil {
				t.Error("expected email field in /users/me response")
			}
		})
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet DB expectations: %v", err)
	}
}

// TestRoleEnforcement_AdminAndAnalyst checks that RBAC rules are enforced:
// admin may read and write profiles; analyst may read but not write.
func TestRoleEnforcement_AdminAndAnalyst(t *testing.T) {
	cfg := &apiConfig{jwtSecret: testSecret}
	handler := newTestRouter(cfg, newIPRateLimiter())

	adminToken := mintToken(t, uuid.Must(uuid.NewV7()), "admin_user", "admin")
	analystToken := mintToken(t, uuid.Must(uuid.NewV7()), "analyst_user", "analyst")

	cases := []struct {
		name     string
		method   string
		path     string
		token    string
		wantCode int
	}{
		{"admin GET /api/profiles", http.MethodGet, "/api/profiles", adminToken, http.StatusOK},
		{"admin POST /api/profiles", http.MethodPost, "/api/profiles", adminToken, http.StatusOK},
		{"analyst GET /api/profiles", http.MethodGet, "/api/profiles", analystToken, http.StatusOK},
		{"analyst POST /api/profiles (forbidden)", http.MethodPost, "/api/profiles", analystToken, http.StatusForbidden},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Header.Set("Authorization", "Bearer "+tc.token)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if rr.Code != tc.wantCode {
				t.Errorf("expected %d, got %d", tc.wantCode, rr.Code)
			}
		})
	}
}
