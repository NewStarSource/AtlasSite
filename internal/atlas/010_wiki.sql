CREATE TABLE wiki_pages (
 id TEXT PRIMARY KEY,
 kind TEXT NOT NULL CHECK(kind IN ('discovery','community','work')),
 state TEXT NOT NULL DEFAULT 'active' CHECK(state IN ('active','hidden','deleted')),
 creator_id TEXT NOT NULL REFERENCES users(id),
 published_version INTEGER NOT NULL DEFAULT 0,
 latest_version INTEGER NOT NULL DEFAULT 0,
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL,
 withdrawn_at INTEGER,
 reason TEXT NOT NULL DEFAULT ''
);
CREATE TABLE wiki_versions (
 page_id TEXT NOT NULL REFERENCES wiki_pages(id),
 number INTEGER NOT NULL,
 base_version INTEGER NOT NULL,
 title TEXT NOT NULL,
 summary TEXT NOT NULL,
 body TEXT NOT NULL,
 fields_json TEXT NOT NULL DEFAULT '{}',
 sources_json TEXT NOT NULL,
 community_id TEXT REFERENCES communities(id),
 editor_id TEXT NOT NULL REFERENCES users(id),
 editor_alias TEXT NOT NULL,
 edit_summary TEXT NOT NULL,
 review_status TEXT NOT NULL CHECK(review_status IN ('accepted','pending','rejected','withdrawn')),
 reviewer_id TEXT REFERENCES users(id),
 review_reason TEXT NOT NULL DEFAULT '',
 reviewed_at INTEGER,
 created_at INTEGER NOT NULL,
 PRIMARY KEY(page_id,number)
);
CREATE INDEX wiki_versions_community ON wiki_versions(community_id,page_id);
CREATE TABLE wiki_replies (
 id TEXT PRIMARY KEY,
 page_id TEXT NOT NULL REFERENCES wiki_pages(id),
 parent_id TEXT REFERENCES wiki_replies(id),
 author_id TEXT NOT NULL REFERENCES users(id),
 author_alias TEXT NOT NULL,
 content TEXT NOT NULL,
 status TEXT NOT NULL CHECK(status IN ('published','deleted')),
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL
);
CREATE INDEX wiki_replies_page ON wiki_replies(page_id,created_at,id);
INSERT INTO schema_migrations VALUES(10,'010_wiki',unixepoch());
