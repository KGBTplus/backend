-- +goose Up
ALTER TABLE profiles ADD COLUMN total_spent INT NOT NULL DEFAULT 0;
ALTER TABLE profiles ADD COLUMN total_earned INT NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE profiles DROP COLUMN IF EXISTS total_earned;
ALTER TABLE profiles DROP COLUMN IF EXISTS total_spent;
