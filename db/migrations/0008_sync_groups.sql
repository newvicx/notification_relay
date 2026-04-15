-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS sync_groups (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    group_name TEXT    NOT NULL UNIQUE,
    created_at TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_by TEXT    NOT NULL
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS sync_groups;

-- +goose StatementEnd
