-- Existing formal membership is verified separately; this register grants no voting rights.
CREATE TABLE tool_eligibility (
 community_id TEXT NOT NULL REFERENCES communities(id),
 user_id TEXT NOT NULL REFERENCES users(id),
 eligible INTEGER NOT NULL CHECK(eligible IN (0,1)),
 standard_url TEXT NOT NULL,
 verified_by TEXT NOT NULL REFERENCES users(id),
 version INTEGER NOT NULL,
 updated_at INTEGER NOT NULL,
 PRIMARY KEY(community_id,user_id)
);
CREATE TABLE tool_collaborators (
 id TEXT PRIMARY KEY,
 community_id TEXT NOT NULL REFERENCES communities(id),
 user_id TEXT NOT NULL REFERENCES users(id),
 kind TEXT NOT NULL CHECK(kind IN ('announcement','guide','discussion')),
 representative_id TEXT NOT NULL REFERENCES users(id),
 representative_version INTEGER NOT NULL,
 eligibility_version INTEGER NOT NULL,
 purpose TEXT NOT NULL,
 expires_at INTEGER NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('pending','accepted','declined','left','revoked')),
 version INTEGER NOT NULL,
 updated_at INTEGER NOT NULL,
 UNIQUE(community_id,user_id,kind)
);
CREATE INDEX tool_collaborators_user ON tool_collaborators(user_id,expires_at);
CREATE TRIGGER tool_collaborator_account_inactive AFTER UPDATE OF status ON users WHEN NEW.status<>'active' BEGIN
 INSERT INTO management_events SELECT lower(hex(randomblob(16))),'identity-sync','tool-auto-revoke',community_id,json_object('id',id,'version',version),json_object('id',id,'version',version+1,'reason','账户状态变化'),unixepoch() FROM tool_collaborators WHERE (user_id=NEW.id OR representative_id=NEW.id) AND state IN ('pending','accepted');
 UPDATE tool_collaborators SET state='revoked',version=version+1,updated_at=unixepoch() WHERE (user_id=NEW.id OR representative_id=NEW.id) AND state IN ('pending','accepted');
 UPDATE tool_eligibility SET eligible=0,version=version+1,updated_at=unixepoch() WHERE user_id=NEW.id AND eligible=1;
END;
CREATE TRIGGER tool_collaborator_community_inactive AFTER UPDATE OF status ON communities WHEN NEW.status NOT IN ('active','uncategorized') BEGIN
 INSERT INTO management_events SELECT lower(hex(randomblob(16))),'community-state','tool-auto-revoke',community_id,json_object('id',id,'version',version),json_object('id',id,'version',version+1,'reason','社群状态变化'),unixepoch() FROM tool_collaborators WHERE community_id=NEW.id AND state IN ('pending','accepted');
 UPDATE tool_collaborators SET state='revoked',version=version+1,updated_at=unixepoch() WHERE community_id=NEW.id AND state IN ('pending','accepted');
END;
INSERT INTO schema_migrations VALUES(9,'009_tool_collaborators',unixepoch());
