-- +goose Up
CREATE TABLE IF NOT EXISTS smtp_cram_credentials (
    username      TEXT NOT NULL PRIMARY KEY,
    secret_nonce  TEXT NOT NULL,
    secret_cipher TEXT NOT NULL,
    roles         TEXT NOT NULL,
    enabled       INTEGER NOT NULL DEFAULT 1,
    created_at    TEXT NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS smtp_cram_credentials;
