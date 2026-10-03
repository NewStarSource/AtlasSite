ALTER TABLE users ADD COLUMN issuer TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX users_issuer_subject ON users(issuer,account_id);
ALTER TABLE sessions ADD COLUMN auth_time INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sessions ADD COLUMN reauthenticated_at INTEGER NOT NULL DEFAULT 0;
CREATE TABLE oidc_flows (
 state_hash TEXT PRIMARY KEY, binding_hash TEXT NOT NULL, nonce TEXT NOT NULL,
 verifier TEXT NOT NULL, expires_at INTEGER NOT NULL, purpose TEXT NOT NULL,
 session_hash TEXT NOT NULL DEFAULT ''
);
CREATE TABLE consumed_tokens(hash TEXT PRIMARY KEY, expires_at INTEGER NOT NULL);
CREATE TABLE identity_cursor(id INTEGER PRIMARY KEY CHECK(id=1), value INTEGER NOT NULL);
INSERT INTO identity_cursor VALUES(1,0);
CREATE TABLE revoked_account_sessions(sid TEXT PRIMARY KEY, expires_at INTEGER NOT NULL);
CREATE TABLE account_states(account_id TEXT PRIMARY KEY, status TEXT NOT NULL, version INTEGER NOT NULL);
CREATE TABLE security_events(id TEXT PRIMARY KEY,event TEXT NOT NULL,created_at INTEGER NOT NULL);
CREATE TABLE auth_limits(key TEXT PRIMARY KEY,count INTEGER NOT NULL,reset_at INTEGER NOT NULL);
INSERT INTO schema_migrations VALUES(2);
