-- +goose Up
CREATE TABLE profiles (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    total_games INT NOT NULL DEFAULT 0,
    wins INT NOT NULL DEFAULT 0,
    losses INT NOT NULL DEFAULT 0,
    ships_sunk INT NOT NULL DEFAULT 0,
    total_shots INT NOT NULL DEFAULT 0,
    hits INT NOT NULL DEFAULT 0
);

-- +goose Down
DROP TABLE profiles;
