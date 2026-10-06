package atlas

import (
	"database/sql"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

func (app *App) subscriptionRoutes(router *chi.Mux) {
	router.Post("/api/v1/subscriptions", app.setSubscription)
	router.Get("/my/subscriptions", func(w http.ResponseWriter, r *http.Request) {
		i := app.requireIdentity(w, r, false)
		if i != nil {
			app.subscriptionPage(w, r, i.ID)
		}
	})
	router.Get("/u/{id}/subscriptions", func(w http.ResponseWriter, r *http.Request) { app.subscriptionPage(w, r, chi.URLParam(r, "id")) })
}
func (app *App) subscriptionForm(r *http.Request, kind, object string) string {
	i, _, _ := app.currentIdentity(r)
	if i == nil {
		return `<p><a href="/login">登录后可订阅此社群或主题</a></p>`
	}
	var count, notify int
	app.db.QueryRow("SELECT count(*),COALESCE(max(notifications),0) FROM subscriptions WHERE user_id=? AND kind=? AND object_id=?", i.ID, kind, object).Scan(&count, &notify)
	checked := ""
	if notify > 0 {
		checked = " checked"
	}
	label := "订阅"
	if count > 0 {
		label = "取消订阅"
	}
	body := formStart(r, "/api/v1/subscriptions") + field("kind", kind) + field("object_id", object) + field("subscribed", fmt.Sprint(count == 0)) + `<label><input type="checkbox" name="notifications" value="true"` + checked + `>接收新讨论通知</label><button>` + label + `</button></form>`
	if count > 0 {
		body += formStart(r, "/api/v1/subscriptions") + field("kind", kind) + field("object_id", object) + field("subscribed", "true") + `<label><input type="checkbox" name="notifications" value="true"` + checked + `>接收新讨论通知</label><button>保存通知偏好</button></form>`
	}
	return body
}
func (app *App) subscriptionTarget(kind, object string) (string, string, error) {
	var name, path string
	if kind == "community" {
		var slug string
		err := app.db.QueryRow("SELECT name,slug FROM communities WHERE id=? AND status IN ('active','uncategorized')", object).Scan(&name, &slug)
		return name, "/c/" + pathID(slug), err
	}
	if kind == "topic" {
		var community, topic string
		err := app.db.QueryRow("SELECT t.name,c.slug,t.slug FROM topics t JOIN communities c ON c.id=t.community_id WHERE t.id=? AND t.status='active' AND c.status IN ('active','uncategorized')", object).Scan(&name, &community, &topic)
		path = "/c/" + pathID(community) + "/t/" + pathID(topic)
		return name, path, err
	}
	return "", "", errInput
}
func (app *App) setSubscription(w http.ResponseWriter, r *http.Request) {
	i := app.contentIdentity(w, r)
	if i == nil {
		return
	}
	if r.ParseForm() != nil {
		fail(w, errInput)
		return
	}
	kind, object, desired := r.FormValue("kind"), r.FormValue("object_id"), r.FormValue("subscribed")
	if desired != "true" && desired != "false" {
		fail(w, errInput)
		return
	}
	_, _, err := app.subscriptionTarget(kind, object)
	if err != nil && desired == "true" {
		fail(w, err)
		return
	}
	if kind != "community" && kind != "topic" {
		fail(w, errInput)
		return
	}
	action := "subscription-off"
	if desired == "true" {
		action = "subscription-on"
	}
	err = app.applyOperation(operation{EventID: randomID(), ObjectID: object, OwnerID: i.ID, TargetKind: kind, Notify: r.FormValue("notifications") == "true", Action: action, CreatedAt: time.Now().Unix()})
	if err != nil {
		fail(w, err)
		return
	}
	http.Redirect(w, r, "/my/subscriptions", 303)
}
func (app *App) subscriptionPage(w http.ResponseWriter, r *http.Request, owner string) {
	i, _, _ := app.currentIdentity(r)
	viewer := ""
	if i != nil {
		viewer = i.ID
	}
	var alias string
	var hidden int
	err := app.db.QueryRow("SELECT u.alias,s.hide_relations FROM users u JOIN user_settings s ON s.user_id=u.id WHERE u.id=? AND u.status='active'", owner).Scan(&alias, &hidden)
	if err != nil || app.blocked(owner, viewer) || (hidden == 1 && owner != viewer) {
		fail(w, sql.ErrNoRows)
		return
	}
	rows, err := app.db.Query("SELECT kind,object_id,notifications FROM subscriptions WHERE user_id=? ORDER BY created_at DESC", owner)
	if err != nil {
		fail(w, err)
		return
	}
	type item struct {
		kind, object string
		notify       int
	}
	var items []item
	for rows.Next() {
		var item item
		rows.Scan(&item.kind, &item.object, &item.notify)
		items = append(items, item)
	}
	rows.Close()
	body := `<h1>` + esc(alias) + `的社群与主题订阅</h1>`
	for _, item := range items {
		name, path, err := app.subscriptionTarget(item.kind, item.object)
		if err != nil {
			continue
		}
		body += `<p><a href="` + path + `">` + esc(name) + `</a></p>`
		if owner == viewer {
			body += app.subscriptionForm(r, item.kind, item.object)
		}
	}
	if owner == viewer {
		body += `<p><a href="/settings">在隐私设置中隐藏全部社群关系</a></p>`
	}
	app.renderPage(w, r, "社群关系", "subscriptions", body)
}
func notifySubscribers(tx *sql.Tx, post, author string, now int64) error {
	_, err := tx.Exec(`INSERT OR IGNORE INTO notifications(id,user_id,kind,object_id,post_id,created_at)
 SELECT lower(hex(randomblob(16))),s.user_id,'post',p.id,p.id,? FROM subscriptions s JOIN users u ON u.id=s.user_id JOIN posts p ON p.id=?
 WHERE s.notifications=1 AND u.status='active' AND s.user_id<>? AND ((s.kind='community' AND s.object_id=p.community_id) OR (s.kind='topic' AND EXISTS(SELECT 1 FROM post_topics WHERE post_id=p.id AND topic_id=s.object_id)))
 AND NOT EXISTS(SELECT 1 FROM blocks WHERE (owner_id=s.user_id AND target_id=?) OR (owner_id=? AND target_id=s.user_id))`, now, post, author, author, author)
	if err != nil {
		return err
	}
	_, err = tx.Exec("INSERT OR IGNORE INTO jobs(id,business_key,kind,object_id,available_at,created_at) VALUES(?,?,'post',?,?,?)", randomID(), "post:"+post, post, now, now)
	return err
}
