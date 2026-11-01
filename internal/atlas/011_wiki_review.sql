CREATE TABLE wiki_suggestions (
 id TEXT PRIMARY KEY,
 page_id TEXT NOT NULL REFERENCES wiki_pages(id),
 base_version INTEGER NOT NULL,
 author_id TEXT NOT NULL REFERENCES users(id),
 author_alias TEXT NOT NULL,
 target TEXT NOT NULL,
 old_text TEXT NOT NULL,
 proposed TEXT NOT NULL,
 evidence TEXT NOT NULL,
 sources_json TEXT NOT NULL,
 basis_hash TEXT NOT NULL,
 was_rejected INTEGER NOT NULL DEFAULT 0 CHECK(was_rejected IN (0,1)),
 state TEXT NOT NULL CHECK(state IN ('pending','accepted','partial','rejected','withdrawn')),
 version INTEGER NOT NULL DEFAULT 1,
 result_version INTEGER NOT NULL DEFAULT 0,
 resolution TEXT NOT NULL DEFAULT '',
 reviewer_id TEXT REFERENCES users(id),
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL
);
CREATE INDEX wiki_suggestions_page ON wiki_suggestions(page_id,created_at);
CREATE TABLE wiki_suggestion_actions (
 id TEXT PRIMARY KEY,
 suggestion_id TEXT NOT NULL REFERENCES wiki_suggestions(id),
 actor_id TEXT NOT NULL REFERENCES users(id),
 decision TEXT NOT NULL,
 note TEXT NOT NULL,
 result_version INTEGER NOT NULL,
 created_at INTEGER NOT NULL
);
INSERT INTO schema_migrations VALUES(11,'011_wiki_review',unixepoch());
