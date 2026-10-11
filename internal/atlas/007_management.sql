CREATE TABLE team_members (
 user_id TEXT PRIMARY KEY REFERENCES users(id),
 role TEXT NOT NULL CHECK(role IN ('admin','member')),
 authority TEXT NOT NULL,
 expires_at INTEGER NOT NULL,
 version INTEGER NOT NULL DEFAULT 1,
 updated_at INTEGER NOT NULL
);
CREATE TABLE community_representatives (
 community_id TEXT PRIMARY KEY REFERENCES communities(id),
 user_id TEXT NOT NULL REFERENCES users(id),
 authority TEXT NOT NULL,
 expires_at INTEGER NOT NULL,
 version INTEGER NOT NULL DEFAULT 1,
 updated_at INTEGER NOT NULL
);
CREATE INDEX representatives_user ON community_representatives(user_id,expires_at);
ALTER TABLE communities ADD COLUMN management_version INTEGER NOT NULL DEFAULT 1;
CREATE TABLE management_events (
 id TEXT PRIMARY KEY,
 actor_id TEXT NOT NULL,
 action TEXT NOT NULL,
 object_id TEXT NOT NULL,
 before_json TEXT NOT NULL,
 after_json TEXT NOT NULL,
 created_at INTEGER NOT NULL
);
CREATE INDEX management_events_time ON management_events(created_at DESC);
INSERT INTO schema_migrations VALUES(7,'007_management',unixepoch());
