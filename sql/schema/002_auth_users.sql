CREATE TABLE auth_users (
    id            UUID PRIMARY KEY,
    github_id     VARCHAR NOT NULL UNIQUE,
    username      VARCHAR NOT NULL,
    email         VARCHAR NOT NULL DEFAULT '',
    avatar_url    VARCHAR NOT NULL DEFAULT '',
    role          VARCHAR NOT NULL DEFAULT 'analyst' CHECK (role IN ('admin', 'analyst')),
    is_active     BOOLEAN NOT NULL DEFAULT true,
    last_login_at TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
