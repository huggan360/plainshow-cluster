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
    id               TEXT PRIMARY KEY,
    name             TEXT NOT NULL,
    owner_account_id TEXT NOT NULL DEFAULT '',
    management_key   TEXT NOT NULL DEFAULT '',
    last_seen        TEXT NOT NULL,
    created_at       TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS network_member (
    network_id TEXT NOT NULL REFERENCES network(id) ON DELETE CASCADE,
    account_id TEXT NOT NULL REFERENCES account(id) ON DELETE CASCADE,
    role       TEXT NOT NULL DEFAULT 'member',
    joined_at  TEXT NOT NULL,
    PRIMARY KEY (network_id, account_id)
);

CREATE TABLE IF NOT EXISTS node_network (
    node_id    TEXT NOT NULL REFERENCES node(id) ON DELETE CASCADE,
    network_id TEXT NOT NULL REFERENCES network(id) ON DELETE CASCADE,
    PRIMARY KEY (node_id, network_id)
);

-- Controller servers are independent applications. The account service only
-- records their owner, address, heartbeat and network authorization; it never
-- accepts or forwards Cowork WebSocket traffic.
CREATE TABLE IF NOT EXISTS controller_server (
    id               TEXT PRIMARY KEY,
    owner_account_id TEXT NOT NULL REFERENCES account(id) ON DELETE CASCADE,
    name             TEXT NOT NULL,
    public_url       TEXT NOT NULL UNIQUE,
    credential_hash  TEXT NOT NULL,
    last_seen        TEXT NOT NULL,
    created_at       TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS controller_network (
    controller_id TEXT NOT NULL REFERENCES controller_server(id) ON DELETE CASCADE,
    network_id    TEXT NOT NULL REFERENCES network(id) ON DELETE CASCADE,
    relay_token   TEXT NOT NULL,
    updated_at    TEXT NOT NULL,
    PRIMARY KEY (controller_id, network_id)
);

CREATE INDEX IF NOT EXISTS session_expiry_idx ON login_session(expires_at);
CREATE INDEX IF NOT EXISTS node_seen_idx ON node(last_seen DESC);
CREATE INDEX IF NOT EXISTS controller_seen_idx ON controller_server(last_seen DESC);
CREATE INDEX IF NOT EXISTS controller_network_network_idx ON controller_network(network_id);
