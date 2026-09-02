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
    network_id  TEXT NOT NULL DEFAULT '',
    name        TEXT NOT NULL,
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

-- People with access to this cluster's projects, and what each may do.
--
-- The capability set mirrors the Plainshow console's so the two products behave
-- the same way, with the two hosting capabilities replaced by the two that
-- matter here: run (start jobs and notebooks) and train (start distributed
-- training runs).
CREATE TABLE IF NOT EXISTS member (
    project_id  TEXT NOT NULL,
    username    TEXT NOT NULL,
    github_login TEXT NOT NULL DEFAULT '',
    view        INTEGER NOT NULL DEFAULT 1,
    code        INTEGER NOT NULL DEFAULT 0,
    push        INTEGER NOT NULL DEFAULT 0,
    run         INTEGER NOT NULL DEFAULT 0,
    train       INTEGER NOT NULL DEFAULT 0,
    manage      INTEGER NOT NULL DEFAULT 0,
    -- The role GitHub last reported, so a later sync can tell "unchanged there"
    -- apart from "somebody changed it there".
    github_role TEXT NOT NULL DEFAULT '',
    owner       INTEGER NOT NULL DEFAULT 0,
    created_at  TEXT NOT NULL,
    PRIMARY KEY (project_id, username)
);

-- A device can participate in many independent cluster networks. Accounts are
-- global to the installation; roles and machines are scoped to one network.
CREATE TABLE IF NOT EXISTS account (
    id            TEXT PRIMARY KEY,
    username      TEXT NOT NULL UNIQUE,
    display_name  TEXT NOT NULL DEFAULT '',
    public_key    TEXT NOT NULL DEFAULT '',
    password_hash TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS login_session (
    token_hash TEXT PRIMARY KEY,
    account_id TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS network (
    id               TEXT PRIMARY KEY,
    name             TEXT NOT NULL,
    owner_account_id TEXT NOT NULL,
    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS network_member (
    network_id      TEXT NOT NULL,
    account_id      TEXT NOT NULL,
    role            TEXT NOT NULL DEFAULT 'member',
    manage_network  INTEGER NOT NULL DEFAULT 0,
    manage_members  INTEGER NOT NULL DEFAULT 0,
    create_projects INTEGER NOT NULL DEFAULT 1,
    run_jobs        INTEGER NOT NULL DEFAULT 1,
    manage_nodes    INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL,
    PRIMARY KEY (network_id, account_id)
);

CREATE TABLE IF NOT EXISTS network_node (
    network_id TEXT NOT NULL,
    node_id    TEXT NOT NULL,
    name       TEXT NOT NULL,
    roles      TEXT NOT NULL DEFAULT '',
    os         TEXT NOT NULL DEFAULT '',
    arch       TEXT NOT NULL DEFAULT '',
    public_key TEXT NOT NULL DEFAULT '',
    fingerprint TEXT NOT NULL DEFAULT '',
    address    TEXT NOT NULL DEFAULT '',
    policy     TEXT NOT NULL DEFAULT '{}',
    capacity   TEXT NOT NULL DEFAULT '{}',
    is_self    INTEGER NOT NULL DEFAULT 0,
    last_seen  TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    PRIMARY KEY (network_id, node_id)
);

CREATE TABLE IF NOT EXISTS invitation (
    id          TEXT PRIMARY KEY,
    network_id  TEXT NOT NULL,
    token_hash  TEXT NOT NULL UNIQUE,
    role        TEXT NOT NULL DEFAULT 'member',
    expires_at  TEXT NOT NULL,
    max_uses    INTEGER NOT NULL DEFAULT 1,
    uses        INTEGER NOT NULL DEFAULT 0,
    created_by  TEXT NOT NULL,
    created_at  TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS network_node_seen_idx ON network_node (network_id, last_seen DESC);

CREATE TABLE IF NOT EXISTS network_controller (
    network_id  TEXT NOT NULL,
    id          TEXT NOT NULL,
    name        TEXT NOT NULL,
    public_key  TEXT NOT NULL,
    fingerprint TEXT NOT NULL,
    address     TEXT NOT NULL,
    collab_token TEXT NOT NULL,
    last_seen   TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    PRIMARY KEY (network_id, id)
);

CREATE TABLE IF NOT EXISTS schema_migration (
    name       TEXT PRIMARY KEY,
    applied_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS collab_document (
    network_id TEXT NOT NULL,
    project_id TEXT NOT NULL,
    path       TEXT NOT NULL,
    content    TEXT NOT NULL DEFAULT '',
    revision   INTEGER NOT NULL DEFAULT 0,
    history    TEXT NOT NULL DEFAULT '[]',
    updated_at TEXT NOT NULL,
    PRIMARY KEY (network_id, project_id, path)
);

CREATE TABLE IF NOT EXISTS dataset (
    id          TEXT PRIMARY KEY,
    network_id  TEXT NOT NULL,
    name        TEXT NOT NULL,
    version     TEXT NOT NULL,
    root_hash   TEXT NOT NULL,
    file_count  INTEGER NOT NULL,
    size_bytes  INTEGER NOT NULL,
    manifest    TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    UNIQUE(network_id, name, version)
);

CREATE TABLE IF NOT EXISTS dataset_placement (
    dataset_id TEXT NOT NULL,
    node_id    TEXT NOT NULL,
    state      TEXT NOT NULL DEFAULT 'ready',
    bytes_done INTEGER NOT NULL DEFAULT 0,
    updated_at TEXT NOT NULL,
    PRIMARY KEY(dataset_id, node_id)
);

CREATE TABLE IF NOT EXISTS training_run (
    id          TEXT PRIMARY KEY,
    network_id  TEXT NOT NULL,
    project_id  TEXT NOT NULL,
    framework   TEXT NOT NULL,
    state       TEXT NOT NULL,
    ranks       TEXT NOT NULL DEFAULT '[]',
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

-- Collaborative edits, one row per operation.
--
-- These used to live as a JSON array inside collab_document, rewritten in full
-- on every keystroke. That cost grew with the document's history: about 5 ms
-- per edit early on and over 30 ms after a thousand, heading for 100 ms at the
-- retention cap. An append-only log makes a keystroke one small insert whatever
-- the history looks like.
CREATE TABLE IF NOT EXISTS collab_operation (
    network_id TEXT    NOT NULL,
    project_id TEXT    NOT NULL,
    path       TEXT    NOT NULL,
    revision   INTEGER NOT NULL,
    payload    TEXT    NOT NULL,
    PRIMARY KEY (network_id, project_id, path, revision)
);
