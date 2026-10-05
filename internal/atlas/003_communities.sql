-- S05: Communities, categories, and content structure

-- Taxonomy: domain -> direction -> subcategory -> community
CREATE TABLE domains (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  description TEXT NOT NULL,
  display_order INTEGER NOT NULL,
  created_at INTEGER NOT NULL
);

CREATE TABLE directions (
  id TEXT PRIMARY KEY,
  domain_id TEXT NOT NULL REFERENCES domains(id),
  name TEXT NOT NULL,
  description TEXT NOT NULL,
  display_order INTEGER NOT NULL,
  created_at INTEGER NOT NULL
);

CREATE TABLE subcategories (
  id TEXT PRIMARY KEY,
  direction_id TEXT NOT NULL REFERENCES directions(id),
  name TEXT NOT NULL,
  description TEXT NOT NULL,
  display_order INTEGER NOT NULL,
  created_at INTEGER NOT NULL
);

-- Communities
CREATE TABLE communities (
  id TEXT PRIMARY KEY,
  slug TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL,
  description TEXT NOT NULL,
  subcategory_id TEXT REFERENCES subcategories(id),
  status TEXT NOT NULL CHECK(status IN ('active','archived','pending','uncategorized')) DEFAULT 'pending',
  contact_method TEXT NOT NULL DEFAULT '',
  source_url TEXT NOT NULL DEFAULT '',
  source_license TEXT NOT NULL DEFAULT '',
  verified INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE INDEX communities_subcategory ON communities(subcategory_id) WHERE status='active';
CREATE INDEX communities_status ON communities(status);

-- Topics within communities
CREATE TABLE topics (
  id TEXT PRIMARY KEY,
  community_id TEXT NOT NULL REFERENCES communities(id),
  slug TEXT NOT NULL,
  name TEXT NOT NULL,
  description TEXT NOT NULL,
  display_order INTEGER NOT NULL,
  status TEXT NOT NULL CHECK(status IN ('active','archived')) DEFAULT 'active',
  created_at INTEGER NOT NULL,
  UNIQUE(community_id, slug)
);

-- Collections: announcements, guides, discussions (read-only for now)
CREATE TABLE collections (
  id TEXT PRIMARY KEY,
  community_id TEXT NOT NULL REFERENCES communities(id),
  type TEXT NOT NULL CHECK(type IN ('announcement','guide','discussion')),
  title TEXT NOT NULL,
  slug TEXT NOT NULL,
  description TEXT NOT NULL,
  content TEXT NOT NULL,
  display_order INTEGER NOT NULL,
  status TEXT NOT NULL CHECK(status IN ('published','draft','archived')) DEFAULT 'draft',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  UNIQUE(community_id, type, slug)
);

CREATE INDEX collections_community ON collections(community_id, type) WHERE status='published';

INSERT INTO schema_migrations VALUES(3);
