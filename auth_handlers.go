package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"insighta_backend/internal/auth"
	"insighta_backend/internal/database"

	"github.com/google/uuid"
)

// handlerGitHubLogin builds the GitHub authorization URL and redirects.
// ?source=web-portal → portal provides callback_url; server generates state+verifier.
// ?source=cli        → CLI provides state and callback_port; server generates verifier.
func (cfg *apiConfig) handlerGitHubLogin(w http.ResponseWriter, r *http.Request) {
	source := r.URL.Query().Get("source")

	switch source {
	case "web-portal":
		callbackURL := r.URL.Query().Get("callback_url")
		if callbackURL == "" {
			respondWithError(w, http.StatusBadRequest, "missing callback_url")
			return
		}
		if cfg.webPortalURL != "" && !strings.HasPrefix(callbackURL, cfg.webPortalURL) {
			respondWithError(w, http.StatusBadRequest, "invalid callback_url")
			return
		}
		state, err := auth.GenerateRandomHex(32)
		if err != nil {
			respondWithError(w, http.StatusInternalServerError, "failed to generate state")
			return
		}
		verifier, err := auth.GenerateRandomHex(32)
		if err != nil {
			respondWithError(w, http.StatusInternalServerError, "failed to generate verifier")
			return
		}
		challenge := auth.CodeChallenge(verifier)
		oauthStates.Lock()
		oauthStates.m[state] = oauthSession{
			codeVerifier: verifier,
			createdAt:    time.Now(),
			source:       "web-portal",
			callbackURL:  callbackURL,
		}
		oauthStates.Unlock()
		redirectURI := cfg.baseURL + "/auth/github/callback"
		githubURL := fmt.Sprintf(
			"https://github.com/login/oauth/authorize?client_id=%s&redirect_uri=%s&state=%s&code_challenge=%s&code_challenge_method=S256&scope=user:email",
			url.QueryEscape(cfg.githubClientID),
			url.QueryEscape(redirectURI),
			url.QueryEscape(state),
			url.QueryEscape(challenge),
		)
		http.Redirect(w, r, githubURL, http.StatusFound)

	case "cli":
		state := r.URL.Query().Get("state")
		callbackPort := r.URL.Query().Get("callback_port")
		if state == "" || callbackPort == "" {
			respondWithError(w, http.StatusBadRequest, "missing state or callback_port")
			return
		}
		verifier, err := auth.GenerateRandomHex(32)
		if err != nil {
			respondWithError(w, http.StatusInternalServerError, "failed to generate verifier")
			return
		}
		challenge := auth.CodeChallenge(verifier)
		oauthStates.Lock()
		oauthStates.m[state] = oauthSession{
			codeVerifier: verifier,
			createdAt:    time.Now(),
			source:       "cli",
			callbackPort: callbackPort,
		}
		oauthStates.Unlock()
		redirectURI := cfg.baseURL + "/auth/github/callback"
		githubURL := fmt.Sprintf(
			"https://github.com/login/oauth/authorize?client_id=%s&redirect_uri=%s&state=%s&code_challenge=%s&code_challenge_method=S256&scope=user:email",
			url.QueryEscape(cfg.githubClientID),
			url.QueryEscape(redirectURI),
			url.QueryEscape(state),
			url.QueryEscape(challenge),
		)
		http.Redirect(w, r, githubURL, http.StatusFound)

	default:
		// No source param: behave like web-portal using the configured portal URL.
		callbackDest := cfg.webPortalURL
		if callbackDest == "" {
			callbackDest = cfg.baseURL
		}
		state, err := auth.GenerateRandomHex(32)
		if err != nil {
			respondWithError(w, http.StatusInternalServerError, "failed to generate state")
			return
		}
		verifier, err := auth.GenerateRandomHex(32)
		if err != nil {
			respondWithError(w, http.StatusInternalServerError, "failed to generate verifier")
			return
		}
		challenge := auth.CodeChallenge(verifier)
		oauthStates.Lock()
		oauthStates.m[state] = oauthSession{
			codeVerifier: verifier,
			createdAt:    time.Now(),
			source:       "web-portal",
			callbackURL:  callbackDest,
		}
		oauthStates.Unlock()
		redirectURI := cfg.baseURL + "/auth/github/callback"
		githubURL := fmt.Sprintf(
			"https://github.com/login/oauth/authorize?client_id=%s&redirect_uri=%s&state=%s&code_challenge=%s&code_challenge_method=S256&scope=user:email",
			url.QueryEscape(cfg.githubClientID),
			url.QueryEscape(redirectURI),
			url.QueryEscape(state),
			url.QueryEscape(challenge),
		)
		http.Redirect(w, r, githubURL, http.StatusFound)
	}
}

