-- +goose Up
ALTER TABLE users ADD COLUMN otp_enabled BOOLEAN DEFAULT FALSE;

-- +goose Down
ALTER TABLE users DROP COLUMN otp_enabled;