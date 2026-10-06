package atlas

import (
	"database/sql"
	"errors"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
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
	ID            string
	Slug          string
	Name          string
	Description   string
	SubcategoryID string
	Status        string
	ContactMethod string
	SourceURL     string
	SourceLicense string
	Verified      bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
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
	// Discovery page
	router.Get("/discover", func(w http.ResponseWriter, r *http.Request) {
		domains, err := app.listDomainsWithHierarchy()
		if err != nil {
			w.WriteHeader(503)
			w.Write([]byte("服务暂时不可用"))
			return
		}

		identity, _, _ := app.currentIdentity(r)

		type PageData struct {
			Title    string
			Page     string
			Identity *Identity
			Content  template.HTML
		}

		var contentBuf strings.Builder
		contentBuf.WriteString(pageHeading("EXPLORE COMMUNITIES", "发现社群", "从领域走向方向，找到可以认真交流的地方。", `<a class="btn" href="/search?type=community">搜索社群</a>`) + `<div class="discover-container">`)

		for _, domain := range domains {
			contentBuf.WriteString(`<div id="domain-` + esc(domain.ID) + `" class="domain-section">
				<h2 class="domain-title">`)
			contentBuf.WriteString(template.HTMLEscapeString(domain.Name))
			contentBuf.WriteString(`</h2>
				<p class="domain-description">`)
			contentBuf.WriteString(template.HTMLEscapeString(domain.Description))
			contentBuf.WriteString(`</p>`)

			for _, direction := range domain.Directions {
				contentBuf.WriteString(`<div id="direction-` + esc(direction.ID) + `" class="direction-section">
					<h3 class="direction-title">`)
				contentBuf.WriteString(template.HTMLEscapeString(direction.Name))
				contentBuf.WriteString(`</h3>`)

				for _, subcategory := range direction.Subcategories {
					if len(subcategory.Communities) > 0 {
						contentBuf.WriteString(`<div id="subcategory-` + esc(subcategory.ID) + `" class="subcategory-section">
							<h4 class="subcategory-title">`)
						contentBuf.WriteString(template.HTMLEscapeString(subcategory.Name))
						contentBuf.WriteString(`</h4>
							<div class="community-grid">`)

						for _, community := range subcategory.Communities {
							contentBuf.WriteString(`<a href="/c/`)
							contentBuf.WriteString(template.URLQueryEscaper(community.Slug))
							contentBuf.WriteString(`" class="community-card">
								<h5 class="community-card-title">`)
							contentBuf.WriteString(template.HTMLEscapeString(community.Name))
							if community.Verified {
								contentBuf.WriteString(` <span class="badge-verified">✓</span>`)
							}
							contentBuf.WriteString(`</h5>
								<p class="community-card-description">`)
							contentBuf.WriteString(template.HTMLEscapeString(community.Description))
							contentBuf.WriteString(`</p>
							</a>`)
						}

						contentBuf.WriteString(`</div></div>`)
					}
				}

				contentBuf.WriteString(`</div>`)
			}

			contentBuf.WriteString(`</div>`)
		}

		rows, err := app.db.Query("SELECT slug,name FROM communities WHERE status='uncategorized' OR (status='active' AND subcategory_id IS NULL) ORDER BY name,id")
		if err != nil {
			fail(w, err)
			return
		}
		contentBuf.WriteString(`<section class="panel" id="uncategorized"><h2>待分类社群</h2>`)
		count := 0
		for rows.Next() {
			var slug, name string
			if err = rows.Scan(&slug, &name); err != nil {
				rows.Close()
				fail(w, err)
				return
			}
			contentBuf.WriteString(`<p><a href="/c/` + pathID(slug) + `">` + esc(name) + `</a></p>`)
			count++
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			fail(w, err)
			return
		}
		if count == 0 {
			contentBuf.WriteString(`<p>暂无待分类社群。</p>`)
		}
		contentBuf.WriteString(`</section></div>`)

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = app.page.Execute(w, PageData{
			Title:    "发现社群",
			Page:     "discover",
			Identity: identity,
			Content:  template.HTML(contentBuf.String()),
		})
	})

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
		if community.Status != "active" && community.Status != "uncategorized" {
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

	router.Get("/c/{slug}", app.communityPage)
	router.Get("/c/{slug}/t/{topic}", app.topicPage)
	router.Get("/communities/{old}", app.oldCommunityPath)
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

func (app *App) listDirections(domainID string) ([]Direction, error) {
	rows, err := app.db.Query(`
		SELECT id, domain_id, name, description, display_order
		FROM directions WHERE domain_id=? ORDER BY display_order
	`, domainID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var directions []Direction
	for rows.Next() {
		var d Direction
		if err := rows.Scan(&d.ID, &d.DomainID, &d.Name, &d.Description, &d.DisplayOrder); err != nil {
			return nil, err
		}
		directions = append(directions, d)
	}
	return directions, rows.Err()
}

func (app *App) listSubcategories(directionID string) ([]Subcategory, error) {
	rows, err := app.db.Query(`
		SELECT id, direction_id, name, description, display_order
		FROM subcategories WHERE direction_id=? ORDER BY display_order
	`, directionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subcategories []Subcategory
	for rows.Next() {
		var s Subcategory
		if err := rows.Scan(&s.ID, &s.DirectionID, &s.Name, &s.Description, &s.DisplayOrder); err != nil {
			return nil, err
		}
		subcategories = append(subcategories, s)
	}
	return subcategories, rows.Err()
}

type DomainWithHierarchy struct {
	Domain
	Directions []DirectionWithHierarchy
}

type DirectionWithHierarchy struct {
	Direction
	Subcategories []SubcategoryWithCommunities
}

type SubcategoryWithCommunities struct {
	Subcategory
	Communities []Community
}

func (app *App) listDomainsWithHierarchy() ([]DomainWithHierarchy, error) {
	domains, err := app.listDomains()
	if err != nil {
		return nil, err
	}

	var result []DomainWithHierarchy
	for _, domain := range domains {
		directions, err := app.listDirections(domain.ID)
		if err != nil {
			return nil, err
		}

		var directionsWithSubs []DirectionWithHierarchy
		for _, direction := range directions {
			subcategories, err := app.listSubcategories(direction.ID)
			if err != nil {
				return nil, err
			}

			var subsWithComms []SubcategoryWithCommunities
			for _, subcategory := range subcategories {
				communities, err := app.listCommunitiesBySubcategory(subcategory.ID)
				if err != nil {
					return nil, err
				}

				subsWithComms = append(subsWithComms, SubcategoryWithCommunities{
					Subcategory: subcategory,
					Communities: communities,
				})
			}

			directionsWithSubs = append(directionsWithSubs, DirectionWithHierarchy{
				Direction:     direction,
				Subcategories: subsWithComms,
			})
		}

		result = append(result, DomainWithHierarchy{
			Domain:     domain,
			Directions: directionsWithSubs,
		})
	}

	return result, nil
}

func (app *App) listCommunitiesBySubcategory(subcategoryID string) ([]Community, error) {
	rows, err := app.db.Query(`
		SELECT id, slug, name, description, subcategory_id, status,
		       contact_method, source_url, source_license, verified,
		       created_at, updated_at
		FROM communities
		WHERE subcategory_id = ? AND status = 'active'
		ORDER BY verified DESC, name
	`, subcategoryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var communities []Community
	for rows.Next() {
		var c Community
		var createdAt, updatedAt int64
		var subcatID sql.NullString
		if err := rows.Scan(&c.ID, &c.Slug, &c.Name, &c.Description, &subcatID,
			&c.Status, &c.ContactMethod, &c.SourceURL, &c.SourceLicense,
			&c.Verified, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		if subcatID.Valid {
			c.SubcategoryID = subcatID.String
		}
		c.CreatedAt = time.Unix(createdAt, 0)
		c.UpdatedAt = time.Unix(updatedAt, 0)
		communities = append(communities, c)
	}
	return communities, rows.Err()
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

func (app *App) searchCommunities(query string) ([]Community, error) {
	rows, err := app.db.Query(`
		SELECT id, slug, name, description, COALESCE(subcategory_id,''), status,
		       contact_method, source_url, source_license, verified, created_at, updated_at
		FROM communities
		WHERE status IN ('active','uncategorized') AND (name LIKE ? OR description LIKE ?)
		ORDER BY verified DESC, name ASC
		LIMIT 50
	`, "%"+query+"%", "%"+query+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var communities []Community
	for rows.Next() {
		var c Community
		var verified int
		var createdAt, updatedAt int64
		if err := rows.Scan(&c.ID, &c.Slug, &c.Name, &c.Description, &c.SubcategoryID, &c.Status,
			&c.ContactMethod, &c.SourceURL, &c.SourceLicense, &verified, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		c.Verified = verified == 1
		c.CreatedAt = time.Unix(createdAt, 0)
		c.UpdatedAt = time.Unix(updatedAt, 0)
		communities = append(communities, c)
	}
	return communities, rows.Err()
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

func randomID() string { return uuid.NewString() }

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
