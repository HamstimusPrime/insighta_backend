# Insighta Backend

A REST API for profile intelligence and demographic analysis. Profiles are enriched with predicted gender, age, and nationality via third-party APIs (Genderize, Agify, Nationalize). Supports role-based access and natural language search queries.

---

## System Architecture

```
insighta_backend/
├── main.go                  # Server entry, routing, middleware wiring
├── auth_handlers.go         # GitHub OAuth + token endpoints
├── handlers.go              # Profile CRUD and search endpoints
├── utils.go                 # NLP query parser, helpers
├── models.go                # Request/response types
└── internal/
    ├── auth/
    │   ├── jwt.go           # Token minting and validation
    │   ├── github.go        # GitHub OAuth client
    │   └── pkce.go          # PKCE code verifier/challenge
    ├── middleware/
    │   ├── auth.go          # JWT validation middleware
    │   └── rbac.go          # Role enforcement middleware
    └── database/
        ├── db.go            # Connection setup
        ├── models.go        # sqlc-generated models
        └── *_queries.sql.go # sqlc-generated query functions
```

**Stack:** Go · chi router · PostgreSQL (sqlc) · GitHub OAuth 2.0 + PKCE · JWT

**Request flow:**

```
Client → Rate Limiter → Authenticate (JWT) → RequireRole (RBAC) → Handler → DB
```

---

## Authentication Flow

Insighta uses GitHub OAuth 2.0 with PKCE. Two flows are supported.

### Web Portal

```
Client → GET /auth/github?source=web-portal&callback_url=<url>
       → Server generates state + PKCE verifier, redirects to GitHub
       → GitHub → GET /auth/github/callback?code=...&state=...
       → Server exchanges code + verifier for tokens
       → Redirects to callback_url with access_token and refresh_token
```

### CLI

```
CLI → starts local HTTP server on callback_port
CLI → GET /auth/github?source=cli&state=<random>&callback_port=<port>
    → Server generates PKCE verifier, redirects to GitHub
    → GitHub → GET /auth/github/callback?code=...&state=...
    → Server exchanges code + verifier for tokens
    → Redirects to localhost:<callback_port> with tokens
```

The CLI flow avoids embedding secrets on the command line. The companion CLI tool opens a browser, starts a local listener, and captures the redirect.

**Development only:** POST `/auth/test/token` with `{username, role}` issues tokens without GitHub, for local testing.

---

## CLI Usage

The CLI tool (separate repository) follows this sequence:

1. Pick a free local port and start an HTTP listener.
2. Call `GET /auth/github?source=cli&state=<random>&callback_port=<port>`.
3. Open the returned URL in the user's browser.
4. Capture the redirect at `localhost:<port>` — it contains `access_token` and `refresh_token`.
5. Store tokens locally and attach `Authorization: Bearer <access_token>` to subsequent API requests.

All protected API routes also require the header `X-API-Version: 1`.

---

## Token Handling

| Token | Lifetime | Format | Storage |
|---|---|---|---|
| Access token | 3 minutes | Signed JWT (HS256) | Client only |
| Refresh token | 5 minutes | 64-char hex (32 random bytes) | Hash stored in DB |

**Access token claims:** `user_id`, `username`, `role`, `is_active`, `exp`, `iat`, `iss`

**Refresh flow:**

```
POST /auth/refresh
Body: { "refresh_token": "<token>" }

→ Server looks up SHA-256 hash in refresh_tokens table
→ Checks not revoked and not expired
→ Revokes old token (single-use)
→ Issues new access_token + refresh_token pair
```

**Logout:** `POST /auth/logout` revokes the refresh token immediately.

Tokens are accepted as a `Authorization: Bearer <token>` header or a `session` cookie.

---

## Role Enforcement

Two roles exist: `admin` and `analyst` (default on signup).

**Middleware chain applied to all `/api/*` routes:**

```
Authenticate  →  RequireRole(allowed...)  →  Handler
```

`Authenticate` validates the JWT and injects claims into the request context. `RequireRole` reads the role from those claims and returns **403** if it is not in the allowed list.

**Endpoint permissions:**

| Endpoint | Method | Roles |
|---|---|---|
| `/api/profiles` | GET | admin, analyst |
| `/api/profiles/{id}` | GET | admin, analyst |
| `/api/profiles/search` | GET | admin, analyst |
| `/api/profiles/export` | GET | admin, analyst |
| `/api/profiles` | POST | admin |
| `/api/profiles/{id}` | DELETE | admin |
| `/api/users/me` | GET | any authenticated |

Rate limits: 10 req/min per IP on `/auth/*`, 60 req/min per user on `/api/*`.

---

## Natural Language Parsing

`GET /api/profiles/search?q=<query>` accepts plain English queries. The parser in [utils.go](utils.go) extracts filters from free text with no external NLP dependency.

**Supported patterns:**

| Filter | Examples |
|---|---|
| Gender | "male", "females", "women", "boys" |
| Age group | "teenagers", "adults", "seniors", "children" |
| Young | "young" → age 16–24 |
| Age floor | "above 30", "over 25" |
| Age ceiling | "below 40", "under 18" |
| Country | "from India", "from United States" |

**Examples:**

```
"Find female adults from India"        → gender=female, age_group=adult, country=IN
"male teenagers above 18"             → gender=male, min_age=18
"young women from United States"      → gender=female, min_age=16, max_age=24, country=US
```

Returns **422** if the query produces no usable filters, **404** if no profiles match.

---

## Configuration

Copy `.env.example` to `.env` and fill in values:

```env
DB_URL=postgres://user:password@localhost:5432/insighta
JWT_SECRET=<64-char hex string>
GITHUB_CLIENT_ID=<from GitHub OAuth app>
GITHUB_CLIENT_SECRET=<from GitHub OAuth app>
BASE_URL=http://localhost:8080
PORT=8080
APP_ENV=development
```

```bash
go run .
```
