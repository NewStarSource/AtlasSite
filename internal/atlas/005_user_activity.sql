-- S07: User activity tracking and bookmarks

-- Add indexes for better query performance
CREATE INDEX IF NOT EXISTS posts_author_created ON posts(author_id, created_at DESC);
CREATE INDEX IF NOT EXISTS replies_author_created ON replies(author_id, created_at DESC);
CREATE INDEX IF NOT EXISTS post_bookmarks_identity ON post_bookmarks(identity_id, created_at DESC);

INSERT INTO schema_migrations VALUES(5, '005_user_activity', unixepoch());
