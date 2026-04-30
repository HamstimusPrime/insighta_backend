package middleware

import (
	"net/http"
	"slices"
)

// RequireRole returns a middleware that allows only the listed roles.
// Must be used after Authenticate (depends on ClaimsKey in context).
func RequireRole(allowedRoles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := ClaimsFromContext(r.Context())
			if claims == nil {
				writeJSON(w, http.StatusUnauthorized, map[string]string{
					"status":  "error",
					"message": "unauthenticated",
				})
				return
			}
			if !slices.Contains(allowedRoles, claims.Role) {
				writeJSON(w, http.StatusForbidden, map[string]string{
					"status":  "error",
					"message": "insufficient permissions",
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
