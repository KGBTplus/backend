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

-- name: GetUserByEmail :one
SELECT id, username, password_hash, email, created_at, email_otp_enabled
FROM users
WHERE email = $1;

-- name: VerifyEmail :exec
UPDATE users SET email_verified = true WHERE id = $1;

-- name: GetUserByUsernameWithVerified :one
SELECT id, username, password_hash, email, created_at, email_otp_enabled, COALESCE(email_verified, false)::boolean as email_verified
FROM users
WHERE username = $1;

-- name: GetUserByIDWithVerified :one
SELECT id, username, password_hash, email, created_at, email_otp_enabled, COALESCE(email_verified, false)::boolean as email_verified
FROM users
WHERE id = $1;

-- name: GetUserByEmailWithVerified :one
SELECT id, username, password_hash, email, created_at, email_otp_enabled, COALESCE(email_verified, false)::boolean as email_verified
FROM users
WHERE email = $1;

-- name: UpdateUserEmail :exec
UPDATE users SET email = $2 WHERE id = $1;

-- name: IncrementTokenVersion :exec
UPDATE users SET token_version = token_version + 1 WHERE id = $1;
