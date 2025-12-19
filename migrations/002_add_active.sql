-- +goose Up
alter table task_definitions add column active boolean default true;

-- +goose Down
alter table task_definitions drop column active;