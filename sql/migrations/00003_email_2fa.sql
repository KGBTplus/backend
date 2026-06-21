-- +goose Up
ALTER TABLE users DROP COLUMN otp_secret;
ALTER TABLE users DROP COLUMN otp_enabled;
ALTER TABLE users ADD COLUMN email_otp_enabled BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose Down
ALTER TABLE users DROP COLUMN email_otp_enabled;
ALTER TABLE users ADD COLUMN otp_enabled BOOLEAN DEFAULT FALSE;
ALTER TABLE users ADD COLUMN otp_secret TEXT;
