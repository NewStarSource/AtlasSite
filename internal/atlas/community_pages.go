package atlas

import (
	"database/sql"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
)

func safeSource(value string) bool {
	u, err := url.Parse(value)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil
}
func (app *App) readableCommunity(r *http.Request) (*Community, error) {
	c, err := app.getCommunityBySlug(chi.URLParam(r, "slug"))
	if err != nil {
		return nil, err
	}
	if c.Status != "active" && c.Status != "uncategorized" {
		return nil, sql.ErrNoRows
	}
	return c, nil
}
func (app *App) communityPage(w http.ResponseWriter, r *http.Request) {
	c, err := app.readableCommunity(r)
	if err != nil {
		fail(w, err)
		return
	}
	i, _, _ := app.currentIdentity(r)
	viewer := ""
	if i != nil {
		viewer = i.ID
	}
	breadcrumb, err := app.communityBreadcrumb(c)
	if err != nil {
		fail(w, err)
		return
	}
	view := strings.TrimPrefix(r.URL.Path, "/c/"+c.Slug)
	view = strings.Trim(view, "/")
	base := "/c/" + pathID(c.Slug)
	verified := "来源待核实"
	if c.Verified {
		verified = "来源已核验"
	}
	body := breadcrumb + `<header class="community-header"><h1 class="community-title">` + esc(c.Name) + `</h1><p class="community-description">` + esc(c.Description) + `</p><div class="community-meta">` + app.subscriptionControl(r, "community", c.ID) + `<a class="btn btn-secondary" href="` + base + `/new">发布</a></div></header><nav class="tabs" aria-label="社群内容">`
	for _, tab := range [][2]string{{"", "最新内容"}, {"announcements", "公告"}, {"guide", "新人指南"}, {"collections", "合集"}, {"about", "关于"}} {
		class := ""
		if tab[0] == view {
			class = ` class="active" aria-current="page"`
		}
		suffix := ""
		if tab[0] != "" {
			suffix = "/" + tab[0]
		}
		body += `<a href="` + base + suffix + `"` + class + `>` + tab[1] + `</a>`
	}
	body += `</nav><section>`

	topics, err := app.listTopics(c.ID)
	if err != nil {
		fail(w, err)
		return
	}
	if view == "" {

		body += `<div class="tools-grid"><a class="tool-card" href="` + base + `/announcements"><h3 class="tool-title">公告</h3></a><a class="tool-card" href="` + base + `/guide"><h3 class="tool-title">新人指南</h3></a><a class="tool-card" href="` + base + `/collections"><h3 class="tool-title">讨论合集</h3></a></div>`
		if len(topics) > 0 {
			body += `<div class="topic-list">`
			for _, t := range topics {
				body += `<a class="topic-tag" href="` + base + `/t/` + pathID(t.Slug) + `">` + esc(t.Name) + `</a>`
			}
			body += `</div>`
		}
		posts, e := app.listPosts(c.ID, 50)
		if e != nil {
			fail(w, e)
			return
		}
		body += `<div class="section-heading"><h2 class="sr-only">最近讨论</h2></div><div class="feed-list">` + app.postCards(posts, viewer, r) + `</div>`
	} else if view == "about" {
		body += `<section class="panel prose"><h2>关于这个社群</h2><p>` + esc(c.Description) + `</p><h3>接触方式</h3><p>` + esc(c.ContactMethod) + `</p><h3>来源与许可</h3><p>` + verified + ` · ` + esc(c.SourceLicense) + `</p>`
		if safeSource(c.SourceURL) {
			body += `<p><a rel="noopener noreferrer" href="` + esc(c.SourceURL) + `">查看来源 ↗</a></p>`
		}
		body += `</section>`
	} else {
		collections, e := app.listCollections(c.ID)
		if e != nil {
			fail(w, e)
			return
		}
		wanted := map[string]string{"announcements": "announcement", "guide": "guide", "collections": "discussion"}[view]
		shown := 0
		for _, collection := range collections {
			if collection.Type != wanted {
				continue
			}
			shown++
			body += `<section class="collection-card"><h2>` + esc(collection.Title) + `</h2>`
			if collection.Type == "discussion" {
				rows, e := app.db.Query("SELECT p.id FROM collection_refs cr JOIN posts p ON p.id=cr.post_id WHERE cr.collection_id=? AND p.community_id=? AND "+visiblePostSQL+" ORDER BY p.created_at,p.id", collection.ID, c.ID)
				if e != nil {
					fail(w, e)
					return
				}
				var ids []string
				for rows.Next() {
					var id string
					rows.Scan(&id)
					ids = append(ids, id)
				}
				rows.Close()
				count := 0
				for _, id := range ids {
					p, e := app.getPost(id)
					if e == nil && app.postVisible(id, viewer) {
						count++
						body += `<p><a href="/p/` + pathID(id) + `">` + esc(p.Title) + ` →</a></p>`
					}
				}
				if count == 0 {
					body += `<p class="muted">暂无可展示的讨论引用。</p>`
				}
			} else {
				body += `<div class="prose">` + renderMarkdown(collection.Content) + `</div>`
			}
			body += `</section>`
		}
		if shown == 0 {
			body += emptyState("这里还没有公开内容", "有新的公开内容时，会显示在这里。", base, "返回社群讨论")
		}
	}
	body += `</section>`
	app.renderPage(w, r, c.Name, "community", body)
}

