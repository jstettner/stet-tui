-- +goose Up
create table task_definitions (
    id text primary key,
    title text not null,
    description text not null,
    created_at datetime default current_timestamp
);

create table task_history (
    id text primary key,
    task_id text not null,
    completed_date date not null,
    unique(task_id, completed_date),
    foreign key(task_id) references task_definitions(id)
);

-- +goose Down
drop table task_history;
drop table task_definitions;