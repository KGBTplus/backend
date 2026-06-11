-- name: CreateProfile :exec
INSERT INTO profiles (user_id) VALUES ($1);

-- name: GetProfile :one
SELECT * FROM profiles WHERE user_id = $1;
