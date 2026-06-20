-- name: CreateLobby :one
INSERT INTO lobbies (id, creator_id, invite_code, max_players)
VALUES ($1, $2, $3, $4)
<<<<<<< HEAD
RETURNING *;
=======
RETURNING id, creator_id, status, invite_code, max_players, created_at;
>>>>>>> date-+++

-- name: GetLobby :one
SELECT * FROM lobbies WHERE id = $1;

-- name: FindLobbyByCode :one
SELECT * FROM lobbies WHERE invite_code = $1 AND status = 'waiting';

-- name: ListLobbies :many
<<<<<<< HEAD
SELECT * FROM lobbies
=======
SELECT id, creator_id, status, invite_code, max_players, created_at FROM lobbies
>>>>>>> date-+++
WHERE (CASE WHEN @status::text = '' THEN true ELSE status = @status END)
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: UpdateLobbyStatus :exec
UPDATE lobbies SET status = $2 WHERE id = $1;

-- name: DeleteLobby :exec
DELETE FROM lobbies WHERE id = $1;

-- name: AddLobbyPlayer :exec
INSERT INTO lobby_players (lobby_id, player_id) VALUES ($1, $2);

-- name: RemoveLobbyPlayer :exec
DELETE FROM lobby_players WHERE lobby_id = $1 AND player_id = $2;

-- name: GetLobbyPlayers :many
SELECT player_id FROM lobby_players WHERE lobby_id = $1;

-- name: IsPlayerInLobby :one
SELECT EXISTS(SELECT 1 FROM lobby_players WHERE lobby_id = $1 AND player_id = $2);

-- name: JoinMatchmaking :exec
INSERT INTO matchmaking_queue (player_id) VALUES ($1)
ON CONFLICT (player_id) DO NOTHING;

-- name: LeaveMatchmaking :exec
DELETE FROM matchmaking_queue WHERE player_id = $1;

-- name: GetMatchmakingStatus :one
SELECT EXISTS(SELECT 1 FROM matchmaking_queue WHERE player_id = $1);

-- name: GetMatchmakingQueueSize :one
SELECT COUNT(*) FROM matchmaking_queue;

-- name: DeleteUserLobbies :exec
DELETE FROM lobbies WHERE id IN (
    SELECT lobby_id FROM lobby_players WHERE player_id = $1
);