// handlerWebCallback handles GET /auth/github/callback — the browser redirect from GitHub.
func (cfg *apiConfig) handlerWebCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")

	if code == "" || state == "" {
		respondWithError(w, http.StatusBadRequest, "missing code or state")
		return
	}

	// test_code: skip state validation and real GitHub exchange, return tokens for seeded admin user.
	if code == "test_code" && cfg.appEnv != "production" {
		testGithubID := "test_user_admin"
		dbUser, err := cfg.db.UpsertAuthUser(r.Context(), database.UpsertAuthUserParams{
			ID:        uuid.Must(uuid.NewV7()),
			GithubID:  testGithubID,
			Username:  "test_admin",
			Email:     "test_admin@test.local",
			AvatarUrl: "",
		})
		if err != nil {
			log.Printf("test_code: upsert user error: %v", err)
			respondWithError(w, http.StatusInternalServerError, "failed to create test user")
			return
		}
		if dbUser.Role != "admin" {
			if _, err := cfg.rawDB.ExecContext(r.Context(),
				"UPDATE auth_users SET role = $1 WHERE github_id = $2",
				"admin", testGithubID); err != nil {
				log.Printf("test_code: role update error: %v", err)
				respondWithError(w, http.StatusInternalServerError, "failed to set role")
				return
			}
			dbUser.Role = "admin"
		}
		accessToken, rawRefresh, err := cfg.issueTokenPair(r.Context(), dbUser)
		if err != nil {
			log.Printf("test_code: issue token pair error: %v", err)
			respondWithError(w, http.StatusInternalServerError, "failed to issue tokens")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"access_token":  accessToken,
			"refresh_token": rawRefresh,
		})
		return
	}

	oauthStates.Lock()
	session, ok := oauthStates.m[state]
	if ok {
		delete(oauthStates.m, state)
	}
	oauthStates.Unlock()

	if !ok || time.Since(session.createdAt) > 5*time.Minute {
		respondWithError(w, http.StatusBadRequest, "invalid or expired state")
		return
	}

	redirectURI := cfg.baseURL + "/auth/github/callback"
	ghToken, err := auth.ExchangeCodeForToken(r.Context(), cfg.githubClientID, cfg.githubClientSecret, code, redirectURI, session.codeVerifier)
	if err != nil {
		log.Printf("github token exchange error: %v", err)
		respondWithError(w, http.StatusBadGateway, "failed to exchange code with GitHub")
		return
	}

	ghUser, err := auth.FetchGitHubUser(r.Context(), ghToken.AccessToken)
	if err != nil {
		log.Printf("github user fetch error: %v", err)
		respondWithError(w, http.StatusBadGateway, "failed to fetch GitHub user")
		return
	}

	dbUser, err := cfg.db.UpsertAuthUser(r.Context(), database.UpsertAuthUserParams{
		ID:        uuid.Must(uuid.NewV7()),
		GithubID:  fmt.Sprintf("%d", ghUser.ID),
		Username:  ghUser.Login,
		Email:     ghUser.Email,
		AvatarUrl: ghUser.AvatarURL,
	})
	if err != nil {
		log.Printf("upsert auth user error: %v", err)
		respondWithError(w, http.StatusInternalServerError, "failed to create user")
		return
	}

	accessToken, rawRefresh, err := cfg.issueTokenPair(r.Context(), dbUser)
	if err != nil {
		log.Printf("issue token pair error: %v", err)
		respondWithError(w, http.StatusInternalServerError, "failed to issue tokens")
		return
	}

	if session.source == "web-portal" {
		portalURL := session.callbackURL +
			"?access_token=" + url.QueryEscape(accessToken) +
			"&refresh_token=" + url.QueryEscape(rawRefresh) +
			"&username=" + url.QueryEscape(dbUser.Username) +
			"&role=" + url.QueryEscape(dbUser.Role)
		http.Redirect(w, r, portalURL, http.StatusFound)
		return
	}

	// CLI flow: redirect tokens to the CLI's local callback server.
	cliURL := fmt.Sprintf(
		"http://localhost:%s/?access_token=%s&refresh_token=%s&username=%s&role=%s&state=%s",
		session.callbackPort,
		url.QueryEscape(accessToken),
		url.QueryEscape(rawRefresh),
		url.QueryEscape(dbUser.Username),
		url.QueryEscape(dbUser.Role),
		url.QueryEscape(state),
	)
	http.Redirect(w, r, cliURL, http.StatusFound)
}

