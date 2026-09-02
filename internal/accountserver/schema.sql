CREATE TABLE IF NOT EXISTS setting (
    name  TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS account (
    id            TEXT PRIMARY KEY,
    username      TEXT NOT NULL COLLATE NOCASE UNIQUE,
    display_name  TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    is_admin      INTEGER NOT NULL DEFAULT 0,
    disabled      INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT NOT NULL,
    last_login_at TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS login_session (
    token_hash TEXT PRIMARY KEY,
    account_id TEXT NOT NULL REFERENCES account(id) ON DELETE CASCADE,
    expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS node (
    id               TEXT PRIMARY KEY,
    owner_account_id TEXT NOT NULL REFERENCES account(id),
    name             TEXT NOT NULL,
    version          TEXT NOT NULL DEFAULT '',
    os               TEXT NOT NULL DEFAULT '',
    arch             TEXT NOT NULL DEFAULT '',
    gpu_count        INTEGER NOT NULL DEFAULT 0,
    project_count    INTEGER NOT NULL DEFAULT 0,
    running_jobs     INTEGER NOT NULL DEFAULT 0,
    last_seen        TEXT NOT NULL,
    created_at       TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS network (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    last_seen  TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS node_network (
    node_id    TEXT NOT NULL REFERENCES node(id) ON DELETE CASCADE,
    network_id TEXT NOT NULL REFERENCES network(id) ON DELETE CASCADE,
    PRIMARY KEY (node_id, network_id)
);

CREATE INDEX IF NOT EXISTS session_expiry_idx ON login_session(expires_at);
CREATE INDEX IF NOT EXISTS node_seen_idx ON node(last_seen DESC);
