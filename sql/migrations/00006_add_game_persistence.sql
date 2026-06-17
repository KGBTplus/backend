-- +goose Up
CREATE TABLE games (
    id UUID PRIMARY KEY,
    player1_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    player2_id UUID REFERENCES users(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'waiting',
    current_turn UUID REFERENCES users(id),
    winner_id UUID REFERENCES users(id),
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE game_ships (
    id UUID PRIMARY KEY,
    game_id UUID NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    player_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    ship_type INT NOT NULL,
    start_x INT NOT NULL,
    start_y INT NOT NULL,
    horizontal BOOLEAN NOT NULL
);

CREATE TABLE game_moves (
    id UUID PRIMARY KEY,
    game_id UUID NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    player_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    x INT NOT NULL,
    y INT NOT NULL,
    hit BOOLEAN NOT NULL,
    sunk_ship_id UUID
);

CREATE INDEX idx_games_status ON games(status);
CREATE INDEX idx_game_ships_game_id ON game_ships(game_id);
CREATE INDEX idx_game_moves_game_id ON game_moves(game_id);

-- +goose Down
DROP INDEX idx_game_moves_game_id;
DROP INDEX idx_game_ships_game_id;
DROP INDEX idx_games_status;
DROP TABLE game_moves;
DROP TABLE game_ships;
DROP TABLE games;
