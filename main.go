package main

import (
	"database/sql"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"insighta_backend/internal/database"
	"insighta_backend/internal/middleware"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

type apiConfig struct {
	db                 *database.Queries
	rawDB              *sql.DB
	jwtSecret          []byte
	githubClientID     string
	githubClientSecret string
	baseURL            string
	webPortalURL       string
	appEnv             string
}

type oauthSession struct {
	codeVerifier string
	createdAt    time.Time
	source       string // "web-portal" or "cli"
	callbackPort string // CLI only
	callbackURL  string // web-portal only
}

// oauthStates holds in-flight web OAuth sessions keyed by state string.
var oauthStates = struct {
	sync.Mutex
	m map[string]oauthSession
}{m: make(map[string]oauthSession)}

// corsMiddleware sets permissive CORS headers for all routes.
func corsMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ipRateLimiter tracks per-IP request timestamps for sliding-window rate limiting.
type ipRateLimiter struct {
	sync.Mutex
	requests map[string][]time.Time
}

func newIPRateLimiter() *ipRateLimiter {
	return &ipRateLimiter{requests: make(map[string][]time.Time)}
}

func (l *ipRateLimiter) allow(ip string, limit int, window time.Duration) bool {
	l.Lock()
	defer l.Unlock()
	now := time.Now()
	cutoff := now.Add(-window)
	var recent []time.Time
	for _, t := range l.requests[ip] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	if len(recent) >= limit {
		l.requests[ip] = recent
		return false
	}
	l.requests[ip] = append(recent, now)
	return true
}

func rateLimitMiddleware(limiter *ipRateLimiter, limit int, window time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				ip = r.RemoteAddr
			}
			if !limiter.allow(ip, limit, window) {
				w.Header().Set("Retry-After", "60")
				http.Error(w, `{"status":"error","message":"rate limit exceeded"}`, http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// userRateLimitMiddleware rate-limits by user ID (from JWT claims) with IP fallback.
func userRateLimitMiddleware(limiter *ipRateLimiter, limit int, window time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.RemoteAddr
			if claims := middleware.ClaimsFromContext(r.Context()); claims != nil {
				key = claims.UserID.String()
			}
			if !limiter.allow(key, limit, window) {
				w.Header().Set("Retry-After", "60")
				http.Error(w, `{"status":"error","message":"rate limit exceeded"}`, http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// apiVersionMiddleware rejects requests that do not supply X-API-Version: 1.
func apiVersionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Version") != "1" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"status":"error","message":"API version header required"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("no .env file found, reading from environment")
	}

	db, err := sql.Open("postgres", mustEnv("DB_URL"))
	if err != nil {
		log.Fatalf("unable to establish connection to database: %v", err)
	}

	cfg := &apiConfig{
		db:                 database.New(db),
		rawDB:              db,
		jwtSecret:          []byte(mustEnv("JWT_SECRET")),
		githubClientID:     mustEnv("GITHUB_CLIENT_ID"),
		githubClientSecret: mustEnv("GITHUB_CLIENT_SECRET"),
		baseURL:            getEnv("BASE_URL", "http://localhost:8080"),
		webPortalURL:       getEnv("WEB_PORTAL_URL", ""),
		appEnv:             getEnv("APP_ENV", "development"),
	}

	authLimiter := newIPRateLimiter()
	apiLimiter := newIPRateLimiter()

	r := chi.NewRouter()

	r.Use(chimiddleware.Logger)
	r.Use(chimiddleware.Recoverer)
	r.Use(chimiddleware.Timeout(9 * time.Second))
	r.Use(corsMiddleware())

	// Public auth routes — all subject to 10 req/min IP-based limit
	r.Route("/auth", func(r chi.Router) {
		r.Use(rateLimitMiddleware(authLimiter, 10, time.Minute))
		r.Get("/github", cfg.handlerGitHubLogin)
		r.Get("/github/callback", cfg.handlerWebCallback)
		r.Post("/refresh", cfg.handlerRefresh)
		r.Post("/logout", cfg.handlerLogout)
		r.Post("/test/token", cfg.handlerTestToken)
	})

	// Protected API routes — require valid JWT + X-API-Version: 1 header + 60 req/min per user.
	// Registered under both /api/ (legacy) and /api/v1/ (versioned).
	registerAPIRoutes := func(r chi.Router) {
		r.Use(middleware.Authenticate(cfg.jwtSecret))
		r.Use(apiVersionMiddleware)
		r.Use(userRateLimitMiddleware(apiLimiter, 60, time.Minute))

		r.Get("/users/me", cfg.handlerGetCurrentUser)

		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireRole("admin", "analyst"))
			r.Get("/profiles", func(w http.ResponseWriter, r *http.Request) {
				handlerGetProfiles(w, r, cfg.db)
			})
			r.Get("/profiles/export", func(w http.ResponseWriter, r *http.Request) {
				handlerExportProfiles(w, r, cfg.db)
			})
			r.Get("/profiles/search", func(w http.ResponseWriter, r *http.Request) {
				handlerNLQsearch(w, r, cfg.db)
			})
			r.Get("/profiles/{id}", func(w http.ResponseWriter, r *http.Request) {
				handlerGetProfileWithID(w, r, cfg.db)
			})
		})

		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireRole("admin"))
			r.Post("/profiles", func(w http.ResponseWriter, r *http.Request) {
				handlerCreateProfile(w, r, cfg.db)
			})
			r.Delete("/profiles/{id}", func(w http.ResponseWriter, r *http.Request) {
				handlerDeleteProfileWithID(w, r, cfg.db)
			})
		})
	}

	r.Route("/api", registerAPIRoutes)
	r.Route("/api/v1", registerAPIRoutes)

	port := getEnv("PORT", "8080")
	fmt.Printf("Server starting on :%s\n", port)
	if err := http.ListenAndServe(":"+port, r); err != nil {
		log.Fatalf("server: %v", err)
	}
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("required env var %s is not set", key)
	}
	return v
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
