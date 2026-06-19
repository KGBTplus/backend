-- +goose Up
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_lobby_players_player_id ON lobby_players(player_id);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_profiles_wins ON profiles(wins DESC);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_lobbies_status_created ON lobbies(status, created_at DESC);

-- +goose Down
DROP INDEX IF EXISTS idx_lobby_players_player_id;
DROP INDEX IF EXISTS idx_profiles_wins;
DROP INDEX IF EXISTS idx_lobbies_status_created;
