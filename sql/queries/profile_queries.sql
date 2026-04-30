-- name: CreateProfile :one
INSERT INTO users(id, name, gender, gender_probability, age,age_group, country_id, country_probability, created_at )
VALUES(
    $1,
    $2,
    $3,
    $4,
    $5,
    $6,
    $7,
    $8,
    NOW()
)RETURNING *;

-- name: GetProfileByName :one
 SELECT * FROM users
WHERE name = $1;



-- name: GetProfileByID :one
SELECT * FROM users
WHERE id = $1;

-- name: GetAllProfiles :many
SELECT * FROM users;

-- name: DeleteProfileByID :exec
DELETE FROM users
WHERE id = $1;