-- +goose Up
-- Remove duplicates: keep the newest user for each email, delete older ones
DELETE FROM users WHERE id IN (
    SELECT id FROM (
        SELECT id, ROW_NUMBER() OVER (PARTITION BY email ORDER BY created_at DESC) AS rn
        FROM users WHERE email IS NOT NULL AND email != ''
    ) dup WHERE dup.rn > 1
);
-- Remove any remaining NULL/empty email users that are not unique
DELETE FROM users WHERE email IS NULL OR email = '';
-- Add unique constraint on email
ALTER TABLE users ADD CONSTRAINT users_email_key UNIQUE (email);

-- +goose Down
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_email_key;
