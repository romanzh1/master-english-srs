-- +goose Up
ALTER TABLE users ADD COLUMN IF NOT EXISTS onenote_auth_code text;

-- +goose Down
ALTER TABLE users DROP COLUMN IF EXISTS onenote_auth_code;

