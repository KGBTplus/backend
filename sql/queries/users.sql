-- name: CreateUser :one
INSERT INTO users (username, password_hash, email)
VALUES ($1, $2, $3)
RETURNING id, username, email, created_at;

-- name: GetUserByUsername :one
SELECT id, username, password_hash, email, created_at, email_otp_enabled
FROM users
WHERE username = $1;

-- name: GetUserByID :one
SELECT id, username, password_hash, email, created_at, email_otp_enabled
FROM users
WHERE id = $1;

-- name: EnableEmailOTP :exec
UPDATE users
SET email_otp_enabled = true
WHERE id = $1;
