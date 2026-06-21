-- +goose Up
ALTER TABLE profiles ADD COLUMN time_in_battle INT NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE profiles DROP COLUMN IF EXISTS time_in_battle;
