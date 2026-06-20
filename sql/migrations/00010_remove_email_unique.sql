-- +goose Up
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_email_key;

-- +goose Down
ALTER TABLE users ADD CONSTRAINT users_email_key UNIQUE (email);
