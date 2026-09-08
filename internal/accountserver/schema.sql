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
    address          TEXT NOT NULL DEFAULT '',
    public_key       TEXT NOT NULL DEFAULT '',
    fingerprint      TEXT NOT NULL DEFAULT '',
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

-- Deleted network ids are retained so an older device cannot recreate a
-- network from stale local configuration on its next heartbeat.
CREATE TABLE IF NOT EXISTS network_tombstone (
    network_id TEXT PRIMARY KEY,
    deleted_at TEXT NOT NULL
);

-- Devices only receive tombstones for accounts that actually belonged to the
-- deleted network.
CREATE TABLE IF NOT EXISTS network_tombstone_member (
    network_id TEXT NOT NULL REFERENCES network_tombstone(network_id) ON DELETE CASCADE,
    account_id TEXT NOT NULL REFERENCES account(id) ON DELETE CASCADE,
    PRIMARY KEY (network_id, account_id)
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

-- An invitation names a person, not a machine. A join code proves you were
-- handed a secret; an invitation proves somebody chose you, which is the thing
-- a network owner actually means.
CREATE TABLE IF NOT EXISTS network_invitation (
    id           TEXT PRIMARY KEY,
    network_id   TEXT NOT NULL REFERENCES network(id) ON DELETE CASCADE,
    account_id   TEXT NOT NULL REFERENCES account(id) ON DELETE CASCADE,
    invited_by   TEXT NOT NULL REFERENCES account(id) ON DELETE CASCADE,
    role         TEXT NOT NULL DEFAULT 'member',
    status       TEXT NOT NULL DEFAULT 'pending',
    created_at   TEXT NOT NULL,
    responded_at TEXT NOT NULL DEFAULT '',
    UNIQUE (network_id, account_id)
);

CREATE INDEX IF NOT EXISTS invitation_account_idx
    ON network_invitation(account_id, status);

-- A project the account knows about. Metadata only: never a file, never a
-- commit, never a byte of anybody's data. This is what lets a second machine
-- show you a project you made somewhere else and offer to fetch it, rather than
-- showing an empty workspace and no explanation.
CREATE TABLE IF NOT EXISTS project (
    id               TEXT PRIMARY KEY,
    owner_account_id TEXT NOT NULL REFERENCES account(id) ON DELETE CASCADE,
    name             TEXT NOT NULL,
    description      TEXT NOT NULL DEFAULT '',
    repository       TEXT NOT NULL DEFAULT '',
    branch           TEXT NOT NULL DEFAULT '',
    size_kb          INTEGER NOT NULL DEFAULT 0,
    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS project_member (
    project_id TEXT NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    account_id TEXT NOT NULL REFERENCES account(id) ON DELETE CASCADE,
    role       TEXT NOT NULL DEFAULT 'member',
    joined_at  TEXT NOT NULL,
    PRIMARY KEY (project_id, account_id)
);

CREATE INDEX IF NOT EXISTS project_member_account_idx
    ON project_member(account_id);

CREATE INDEX IF NOT EXISTS session_expiry_idx ON login_session(expires_at);
CREATE INDEX IF NOT EXISTS node_seen_idx ON node(last_seen DESC);
CREATE INDEX IF NOT EXISTS controller_seen_idx ON controller_server(last_seen DESC);
CREATE INDEX IF NOT EXISTS controller_network_network_idx ON controller_network(network_id);
CREATE INDEX IF NOT EXISTS network_tombstone_account_idx ON network_tombstone_member(account_id);
