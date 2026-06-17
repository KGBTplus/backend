-- name: CreateGame :exec
INSERT INTO games (id, player1_id, status, created_at)
VALUES ($1, $2, $3, $4);

-- name: GetGame :one
SELECT id, player1_id, player2_id, status, current_turn, winner_id, finished_at, created_at
FROM games WHERE id = $1;

-- name: GetActiveGames :many
SELECT id, player1_id, player2_id, status, current_turn, winner_id, finished_at, created_at
FROM games WHERE status IN ('waiting', 'placing_ships', 'playing');

-- name: GetPlayerActiveGames :many
SELECT id, player1_id, player2_id, status, current_turn, winner_id, finished_at, created_at
FROM games
WHERE (player1_id = $1 OR player2_id = $1)
  AND status IN ('waiting', 'placing_ships', 'playing');

-- name: UpdateGamePlayer2 :exec
UPDATE games SET player2_id = $2, status = 'placing_ships' WHERE id = $1;

-- name: UpdateGameStatus :exec
UPDATE games SET status = $2, current_turn = $3, winner_id = $4, finished_at = $5 WHERE id = $1;

-- name: SaveGameShip :exec
INSERT INTO game_ships (id, game_id, player_id, ship_type, start_x, start_y, horizontal)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: GetGameShips :many
SELECT id, game_id, player_id, ship_type, start_x, start_y, horizontal
FROM game_ships WHERE game_id = $1;

-- name: GetPlayerGameShips :many
SELECT id, game_id, player_id, ship_type, start_x, start_y, horizontal
FROM game_ships WHERE game_id = $1 AND player_id = $2;

-- name: DeleteGameShips :exec
DELETE FROM game_ships WHERE game_id = $1;

-- name: DeletePlayerGameShips :exec
DELETE FROM game_ships WHERE game_id = $1 AND player_id = $2;

-- name: SaveGameMove :exec
INSERT INTO game_moves (id, game_id, player_id, x, y, hit, sunk_ship_id)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: GetGameMoves :many
SELECT id, game_id, player_id, x, y, hit, sunk_ship_id
FROM game_moves WHERE game_id = $1;
