-- name: CreateGameState :exec
INSERT INTO game_state (id, player1_id, player2_id, status, current_turn, winner_id, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: GetGameState :one
SELECT * FROM game_state WHERE id = $1;

-- name: UpdateGameState :exec
UPDATE game_state SET
    player2_id = $2,
    status = $3,
    current_turn = $4,
    winner_id = $5
WHERE id = $1;

-- name: FinishGameState :exec
UPDATE game_state SET
    status = 'finished',
    winner_id = $2,
    current_turn = NULL,
    finished_at = NOW()
WHERE id = $1;

-- name: SetGameStatus :exec
UPDATE game_state SET status = $2 WHERE id = $1;

-- name: SetGameCurrentTurn :exec
UPDATE game_state SET current_turn = $2 WHERE id = $1;

-- name: CreateShip :exec
INSERT INTO game_ships (id, game_id, player_id, ship_type, start_x, start_y, horizontal, sunk)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: DeletePlayerShips :exec
DELETE FROM game_ships WHERE game_id = $1 AND player_id = $2;

-- name: GetGameShips :many
SELECT * FROM game_ships WHERE game_id = $1;

-- name: SetShipSunk :exec
UPDATE game_ships SET sunk = true WHERE id = $1;

-- name: CreateMove :exec
INSERT INTO game_moves (id, game_id, player_id, x, y, hit, sunk_ship_id)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: GetGameMoves :many
SELECT * FROM game_moves WHERE game_id = $1 ORDER BY created_at ASC;

-- name: GetActiveGamesForPlayer :many
SELECT * FROM game_state WHERE (player1_id = $1 OR player2_id = $1) AND status IN ('placing_ships', 'playing');

-- name: GetAllActiveGames :many
SELECT * FROM game_state WHERE status IN ('waiting', 'placing_ships', 'playing');
