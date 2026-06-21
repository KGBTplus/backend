-- +goose Up
CREATE TABLE lobbies (
    id UUID PRIMARY KEY,
    creator_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'waiting',
    invite_code TEXT NOT NULL UNIQUE,
    max_players INT NOT NULL DEFAULT 2,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE lobby_players (
    lobby_id UUID NOT NULL REFERENCES lobbies(id) ON DELETE CASCADE,
    player_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY (lobby_id, player_id)
);

CREATE TABLE matchmaking_queue (
    player_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose Down
DROP TABLE matchmaking_queue;
DROP TABLE lobby_players;
DROP TABLE lobbies;
