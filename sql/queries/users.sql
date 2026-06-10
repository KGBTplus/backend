-- name: CreateUser :one
INSERT INTO users (username, password_hash, email)
VALUES ($1, $2, $3)
RETURNING id, username, email, created_at;

-- name: GetUserByUsername :one
SELECT id, username, password_hash, email, created_at, otp_enabled, otp_secret
FROM users
WHERE username = $1;

-- name: GetUserByID :one
SELECT id, username, password_hash, email, created_at, otp_enabled, otp_secret
FROM users
WHERE id = $1;

-- name: UpdateOTPSecret :exec
UPDATE users
SET otp_secret = $2
WHERE id = $1;

-- name: EnableOTP :exec
UPDATE users
SET otp_enabled = true
WHERE id = $1;