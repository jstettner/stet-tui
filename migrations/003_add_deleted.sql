-- +goose Up
ALTER TABLE task_definitions ADD COLUMN deleted BOOLEAN DEFAULT FALSE;

-- +goose Down
ALTER TABLE task_definitions DROP COLUMN deleted;
