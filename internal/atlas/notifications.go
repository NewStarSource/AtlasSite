package atlas

import (
	"database/sql"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

func (app *App) notificationRoutes(router *chi.Mux) {
	router.Get("/notifications", func(w http.ResponseWriter, r *http.Request) {
		i := app.requireIdentity(w, r, false)
		if i == nil {
			return
		}
		rows, err := app.db.Query("SELECT id,kind,object_id,post_id,read_at FROM notifications WHERE user_id=? AND created_at>? ORDER BY created_at DESC,id LIMIT 100", i.ID, time.Now().Add(-180*24*time.Hour).Unix())
		if err != nil {
			fail(w, err)
			return
		}
		type item struct {
			id, kind, object, post string
			read                   sql.NullInt64
		}
		var items []item
		for rows.Next() {
			var n item
			rows.Scan(&n.id, &n.kind, &n.object, &n.post, &n.read)
			items = append(items, n)
		}
		rows.Close()
		body := pageHeading("NOTIFICATIONS", "站内通知", "回复、订阅讨论与处理结论的更新。", `<a class="btn" href="/settings">通知偏好</a>`)
		shown := 0
		for _, n := range items {
			label, target := "事务状态更新", "/my/cases/"+pathID(n.object)
			if n.kind == "post" {
				if !app.postVisible(n.object, i.ID) {
					continue
				}
				label, target = "你订阅的社群或主题有新讨论", "/p/"+pathID(n.object)
			}
			if n.kind == "reply" {
				if !app.replyVisible(n.object, i.ID) {
					continue
				}
				var enabled int
				app.db.QueryRow("SELECT reply_notifications FROM user_settings WHERE user_id=?", i.ID).Scan(&enabled)
				if enabled == 0 {
					continue
				}
				label, target = "你参与的讨论有新回复", "/p/"+pathID(n.post)
			}
			shown++
			body += `<article class="notification-card"><span class="avatar avatar-small">↳</span><a href="` + target + `">` + label + `</a>`
			if !n.read.Valid {
				body += formStart(r, "/api/v1/notifications/"+pathID(n.id)+"/read") + `<button>标记已读</button></form>`
			}
			body += formStart(r, "/api/v1/notifications/"+pathID(n.id)+"/delete") + `<button>删除通知</button></form></article>`
		}
		if shown == 0 {
			body += emptyState("暂时没有新通知", "参与讨论，或订阅感兴趣的社群。新的更新会出现在这里。", "/discover", "发现社群")
		}
		app.renderPage(w, r, "通知", "notifications", body)
	})
	router.Post("/api/v1/notifications/{id}/read", func(w http.ResponseWriter, r *http.Request) { app.updateNotification(w, r, false) })
	router.Post("/api/v1/notifications/{id}/delete", func(w http.ResponseWriter, r *http.Request) { app.updateNotification(w, r, true) })
	router.Post("/api/v1/preview", func(w http.ResponseWriter, r *http.Request) {
		i := app.requireIdentity(w, r, false)
		if i == nil {
			return
		}
		if r.ParseForm() != nil || !checkText(r.FormValue("content"), 50000) {
			fail(w, errInput)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(renderMarkdown(r.FormValue("content"))))
	})
	router.Get("/assets/core.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Write([]byte(coreJS))
	})
}
func (app *App) updateNotification(w http.ResponseWriter, r *http.Request, remove bool) {
	i := app.requireIdentity(w, r, false)
	if i == nil {
		return
	}
	id := chi.URLParam(r, "id")
	var n int
	if app.db.QueryRow("SELECT count(*) FROM notifications WHERE id=? AND user_id=?", id, i.ID).Scan(&n) != nil || n != 1 {
		fail(w, sql.ErrNoRows)
		return
	}
	var err error
	if remove {
		err = app.applyOperation(operation{EventID: randomID(), ObjectID: id, OwnerID: i.ID, Action: "delete-notification", CreatedAt: time.Now().Unix()})
	} else {
		_, err = app.db.Exec("UPDATE notifications SET read_at=unixepoch() WHERE id=? AND user_id=?", id, i.ID)
	}
	if err != nil {
		fail(w, err)
		return
	}
	http.Redirect(w, r, "/notifications", 303)
}
func (app *App) processJobs() error {
	app.jobsMu.Lock()
	defer app.jobsMu.Unlock()
	now := time.Now().Unix()
	tx, err := app.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id, kind, object string
	var attempts, generation int
	err = tx.QueryRow("SELECT id,kind,object_id,attempts,generation FROM jobs WHERE (status='pending' OR (status='running' AND lease_until<?)) AND available_at<=? ORDER BY available_at,id LIMIT 1", now, now).Scan(&id, &kind, &object, &attempts, &generation)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec("UPDATE jobs SET status='running',lease_until=?,generation=generation+1 WHERE id=? AND generation=?", now+600, id, generation)
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	// Reply delivery is an idempotent DB outbox check, with no external recipient or body.
	if kind == "reply" || kind == "post" {
		_, err = app.db.Exec("UPDATE jobs SET status='done',lease_until=0,error_code='' WHERE id=? AND status='running' AND generation=?", id, generation+1)
	} else {
		attempts++
		status := "pending"
		delay := []int64{60, 300, 1800, 1800}[min(attempts-1, 3)]
		if attempts > 3 {
			status = "failed"
		}
		_, err = app.db.Exec("UPDATE jobs SET status=?,attempts=?,available_at=?,lease_until=0,error_code='HANDLER_UNAVAILABLE' WHERE id=? AND generation=?", status, attempts, now+delay, id, generation+1)
	}
	return err
}