// handlerRefresh rotates the refresh token pair.
func (cfg *apiConfig) handlerRefresh(w http.ResponseWriter, r *http.Request) {
	var req RefreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.RefreshToken == "" {
		respondWithError(w, http.StatusBadRequest, "missing refresh_token")
		return
	}

	hash := hashToken(req.RefreshToken)
	storedToken, err := cfg.db.GetRefreshToken(r.Context(), hash)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "invalid or expired refresh token")
		return
	}

	if err := cfg.db.RevokeRefreshToken(r.Context(), hash); err != nil {
		log.Printf("revoke refresh token error: %v", err)
		respondWithError(w, http.StatusInternalServerError, "failed to revoke token")
		return
	}

	dbUser, err := cfg.db.GetAuthUserByID(r.Context(), storedToken.UserID)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "user not found")
		return
	}
	if !dbUser.IsActive {
		respondWithError(w, http.StatusForbidden, "account is deactivated")
		return
	}

	accessToken, rawRefresh, err := cfg.issueTokenPair(r.Context(), dbUser)
	if err != nil {
		log.Printf("issue token pair error: %v", err)
		respondWithError(w, http.StatusInternalServerError, "failed to issue tokens")
		return
	}

	respondWithJSON(w, map[string]string{
		"status":        "success",
		"access_token":  accessToken,
		"refresh_token": rawRefresh,
		"username":      dbUser.Username,
		"role":          dbUser.Role,
	}, http.StatusOK)
}

// handlerLogout invalidates the refresh token server-side.
func (cfg *apiConfig) handlerLogout(w http.ResponseWriter, r *http.Request) {
	var req LogoutRequest
	json.NewDecoder(r.Body).Decode(&req)

	if req.RefreshToken == "" {
		if cookie, err := r.Cookie("refresh_token"); err == nil {
			req.RefreshToken = cookie.Value
		}
	}

	if req.RefreshToken == "" {
		respondWithError(w, http.StatusBadRequest, "missing refresh_token")
		return
	}

	hash := hashToken(req.RefreshToken)
	cfg.db.RevokeRefreshToken(r.Context(), hash)

	respondWithJSON(w, map[string]string{"status": "success"}, http.StatusOK)
}

// issueTokenPair mints a new access token and opaque refresh token, storing the hash in DB.
func (cfg *apiConfig) issueTokenPair(ctx context.Context, user database.AuthUser) (accessToken, rawRefresh string, err error) {
	accessToken, err = auth.MintAccessToken(user.ID, user.Username, user.Role, user.IsActive, cfg.jwtSecret)
	if err != nil {
		return
	}
	rawRefresh, err = auth.GenerateRandomHex(32)
	if err != nil {
		return
	}
	hash := hashToken(rawRefresh)
	_, err = cfg.db.CreateRefreshToken(ctx, database.CreateRefreshTokenParams{
		UserID:    user.ID,
		TokenHash: hash,
		ExpiresAt: time.Now().Add(5 * time.Minute),
	})
	return
}

// hashToken returns the SHA-256 hex digest of the raw token string.
func hashToken(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return fmt.Sprintf("%x", h)
}

// handlerTestToken issues a real token pair for a seeded test user. Only
// available when APP_ENV is not "production". Used by automated test suites
// that cannot complete a real GitHub OAuth flow.
func (cfg *apiConfig) handlerTestToken(w http.ResponseWriter, r *http.Request) {
	if cfg.appEnv == "production" {
		respondWithError(w, http.StatusNotFound, "not found")
		return
	}
	var req struct {
		Username string `json:"username"`
		Role     string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Username == "" {
		respondWithError(w, http.StatusBadRequest, "missing username")
		return
	}
	if req.Role != "admin" && req.Role != "analyst" {
		respondWithError(w, http.StatusBadRequest, "role must be admin or analyst")
		return
	}

	testGithubID := "test_user_" + req.Role
	dbUser, err := cfg.db.UpsertAuthUser(r.Context(), database.UpsertAuthUserParams{
		ID:        uuid.Must(uuid.NewV7()),
		GithubID:  testGithubID,
		Username:  req.Username,
		Email:     req.Username + "@test.local",
		AvatarUrl: "",
	})
	if err != nil {
		log.Printf("test token: upsert user error: %v", err)
		respondWithError(w, http.StatusInternalServerError, "failed to create test user")
		return
	}

	if dbUser.Role != req.Role {
		_, err = cfg.rawDB.ExecContext(r.Context(),
			"UPDATE auth_users SET role = $1 WHERE github_id = $2",
			req.Role, testGithubID)
		if err != nil {
			log.Printf("test token: role update error: %v", err)
			respondWithError(w, http.StatusInternalServerError, "failed to set role")
			return
		}
		dbUser.Role = req.Role
	}

	accessToken, rawRefresh, err := cfg.issueTokenPair(r.Context(), dbUser)
	if err != nil {
		log.Printf("test token: issue token pair error: %v", err)
		respondWithError(w, http.StatusInternalServerError, "failed to issue tokens")
		return
	}

	respondWithJSON(w, map[string]string{
		"access_token":  accessToken,
		"refresh_token": rawRefresh,
		"username":      dbUser.Username,
		"role":          dbUser.Role,
	}, http.StatusOK)
}
