-- name: UpsertAuthUser :one
INSERT INTO auth_users (id, github_id, username, email, avatar_url, last_login_at)
VALUES ($1, $2, $3, $4, $5, NOW())
ON CONFLICT (github_id) DO UPDATE
    SET username      = EXCLUDED.username,
        email         = EXCLUDED.email,
        avatar_url    = EXCLUDED.avatar_url,
        last_login_at = NOW()
RETURNING *;

-- name: GetAuthUserByID :one
SELECT * FROM auth_users WHERE id = $1;

-- name: GetAuthUserByGithubID :one
SELECT * FROM auth_users WHERE github_id = $1;

-- name: CreateRefreshToken :one
INSERT INTO refresh_tokens (user_id, token_hash, expires_at)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetRefreshToken :one
SELECT * FROM refresh_tokens
WHERE token_hash = $1
  AND revoked = false
  AND expires_at > NOW();

-- name: RevokeRefreshToken :exec
UPDATE refresh_tokens SET revoked = true WHERE token_hash = $1;

-- name: RevokeAllUserRefreshTokens :exec
UPDATE refresh_tokens SET revoked = true WHERE user_id = $1;
