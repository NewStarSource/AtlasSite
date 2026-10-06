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
		body := `<h1>` + esc(u.Name) + `</h1><p>加入于 ` + formatTime(u.CreatedAt) + `</p><p><a href="/u/` + pathID(id) + `/profile">查看完整资料</a> · <a href="/u/` + pathID(id) + `/subscriptions">社群与主题订阅</a></p><h2>最近的帖子</h2>` + app.postCards(posts, viewer) + `<h2>最近的回复</h2>`
		for _, reply := range replies {
			if app.replyVisible(reply.ID, viewer) {
				body += `<article><a href="/p/` + pathID(reply.PostID) + `">` + esc(reply.PostTitle) + `</a><p>` + esc(reply.Content) + `</p></article>`
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
		body := `<h1>` + esc(alias) + `</h1><p>` + esc(profile.DisplayName) + `</p><p>` + esc(profile.Bio) + `</p><p>` + esc(profile.Location) + `</p>`
		if safeSource(profile.Website) {
			body += `<p><a rel="noopener noreferrer" href="` + esc(profile.Website) + `">个人网站</a></p>`
		}
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
		app.renderPage(w, r, "我的收藏", "bookmarks", `<h1>我的收藏</h1>`+app.postCards(posts, i.ID))
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
		respond(w, 200, map[string]any{name: v == "true"})
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
	body := `<h1>站内搜索</h1><form action="/search"><input type="search" name="q" value="` + esc(q) + `" maxlength="100"><select name="type"><option value="">全部</option><option value="community">社群</option><option value="post">动态</option><option value="topic">主题</option><option value="user">用户化名</option></select><button>搜索</button></form><p>支持连续中文关键词；不会记录搜索原词。</p>`
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
			body += `<h2>动态</h2>` + app.postCards(posts, viewer)
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
