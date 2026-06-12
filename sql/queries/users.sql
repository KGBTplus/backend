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

-- name: DisableEmailOTP :exec
UPDATE users
SET email_otp_enabled = false
WHERE id = $1;

-- name: UpdateUsername :exec
UPDATE users
SET username = $2
WHERE id = $1;

-- name: UpdatePassword :exec
UPDATE users
SET password_hash = $2
WHERE id = $1;
