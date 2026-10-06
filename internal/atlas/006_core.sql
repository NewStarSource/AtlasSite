-- Preserve legacy demonstration authors without treating email as a login identity.
INSERT OR IGNORE INTO users(id,account_id,subject_id,alias,status,status_version,issuer)
SELECT id,'legacy:'||id,'legacy:'||id,name,'active',1,'' FROM identities;
CREATE TABLE posts_next (
 id TEXT PRIMARY KEY, community_id TEXT REFERENCES communities(id), author_id TEXT NOT NULL REFERENCES users(id),
 title TEXT NOT NULL, content TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'published' CHECK(status IN ('published','deleted','hidden')),
 view_count INTEGER NOT NULL DEFAULT 0, reply_count INTEGER NOT NULL DEFAULT 0, bookmark_count INTEGER NOT NULL DEFAULT 0,
 created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, version_number INTEGER NOT NULL DEFAULT 1,
 license TEXT NOT NULL DEFAULT 'reserved', request_id TEXT, UNIQUE(author_id,request_id)
);
INSERT INTO posts_next(id,community_id,author_id,title,content,status,view_count,reply_count,bookmark_count,created_at,updated_at)
SELECT id,community_id,author_id,title,content,CASE WHEN status='published' THEN 'published' ELSE 'hidden' END,view_count,reply_count,bookmark_count,created_at,updated_at FROM posts;
CREATE TABLE replies_next (
 id TEXT PRIMARY KEY,post_id TEXT NOT NULL REFERENCES posts_next(id),parent_id TEXT REFERENCES replies_next(id),
 author_id TEXT NOT NULL REFERENCES users(id),content TEXT NOT NULL,level INTEGER NOT NULL DEFAULT 0,
 status TEXT NOT NULL DEFAULT 'published',created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL,
 version_number INTEGER NOT NULL DEFAULT 1,request_id TEXT,UNIQUE(author_id,request_id)
);
INSERT INTO replies_next(id,post_id,parent_id,author_id,content,level,status,created_at,updated_at) SELECT id,post_id,parent_id,author_id,content,level,status,created_at,updated_at FROM replies;
CREATE TABLE bookmarks_next(id TEXT PRIMARY KEY,post_id TEXT NOT NULL REFERENCES posts_next(id),identity_id TEXT NOT NULL REFERENCES users(id),created_at INTEGER NOT NULL,UNIQUE(post_id,identity_id));
INSERT INTO bookmarks_next SELECT * FROM post_bookmarks;
DROP TABLE post_bookmarks;
DROP TABLE replies;
DROP TABLE posts;
ALTER TABLE posts_next RENAME TO posts;
ALTER TABLE replies_next RENAME TO replies;
ALTER TABLE bookmarks_next RENAME TO post_bookmarks;
DROP TABLE identities;
CREATE INDEX posts_community ON posts(community_id,status,created_at DESC,id);
CREATE INDEX posts_recent ON posts(created_at DESC,id) WHERE status='published';
CREATE INDEX posts_author ON posts(author_id,created_at DESC,id);
CREATE INDEX replies_post ON replies(post_id,created_at,id);
CREATE INDEX replies_author ON replies(author_id,created_at DESC,id);
CREATE INDEX bookmarks_owner ON post_bookmarks(identity_id,created_at DESC,id);
CREATE TABLE user_settings(user_id TEXT PRIMARY KEY REFERENCES users(id),hide_relations INTEGER NOT NULL DEFAULT 0,reply_notifications INTEGER NOT NULL DEFAULT 1,retain_content INTEGER NOT NULL DEFAULT 0,created_at INTEGER NOT NULL);
INSERT INTO user_settings(user_id,created_at) SELECT id,unixepoch() FROM users;
CREATE TRIGGER settings_for_user AFTER INSERT ON users BEGIN INSERT OR IGNORE INTO user_settings(user_id,created_at) VALUES(new.id,unixepoch()); END;
CREATE TABLE subscriptions(user_id TEXT NOT NULL REFERENCES users(id),kind TEXT NOT NULL CHECK(kind IN ('community','topic')),object_id TEXT NOT NULL,notifications INTEGER NOT NULL DEFAULT 0,created_at INTEGER NOT NULL,PRIMARY KEY(user_id,kind,object_id));
CREATE TABLE retained_objects(user_id TEXT NOT NULL REFERENCES users(id),object_id TEXT NOT NULL,PRIMARY KEY(user_id,object_id));
CREATE TABLE blocks(owner_id TEXT NOT NULL REFERENCES users(id),target_id TEXT NOT NULL REFERENCES users(id),created_at INTEGER NOT NULL,PRIMARY KEY(owner_id,target_id),CHECK(owner_id<>target_id));
CREATE TABLE post_likes(user_id TEXT NOT NULL REFERENCES users(id),post_id TEXT NOT NULL REFERENCES posts(id),created_at INTEGER NOT NULL,PRIMARY KEY(user_id,post_id));
CREATE TABLE drafts(id TEXT PRIMARY KEY,author_id TEXT NOT NULL REFERENCES users(id),version_number INTEGER NOT NULL DEFAULT 1,updated_at INTEGER NOT NULL,object_id TEXT NOT NULL);
CREATE TABLE post_topics(post_id TEXT NOT NULL REFERENCES posts(id),topic_id TEXT NOT NULL REFERENCES topics(id),PRIMARY KEY(post_id,topic_id));
CREATE TABLE media(id TEXT PRIMARY KEY,owner_id TEXT NOT NULL REFERENCES users(id),post_id TEXT REFERENCES posts(id),mime TEXT NOT NULL,width INTEGER NOT NULL,height INTEGER NOT NULL,license TEXT NOT NULL,body BLOB NOT NULL,thumbnail BLOB NOT NULL,created_at INTEGER NOT NULL,removed_at INTEGER);
CREATE TABLE restrictions(id TEXT PRIMARY KEY,object_id TEXT NOT NULL,reason_code TEXT NOT NULL,created_at INTEGER NOT NULL,removed_at INTEGER);
CREATE TABLE isolated_children(root_id TEXT NOT NULL,child_id TEXT NOT NULL,PRIMARY KEY(root_id,child_id));
CREATE INDEX restrictions_object ON restrictions(object_id) WHERE removed_at IS NULL;
CREATE TABLE cases(id TEXT PRIMARY KEY,request_id TEXT NOT NULL,user_id TEXT REFERENCES users(id),subject_id TEXT REFERENCES users(id),kind TEXT NOT NULL CHECK(kind IN ('report','emergency','appeal')),object_id TEXT NOT NULL DEFAULT '',parent_id TEXT REFERENCES cases(id),status TEXT NOT NULL DEFAULT 'received',received_at INTEGER NOT NULL,due_at INTEGER NOT NULL,closed_at INTEGER,decision TEXT NOT NULL DEFAULT '',reviewer TEXT NOT NULL DEFAULT '',conflict INTEGER NOT NULL DEFAULT 0,UNIQUE(user_id,request_id));
CREATE UNIQUE INDEX case_requests ON cases(IFNULL(user_id,''),request_id);
CREATE TABLE notifications(id TEXT PRIMARY KEY,user_id TEXT NOT NULL REFERENCES users(id),kind TEXT NOT NULL,object_id TEXT NOT NULL,post_id TEXT NOT NULL DEFAULT '',created_at INTEGER NOT NULL,read_at INTEGER,UNIQUE(user_id,kind,object_id));
CREATE TABLE jobs(id TEXT PRIMARY KEY,business_key TEXT NOT NULL UNIQUE,kind TEXT NOT NULL,object_id TEXT NOT NULL,status TEXT NOT NULL DEFAULT 'pending',attempts INTEGER NOT NULL DEFAULT 0,available_at INTEGER NOT NULL,lease_until INTEGER NOT NULL DEFAULT 0,generation INTEGER NOT NULL DEFAULT 0,error_code TEXT NOT NULL DEFAULT '',created_at INTEGER NOT NULL);
CREATE TABLE operation_events(sequence INTEGER PRIMARY KEY AUTOINCREMENT,event_id TEXT NOT NULL UNIQUE,object_id TEXT NOT NULL,action TEXT NOT NULL,created_at INTEGER NOT NULL);
CREATE TABLE runtime_state(id INTEGER PRIMARY KEY CHECK(id=1),mode TEXT NOT NULL DEFAULT 'normal',updated_at INTEGER NOT NULL);
INSERT INTO runtime_state VALUES(1,'normal',unixepoch());
CREATE TABLE community_paths(path TEXT PRIMARY KEY,community_id TEXT NOT NULL REFERENCES communities(id));
CREATE TABLE collection_refs(collection_id TEXT NOT NULL REFERENCES collections(id),post_id TEXT NOT NULL REFERENCES posts(id),PRIMARY KEY(collection_id,post_id));
CREATE TABLE alias_history(id TEXT PRIMARY KEY,user_id TEXT NOT NULL REFERENCES users(id),alias TEXT NOT NULL,created_at INTEGER NOT NULL);
CREATE TRIGGER alias_initial AFTER INSERT ON users BEGIN INSERT INTO alias_history VALUES(lower(hex(randomblob(16))),new.id,new.alias,unixepoch()); END;
CREATE TRIGGER alias_updated AFTER UPDATE OF alias ON users WHEN old.alias<>new.alias BEGIN INSERT INTO alias_history VALUES(lower(hex(randomblob(16))),new.id,new.alias,unixepoch()); END;
INSERT INTO alias_history SELECT lower(hex(randomblob(16))),id,alias,unixepoch() FROM users;
INSERT INTO schema_migrations VALUES(6,'006_core',unixepoch());