func (app *App) communityBreadcrumb(c *Community) (string, error) {
	body := `<nav class="breadcrumb" aria-label="分类路径"><a href="/discover">社群目录</a>`
	if c.Status == "uncategorized" || c.SubcategoryID == "" {
		return body + ` / <a href="/discover#uncategorized">待分类</a> / ` + esc(c.Name) + `</nav>`, nil
	}
	var domainID, domain, directionID, direction, subcategory string
	err := app.db.QueryRow(`SELECT d.id,d.name,di.id,di.name,s.name FROM subcategories s JOIN directions di ON di.id=s.direction_id JOIN domains d ON d.id=di.domain_id WHERE s.id=?`, c.SubcategoryID).Scan(&domainID, &domain, &directionID, &direction, &subcategory)
	if err != nil {
		return "", err
	}
	body += ` / <a href="/discover#domain-` + pathID(domainID) + `">` + esc(domain) + `</a>`
	body += ` / <a href="/discover#direction-` + pathID(directionID) + `">` + esc(direction) + `</a>`
	body += ` / <a href="/discover#subcategory-` + pathID(c.SubcategoryID) + `">` + esc(subcategory) + `</a>`
	return body + ` / ` + esc(c.Name) + `</nav>`, nil
}
func (app *App) topicPage(w http.ResponseWriter, r *http.Request) {
	c, err := app.readableCommunity(r)
	if err != nil {
		fail(w, err)
		return
	}
	var id, name, description string
	err = app.db.QueryRow("SELECT id,name,description FROM topics WHERE community_id=? AND slug=? AND status='active'", c.ID, chi.URLParam(r, "topic")).Scan(&id, &name, &description)
	if err != nil {
		fail(w, err)
		return
	}
	posts, err := app.listPosts(c.ID, 100)
	if err != nil {
		fail(w, err)
		return
	}
	selected := make([]Post, 0, len(posts))
	for _, p := range posts {
		var n int
		if app.db.QueryRow("SELECT count(*) FROM post_topics WHERE post_id=? AND topic_id=?", p.ID, id).Scan(&n) == nil && n > 0 {
			selected = append(selected, p)
		}
	}
	i, _, _ := app.currentIdentity(r)
	viewer := ""
	if i != nil {
		viewer = i.ID
	}
	app.renderPage(w, r, name, "topic", `<nav class="breadcrumb"><a href="/c/`+pathID(c.Slug)+`">`+esc(c.Name)+`</a><span>/</span><span>主题讨论</span></nav>`+pageHeading("TOPIC", name, description, "")+`<section class="panel">`+app.subscriptionControl(r, "topic", id)+`</section><div class="feed-list">`+app.postCards(selected, viewer, r)+`</div>`)
}
func (app *App) oldCommunityPath(w http.ResponseWriter, r *http.Request) {
	var slug string
	err := app.db.QueryRow("SELECT c.slug FROM community_paths cp JOIN communities c ON c.id=cp.community_id WHERE cp.path=? AND c.status IN ('active','uncategorized')", chi.URLParam(r, "old")).Scan(&slug)
	if err != nil {
		fail(w, err)
		return
	}
	http.Redirect(w, r, "/c/"+pathID(slug), 301)
}
func (app *App) associationForm(r *http.Request, p *Post, version int) string {
	body := `<h2>社群与主题关联</h2>` + formStart(r, "/api/v1/p/"+pathID(p.ID)+"/association") + versionField(version) + `<label for="association-community">关联社群</label>` + app.communitySelect("association-community", p.CommunitySlug)
	if p.CommunityID != "" {
		topics, _ := app.listTopics(p.CommunityID)
		for _, t := range topics {
			var n int
			app.db.QueryRow("SELECT count(*) FROM post_topics WHERE post_id=? AND topic_id=?", p.ID, t.ID).Scan(&n)
			checked := ""
			if n > 0 {
				checked = " checked"
			}
			body += `<label><input type="checkbox" name="topic_ids" value="` + esc(t.ID) + `"` + checked + `>` + esc(t.Name) + `</label>`
		}
	}
	return strings.TrimSpace(body) + `<button>保存关联</button></form>`
}
