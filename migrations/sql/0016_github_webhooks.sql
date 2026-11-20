CREATE TABLE github_webhook_deliveries (
    project_id    uuid        NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
    delivery_id   uuid        NOT NULL,
    digest        text        NOT NULL CHECK (digest ~ '^[0-9a-f]{64}$'),
    event         text        NOT NULL CHECK (event ~ '^[a-z][a-z_]{0,79}$'),
    actor_id      uuid        NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    repository_id bigint      NOT NULL CHECK (repository_id > 0),
    repository    text        NOT NULL,
    source_host   text        NOT NULL,
    pull_number   integer     NOT NULL CHECK (pull_number >= 0),
    head_sha      text        NOT NULL CHECK (head_sha = '' OR head_sha ~ '^([0-9a-f]{40}|[0-9a-f]{64})$'),
    comment_id    bigint      NOT NULL CHECK (comment_id >= 0),
    commenter     text        NOT NULL,
    commenter_id  bigint      NOT NULL CHECK (commenter_id >= 0),
    request_id    text        NOT NULL,
    status        text        NOT NULL CHECK (status IN ('pending', 'ignored', 'enqueued', 'failed')),
    reason        text        NOT NULL DEFAULT '',
    run_id        uuid        NULL,
    attempts      integer     NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_at       timestamptz NOT NULL DEFAULT clock_timestamp(),
    created_at    timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (project_id, delivery_id),
    FOREIGN KEY (project_id, run_id) REFERENCES runs(project_id, id) ON DELETE RESTRICT,
    CHECK ((status = 'enqueued') = (run_id IS NOT NULL))
);

CREATE INDEX github_webhook_deliveries_due_idx ON github_webhook_deliveries(next_at) WHERE status='pending';
CREATE INDEX github_webhook_deliveries_run_idx ON github_webhook_deliveries(run_id) WHERE run_id IS NOT NULL;
