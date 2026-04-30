-- name: CreateUserTable :one
CREATE TABLE users(
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL UNIQUE,
    gender TEXT NOT NULL,
    gender_probability FLOAT NOT NULL,
    age INT NOT NULL,
    country_name TEXT NOT NULL,
    age_group TEXT NOT NULL,
    country_id TEXT NOT NULL,
    country_probability FLOAT NOT NULL,
    created_at TEXT DEFAULT to_char(NOW(), 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
);