package atlas

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

func (app *App) rightsRoutes(router *chi.Mux) {
	router.Get("/settings", app.settingsPage)
	router.Post("/api/v1/settings", app.updateSettings)
	router.Post("/api/v1/blocks/{id}", app.setBlock)
	router.Get("/help", func(w http.ResponseWriter, r *http.Request) {
		app.renderPage(w, r, "使用与权利帮助", "help", `<h1>星图本地测试帮助</h1><p>从新星账户登录，可独立发动态或从社群页发帖。资料在新星账户编辑。</p><p>图片发布前仅本人可见；删除后原帖、回复、图片、搜索和通知同时停止展示。受限旧正文最多保留 30 天，争议冻结部分按案件期限处理。</p><p>普通加密备份最长 12 个月，恢复先重放删除清单；草稿及举报说明不进入普通备份。</p><p><a href="/settings">隐私、屏蔽、导出与注销</a> · <a href="/help/report">举报及申诉</a> · <a href="/help/emergency">紧急请求</a> · <a href="/status">运行状态</a></p><p>当前本地合成测试由项目负责人处理，尚未接入真实用户或组成独立复核组。涉及处理者自身的申诉保持待独立复核，不由被投诉者终审。</p>`)
	})
	router.Get("/help/report", func(w http.ResponseWriter, r *http.Request) { app.caseForm(w, r, false) })
	router.Get("/help/emergency", func(w http.ResponseWriter, r *http.Request) { app.caseForm(w, r, true) })
	router.Post("/api/v1/cases", app.submitCase)
	router.Get("/my/cases", app.casesPage)
	router.Get("/my/cases/{id}", app.casePage)
	router.Get("/my/export", app.exportPage)
	router.Post("/api/v1/me/export", app.exportOwn)
	router.Post("/api/v1/me/deactivate", app.deactivateAtlas)
	router.Get("/recover", func(w http.ResponseWriter, r *http.Request) {
		app.renderPage(w, r, "恢复账户", "settings", `<h1>恢复账户</h1><p>注销后 30 天内，请在新星账户使用用户名和密码重新验证并恢复；之后重新登录星图。旧会话不会复活。</p><p><a href="`+esc(app.config.AccountOrigin)+`/recover">前往新星账户恢复</a></p>`)
	})
}
func (app *App) settingsPage(w http.ResponseWriter, r *http.Request) {
	i := app.requireIdentity(w, r, false)
	if i == nil {
		return
	}
	var hidden, reply, retain int
	err := app.db.QueryRow("SELECT hide_relations,reply_notifications,retain_content FROM user_settings WHERE user_id=?", i.ID).Scan(&hidden, &reply, &retain)
	if err != nil {
		fail(w, err)
		return
	}
	checked := func(v int) string {
		if v == 1 {
			return " checked"
		}
		return ""
	}
	body := `<h1>隐私与账户权利</h1>` + formStart(r, "/api/v1/settings") + `<label><input type="checkbox" name="hide_relations" value="1"` + checked(hidden) + `>隐藏全部社群关系</label><label><input type="checkbox" name="reply_notifications" value="1"` + checked(reply) + `>接收普通回复通知</label><p>安全和案件结论通知不能关闭。当前没有用户间关注关系。</p><button>保存设置</button></form><p><a href="` + esc(app.config.AccountOrigin) + `/profile">编辑账户个人资料</a> · <a href="/security">重新确认身份</a> · <a href="/my/export">导出本人数据</a> · <a href="/my/cases">我的举报与申诉</a> · <a href="/my/bookmarks">我的收藏</a> · <a href="/my/drafts">我的草稿</a></p><p><a href="/my/subscriptions">我的社群与主题订阅</a></p><h2>已屏蔽用户</h2>`
	rows, err := app.db.Query("SELECT b.target_id,u.alias FROM blocks b JOIN users u ON u.id=b.target_id WHERE b.owner_id=? ORDER BY b.created_at DESC", i.ID)
	if err != nil {
		fail(w, err)
		return
	}
	for rows.Next() {
		var id, alias string
		rows.Scan(&id, &alias)
		body += `<p>` + esc(alias) + `</p>` + formStart(r, "/api/v1/blocks/"+pathID(id)) + field("blocked", "false") + `<button>解除屏蔽</button></form>`
	}
	rows.Close()
	var choices strings.Builder
	ownRows, e := app.db.Query("SELECT id,title FROM posts WHERE author_id=? AND status='published' ORDER BY created_at DESC LIMIT 100", i.ID)
	if e != nil {
		fail(w, e)
		return
	}
	for ownRows.Next() {
		var id, title string
		ownRows.Scan(&id, &title)
		choices.WriteString(`<label><input type="checkbox" name="retain_ids" value="` + esc(id) + `">保留动态 ` + esc(title) + `</label>`)
	}
	ownRows.Close()
	replyRows, e := app.db.Query("SELECT id,content FROM replies WHERE author_id=? AND status='published' ORDER BY created_at DESC LIMIT 100", i.ID)
	if e != nil {
		fail(w, e)
		return
	}
	for replyRows.Next() {
		var id, text string
		replyRows.Scan(&id, &text)
		choices.WriteString(`<label><input type="checkbox" name="retain_ids" value="` + esc(id) + `">保留回复 ` + esc(excerpt(text, 40)) + `</label>`)
	}
	replyRows.Close()
	body += `<h2>注销</h2><p>需要先重新确认身份。选择删除内容时立即停止全部下游展示；保留内容仍显示化名及已注销标记。恢复期 30 天，恢复不自动撤销已执行的内容删除。</p>` + formStart(r, "/api/v1/me/deactivate") + `<label><input type="checkbox" name="retain_content" value="1"` + checked(retain) + `>保留全部已公开内容</label>` + choices.String() + `<p>也可仅勾选上面希望保留的内容；原帖已删除的回复不能独立公开。列表为最近 100 条动态和 100 条回复。</p><label><input type="checkbox" name="confirm" value="deactivate" required>确认注销</label><button>注销并撤销全部会话</button></form>`
	app.renderPage(w, r, "隐私与权利", "settings", body)
}
func (app *App) updateSettings(w http.ResponseWriter, r *http.Request) {
	i := app.requireIdentity(w, r, false)
	if i == nil {
		return
	}
	if r.ParseForm() != nil {
		fail(w, errInput)
		return
	}
	hidden, reply := integer(r.FormValue("hide_relations")), integer(r.FormValue("reply_notifications"))
	if (hidden != 0 && hidden != 1) || (reply != 0 && reply != 1) {
		fail(w, errInput)
		return
	}
	action := "relations-public"
	if hidden == 1 {
		action = "relations-private"
	}
	if err := app.applyOperation(operation{EventID: randomID(), ObjectID: i.ID, Action: action, CreatedAt: time.Now().Unix()}); err != nil {
		fail(w, err)
		return
	}
	if _, err := app.db.Exec("UPDATE user_settings SET reply_notifications=? WHERE user_id=?", reply, i.ID); err != nil {
		fail(w, err)
		return
	}
	http.Redirect(w, r, "/settings", 303)
}
func (app *App) setBlock(w http.ResponseWriter, r *http.Request) {
	i := app.requireIdentity(w, r, false)
	if i == nil {
		return
	}
	if r.ParseForm() != nil {
		fail(w, errInput)
		return
	}
	target := chi.URLParam(r, "id")
	if target == i.ID {
		fail(w, errInput)
		return
	}
	var n int
	if app.db.QueryRow("SELECT count(*) FROM users WHERE id=?", target).Scan(&n) != nil || n != 1 {
		fail(w, sql.ErrNoRows)
		return
	}
	action := "block-off"
	if r.FormValue("blocked") == "true" {
		action = "block-on"
	} else if r.FormValue("blocked") != "false" {
		fail(w, errInput)
		return
	}
	if err := app.applyOperation(operation{EventID: randomID(), ObjectID: target, OwnerID: i.ID, Action: action, CreatedAt: time.Now().Unix()}); err != nil {
		fail(w, err)
		return
	}
	http.Redirect(w, r, "/settings", 303)
}
func (app *App) caseForm(w http.ResponseWriter, r *http.Request, emergency bool) {
	kind, title := "report", "举报与申诉"
	if emergency {
		kind, title = "emergency", "紧急请求"
	} else {
		if app.requireIdentity(w, r, false) == nil {
			return
		}
	}
	body := `<h1>` + title + `</h1><p>仅提交处理所必需的信息，不上传第三人的私密材料。说明独立加密保存，结案后 30 天清理，不送外部 AI、不进入普通长期备份。</p><p>紧急请求 24 小时内响应，普通举报和申诉 168 小时内初步处理。当前仅本地测试，未提供全天候真实值守。</p>` + formStart(r, "/api/v1/cases") + field("request_id", randomID()) + field("kind", kind) + `<label>内容 ID<input name="object_id" value="` + esc(r.URL.Query().Get("object_id")) + `"></label><label>申诉原案件 ID（普通举报留空）<input name="parent_id"></label><label>必要说明<textarea name="detail" maxlength="4000" required></textarea></label><label><input type="checkbox" name="conflict" value="1">涉及处理人员或利益冲突</label><button>提交请求</button></form>`
	app.renderPage(w, r, title, "help", body)
}
func (app *App) submitCase(w http.ResponseWriter, r *http.Request) {
	if r.ParseForm() != nil {
		fail(w, errInput)
		return
	}
	kind, key, object, parent, detail := r.FormValue("kind"), r.FormValue("request_id"), r.FormValue("object_id"), r.FormValue("parent_id"), strings.TrimSpace(r.FormValue("detail"))
	i, _, _ := app.currentIdentity(r)
	owner := ""
	if i != nil {
		owner = i.ID
	}
	if kind != "emergency" && i == nil {
		fail(w, errForbidden)
		return
	}
	if kind != "report" && kind != "emergency" {
		fail(w, errInput)
		return
	}
	if detail == "" || !checkText(detail, 4000) || !uuidRequest(key) || (object != "" && !uuidRequest(object)) {
		fail(w, errInput)
		return
	}
	rateOwner := owner
	if rateOwner == "" {
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		rateOwner = operationMAC(app.vaultKey, operation{ObjectID: host, Action: "case-rate"})
	}
	if !app.rateLimit(w, r, "case:"+rateOwner, 10) {
		return
	}
	if parent != "" {
		var parentOwner, subject sql.NullString
		err := app.db.QueryRow("SELECT user_id,subject_id,object_id FROM cases WHERE id=?", parent).Scan(&parentOwner, &subject, &object)
		if err != nil || (parentOwner.String != owner && subject.String != owner) || owner == "" {
			fail(w, errForbidden)
			return
		}
		kind = "appeal"
	}
	var existing string
	err := app.db.QueryRow("SELECT id FROM cases WHERE user_id IS ? AND request_id=?", nullable(owner), key).Scan(&existing)
	if err == nil {
		respond(w, 202, map[string]string{"id": existing, "code": "REQUEST_ACCEPTED"})
		return
	}
	if err != sql.ErrNoRows {
		fail(w, err)
		return
	}
	var subject sql.NullString
	if object != "" {
		err = app.db.QueryRow("SELECT author_id FROM posts WHERE id=? UNION ALL SELECT author_id FROM replies WHERE id=? LIMIT 1", object, object).Scan(&subject)
		if err != nil {
			fail(w, err)
			return
		}
	}
	id, now := randomID(), time.Now().Unix()
	due := now + 168*3600
	if kind == "emergency" {
		due = now + 24*3600
	}
	if err = app.privatePut("case:"+id, owner, "case", app.caseExplanation(detail, object, owner), 0); err != nil {
		fail(w, err)
		return
	}
	tx, err := app.db.Begin()
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback()
	status := "received"
	conflict := integer(r.FormValue("conflict"))
	if conflict == 1 {
		status = "waiting_independent_review"
	}
	_, err = tx.Exec("INSERT INTO cases(id,request_id,user_id,subject_id,kind,object_id,parent_id,status,received_at,due_at,conflict) VALUES(?,?,?,?,?,?,?,?,?,?,?)", id, key, nullable(owner), subject, kind, object, nullable(parent), status, now, due, conflict)
	if err == nil && owner != "" {
		_, err = tx.Exec("INSERT INTO notifications VALUES(?,?,?,?,?,?,NULL)", randomID(), owner, "case", id, "", now)
	}
	if err == nil {
		_, err = tx.Exec("INSERT INTO jobs(id,business_key,kind,object_id,status,available_at,created_at) VALUES(?,?,? ,?,'manual',?,?)", randomID(), "case:"+id, "case", id, now, now)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		fail(w, err)
		return
	}
	app.renderPage(w, r, "请求已受理", "help", `<h1>请求已受理</h1><p>受理编号 `+esc(id)+`。处理进度可在我的举报与申诉中查看。未登录紧急请求请保存编号并交给本地项目负责人。</p><p><a href="/my/cases">我的请求</a></p>`)
}
func (app *App) casesPage(w http.ResponseWriter, r *http.Request) {
	i := app.requireIdentity(w, r, false)
	if i == nil {
		return
	}
	rows, err := app.db.Query("SELECT id,kind,status FROM cases WHERE user_id=? OR (subject_id=? AND decision<>'') ORDER BY received_at DESC,id", i.ID, i.ID)
	if err != nil {
		fail(w, err)
		return
	}
	body := `<h1>我的举报与申诉</h1>`
	for rows.Next() {
		var id, kind, status string
		rows.Scan(&id, &kind, &status)
		body += `<p><a href="/my/cases/` + pathID(id) + `">` + esc(kind) + ` · ` + esc(status) + `</a></p>`
	}
	rows.Close()
	app.renderPage(w, r, "我的请求", "help", body)
}
func (app *App) casePage(w http.ResponseWriter, r *http.Request) {
	i := app.requireIdentity(w, r, false)
	if i == nil {
		return
	}
	id := chi.URLParam(r, "id")
	var kind, status, decision, object string
	var due int64
	err := app.db.QueryRow("SELECT kind,status,decision,object_id,due_at FROM cases WHERE id=? AND (user_id=? OR (subject_id=? AND decision<>''))", id, i.ID, i.ID).Scan(&kind, &status, &decision, &object, &due)
	if err != nil {
		fail(w, err)
		return
	}
	body := `<h1>请求状态</h1><p>` + esc(kind) + ` · ` + esc(status) + ` · ` + esc(decision) + `</p><p>初步处理期限 ` + time.Unix(due, 0).In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("2006-01-02 15:04") + `</p>` + formStart(r, "/api/v1/cases") + field("request_id", randomID()) + field("kind", "report") + field("parent_id", id) + field("object_id", object) + `<label>申诉说明<textarea name="detail" maxlength="4000" required></textarea></label><button>提交申诉</button></form>`
	app.renderPage(w, r, "请求状态", "help", body)
}
func (app *App) exportPage(w http.ResponseWriter, r *http.Request) {
	if app.requireIdentity(w, r, false) == nil {
		return
	}
	app.renderPage(w, r, "本人导出", "settings", `<h1>导出本人数据</h1><p>导出仅含本人内容、关系、设置、通知与案件最小状态；排除举报证据、他人私密资料和账户内部映射。请先重新确认身份。</p>`+formStart(r, "/api/v1/me/export")+`<button>生成并下载本人 ZIP</button></form>`)
}
func (app *App) exportOwn(w http.ResponseWriter, r *http.Request) {
	i := app.requireIdentity(w, r, true)
	if i == nil {
		return
	}
	var mode string
	if app.db.QueryRow("SELECT mode FROM runtime_state WHERE id=1").Scan(&mode) != nil || mode == "isolated" {
		fail(w, errForbidden)
		return
	}
	data, err := app.ownExport(r, i)
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="staratlas-own-data.zip"`)
	w.Write(data)
}
func (app *App) ownExport(r *http.Request, i *Identity) ([]byte, error) {
	app.opsMu.Lock()
	defer app.opsMu.Unlock()
	unlock, lockErr := app.journalLock()
	if lockErr != nil {
		return nil, lockErr
	}
	defer unlock()
	dir, err := os.MkdirTemp(filepath.Dir(app.config.Database), "export-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	if err = restrictPath(dir, true); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "snapshot.db")
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	if _, err = app.db.ExecContext(ctx, "VACUUM INTO ?", path); err != nil {
		return nil, err
	}
	snapshot, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		return nil, err
	}
	defer snapshot.Close()
	result := map[string]any{"snapshot_at": time.Now().UTC().Format(time.RFC3339), "alias": i.Alias}
	queries := map[string]string{
		"posts":         "SELECT p.id,p.title,p.content,p.status,p.license,p.version_number,p.created_at,p.updated_at FROM posts p WHERE p.author_id=? AND NOT EXISTS(SELECT 1 FROM restrictions WHERE object_id=p.id AND removed_at IS NULL)",
		"replies":       `SELECT r.id,r.post_id,r.content,r.status,r.created_at,r.updated_at FROM replies r JOIN posts p ON p.id=r.post_id WHERE r.author_id=? AND ` + visiblePostSQL + ` AND NOT EXISTS(WITH RECURSIVE chain(id,parent_id,status) AS (SELECT id,parent_id,status FROM replies WHERE id=r.id UNION ALL SELECT parent.id,parent.parent_id,parent.status FROM replies parent JOIN chain ch ON parent.id=ch.parent_id) SELECT 1 FROM chain WHERE status<>'published' OR EXISTS(SELECT 1 FROM restrictions WHERE object_id=chain.id AND removed_at IS NULL))`,
		"bookmarks":     "SELECT post_id,created_at FROM post_bookmarks WHERE identity_id=?",
		"likes":         "SELECT post_id,created_at FROM post_likes WHERE user_id=?",
		"settings":      "SELECT hide_relations,reply_notifications,retain_content FROM user_settings WHERE user_id=?",
		"blocks":        "SELECT target_id,created_at FROM blocks WHERE owner_id=?",
		"notifications": "SELECT id,kind,object_id,created_at,read_at FROM notifications WHERE user_id=?",
		"cases":         "SELECT id,kind,status,received_at,due_at,decision FROM cases WHERE user_id=?",
		"alias_history": "SELECT alias,created_at FROM alias_history WHERE user_id=?",
		"subscriptions": "SELECT kind,object_id,notifications,created_at FROM subscriptions WHERE user_id=?",
	}
	for name, query := range queries {
		rows, err := snapshot.Query(query, i.ID)
		if err != nil {
			return nil, err
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			return nil, err
		}
		records := []map[string]any{}
		for rows.Next() {
			values := make([]any, len(columns))
			targets := make([]any, len(columns))
			for n := range values {
				targets[n] = &values[n]
			}
			if err = rows.Scan(targets...); err != nil {
				rows.Close()
				return nil, err
			}
			record := map[string]any{}
			for n, col := range columns {
				record[col] = values[n]
			}
			records = append(records, record)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		result[name] = records
	}
	draftRows, err := snapshot.Query("SELECT id,object_id,version_number FROM drafts WHERE author_id=? AND updated_at>unixepoch()-30*86400", i.ID)
	if err != nil {
		return nil, err
	}
	drafts := []map[string]any{}
	for draftRows.Next() {
		var id, object string
		var version int
		if err = draftRows.Scan(&id, &object, &version); err != nil {
			draftRows.Close()
			return nil, err
		}
		var body draftBody
		if app.privateGet(object, i.ID, "draft", &body) == nil {
			drafts = append(drafts, map[string]any{"id": id, "version": version, "body": body})
		}
	}
	draftRows.Close()
	result["drafts"] = drafts
	raw, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	f, err := z.Create("own-data.json")
	if err != nil {
		return nil, err
	}
	if _, err = f.Write(raw); err != nil {
		return nil, err
	}
	mediaRows, err := snapshot.Query("SELECT id,post_id,body,mime FROM media WHERE owner_id=? AND removed_at IS NULL", i.ID)
	if err != nil {
		return nil, err
	}
	for mediaRows.Next() {
		var id string
		var post sql.NullString
		var sealed []byte
		var mime string
		if err = mediaRows.Scan(&id, &post, &sealed, &mime); err != nil {
			mediaRows.Close()
			return nil, err
		}
		if post.Valid && !app.postVisible(post.String, i.ID) {
			continue
		}
		var key []byte
		if app.vault.QueryRow("SELECT key FROM media_keys WHERE id=?", id).Scan(&key) != nil {
			continue
		}
		raw, e := decrypt(key, sealed, id+":image")
		if e != nil {
			mediaRows.Close()
			return nil, e
		}
		extension := ".png"
		if mime == "image/jpeg" {
			extension = ".jpg"
		}
		f, e := z.Create("images/" + id + extension)
		if e != nil {
			mediaRows.Close()
			return nil, e
		}
		if _, e = f.Write(raw); e != nil {
			mediaRows.Close()
			return nil, e
		}
	}
	mediaRows.Close()
	if err = z.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
func (app *App) deactivateAtlas(w http.ResponseWriter, r *http.Request) {
	i := app.requireIdentity(w, r, true)
	if i == nil {
		return
	}
	if r.ParseForm() != nil || r.FormValue("confirm") != "deactivate" {
		fail(w, errInput)
		return
	}
	retain := integer(r.FormValue("retain_content"))
	if retain != 0 && retain != 1 {
		fail(w, errInput)
		return
	}
	retained := r.PostForm["retain_ids"]
	if len(retained) > 1000 {
		fail(w, errInput)
		return
	}
	tx, err := app.db.Begin()
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback()
	if _, err = tx.Exec("DELETE FROM retained_objects WHERE user_id=?", i.ID); err != nil {
		fail(w, err)
		return
	}
	for _, id := range retained {
		var n int
		if err = tx.QueryRow("SELECT (SELECT count(*) FROM posts WHERE id=? AND author_id=?)+(SELECT count(*) FROM replies WHERE id=? AND author_id=?)", id, i.ID, id, i.ID).Scan(&n); err != nil || n != 1 {
			fail(w, errForbidden)
			return
		}
		if _, err = tx.Exec("INSERT OR IGNORE INTO retained_objects VALUES(?,?)", i.ID, id); err != nil {
			fail(w, err)
			return
		}
	}
	if _, err = tx.Exec("UPDATE user_settings SET retain_content=? WHERE user_id=?", retain, i.ID); err != nil {
		fail(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		fail(w, err)
		return
	}
	request, err := http.NewRequestWithContext(r.Context(), "POST", app.config.AccountOrigin+"/internal/identity/deactivate", strings.NewReader(urlValues(i.AccountID, i.SID)))
	if err != nil {
		fail(w, err)
		return
	}
	request.SetBasicAuth("star-atlas", app.secret)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := app.httpClient.Do(request)
	if err != nil {
		fail(w, err)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		respond(w, 503, map[string]string{"code": "ACCOUNT_ACTION_UNAVAILABLE"})
		return
	}
	action := "deactivate-user"
	if retain == 1 {
		action = "retain-user"
	}
	if err = app.applyOperation(operation{EventID: randomID(), ObjectID: i.ID, Action: action, Retained: retained, CreatedAt: time.Now().Unix()}); err != nil {
		fail(w, err)
		return
	}
	cookie(w, "star_atlas_session", "", -1)
	http.Redirect(w, r, "/recover", 303)
}
func urlValues(account, sid string) string { return "account_id=" + account + "&sid=" + sid }
func (app *App) CaseList() ([]map[string]any, error) {
	rows, err := app.db.Query("SELECT id,kind,status,due_at,conflict FROM cases ORDER BY CASE kind WHEN 'emergency' THEN 0 WHEN 'appeal' THEN 1 ELSE 2 END,received_at")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []map[string]any{}
	for rows.Next() {
		var id, kind, status string
		var due int64
		var conflict int
		rows.Scan(&id, &kind, &status, &due, &conflict)
		result = append(result, map[string]any{"id": id, "kind": kind, "status": status, "due_at": due, "conflict": conflict})
	}
	return result, rows.Err()
}
func (app *App) ResolveCase(id, reviewer, decision string) error {
	if reviewer == "" || len(reviewer) > 100 {
		return errInput
	}
	if decision != "dismissed" && decision != "restricted" && decision != "released" && decision != "needs-independent-review" {
		return errInput
	}
	var object, kind, status string
	var parent, owner, subject sql.NullString
	var conflict int
	err := app.db.QueryRow("SELECT object_id,kind,parent_id,user_id,subject_id,conflict,status FROM cases WHERE id=?", id).Scan(&object, &kind, &parent, &owner, &subject, &conflict, &status)
	if err != nil {
		return err
	}
	if status == "resolved" {
		return errConflict
	}
	if conflict == 1 && decision != "needs-independent-review" {
		return errForbidden
	}
	if kind == "appeal" {
		var previous string
		if app.db.QueryRow("SELECT reviewer FROM cases WHERE id=?", parent.String).Scan(&previous) != nil || previous == reviewer {
			return errForbidden
		}
	}
	if decision == "restricted" {
		if object == "" {
			return errInput
		}
		err = app.applyOperation(operation{EventID: id, ObjectID: object, Action: "restrict", CreatedAt: time.Now().Unix()})
		if err != nil {
			return err
		}
	}
	if decision == "released" {
		if kind != "appeal" || !parent.Valid {
			return errForbidden
		}
		err = app.applyOperation(operation{EventID: randomID(), ObjectID: parent.String, Action: "release-restriction", CreatedAt: time.Now().Unix()})
		if err != nil {
			return err
		}
	}
	tx, err := app.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	state := "resolved"
	if decision == "needs-independent-review" {
		state = "waiting_independent_review"
	}
	var closed any = time.Now().Unix()
	if state == "waiting_independent_review" {
		closed = nil
	}
	res, e := tx.Exec("UPDATE cases SET status=?,decision=?,reviewer=?,closed_at=? WHERE id=? AND status<>'resolved'", state, decision, reviewer, closed, id)
	err = e
	if err == nil {
		n, _ := res.RowsAffected()
		if n != 1 {
			err = errConflict
		}
	}
	if err == nil && owner.Valid {
		_, err = tx.Exec("INSERT OR IGNORE INTO notifications VALUES(?,?,?,?,?,?,NULL)", randomID(), owner.String, "case-result", id, "", time.Now().Unix())
	}
	if err == nil && subject.Valid && subject.String != owner.String {
		_, err = tx.Exec("INSERT OR IGNORE INTO notifications VALUES(?,?,?,?,?,?,NULL)", randomID(), subject.String, "case-result", id, "", time.Now().Unix())
	}
	if err == nil {
		if closed != nil {
			_, err = app.vault.Exec("UPDATE objects SET expires_at=? WHERE id=? AND kind='case'", time.Now().Add(30*24*time.Hour).Unix(), "case:"+id)
		}
	}
	if err == nil && state == "resolved" {
		_, err = tx.Exec("UPDATE jobs SET status='done' WHERE business_key=?", "case:"+id)
	}
	if err == nil {
		err = tx.Commit()
	}
	return err
}
