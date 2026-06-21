package db

import (
	"context"

	"github.com/google/uuid"
)

type MatchHistoryRow struct {
	ID           uuid.UUID   `json:"id"`
	UserID       uuid.UUID   `json:"user_id"`
	GameID       uuid.UUID   `json:"game_id"`
	Result       string      `json:"result"`
	CoinsChange  int32       `json:"coins_change"`
	OpponentName string      `json:"opponent_name"`
	CreatedAt    interface{} `json:"created_at"`
}

const insertMatchHistory = `INSERT INTO match_history (id, user_id, game_id, result, coins_change, opponent_name) VALUES ($1, $2, $3, $4, $5, $6)`

func (q *Queries) InsertMatchHistory(ctx context.Context, userID, gameID uuid.UUID, result string, coinsChange int32, opponentName string) error {
	_, err := q.db.ExecContext(ctx, insertMatchHistory, uuid.New(), userID, gameID, result, coinsChange, opponentName)
	return err
}

const getMatchHistory = `SELECT id, user_id, game_id, result, coins_change, opponent_name, created_at FROM match_history WHERE user_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`

func (q *Queries) GetMatchHistory(ctx context.Context, userID uuid.UUID, limit, offset int32) ([]MatchHistoryRow, error) {
	rows, err := q.db.QueryContext(ctx, getMatchHistory, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []MatchHistoryRow
	for rows.Next() {
		var r MatchHistoryRow
		if err := rows.Scan(&r.ID, &r.UserID, &r.GameID, &r.Result, &r.CoinsChange, &r.OpponentName, &r.CreatedAt); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

const countMatchHistory = `SELECT COUNT(*) FROM match_history WHERE user_id = $1`

func (q *Queries) CountMatchHistory(ctx context.Context, userID uuid.UUID) (int64, error) {
	var count int64
	err := q.db.QueryRowContext(ctx, countMatchHistory, userID).Scan(&count)
	return count, err
}
