package db

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// GameStateRow represents a persisted game
type GameStateRow struct {
	ID          uuid.UUID
	Player1ID   uuid.UUID
	Player2ID   *uuid.UUID
	Status      string
	CurrentTurn *uuid.UUID
	WinnerID    *uuid.UUID
	CreatedAt   time.Time
	FinishedAt  *time.Time
}

const createGameState = `INSERT INTO game_state (id, player1_id, player2_id, status, current_turn, winner_id, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)`

type CreateGameStateParams struct {
	ID          uuid.UUID
	Player1ID   uuid.UUID
	Player2ID   *uuid.UUID
	Status      string
	CurrentTurn *uuid.UUID
	WinnerID    *uuid.UUID
	CreatedAt   time.Time
}

func (q *Queries) CreateGameState(ctx context.Context, arg CreateGameStateParams) error {
	_, err := q.db.ExecContext(ctx, createGameState,
		arg.ID, arg.Player1ID, arg.Player2ID, arg.Status, arg.CurrentTurn, arg.WinnerID, arg.CreatedAt,
	)
	return err
}

const getGameState = `SELECT id, player1_id, player2_id, status, current_turn, winner_id, created_at, finished_at FROM game_state WHERE id = $1`

func (q *Queries) GetGameState(ctx context.Context, id uuid.UUID) (GameStateRow, error) {
	row := q.db.QueryRowContext(ctx, getGameState, id)
	var i GameStateRow
	err := row.Scan(
		&i.ID, &i.Player1ID, &i.Player2ID, &i.Status, &i.CurrentTurn, &i.WinnerID, &i.CreatedAt, &i.FinishedAt,
	)
	return i, err
}

const updateGameState = `UPDATE game_state SET player2_id = $2, status = $3, current_turn = $4, winner_id = $5 WHERE id = $1`

type UpdateGameStateParams struct {
	ID          uuid.UUID
	Player2ID   *uuid.UUID
	Status      string
	CurrentTurn *uuid.UUID
	WinnerID    *uuid.UUID
}

func (q *Queries) UpdateGameState(ctx context.Context, arg UpdateGameStateParams) error {
	_, err := q.db.ExecContext(ctx, updateGameState, arg.ID, arg.Player2ID, arg.Status, arg.CurrentTurn, arg.WinnerID)
	return err
}

const finishGameState = `UPDATE game_state SET status = 'finished', winner_id = $2, current_turn = NULL, finished_at = NOW() WHERE id = $1`

func (q *Queries) FinishGameState(ctx context.Context, id, winnerID uuid.UUID) error {
	_, err := q.db.ExecContext(ctx, finishGameState, id, winnerID)
	return err
}

const setGameStatus = `UPDATE game_state SET status = $2 WHERE id = $1`

func (q *Queries) SetGameStatus(ctx context.Context, id uuid.UUID, status string) error {
	_, err := q.db.ExecContext(ctx, setGameStatus, id, status)
	return err
}

const setGameCurrentTurn = `UPDATE game_state SET current_turn = $2 WHERE id = $1`

func (q *Queries) SetGameCurrentTurn(ctx context.Context, id uuid.UUID, currentTurn *uuid.UUID) error {
	_, err := q.db.ExecContext(ctx, setGameCurrentTurn, id, currentTurn)
	return err
}

// GameShipRow represents a persisted ship
type GameShipRow struct {
	ID         uuid.UUID
	GameID     uuid.UUID
	PlayerID   uuid.UUID
	ShipType   int32
	StartX     int32
	StartY     int32
	Horizontal bool
	Sunk       bool
}

const createShip = `INSERT INTO game_ships (id, game_id, player_id, ship_type, start_x, start_y, horizontal, sunk)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`

type CreateShipParams struct {
	ID         uuid.UUID
	GameID     uuid.UUID
	PlayerID   uuid.UUID
	ShipType   int32
	StartX     int32
	StartY     int32
	Horizontal bool
	Sunk       bool
}

func (q *Queries) CreateShip(ctx context.Context, arg CreateShipParams) error {
	_, err := q.db.ExecContext(ctx, createShip, arg.ID, arg.GameID, arg.PlayerID, arg.ShipType, arg.StartX, arg.StartY, arg.Horizontal, arg.Sunk)
	return err
}

