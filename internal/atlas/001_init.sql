CREATE TABLE IF NOT EXISTS schema_migrations(
  version INTEGER PRIMARY KEY,
  name TEXT NOT NULL DEFAULT '',
  applied_at INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS users(
 id TEXT PRIMARY KEY, account_id TEXT NOT NULL UNIQUE, subject_id TEXT NOT NULL,
 alias TEXT NOT NULL, status TEXT NOT NULL CHECK(status IN ('active','suspended','deactivated','recovery_pending','deleted')),
 status_version INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions(
 id TEXT PRIMARY KEY, token_hash TEXT NOT NULL UNIQUE, user_id TEXT NOT NULL REFERENCES users(id),
 account_session_id TEXT NOT NULL, created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL, revoked_at INTEGER
);
INSERT OR IGNORE INTO schema_migrations VALUES(1, '001_init', 0);
