CREATE TABLE collection_details (
 collection_id TEXT PRIMARY KEY REFERENCES collections(id) ON DELETE CASCADE,
 version INTEGER NOT NULL DEFAULT 1,
 pinned INTEGER NOT NULL DEFAULT 0 CHECK(pinned IN (0,1)),
 editor_id TEXT REFERENCES users(id)
);
INSERT INTO collection_details(collection_id) SELECT id FROM collections;
CREATE TABLE collection_positions (
 collection_id TEXT NOT NULL,
 post_id TEXT NOT NULL,
 position INTEGER NOT NULL,
 PRIMARY KEY(collection_id,post_id),
 FOREIGN KEY(collection_id,post_id) REFERENCES collection_refs(collection_id,post_id) ON DELETE CASCADE
);
INSERT INTO collection_positions SELECT cr.collection_id,cr.post_id,row_number() OVER(PARTITION BY cr.collection_id ORDER BY p.created_at,p.id) FROM collection_refs cr JOIN posts p ON p.id=cr.post_id;
CREATE TABLE collection_versions (
 collection_id TEXT NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
 version INTEGER NOT NULL,
 title TEXT NOT NULL,
 description TEXT NOT NULL,
 content TEXT NOT NULL,
 status TEXT NOT NULL,
 display_order INTEGER NOT NULL,
 pinned INTEGER NOT NULL,
 editor_id TEXT REFERENCES users(id),
 changed_at INTEGER NOT NULL,
 summary TEXT NOT NULL,
 refs_json TEXT NOT NULL,
 PRIMARY KEY(collection_id,version)
);
INSERT INTO collection_versions SELECT id,1,title,description,content,status,display_order,0,NULL,updated_at,'原有内容迁移',COALESCE((SELECT json_group_array(post_id) FROM (SELECT cr.post_id FROM collection_refs cr JOIN posts p ON p.id=cr.post_id WHERE cr.collection_id=collections.id ORDER BY p.created_at,p.id)),'[]') FROM collections;
INSERT INTO schema_migrations VALUES(8,'008_community_tools',unixepoch());
