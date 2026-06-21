-- +goose Up
CREATE TABLE game_state (
    id UUID PRIMARY KEY,
    player1_id UUID NOT NULL REFERENCES users(id),
    player2_id UUID REFERENCES users(id),
    status TEXT NOT NULL DEFAULT 'waiting',
    current_turn UUID REFERENCES users(id),
    winner_id UUID REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at TIMESTAMPTZ
);

CREATE TABLE game_ships (
    id UUID PRIMARY KEY,
    game_id UUID NOT NULL REFERENCES game_state(id) ON DELETE CASCADE,
    player_id UUID NOT NULL REFERENCES users(id),
    ship_type INT NOT NULL,
    start_x INT NOT NULL,
    start_y INT NOT NULL,
    horizontal BOOLEAN NOT NULL,
    sunk BOOLEAN NOT NULL DEFAULT false
);

CREATE TABLE game_moves (
    id UUID PRIMARY KEY,
    game_id UUID NOT NULL REFERENCES game_state(id) ON DELETE CASCADE,
    player_id UUID NOT NULL REFERENCES users(id),
    x INT NOT NULL,
    y INT NOT NULL,
    hit BOOLEAN NOT NULL DEFAULT false,
    sunk_ship_id UUID REFERENCES game_ships(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_game_ships_game_id ON game_ships(game_id);
CREATE INDEX idx_game_moves_game_id ON game_moves(game_id);
CREATE INDEX idx_game_state_player1 ON game_state(player1_id);
CREATE INDEX idx_game_state_player2 ON game_state(player2_id);

-- +goose Down
DROP TABLE IF EXISTS game_moves;
DROP TABLE IF EXISTS game_ships;
DROP TABLE IF EXISTS game_state;
