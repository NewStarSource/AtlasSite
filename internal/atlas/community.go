package atlas

import (
	"database/sql"
	"errors"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

type Domain struct {
	ID           string
	Name         string
	Description  string
	DisplayOrder int
}

type Direction struct {
	ID           string
	DomainID     string
	Name         string
	Description  string
	DisplayOrder int
}

type Subcategory struct {
	ID           string
	DirectionID  string
	Name         string
	Description  string
	DisplayOrder int
}

type Community struct {
	ID             string
	Slug           string
	Name           string
	Description    string
	SubcategoryID  string
	Status         string
	ContactMethod  string
	SourceURL      string
	SourceLicense  string
	Verified       bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type Topic struct {
	ID           string
	CommunityID  string
	Slug         string
	Name         string
	Description  string
	DisplayOrder int
	Status       string
}

type Collection struct {
	ID          string
	CommunityID string
	Type        string
	Title       string
	Slug        string
	Description string
	Content     string
	Status      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (app *App) communityRoutes(router *chi.Mux) {
	// Domain listing API
	router.Get("/api/v1/discover", func(w http.ResponseWriter, r *http.Request) {
		domains, err := app.listDomains()
		if err != nil {
			respond(w, 503, map[string]string{"code": "DEPENDENCY_UNAVAILABLE"})
			return
		}
		respond(w, 200, map[string]any{"domains": domains})
	})

	// Community detail API
	router.Get("/api/v1/c/{slug}", func(w http.ResponseWriter, r *http.Request) {
		slug := chi.URLParam(r, "slug")
		community, err := app.getCommunityBySlug(slug)
		if err == sql.ErrNoRows {
			respond(w, 404, map[string]string{"code": "NOT_FOUND", "message": "社群不存在"})
			return
		}
		if err != nil {
			respond(w, 503, map[string]string{"code": "DEPENDENCY_UNAVAILABLE"})
			return
		}
		if community.Status != "active" {
			respond(w, 404, map[string]string{"code": "NOT_FOUND", "message": "社群不可用"})
			return
		}

		topics, _ := app.listTopics(community.ID)
		collections, _ := app.listCollections(community.ID)

		respond(w, 200, map[string]any{
			"community":   community,
			"topics":      topics,
			"collections": collections,
		})
	})

	// Community detail page
	router.Get("/c/{slug}", func(w http.ResponseWriter, r *http.Request) {
		slug := chi.URLParam(r, "slug")
		community, err := app.getCommunityBySlug(slug)
		if err == sql.ErrNoRows {
			w.WriteHeader(404)
			w.Write([]byte("社群不存在"))
			return
		}
		if err != nil {
			w.WriteHeader(503)
			w.Write([]byte("服务暂时不可用"))
			return
		}
		if community.Status != "active" {
			w.WriteHeader(404)
			w.Write([]byte("社群不可用"))
			return
		}

		topics, _ := app.listTopics(community.ID)
		collections, _ := app.listCollections(community.ID)

		identity, _, _ := app.currentIdentity(r)

		type PageData struct {
			Title      string
			Page       string
			Identity   *Identity
			Content    template.HTML
			Community  *Community
			Topics     []Topic
			Collections []Collection
		}

		var contentBuf strings.Builder
		contentBuf.WriteString(`<nav class="breadcrumb">
			<a href="/">首页</a>
			<span class="breadcrumb-sep">/</span>
			<a href="/discover">发现</a>
			<span class="breadcrumb-sep">/</span>
			<span>`)
		contentBuf.WriteString(template.HTMLEscapeString(community.Name))
		contentBuf.WriteString(`</span>
		</nav>
		<div class="community-header">
			<h1 class="community-title">`)
		contentBuf.WriteString(template.HTMLEscapeString(community.Name))
		if community.Verified {
			contentBuf.WriteString(` <span class="status-badge verified">已验证</span>`)
		}
		contentBuf.WriteString(`</h1>
			<p class="community-subtitle">`)
		contentBuf.WriteString(template.HTMLEscapeString(community.Description))
		contentBuf.WriteString(`</p>
			<div class="community-actions">
				<button class="btn">关注</button>
				<button class="btn btn-secondary">分享</button>
			</div>
		</div>`)

		if len(topics) > 0 {
			contentBuf.WriteString(`<div class="content-section">
				<h2 class="section-title">讨论主题</h2>
				<div class="topic-list">`)
			for _, topic := range topics {
				contentBuf.WriteString(`<a href="/c/`)
				contentBuf.WriteString(template.URLQueryEscaper(slug))
				contentBuf.WriteString(`/t/`)
				contentBuf.WriteString(template.URLQueryEscaper(topic.Slug))
				contentBuf.WriteString(`" class="topic-tag">`)
				contentBuf.WriteString(template.HTMLEscapeString(topic.Name))
				contentBuf.WriteString(`</a>`)
			}
			contentBuf.WriteString(`</div></div>`)
		}

		if len(collections) > 0 {
			contentBuf.WriteString(`<div class="content-section">
				<h2 class="section-title">`)
			for _, coll := range collections {
				if coll.Type == "announcement" {
					contentBuf.WriteString(`公告`)
				} else if coll.Type == "guide" {
					contentBuf.WriteString(`新人指引`)
				} else {
					contentBuf.WriteString(`精选讨论`)
				}
				contentBuf.WriteString(`</h2>
				<div class="collection-card">
					<h3 class="collection-title">`)
				contentBuf.WriteString(template.HTMLEscapeString(coll.Title))
				contentBuf.WriteString(`</h3>
					<div class="collection-content">`)
				contentBuf.WriteString(template.HTMLEscapeString(coll.Content))
				contentBuf.WriteString(`</div>
				</div>`)
				break
			}
			contentBuf.WriteString(`</div>`)
		}

		if community.SourceURL != "" {
			contentBuf.WriteString(`<div class="content-section">
				<h2 class="section-title">来源信息</h2>
				<p style="color: var(--text-secondary); font-size: var(--text-sm);">
					内容来源：<a href="`)
			contentBuf.WriteString(template.HTMLEscapeString(community.SourceURL))
			contentBuf.WriteString(`" target="_blank" rel="noopener">`)
			contentBuf.WriteString(template.HTMLEscapeString(community.SourceURL))
			contentBuf.WriteString(`</a><br>
					许可协议：`)
			contentBuf.WriteString(template.HTMLEscapeString(community.SourceLicense))
			contentBuf.WriteString(`
				</p>
			</div>`)
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = app.page.Execute(w, PageData{
			Title:       community.Name,
			Page:        "community",
			Identity:    identity,
			Content:     template.HTML(contentBuf.String()),
			Community:   community,
			Topics:      topics,
			Collections: collections,
		})
	})
}

func (app *App) listDomains() ([]Domain, error) {
	rows, err := app.db.Query("SELECT id, name, description, display_order FROM domains ORDER BY display_order")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var domains []Domain
	for rows.Next() {
		var d Domain
		if err := rows.Scan(&d.ID, &d.Name, &d.Description, &d.DisplayOrder); err != nil {
			return nil, err
		}
		domains = append(domains, d)
	}
	return domains, rows.Err()
}

func (app *App) getCommunityBySlug(slug string) (*Community, error) {
	var c Community
	var verified int
	var createdAt, updatedAt int64
	err := app.db.QueryRow(`
		SELECT id, slug, name, description, COALESCE(subcategory_id,''), status,
		       contact_method, source_url, source_license, verified, created_at, updated_at
		FROM communities WHERE slug=?
	`, slug).Scan(&c.ID, &c.Slug, &c.Name, &c.Description, &c.SubcategoryID, &c.Status,
		&c.ContactMethod, &c.SourceURL, &c.SourceLicense, &verified, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	c.Verified = verified == 1
	c.CreatedAt = time.Unix(createdAt, 0)
	c.UpdatedAt = time.Unix(updatedAt, 0)
	return &c, nil
}

func (app *App) listTopics(communityID string) ([]Topic, error) {
	rows, err := app.db.Query(`
		SELECT id, community_id, slug, name, description, display_order, status
		FROM topics WHERE community_id=? AND status='active' ORDER BY display_order
	`, communityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var topics []Topic
	for rows.Next() {
		var t Topic
		if err := rows.Scan(&t.ID, &t.CommunityID, &t.Slug, &t.Name, &t.Description, &t.DisplayOrder, &t.Status); err != nil {
			return nil, err
		}
		topics = append(topics, t)
	}
	return topics, rows.Err()
}

func (app *App) listCollections(communityID string) ([]Collection, error) {
	rows, err := app.db.Query(`
		SELECT id, community_id, type, title, slug, description, content, status, created_at, updated_at
		FROM collections WHERE community_id=? AND status='published' ORDER BY display_order
	`, communityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var collections []Collection
	for rows.Next() {
		var c Collection
		var createdAt, updatedAt int64
		if err := rows.Scan(&c.ID, &c.CommunityID, &c.Type, &c.Title, &c.Slug, &c.Description,
			&c.Content, &c.Status, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		c.CreatedAt = time.Unix(createdAt, 0)
		c.UpdatedAt = time.Unix(updatedAt, 0)
		collections = append(collections, c)
	}
	return collections, rows.Err()
}

func randomID() string {
	const charset = "0123456789abcdefghijklmnopqrstuvwxyz"
	b := make([]byte, 16)
	now := time.Now().UnixNano()
	for i := range b {
		now = now*1103515245 + 12345
		idx := (now / 65536) % int64(len(charset))
		if idx < 0 {
			idx = -idx
		}
		b[i] = charset[idx]
	}
	return string(b)
}

func slugify(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, " ", "-")
	var result []rune
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			result = append(result, r)
		}
	}
	return string(result)
}

var ErrDuplicateSlug = errors.New("slug already exists")

func (app *App) createCommunity(name, description, sourceURL, sourceLicense string) (*Community, error) {
	slug := slugify(name)
	// If slugify returns empty (e.g., for Chinese names), use random ID as slug
	if slug == "" {
		slug = randomID()
	}
	id := randomID()
	now := time.Now().Unix()

	_, err := app.db.Exec(`
		INSERT INTO communities (id, slug, name, description, status, source_url, source_license, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'uncategorized', ?, ?, ?, ?)
	`, id, slug, name, description, sourceURL, sourceLicense, now, now)

	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, ErrDuplicateSlug
		}
		return nil, err
	}

	return app.getCommunityBySlug(slug)
}
