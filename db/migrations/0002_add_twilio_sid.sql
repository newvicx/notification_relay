-- +goose Up
-- +goose StatementBegin

ALTER TABLE deliveries ADD COLUMN twilio_sid TEXT;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE deliveries DROP COLUMN twilio_sid;

-- +goose StatementEnd
