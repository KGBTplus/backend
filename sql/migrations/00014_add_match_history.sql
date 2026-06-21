-- +goose Up
CREATE TABLE match_history (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    game_id      UUID NOT NULL,
    result       TEXT NOT NULL CHECK (result IN ('win', 'loss', 'draw')),
    coins_change INT NOT NULL DEFAULT 0,
    opponent_name TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_match_history_user_id ON match_history(user_id);
CREATE INDEX idx_match_history_user_created ON match_history(user_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS match_history;
