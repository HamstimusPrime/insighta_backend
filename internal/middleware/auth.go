package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"insighta_backend/internal/auth"
)

type contextKey string

const ClaimsKey contextKey = "claims"

// ClaimsFromContext retrieves *auth.Claims from the request context.
func ClaimsFromContext(ctx context.Context) *auth.Claims {
	c, _ := ctx.Value(ClaimsKey).(*auth.Claims)
	return c
}

// Authenticate validates the Bearer JWT (or session cookie fallback) and injects claims into context.
// Returns 401 on missing/invalid token, 403 if the account is inactive.
func Authenticate(jwtSecret []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var tokenStr string
			if header := r.Header.Get("Authorization"); strings.HasPrefix(header, "Bearer ") {
				tokenStr = strings.TrimPrefix(header, "Bearer ")
			} else if cookie, err := r.Cookie("session"); err == nil && cookie.Value != "" {
				tokenStr = cookie.Value
			} else {
				writeJSON(w, http.StatusUnauthorized, map[string]string{
					"status":  "error",
					"message": "missing or malformed Authorization header",
				})
				return
			}
			claims, err := auth.ParseAccessToken(tokenStr, jwtSecret)
			if err != nil {
				writeJSON(w, http.StatusUnauthorized, map[string]string{
					"status":  "error",
					"message": "invalid or expired token",
				})
				return
			}
			if !claims.IsActive {
				writeJSON(w, http.StatusForbidden, map[string]string{
					"status":  "error",
					"message": "account is deactivated",
				})
				return
			}
			ctx := context.WithValue(r.Context(), ClaimsKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}
