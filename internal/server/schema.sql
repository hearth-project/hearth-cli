PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS users (
  id                     INTEGER PRIMARY KEY,
  email                  TEXT NOT NULL UNIQUE,
  kdf_params             TEXT NOT NULL,
  kdf_salt               BLOB NOT NULL,
  auth_salt              BLOB NOT NULL,
  auth_hash              BLOB NOT NULL,
  enc_vault_key          BLOB NOT NULL,
  enc_vault_key_recovery BLOB NOT NULL,
  enc_vault_key_identity BLOB NOT NULL DEFAULT x'',
  identity_salt          BLOB NOT NULL DEFAULT x'',
  identity_hash          BLOB NOT NULL DEFAULT x'',
  identity_disabled      INTEGER NOT NULL DEFAULT 0,
  created_at             INTEGER NOT NULL,
  status                 TEXT NOT NULL DEFAULT 'active', -- active | disabled
  quota_bytes            INTEGER NOT NULL DEFAULT -1         -- -1 = server default, 0 = unlimited
);

CREATE TABLE IF NOT EXISTS devices (
  id         TEXT PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name       TEXT NOT NULL,
  status     TEXT NOT NULL DEFAULT 'active',
  created_at INTEGER NOT NULL,
  last_seen  INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS tokens (
  token_hash BLOB PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  device_id  TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
  expires_at INTEGER NOT NULL
);

-- One row per synced session. session_id is an HMAC computed by the client;
-- the server never learns the agent, path or session name.
CREATE TABLE IF NOT EXISTS sessions (
  user_id          INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  session_id       TEXT NOT NULL,
  enc_meta         BLOB NOT NULL,
  generation       INTEGER NOT NULL DEFAULT 1,
  size_bytes       INTEGER NOT NULL DEFAULT 0,
  status           TEXT NOT NULL DEFAULT 'active', -- active | expired
  pinned           INTEGER NOT NULL DEFAULT 0,
  last_accessed_at INTEGER NOT NULL,
  expired_at       INTEGER,
  lease_device     TEXT,
  lease_expires_at INTEGER,
  updated_at       INTEGER NOT NULL,
  active_at        INTEGER NOT NULL DEFAULT 0, -- last append: lists sort newest first
  updated_by       TEXT NOT NULL DEFAULT '',   -- device that last wrote it
  PRIMARY KEY (user_id, session_id)
);

CREATE TABLE IF NOT EXISTS session_chunks (
  user_id    INTEGER NOT NULL,
  session_id TEXT NOT NULL,
  generation INTEGER NOT NULL,
  "offset"   INTEGER NOT NULL,
  length     INTEGER NOT NULL,
  object     TEXT NOT NULL,
  PRIMARY KEY (user_id, session_id, generation, "offset"),
  FOREIGN KEY (user_id, session_id) REFERENCES sessions(user_id, session_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS session_tombstones (
  user_id    INTEGER NOT NULL,
  session_id TEXT NOT NULL,
  deleted_at INTEGER NOT NULL,
  PRIMARY KEY (user_id, session_id)
);

-- Objects whose rows are gone but whose files may still exist (crash-safe GC).
CREATE TABLE IF NOT EXISTS pending_deletes (
  object TEXT PRIMARY KEY
);

-- Single-use registration invites. Only the SHA-256 of the code is stored.
CREATE TABLE IF NOT EXISTS invites (
  code_hash  BLOB PRIMARY KEY,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  used_by    INTEGER,
  used_at    INTEGER
);

-- Per-user library (skills, agents, instructions) as one bundle. Stored in
-- plaintext by design (Registry content, see design doc 8.2); access is
-- restricted to the owner.
CREATE TABLE IF NOT EXISTS libraries (
  user_id    INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  version    INTEGER NOT NULL,
  sha256     TEXT NOT NULL,
  object     TEXT NOT NULL,
  size_bytes INTEGER NOT NULL,
  updated_by TEXT NOT NULL,
  updated_at INTEGER NOT NULL
);

-- Teams share skills and agents (design doc section 8). Packages
-- are plaintext; only members can read them.
CREATE TABLE IF NOT EXISTS teams (
  id         INTEGER PRIMARY KEY,
  name       TEXT NOT NULL UNIQUE,
  created_at INTEGER NOT NULL,
  rev        INTEGER NOT NULL DEFAULT 0 -- bumped on publish; invalidates search caches
);

CREATE TABLE IF NOT EXISTS team_members (
  team_id   INTEGER NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
  user_id  INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role     TEXT NOT NULL, -- owner | maintainer | member (reader: via team_shares, not stored)
  added_at INTEGER NOT NULL,
  PRIMARY KEY (team_id, user_id)
);

CREATE TABLE IF NOT EXISTS packages (
  id          INTEGER PRIMARY KEY,
  team_id      INTEGER NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
  type        TEXT NOT NULL, -- skill | agent
  name        TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  latest      INTEGER NOT NULL DEFAULT 0,
  UNIQUE (team_id, type, name)
);

-- Invite codes to join a team (only their hash is stored).
CREATE TABLE IF NOT EXISTS team_invites (
  code_hash  TEXT PRIMARY KEY,
  team_id    INTEGER NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
  role       TEXT NOT NULL,
  created_by INTEGER NOT NULL,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  max_uses   INTEGER NOT NULL DEFAULT 0, -- 0 = unlimited until it expires
  uses       INTEGER NOT NULL DEFAULT 0
);

-- A team shares its packages (read-only) with another team's members.
CREATE TABLE IF NOT EXISTS team_shares (
  team_id    INTEGER NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
  grantee_id INTEGER NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (team_id, grantee_id)
);
CREATE INDEX IF NOT EXISTS team_shares_by_grantee ON team_shares(grantee_id);

-- Versions are immutable once published.
CREATE TABLE IF NOT EXISTS package_versions (
  package_id   INTEGER NOT NULL REFERENCES packages(id) ON DELETE CASCADE,
  version      INTEGER NOT NULL,
  sha256       TEXT NOT NULL,
  object       TEXT NOT NULL,
  size_bytes   INTEGER NOT NULL,
  description  TEXT NOT NULL DEFAULT '',
  published_by INTEGER NOT NULL,
  published_at INTEGER NOT NULL,
  PRIMARY KEY (package_id, version)
);

-- End-to-end encrypted credentials (design doc 5). The id is an HMAC of
-- the name; the server never sees names or values.
CREATE TABLE IF NOT EXISTS secrets (
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  secret_id  TEXT NOT NULL,
  ciphertext BLOB NOT NULL,
  updated_by TEXT NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (user_id, secret_id)
);

-- Opt-in usage counts of team packages, aggregated per user and day
-- (design doc 9.6). Only names of packages published in the team.
CREATE TABLE IF NOT EXISTS usage_daily (
  team_id INTEGER NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
  type    TEXT NOT NULL,
  name    TEXT NOT NULL,
  day     TEXT NOT NULL,
  user_id INTEGER NOT NULL,
  count   INTEGER NOT NULL,
  PRIMARY KEY (team_id, type, name, day, user_id)
);

-- Per-user skill usage, uploaded by the user's own machines so the account
-- can rank its skills and choose resident ones. Names only, no content.
CREATE TABLE IF NOT EXISTS user_usage_daily (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  type    TEXT NOT NULL,
  name    TEXT NOT NULL,
  day     TEXT NOT NULL,
  count   INTEGER NOT NULL,
  PRIMARY KEY (user_id, type, name, day)
);

-- Server-wide settings shared by all instances (e.g. the prelogin secret).
CREATE TABLE IF NOT EXISTS server_kv (
  key   TEXT PRIMARY KEY,
  value BLOB NOT NULL
);

-- Server admins: may use the admin panel (/admin).
CREATE TABLE IF NOT EXISTS admins (
  user_id  INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  added_at INTEGER NOT NULL
);

-- What admins did (panel and CLI), newest first.
CREATE TABLE IF NOT EXISTS admin_audit (
  at     INTEGER NOT NULL,
  actor  TEXT NOT NULL,
  action TEXT NOT NULL,
  target TEXT NOT NULL DEFAULT '',
  detail TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS admin_audit_by_time ON admin_audit(at DESC);

-- Uploads per user and day (UTC), counted in memory and flushed every
-- minute: the dashboard's activity charts.
CREATE TABLE IF NOT EXISTS activity_daily (
  day     TEXT NOT NULL,
  user_id INTEGER NOT NULL,
  chunks  INTEGER NOT NULL DEFAULT 0,
  bytes   INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (day, user_id)
);

-- Changes asked for in the web console that the user's own machines carry
-- out (they own the library): link a team skill, set resident skills, ...
CREATE TABLE IF NOT EXISTS user_intents (
  id INTEGER PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind       TEXT NOT NULL,
  data       TEXT NOT NULL,
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS user_intents_by_user ON user_intents(user_id, id);

-- Indexes for the hot read paths.
CREATE INDEX IF NOT EXISTS sessions_by_access ON sessions(user_id, last_accessed_at DESC);
CREATE INDEX IF NOT EXISTS sessions_for_gc ON sessions(status, last_accessed_at);
CREATE INDEX IF NOT EXISTS devices_by_user ON devices(user_id, created_at);
CREATE INDEX IF NOT EXISTS tokens_by_user ON tokens(user_id);
CREATE INDEX IF NOT EXISTS team_members_by_user ON team_members(user_id);
CREATE INDEX IF NOT EXISTS tombstones_by_time ON session_tombstones(deleted_at);