const deletePlayerShips = `DELETE FROM game_ships WHERE game_id = $1 AND player_id = $2`

func (q *Queries) DeletePlayerShips(ctx context.Context, gameID, playerID uuid.UUID) error {
	_, err := q.db.ExecContext(ctx, deletePlayerShips, gameID, playerID)
	return err
}

const getGameShips = `SELECT id, game_id, player_id, ship_type, start_x, start_y, horizontal, sunk FROM game_ships WHERE game_id = $1`

func (q *Queries) GetGameShips(ctx context.Context, gameID uuid.UUID) ([]GameShipRow, error) {
	rows, err := q.db.QueryContext(ctx, getGameShips, gameID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []GameShipRow
	for rows.Next() {
		var i GameShipRow
		if err := rows.Scan(&i.ID, &i.GameID, &i.PlayerID, &i.ShipType, &i.StartX, &i.StartY, &i.Horizontal, &i.Sunk); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, nil
}

const setShipSunk = `UPDATE game_ships SET sunk = true WHERE id = $1`

func (q *Queries) SetShipSunk(ctx context.Context, id uuid.UUID) error {
	_, err := q.db.ExecContext(ctx, setShipSunk, id)
	return err
}

// GameMoveRow represents a persisted move
type GameMoveRow struct {
	ID         uuid.UUID
	GameID     uuid.UUID
	PlayerID   uuid.UUID
	X          int32
	Y          int32
	Hit        bool
	SunkShipID *uuid.UUID
	CreatedAt  time.Time
}

const createMove = `INSERT INTO game_moves (id, game_id, player_id, x, y, hit, sunk_ship_id)
VALUES ($1, $2, $3, $4, $5, $6, $7)`

type CreateMoveParams struct {
	ID         uuid.UUID
	GameID     uuid.UUID
	PlayerID   uuid.UUID
	X          int32
	Y          int32
	Hit        bool
	SunkShipID *uuid.UUID
}

func (q *Queries) CreateMove(ctx context.Context, arg CreateMoveParams) error {
	_, err := q.db.ExecContext(ctx, createMove, arg.ID, arg.GameID, arg.PlayerID, arg.X, arg.Y, arg.Hit, arg.SunkShipID)
	return err
}

const getGameMoves = `SELECT id, game_id, player_id, x, y, hit, sunk_ship_id, created_at FROM game_moves WHERE game_id = $1 ORDER BY created_at ASC`

func (q *Queries) GetGameMoves(ctx context.Context, gameID uuid.UUID) ([]GameMoveRow, error) {
	rows, err := q.db.QueryContext(ctx, getGameMoves, gameID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []GameMoveRow
	for rows.Next() {
		var i GameMoveRow
		if err := rows.Scan(&i.ID, &i.GameID, &i.PlayerID, &i.X, &i.Y, &i.Hit, &i.SunkShipID, &i.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, nil
}

const getActiveGamesForPlayer = `SELECT id, player1_id, player2_id, status, current_turn, winner_id, created_at, finished_at FROM game_state WHERE (player1_id = $1 OR player2_id = $1) AND status IN ('placing_ships', 'playing')`

func (q *Queries) GetActiveGamesForPlayer(ctx context.Context, playerID uuid.UUID) ([]GameStateRow, error) {
	rows, err := q.db.QueryContext(ctx, getActiveGamesForPlayer, playerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []GameStateRow
	for rows.Next() {
		var i GameStateRow
		if err := rows.Scan(&i.ID, &i.Player1ID, &i.Player2ID, &i.Status, &i.CurrentTurn, &i.WinnerID, &i.CreatedAt, &i.FinishedAt); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, nil
}

const getAllActiveGames = `SELECT id, player1_id, player2_id, status, current_turn, winner_id, created_at, finished_at FROM game_state WHERE status IN ('waiting', 'placing_ships', 'playing')`

func (q *Queries) GetAllActiveGames(ctx context.Context) ([]GameStateRow, error) {
	rows, err := q.db.QueryContext(ctx, getAllActiveGames)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []GameStateRow
	for rows.Next() {
		var i GameStateRow
		if err := rows.Scan(&i.ID, &i.Player1ID, &i.Player2ID, &i.Status, &i.CurrentTurn, &i.WinnerID, &i.CreatedAt, &i.FinishedAt); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, nil
}
