-- name: CreateProfile :exec
INSERT INTO profiles (user_id) VALUES ($1);

-- name: GetProfile :one
SELECT * FROM profiles WHERE user_id = $1;

-- name: GetLeaderboard :many
SELECT
    ROW_NUMBER() OVER (ORDER BY p.wins DESC) AS rank,
    u.id AS player_id,
    u.username,
    p.wins,
    p.losses,
    p.total_games,
    CASE WHEN p.total_games > 0 THEN (p.wins::float8 / p.total_games * 100)::float8 ELSE 0::float8 END AS win_rate,
    CASE WHEN p.total_shots > 0 THEN (p.hits::float8 / p.total_shots * 100)::float8 ELSE 0::float8 END AS hit_rate
FROM profiles p
JOIN users u ON u.id = p.user_id
ORDER BY p.wins DESC
LIMIT $1;

-- name: GetPlayerRank :one
SELECT u.id, u.username, p.wins, p.losses, p.total_games, p.total_shots, p.hits
FROM profiles p
JOIN users u ON u.id = p.user_id
WHERE u.id = $1;

-- name: UpdateProfileStats :exec
UPDATE profiles SET
    total_games = total_games + $2,
    wins = wins + $3,
    losses = losses + $4,
    ships_sunk = ships_sunk + $5,
    total_shots = total_shots + $6,
    hits = hits + $7
WHERE user_id = $1;
