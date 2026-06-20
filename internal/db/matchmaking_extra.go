package db

import (
	"context"
	"database/sql"
	"github.com/google/uuid"
)

const popMatchmakingPair = `
WITH matched AS (
    DELETE FROM matchmaking_queue
    WHERE player_id = (
        SELECT player_id FROM matchmaking_queue
        WHERE player_id != $1
        ORDER BY joined_at ASC
        LIMIT 1
    )
    RETURNING player_id
)
DELETE FROM matchmaking_queue
WHERE player_id = $1
    AND EXISTS (SELECT 1 FROM matched)
RETURNING (SELECT player_id FROM matched) AS opponent_id`

func (q *Queries) PopMatchmakingPair(ctx context.Context, playerID uuid.UUID) (*uuid.UUID, error) {
	row := q.db.QueryRowContext(ctx, popMatchmakingPair, playerID)
	var id uuid.UUID
	err := row.Scan(&id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &id, nil
}
