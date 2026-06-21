-- +goose Up
ALTER TABLE profiles ADD COLUMN coins INT NOT NULL DEFAULT 0;
ALTER TABLE profiles ADD COLUMN inventory TEXT[] NOT NULL DEFAULT '{}';
ALTER TABLE profiles ADD COLUMN active_fish TEXT[] NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE profiles DROP COLUMN IF EXISTS active_fish;
ALTER TABLE profiles DROP COLUMN IF EXISTS inventory;
ALTER TABLE profiles DROP COLUMN IF EXISTS coins;
