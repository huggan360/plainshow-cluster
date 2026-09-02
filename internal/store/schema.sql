-- Plainshow Cluster node state.
--
-- Deliberately small. The filesystem is the source of truth for project files;
-- live worker telemetry is streamed and never persisted. What lives here is
-- only what cannot be recomputed: which machines belong to the cluster, which
-- projects exist, and what has run.

CREATE TABLE IF NOT EXISTS machine (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    roles       TEXT NOT NULL DEFAULT '',
    os          TEXT NOT NULL DEFAULT '',
    arch        TEXT NOT NULL DEFAULT '',
    address     TEXT NOT NULL DEFAULT '',
    is_self     INTEGER NOT NULL DEFAULT 0,
    last_seen   TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS project (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS job (
    id          TEXT PRIMARY KEY,
    project_id  TEXT NOT NULL DEFAULT '',
    machine_id  TEXT NOT NULL DEFAULT '',
    kind        TEXT NOT NULL DEFAULT 'script',
    title       TEXT NOT NULL DEFAULT '',
    command     TEXT NOT NULL DEFAULT '',
    workdir     TEXT NOT NULL DEFAULT '',
    state       TEXT NOT NULL DEFAULT 'queued',
    exit_code   INTEGER NOT NULL DEFAULT -1,
    error       TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    started_at  TEXT NOT NULL DEFAULT '',
    ended_at    TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS job_created_idx ON job (created_at DESC);
CREATE INDEX IF NOT EXISTS job_project_idx ON job (project_id, created_at DESC);
