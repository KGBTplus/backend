-- name: CreateUser :one
INSERT INTO users (username, password_hash, email)
VALUES ($1, $2, $3)
RETURNING id, username, email, created_at;

-- name: GetUserByUsername :one
SELECT id, username, password_hash, email, created_at
FROM users
WHERE username = $1;