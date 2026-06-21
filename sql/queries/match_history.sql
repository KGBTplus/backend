-- name: InsertMatchHistory :exec
INSERT INTO match_history (id, user_id, game_id, result, coins_change, opponent_name)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetMatchHistory :many
SELECT id, user_id, game_id, result, coins_change, opponent_name, created_at
FROM match_history
WHERE user_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: CountMatchHistory :one
SELECT COUNT(*) FROM match_history WHERE user_id = $1;
