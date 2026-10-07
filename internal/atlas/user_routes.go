package atlas

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/go-chi/chi/v5"
)

type publicProfile struct {
	DisplayName string `json:"display_name"`
	Bio         string `json:"bio"`
	Location    string `json:"location"`
	Website     string `json:"website"`
}

func (app *App) registerUserRoutes(router *chi.Mux) {
	router.Get("/profile", func(w http.ResponseWriter, r *http.Request) {
		i := app.requireIdentity(w, r, false)
		if i != nil {
			http.Redirect(w, r, "/u/"+pathID(i.ID), 303)
		}
	})
	router.Get("/u/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		u, err := app.getIdentity(id)
		if err != nil {
			fail(w, err)
			return
		}
		i, _, _ := app.currentIdentity(r)
		viewer := ""
		if i != nil {
			viewer = i.ID
		}
		if app.blocked(viewer, id) {
			fail(w, sql.ErrNoRows)
			return
		}
		var status string
		app.db.QueryRow("SELECT status FROM users WHERE id=?", id).Scan(&status)
		if status != "active" {
			fail(w, sql.ErrNoRows)
			return
		}
		posts, err := app.listPostsByAuthor(id, 50)
		if err != nil {
			fail(w, err)
			return
		}
		replies, err := app.listRepliesByAuthor(id, 50)
		if err != nil {
			fail(w, err)
			return
		}

		var profile publicProfile
		if app.secret != "" {
			var accountID string
			if app.db.QueryRow("SELECT account_id FROM users WHERE id=?", id).Scan(&accountID) == nil {
				_ = app.getInternal(r.Context(), "/internal/identity/profile?account_id="+url.QueryEscape(accountID), &profile)
			}
		}
		name := u.Name
		if profile.DisplayName != "" {
			name = profile.DisplayName
		}
		body := `<header class="profile-header"><span class="avatar">` + esc(avatarInitial(name)) + `</span><div class="profile-info"><h1 class="profile-name">` + esc(name) + `</h1>`
		if profile.Bio != "" {
			body += `<div class="profile-bio prose">` + renderProfileMarkdown(profile.Bio) + `</div>`
		}
		if profile.Location != "" {
			body += `<p class="profile-meta">` + esc(profile.Location) + `</p>`
		}
		if safeSource(profile.Website) {
			body += `<a href="` + esc(profile.Website) + `" rel="noopener noreferrer">个人网站</a>`
		}
		body += `</div>`
		if viewer == id {
			body += `<a class="btn btn-secondary" href="` + esc(app.config.AccountOrigin) + `/profile">编辑资料</a>`
		}
		body += `</header><nav class="settings-tabs" aria-label="个人内容">`
		tab := r.URL.Query().Get("tab")
		if tab != "replies" {
			tab = "posts"
		}
		for _, link := range [][3]string{{"posts", "内容", "/u/" + pathID(id)}, {"replies", "回复", "/u/" + pathID(id) + "?tab=replies"}, {"communities", "社群", "/u/" + pathID(id) + "/subscriptions"}} {
			class := ""
			if link[0] == tab {
				class = ` class="active" aria-current="page"`
			}
			body += `<a href="` + link[2] + `"` + class + `>` + link[1] + `</a>`
		}
		body += `</nav>`
		if tab == "posts" {
			body += `<div class="feed">` + app.postCards(posts, viewer, r) + `</div>`
		} else {
			shown := 0
			for _, reply := range replies {
				if app.replyVisible(reply.ID, viewer) {
					shown++
					body += `<article class="post"><a class="post-community" href="/p/` + pathID(reply.PostID) + `#reply-` + pathID(reply.ID) + `">` + esc(reply.PostTitle) + `</a><div class="prose">` + renderMarkdown(reply.Content) + `</div></article>`
				}
			}
			if shown == 0 {
				body += emptyState("暂无回复", "", "/", "浏览内容")
			}
		}

		if i != nil && i.ID != id {
			body += formStart(r, "/api/v1/blocks/"+pathID(id)) + field("blocked", "true") + `<button>屏蔽该用户</button></form>`
		}
		app.renderPage(w, r, u.Name, "user", body)
	})
	router.Get("/u/{id}/profile", func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		i, _, _ := app.currentIdentity(r)
		viewer := ""
		if i != nil {
			viewer = i.ID
		}
		if app.blocked(viewer, id) {
			fail(w, sql.ErrNoRows)
			return
		}
		var accountID, alias string
		err := app.db.QueryRow("SELECT account_id,alias FROM users WHERE id=? AND status='active'", id).Scan(&accountID, &alias)
		if err != nil {
			fail(w, err)
			return
		}
		var profile publicProfile
		if app.secret == "" || app.getInternal(r.Context(), "/internal/identity/profile?account_id="+url.QueryEscape(accountID), &profile) != nil {
			respond(w, 503, map[string]string{"code": "PROFILE_UNAVAILABLE"})
			return
		}
		body := pageHeading("PUBLIC PROFILE", alias, "公开个人资料", `<a class="btn" href="/u/`+pathID(id)+`">返回公开主页</a>`) + `<section class="panel"><h2>` + esc(profile.DisplayName) + `</h2><div class="profile-bio prose">` + renderProfileMarkdown(profile.Bio) + `</div><p class="muted">` + esc(profile.Location) + `</p>`
		if safeSource(profile.Website) {
			body += `<p><a rel="noopener noreferrer" href="` + esc(profile.Website) + `">个人网站</a></p>`
		}
		body += `</section>`
		app.renderPage(w, r, "个人资料", "user", body)
	})
	router.Get("/my/bookmarks", func(w http.ResponseWriter, r *http.Request) {
		i := app.requireIdentity(w, r, false)
		if i == nil {
			return
		}
		posts, err := app.listBookmarks(i.ID, 100)
		if err != nil {
			fail(w, err)
			return
		}
		app.renderPage(w, r, "我的收藏", "bookmarks", pageHeading("BOOKMARKS", "我的收藏", "", `<a class="btn" href="/">浏览动态</a>`)+`<div class="feed-list">`+app.postCards(posts, i.ID, r)+`</div>`)
	})
	router.Post("/api/v1/p/{id}/bookmark", func(w http.ResponseWriter, r *http.Request) { app.setInteraction(w, r, "bookmark") })
	router.Post("/api/v1/p/{id}/like", func(w http.ResponseWriter, r *http.Request) { app.setInteraction(w, r, "like") })
}
func (app *App) setInteraction(w http.ResponseWriter, r *http.Request, kind string) {
	i := app.contentIdentity(w, r)
	if i == nil {
		return
	}
	if r.ParseForm() != nil {
		fail(w, errInput)
		return
	}
	id := chi.URLParam(r, "id")
	if !app.postVisible(id, i.ID) {
		fail(w, sql.ErrNoRows)
		return
	}
	name := "bookmarked"
	if kind == "like" {
		name = "liked"
	}
	v := r.FormValue(name)
	if v != "true" && v != "false" {
		fail(w, errInput)
		return
	}
	action := "bookmark-off"
	if kind == "like" {
		action = "like-off"
	}
	if v == "true" {
		action = "bookmark-on"
		if kind == "like" {
			action = "like-on"
		}
	}
	if err := app.applyOperation(operation{EventID: randomID(), ObjectID: id, OwnerID: i.ID, Action: action, CreatedAt: time.Now().Unix()}); err != nil {
		fail(w, err)
		return
	}
	if r.Header.Get("Accept") == "application/json" {
		result := map[string]any{name: v == "true"}
		if kind == "bookmark" {
			var count int
			if err := app.db.QueryRow("SELECT count(*) FROM post_bookmarks WHERE post_id=?", id).Scan(&count); err != nil {
				fail(w, err)
				return
			}
			result["count"] = count
		}
		respond(w, 200, result)
		return
	}
	http.Redirect(w, r, "/p/"+pathID(id), 303)
}
func (app *App) searchPage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	kind := r.URL.Query().Get("type")
	if !checkText(q, 100) {
		fail(w, errInput)
		return
	}

	body := `<h1 class="sr-only">搜索</h1><form class="search-header" action="/search"><div class="search-input-container"><button class="search-submit" type="submit" aria-label="搜索" title="搜索">` + uiIcon("search") + `</button><input class="search-input" aria-label="搜索关键词" placeholder="搜索内容、社群或用户…" type="search" name="q" value="` + esc(q) + `" maxlength="100">` + field("type", kind) + `</div></form><nav class="filter-group" aria-label="搜索类型">`
	for _, filter := range [][2]string{{"", "全部"}, {"post", "内容"}, {"community", "社群"}, {"topic", "主题"}, {"user", "用户"}} {
		class := "filter-btn"
		if filter[0] == kind {
			class += " active"
		}
		body += `<a class="` + class + `" href="/search?q=` + esc(url.QueryEscape(q)) + `&amp;type=` + filter[0] + `">` + filter[1] + `</a>`
	}
	body += `</nav>`

	i, _, _ := app.currentIdentity(r)
	viewer := ""
	if i != nil {
		viewer = i.ID
	}
	if q != "" {
		pattern := "%" + q + "%"
		if kind == "" || kind == "community" {
			communities, err := app.searchCommunities(q)
			if err != nil {
				fail(w, err)
				return
			}
			body += `<h2>社群</h2>`
			for _, c := range communities {
				body += `<p><a href="/c/` + pathID(c.Slug) + `">` + esc(c.Name) + `</a> · ` + esc(c.Description) + `</p>`
			}
		}
		if kind == "" || kind == "post" {
			posts, err := app.queryPosts(" AND (p.title LIKE ? OR p.content LIKE ?)", 100, pattern, pattern)
			if err != nil {
				fail(w, err)
				return
			}
			body += `<h2>动态</h2>` + app.postCards(posts, viewer, r)
		}
		if kind == "" || kind == "topic" {
			rows, err := app.db.Query("SELECT t.name,c.slug,t.slug FROM topics t JOIN communities c ON c.id=t.community_id WHERE t.status='active' AND c.status IN ('active','uncategorized') AND (t.name LIKE ? OR t.description LIKE ?) ORDER BY t.name,t.id LIMIT 50", pattern, pattern)
			if err != nil {
				fail(w, err)
				return
			}
			body += `<h2>主题</h2>`
			for rows.Next() {
				var name, c, t string
				rows.Scan(&name, &c, &t)
				body += `<p><a href="/c/` + pathID(c) + `/t/` + pathID(t) + `">` + esc(name) + `</a></p>`
			}
			rows.Close()
		}
		if kind == "" || kind == "user" {
			rows, err := app.db.Query("SELECT id,alias FROM users WHERE status='active' AND alias LIKE ? ORDER BY alias,id LIMIT 50", pattern)
			if err != nil {
				fail(w, err)
				return
			}
			type user struct{ id, name string }
			var users []user
			for rows.Next() {
				var u user
				rows.Scan(&u.id, &u.name)
				users = append(users, u)
			}
			rows.Close()
			body += `<h2>用户化名</h2>`
			for _, u := range users {
				if !app.blocked(viewer, u.id) {
					body += fmt.Sprintf(`<p><a href="/u/%s">%s</a></p>`, pathID(u.id), esc(u.name))
				}
			}
		}
	}
	app.renderPage(w, r, "搜索", "search", body)
}
