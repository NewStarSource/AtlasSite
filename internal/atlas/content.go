package atlas

import (
	"bytes"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/csrf"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	gmtext "github.com/yuin/goldmark/text"
)

type draftBody struct{ Title, Content, Community, License string }

var markdown = goldmark.New(goldmark.WithExtensions(extension.Table, extension.Strikethrough, extension.Footnote))

func renderMarkdown(value string) string {
	source := []byte(value)
	doc := markdown.Parser().Parse(gmtext.NewReader(source))
	ast.Walk(doc, func(n ast.Node, enter bool) (ast.WalkStatus, error) {
		if enter {
			if image, ok := n.(*ast.Image); ok && !strings.HasPrefix(string(image.Destination), "/media/") {
				n.Parent().RemoveChild(n.Parent(), n)
				return ast.WalkSkipChildren, nil
			}
		}
		return ast.WalkContinue, nil
	})
	var buf bytes.Buffer
	if markdown.Renderer().Render(&buf, source, doc) != nil {
		return esc(value)
	}
	return buf.String()
}
func mediaIDs(value string) ([]string, error) {
	source := []byte(value)
	doc := markdown.Parser().Parse(gmtext.NewReader(source))
	ids := []string{}
	err := ast.Walk(doc, func(n ast.Node, enter bool) (ast.WalkStatus, error) {
		if enter {
			if image, ok := n.(*ast.Image); ok {
				dest := string(image.Destination)
				id := strings.TrimPrefix(dest, "/media/")
				if !strings.HasPrefix(dest, "/media/") || !uuidRequest(id) {
					return ast.WalkStop, errInput
				}
				ids = append(ids, id)
			}
		}
		return ast.WalkContinue, nil
	})
	if len(ids) > 4 {
		return nil, errInput
	}
	return ids, err
}
func (app *App) registerPostRoutes(router *chi.Mux) {
	router.Get("/p/{id}", app.postPage)
	router.Get("/c/{slug}/new", app.editorPage)
	router.Post("/api/v1/c/{slug}/posts", app.publishPost)
	router.Post("/api/v1/p/{id}/replies", app.publishReply)
}
func (app *App) contentRoutes(router *chi.Mux) {
	router.Get("/new", app.editorPage)
	router.Get("/p/{id}/edit", app.editorPage)
	router.Post("/api/v1/posts", app.publishPost)
	router.Post("/api/v1/p/{id}/edit", app.editPost)
	router.Post("/api/v1/p/{id}/delete", app.deletePost)
	router.Post("/api/v1/p/{id}/association", app.associatePost)
	router.Post("/api/v1/replies/{id}/edit", app.editReply)
	router.Post("/api/v1/replies/{id}/delete", app.deleteReply)
	router.Get("/replies/{id}/edit", app.replyEditor)
	router.Get("/my/drafts", app.draftsPage)
	router.Get("/drafts/{id}", app.draftPage)
	router.Post("/api/v1/drafts/{id}", app.saveDraftHTTP)
	router.Post("/api/v1/drafts/{id}/delete", app.deleteDraft)
}
func (app *App) postPage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	i, _, _ := app.currentIdentity(r)
	viewer := ""
	if i != nil {
		viewer = i.ID
	}
	if !app.postVisible(id, viewer) {
		fail(w, sql.ErrNoRows)
		return
	}
	p, err := app.getPost(id)
	if err != nil {
		fail(w, err)
		return
	}
	var version int
	var license string
	app.db.QueryRow("SELECT version_number,license FROM posts WHERE id=?", id).Scan(&version, &license)
	content := `<article><div class="post-meta">` + publicAuthor(*p) + ` · ` + formatTime(p.CreatedAt) + `</div><h1>` + esc(p.Title) + `</h1><div class="post-content">` + renderMarkdown(p.Content) + `</div><p>许可 ` + esc(license) + ` · 最后修改 ` + formatTime(p.UpdatedAt) + `</p></article>`
	if i != nil {
		marked, _ := app.isBookmarked(i.ID, id)
		label := "收藏"
		if marked {
			label = "取消收藏"
		}
		content += formStart(r, "/api/v1/p/"+pathID(id)+"/bookmark") + field("bookmarked", fmt.Sprint(!marked)) + `<button class="btn">` + label + `</button></form>`
		var liked int
		app.db.QueryRow("SELECT count(*) FROM post_likes WHERE user_id=? AND post_id=?", i.ID, id).Scan(&liked)
		content += formStart(r, "/api/v1/p/"+pathID(id)+"/like") + field("liked", fmt.Sprint(liked == 0)) + `<button class="btn">` + map[bool]string{true: "取消赞", false: "赞"}[liked > 0] + `</button></form>`
		if i.ID == p.AuthorID {
			content += `<p><a href="/p/` + pathID(id) + `/edit">编辑内容</a></p>` + formStart(r, "/api/v1/p/"+pathID(id)+"/delete") + versionField(version) + `<button>删除内容及下游展示</button></form>` + formStart(r, "/api/v1/p/"+pathID(id)+"/association") + versionField(version) + field("community", "") + `<button>解除社群与主题关联</button></form>`
		}
		content += app.associationForm(r, p, version)
		content += `<p><a href="/help/report?object_id=` + pathID(id) + `">举报或紧急求助</a></p>` + formStart(r, "/api/v1/p/"+pathID(id)+"/replies") + field("request_id", randomID()) + `<label>回复<textarea name="content" maxlength="20000" required></textarea></label><label>回复对象 ID（直接回复帖子可留空）<input name="parent_id"></label><button class="btn">发表回复</button></form>`
	}
	replies, err := app.listReplies(id)
	if err != nil {
		fail(w, err)
		return
	}
	content += `<h2>回复</h2>`
	for _, reply := range replies {
		if !app.replyVisible(reply.ID, viewer) {
			continue
		}
		authorName := reply.AuthorName
		var authorStatus string
		app.db.QueryRow("SELECT status FROM users WHERE id=?", reply.AuthorID).Scan(&authorStatus)
		if authorStatus == "deactivated" || authorStatus == "deleted" {
			authorName += "（已注销）"
		}
		content += `<article class="reply-item"><p>` + esc(authorName) + ` · ` + formatTime(reply.CreatedAt) + ` · 回复 ID ` + esc(reply.ID) + `</p><div>` + renderMarkdown(reply.Content) + `</div>`
		if reply.ParentID.Valid {
			content += `<p>回复 ` + esc(reply.ParentID.String) + `</p>`
		}
		if i != nil && i.ID == reply.AuthorID {
			content += `<a href="/replies/` + pathID(reply.ID) + `/edit">编辑回复</a>`
		}
		content += `</article>`
	}
	app.renderPage(w, r, p.Title, "post", content)
}
func editorContent(r *http.Request, action, id string, version int, d draftBody) string {
	draftButton := ""
	if id != "" {
		draftButton = `<button formnovalidate formaction="/api/v1/drafts/` + pathID(id) + `">保存私人草稿</button>`
	}
	return `<h1>编辑内容</h1><p>可独立发布，也可填写社群路径关联。正文支持 Markdown；每篇最多四张自有图片。</p>` + formStart(r, action) + field("request_id", randomID()) + field("draft_id", id) + versionField(version) + `<label>标题<input name="title" value="` + esc(d.Title) + `" maxlength="200" required></label><label>正文<textarea name="content" maxlength="50000" required>` + esc(d.Content) + `</textarea></label><label>社群路径（留空独立发布）<input name="community" value="` + esc(d.Community) + `"></label><label>许可<select name="license"><option value="` + esc(d.License) + `">` + esc(d.License) + `</option><option value="reserved">保留全部权利</option><option>CC BY 4.0</option><option>CC BY-SA 4.0</option></select></label><p>CC 授权已产生的合法复制权不会因删除撤销；保留全部权利不授权站外转载。</p><button class="btn">保存发布</button>` + draftButton + `<button type="button" data-preview>预览</button><div data-preview-output></div></form>`
}
func (app *App) editorPage(w http.ResponseWriter, r *http.Request) {
	i := app.requireIdentity(w, r, false)
	if i == nil {
		return
	}
	id := chi.URLParam(r, "id")
	d := draftBody{Community: chi.URLParam(r, "slug"), License: "reserved"}
	action := "/api/v1/posts"
	version := 0
	if id != "" {
		p, err := app.getPost(id)
		if err != nil {
			fail(w, err)
			return
		}
		if p.AuthorID != i.ID || !app.postVisible(id, i.ID) {
			fail(w, errForbidden)
			return
		}
		d.Title, d.Content = p.Title, p.Content
		app.db.QueryRow("SELECT version_number,license FROM posts WHERE id=?", id).Scan(&version, &d.License)
		if p.CommunityID != "" {
			app.db.QueryRow("SELECT slug FROM communities WHERE id=?", p.CommunityID).Scan(&d.Community)
		}
		action = "/api/v1/p/" + pathID(id) + "/edit"
		id = ""
	}
	if version == 0 {
		id = randomID()
	}
	body := editorContent(r, action, id, version, d)
	if version == 0 {
		body += `<p role="status" data-draft-status>草稿每 15 秒自动保存；也可手动保存。</p><button type="button" data-save-draft>保存私人草稿</button>`
	}
	body += `<h2>上传自有图片</h2><p>支持 JPEG/PNG，最大 8 MiB、1600 万像素。上传后将返回的 Markdown 插入正文；未发布上传 24 小时后清理。</p><form method="post" action="/api/v1/media" enctype="multipart/form-data">` + stringCSRF(r) + `<input type="file" name="image" accept="image/jpeg,image/png" required><select name="license"><option value="reserved">保留全部权利</option><option>CC BY 4.0</option><option>CC BY-SA 4.0</option></select><button>上传图片</button></form>`
	app.renderPage(w, r, "编辑内容", "editor", body)
}
func stringCSRF(r *http.Request) string { return string(csrf.TemplateField(r)) }
func (app *App) communityID(slug string) (string, error) {
	if slug == "" {
		return "", nil
	}
	var id string
	err := app.db.QueryRow("SELECT id FROM communities WHERE slug=? AND status IN ('active','uncategorized')", slug).Scan(&id)
	return id, err
}
func (app *App) publishPost(w http.ResponseWriter, r *http.Request) {
	i := app.contentIdentity(w, r)
	if i == nil || !app.rateLimit(w, r, i.ID, 60) {
		return
	}
	if r.ParseForm() != nil {
		fail(w, errInput)
		return
	}
	slug := r.FormValue("community")
	if v := chi.URLParam(r, "slug"); v != "" {
		slug = v
	}
	community, err := app.communityID(slug)
	if err != nil {
		fail(w, err)
		return
	}
	title, content, license, key := strings.TrimSpace(r.FormValue("title")), strings.TrimSpace(r.FormValue("content")), r.FormValue("license"), r.FormValue("request_id")
	p, err := app.publish(community, i.ID, title, content, license, key)
	if err != nil {
		fail(w, err)
		return
	}
	if draft := r.FormValue("draft_id"); draft != "" {
		app.db.Exec("DELETE FROM drafts WHERE id=? AND author_id=?", draft, i.ID)
		app.vault.Exec("DELETE FROM objects WHERE owner_id=? AND kind='draft' AND id LIKE ?", i.ID, draft+":%")
	}
	http.Redirect(w, r, "/p/"+pathID(p.ID), 303)
}
func (app *App) publish(community, owner, title, content, license, key string) (*Post, error) {
	app.opsMu.Lock()
	defer app.opsMu.Unlock()

	if title == "" || content == "" || !checkText(title, 200) || !checkText(content, 50000) || !validLicense(license) || !uuidRequest(key) {
		return nil, errInput
	}
	images, err := mediaIDs(content)
	if err != nil {
		return nil, err
	}
	tx, err := app.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var existing string
	err = tx.QueryRow("SELECT id FROM posts WHERE author_id=? AND request_id=?", owner, key).Scan(&existing)
	if err == nil {
		var t, c, l string
		tx.QueryRow("SELECT title,content,license FROM posts WHERE id=?", existing).Scan(&t, &c, &l)
		if t != title || c != content || l != license {
			return nil, errConflict
		}
		tx.Rollback()
		return app.getPost(existing)
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	if err = checkAuthorCommunity(tx, owner, community); err != nil {
		return nil, err
	}
	id, now := randomID(), time.Now().Unix()
	_, err = tx.Exec("INSERT INTO posts(id,community_id,author_id,title,content,status,created_at,updated_at,license,request_id) VALUES(?,?,?,?,?,'published',?,?,?,?)", id, nullable(community), owner, title, content, now, now, license, key)
	if err != nil {
		return nil, err
	}
	if err = attachImages(tx, images, owner, id); err != nil {
		return nil, err
	}
	if err = notifySubscribers(tx, id, owner, now); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return app.getPost(id)
}
func checkAuthorCommunity(tx *sql.Tx, owner, community string) error {
	var n int
	if err := tx.QueryRow("SELECT count(*) FROM users WHERE id=? AND status='active'", owner).Scan(&n); err != nil {
		return err
	}
	if n != 1 {
		return errForbidden
	}
	if community != "" {
		if err := tx.QueryRow("SELECT count(*) FROM communities WHERE id=? AND status IN ('active','uncategorized')", community).Scan(&n); err != nil {
			return err
		}
		if n != 1 {
			return errForbidden
		}
	}
	return nil
}
func attachImages(tx *sql.Tx, images []string, owner, post string) error {
	for _, id := range images {
		result, err := tx.Exec("UPDATE media SET post_id=? WHERE id=? AND owner_id=? AND removed_at IS NULL AND ((post_id IS NULL AND created_at>?) OR post_id=?)", post, id, owner, time.Now().Add(-24*time.Hour).Unix(), post)
		if err != nil {
			return err
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			return errForbidden
		}
	}
	return nil
}
func (app *App) editPost(w http.ResponseWriter, r *http.Request) {
	i := app.contentIdentity(w, r)
	if i == nil {
		return
	}
	app.opsMu.Lock()
	defer app.opsMu.Unlock()
	if r.ParseForm() != nil {
		fail(w, errInput)
		return
	}
	id := chi.URLParam(r, "id")
	p, err := app.getPost(id)
	if err != nil {
		fail(w, err)
		return
	}
	if p.AuthorID != i.ID || !app.postVisible(id, i.ID) {
		fail(w, errForbidden)
		return
	}
	title, content, license := strings.TrimSpace(r.FormValue("title")), strings.TrimSpace(r.FormValue("content")), r.FormValue("license")
	if title == "" || content == "" || !checkText(title, 200) || !checkText(content, 50000) || !validLicense(license) {
		fail(w, errInput)
		return
	}
	community, err := app.communityID(r.FormValue("community"))
	if err != nil {
		fail(w, err)
		return
	}
	images, err := mediaIDs(content)
	if err != nil {
		fail(w, err)
		return
	}
	if err = app.privatePut("version:"+randomID(), i.ID, "revision:"+p.ID, p, time.Now().Add(30*24*time.Hour).Unix()); err != nil {
		fail(w, err)
		return
	}
	tx, err := app.db.Begin()
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback()
	if err = checkAuthorCommunity(tx, i.ID, community); err != nil {
		fail(w, err)
		return
	}
	result, err := tx.Exec("UPDATE posts SET title=?,content=?,community_id=?,license=?,version_number=version_number+1,updated_at=? WHERE id=? AND author_id=? AND version_number=? AND status='published' AND NOT EXISTS(SELECT 1 FROM restrictions WHERE object_id=posts.id AND removed_at IS NULL)", title, content, nullable(community), license, time.Now().Unix(), id, i.ID, integer(r.FormValue("version")))
	if err == nil {
		n, _ := result.RowsAffected()
		if n != 1 {
			err = errConflict
		}
	}
	if err == nil {
		err = attachImages(tx, images, i.ID, id)
	}
	if err == nil {
		_, err = tx.Exec("DELETE FROM post_topics WHERE post_id=? AND topic_id NOT IN (SELECT id FROM topics WHERE community_id=?)", id, nullable(community))
	}
	if err == nil {
		oldImages, _ := mediaIDs(p.Content)
		keep := map[string]bool{}
		for _, id := range images {
			keep[id] = true
		}
		for _, image := range oldImages {
			if !keep[image] {
				op := operation{EventID: randomID(), ObjectID: image, OwnerID: i.ID, Action: "delete-media", CreatedAt: time.Now().Unix()}
				if err = app.appendOperation(op); err != nil {
					break
				}
				if err = applyOperationSQL(tx, op); err != nil {
					break
				}
			}
		}
		if err == nil && p.CommunityID != community {
			op := operation{EventID: randomID(), ObjectID: p.ID, OwnerID: i.ID, Action: "association-state", VersionNumber: integer(r.FormValue("version")) + 1, CommunityID: community, CreatedAt: time.Now().Unix()}
			if err = app.appendOperation(op); err == nil {
				err = applyOperationSQL(tx, op)
			}
		}
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		fail(w, err)
		return
	}
	http.Redirect(w, r, "/p/"+pathID(id), 303)
}
func (app *App) deletePost(w http.ResponseWriter, r *http.Request) {
	i := app.contentIdentity(w, r)
	if i == nil {
		return
	}
	if r.ParseForm() != nil {
		fail(w, errInput)
		return
	}
	id := chi.URLParam(r, "id")
	var owner string
	var v int
	err := app.db.QueryRow("SELECT author_id,version_number FROM posts WHERE id=?", id).Scan(&owner, &v)
	if err != nil {
		fail(w, err)
		return
	}
	if owner != i.ID {
		fail(w, errForbidden)
		return
	}
	if v != integer(r.FormValue("version")) {
		fail(w, errConflict)
		return
	}
	if err = app.applyOperation(operation{EventID: randomID(), ObjectID: id, Action: "delete-post", CreatedAt: time.Now().Unix()}, v); err != nil {
		fail(w, err)
		return
	}
	http.Redirect(w, r, "/", 303)
}
func (app *App) associatePost(w http.ResponseWriter, r *http.Request) {
	i := app.contentIdentity(w, r)
	if i == nil {
		return
	}
	if r.ParseForm() != nil {
		fail(w, errInput)
		return
	}
	id := chi.URLParam(r, "id")
	p, err := app.getPost(id)
	if err != nil {
		fail(w, err)
		return
	}
	if p.AuthorID != i.ID || !app.postVisible(id, i.ID) {
		fail(w, errForbidden)
		return
	}
	community, err := app.communityID(r.FormValue("community"))
	if err != nil {
		fail(w, err)
		return
	}
	topics := r.Form["topic_ids"]
	if len(topics) > 10 {
		fail(w, errInput)
		return
	}
	for _, topic := range topics {
		var n int
		if app.db.QueryRow("SELECT count(*) FROM topics WHERE id=? AND community_id=? AND status='active'", topic, community).Scan(&n) != nil || n != 1 {
			fail(w, errInput)
			return
		}
	}
	err = app.applyOperation(operation{EventID: randomID(), ObjectID: id, OwnerID: i.ID, Action: "association", CommunityID: community, TopicIDs: topics, CreatedAt: time.Now().Unix()}, integer(r.FormValue("version")))
	if err != nil {
		fail(w, err)
		return
	}
	http.Redirect(w, r, "/p/"+pathID(id), 303)
}
func (app *App) publishReply(w http.ResponseWriter, r *http.Request) {
	i := app.contentIdentity(w, r)
	if i == nil || !app.rateLimit(w, r, i.ID, 100) {
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
	_, err := app.saveReply(id, i.ID, strings.TrimSpace(r.FormValue("content")), r.FormValue("parent_id"), r.FormValue("request_id"))
	if err != nil {
		fail(w, err)
		return
	}
	http.Redirect(w, r, "/p/"+pathID(id), 303)
}
func (app *App) saveReply(postID, owner, content, parent, key string) (*Reply, error) {
	app.opsMu.Lock()
	defer app.opsMu.Unlock()

	if content == "" || !checkText(content, 20000) || !uuidRequest(key) {
		return nil, errInput
	}
	if !app.postVisible(postID, owner) {
		return nil, errForbidden
	}
	if parent != "" && !app.replyVisible(parent, owner) {
		return nil, errForbidden
	}
	tx, err := app.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = checkAuthorCommunity(tx, owner, ""); err != nil {
		return nil, err
	}
	var existing string
	err = tx.QueryRow("SELECT id FROM replies WHERE author_id=? AND request_id=?", owner, key).Scan(&existing)
	if err == nil {
		var p, c string
		var par sql.NullString
		tx.QueryRow("SELECT post_id,content,parent_id FROM replies WHERE id=?", existing).Scan(&p, &c, &par)
		if p != postID || c != content || par.String != parent {
			return nil, errConflict
		}
		tx.Rollback()
		return app.getReply(existing)
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	level := 0
	var postOwner string
	if err = tx.QueryRow("SELECT author_id FROM posts p WHERE id=? AND "+visiblePostSQL, postID).Scan(&postOwner); err != nil {
		return nil, err
	}
	if parent != "" {
		var parentOwner string
		if err = tx.QueryRow("SELECT author_id,level FROM replies WHERE id=? AND post_id=? AND status='published'", parent, postID).Scan(&parentOwner, &level); err != nil {
			return nil, errForbidden
		}
		if level >= 20 {
			return nil, errInput
		}
		level++
		var blocked int
		tx.QueryRow("SELECT count(*) FROM blocks WHERE (owner_id=? AND target_id=?) OR (owner_id=? AND target_id=?)", owner, parentOwner, parentOwner, owner).Scan(&blocked)
		if blocked > 0 {
			return nil, errForbidden
		}
	}
	id, now := randomID(), time.Now().Unix()
	_, err = tx.Exec("INSERT INTO replies(id,post_id,parent_id,author_id,content,level,status,created_at,updated_at,request_id) VALUES(?,?,?,?,?,?,'published',?,?,?)", id, postID, nullable(parent), owner, content, level, now, now, key)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec("UPDATE posts SET reply_count=reply_count+1 WHERE id=?", postID)
	if err != nil {
		return nil, err
	}
	recipients, err := tx.Query("SELECT id FROM users u JOIN user_settings s ON s.user_id=u.id WHERE u.status='active' AND s.reply_notifications=1 AND u.id<>? AND (u.id=? OR EXISTS(SELECT 1 FROM replies WHERE post_id=? AND author_id=u.id AND status='published')) AND NOT EXISTS(SELECT 1 FROM blocks WHERE (owner_id=u.id AND target_id=?) OR (owner_id=? AND target_id=u.id))", owner, postOwner, postID, owner, owner)
	if err != nil {
		return nil, err
	}
	var recipientIDs []string
	for recipients.Next() {
		var id string
		if err = recipients.Scan(&id); err != nil {
			recipients.Close()
			return nil, err
		}
		recipientIDs = append(recipientIDs, id)
	}
	recipients.Close()
	for _, recipient := range recipientIDs {
		if _, err = tx.Exec("INSERT OR IGNORE INTO notifications VALUES(?,?,?,?,?,?,NULL)", randomID(), recipient, "reply", id, postID, now); err != nil {
			return nil, err
		}
	}
	_, err = tx.Exec("INSERT OR IGNORE INTO jobs(id,business_key,kind,object_id,available_at,created_at) VALUES(?,?,'reply',?,?,?)", randomID(), "reply:"+id, id, now, now)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return app.getReply(id)
}
func (app *App) replyEditor(w http.ResponseWriter, r *http.Request) {
	i := app.requireIdentity(w, r, false)
	if i == nil {
		return
	}
	id := chi.URLParam(r, "id")
	reply, err := app.getReply(id)
	if err != nil {
		fail(w, err)
		return
	}
	if reply.AuthorID != i.ID || !app.replyVisible(id, i.ID) {
		fail(w, errForbidden)
		return
	}
	var version int
	app.db.QueryRow("SELECT version_number FROM replies WHERE id=?", id).Scan(&version)
	app.renderPage(w, r, "编辑回复", "editor", `<h1>编辑回复</h1>`+formStart(r, "/api/v1/replies/"+pathID(id)+"/edit")+versionField(version)+`<textarea name="content" maxlength="20000" required>`+esc(reply.Content)+`</textarea><button>保存</button></form>`+formStart(r, "/api/v1/replies/"+pathID(id)+"/delete")+versionField(version)+`<button>删除回复及下游展示</button></form>`)
}
func (app *App) editReply(w http.ResponseWriter, r *http.Request) {
	i := app.contentIdentity(w, r)
	if i == nil {
		return
	}
	app.opsMu.Lock()
	defer app.opsMu.Unlock()
	if r.ParseForm() != nil {
		fail(w, errInput)
		return
	}
	id := chi.URLParam(r, "id")
	reply, err := app.getReply(id)
	if err != nil {
		fail(w, err)
		return
	}
	if reply.AuthorID != i.ID || !app.replyVisible(id, i.ID) {
		fail(w, errForbidden)
		return
	}
	content := strings.TrimSpace(r.FormValue("content"))
	if content == "" || !checkText(content, 20000) {
		fail(w, errInput)
		return
	}
	if err = app.privatePut("version:"+randomID(), i.ID, "revision:"+reply.ID, reply, time.Now().Add(30*24*time.Hour).Unix()); err != nil {
		fail(w, err)
		return
	}
	result, err := app.db.Exec("UPDATE replies SET content=?,version_number=version_number+1,updated_at=? WHERE id=? AND author_id=? AND version_number=? AND status='published' AND NOT EXISTS(SELECT 1 FROM restrictions WHERE object_id=replies.id AND removed_at IS NULL) AND EXISTS(SELECT 1 FROM users WHERE id=replies.author_id AND status='active')", content, time.Now().Unix(), id, i.ID, integer(r.FormValue("version")))
	if err == nil {
		n, _ := result.RowsAffected()
		if n != 1 {
			err = errConflict
		}
	}
	if err != nil {
		fail(w, err)
		return
	}
	http.Redirect(w, r, "/p/"+pathID(reply.PostID), 303)
}
func (app *App) deleteReply(w http.ResponseWriter, r *http.Request) {
	i := app.contentIdentity(w, r)
	if i == nil {
		return
	}
	if r.ParseForm() != nil {
		fail(w, errInput)
		return
	}
	id := chi.URLParam(r, "id")
	reply, err := app.getReply(id)
	if err != nil {
		fail(w, err)
		return
	}
	if reply.AuthorID != i.ID {
		fail(w, errForbidden)
		return
	}
	var v int
	app.db.QueryRow("SELECT version_number FROM replies WHERE id=?", id).Scan(&v)
	if v != integer(r.FormValue("version")) {
		fail(w, errConflict)
		return
	}
	err = app.applyOperation(operation{EventID: randomID(), ObjectID: id, Action: "delete-reply", CreatedAt: time.Now().Unix()}, v)
	if err != nil {
		fail(w, err)
		return
	}
	http.Redirect(w, r, "/p/"+pathID(reply.PostID), 303)
}
func (app *App) replyVisible(id, viewer string) bool {
	var postID string
	var n int
	err := app.db.QueryRow(`WITH RECURSIVE chain(id,parent_id,status,author_id) AS (
        SELECT id,parent_id,status,author_id FROM replies WHERE id=? UNION ALL SELECT r.id,r.parent_id,r.status,r.author_id FROM replies r JOIN chain c ON r.id=c.parent_id)
        SELECT count(*) FROM chain WHERE status<>'published'
        OR EXISTS(SELECT 1 FROM restrictions WHERE object_id=chain.id AND removed_at IS NULL)
        OR EXISTS(SELECT 1 FROM blocks WHERE (owner_id=? AND target_id=chain.author_id) OR (target_id=? AND owner_id=chain.author_id))
        OR NOT EXISTS(SELECT 1 FROM users u JOIN user_settings s ON s.user_id=u.id WHERE u.id=chain.author_id AND (u.status='active' OR (u.status IN ('deactivated','deleted') AND (s.retain_content=1 OR EXISTS(SELECT 1 FROM retained_objects ro WHERE ro.user_id=u.id AND ro.object_id=chain.id)))))`, id, viewer, viewer).Scan(&n)
	if err != nil || n > 0 {
		return false
	}
	if app.db.QueryRow("SELECT post_id FROM replies WHERE id=?", id).Scan(&postID) != nil {
		return false
	}
	return app.postVisible(postID, viewer)
}
func (app *App) draftsPage(w http.ResponseWriter, r *http.Request) {
	i := app.requireIdentity(w, r, false)
	if i == nil {
		return
	}
	rows, err := app.db.Query("SELECT id FROM drafts WHERE author_id=? AND updated_at>? ORDER BY updated_at DESC,id", i.ID, time.Now().Add(-30*24*time.Hour).Unix())
	if err != nil {
		fail(w, err)
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	body := `<h1>我的私人草稿</h1><p>最后保存后 30 天清理。草稿不公开，不进入普通长期备份。</p>`
	for _, id := range ids {
		body += `<p><a href="/drafts/` + pathID(id) + `">打开草稿</a></p>` + formStart(r, "/api/v1/drafts/"+pathID(id)+"/delete") + `<button>删除草稿</button></form>`
	}
	app.renderPage(w, r, "私人草稿", "drafts", body)
}
func (app *App) draftPage(w http.ResponseWriter, r *http.Request) {
	i := app.requireIdentity(w, r, false)
	if i == nil {
		return
	}
	id := chi.URLParam(r, "id")
	var v int
	var object string
	err := app.db.QueryRow("SELECT version_number,object_id FROM drafts WHERE id=? AND author_id=? AND updated_at>?", id, i.ID, time.Now().Add(-30*24*time.Hour).Unix()).Scan(&v, &object)
	var d draftBody
	if err == nil {
		err = app.privateGet(object, i.ID, "draft", &d)
	}
	if err != nil {
		fail(w, err)
		return
	}
	app.renderPage(w, r, "私人草稿", "editor", editorContent(r, "/api/v1/posts", id, v, d)+`<button type="button" data-save-draft>保存私人草稿</button><p data-draft-status role="status"></p>`)
}
func (app *App) saveDraftHTTP(w http.ResponseWriter, r *http.Request) {
	i := app.contentIdentity(w, r)
	if i == nil {
		return
	}
	if r.ParseForm() != nil {
		fail(w, errInput)
		return
	}
	id := chi.URLParam(r, "id")
	d := draftBody{r.FormValue("title"), r.FormValue("content"), r.FormValue("community"), r.FormValue("license")}
	if !uuidRequest(id) || !checkText(d.Title, 200) || !checkText(d.Content, 50000) || !validLicense(d.License) {
		fail(w, errInput)
		return
	}
	object := id + ":" + randomID()
	if err := app.privatePut(object, i.ID, "draft", d, time.Now().Add(30*24*time.Hour).Unix()); err != nil {
		fail(w, err)
		return
	}
	v := integer(r.FormValue("version"))
	var res sql.Result
	var err error
	if v == 0 {
		res, err = app.db.Exec("INSERT INTO drafts(id,author_id,updated_at,object_id) VALUES(?,?,?,?) ON CONFLICT(id) DO NOTHING", id, i.ID, time.Now().Unix(), object)
	} else {
		res, err = app.db.Exec("UPDATE drafts SET object_id=?,version_number=version_number+1,updated_at=? WHERE id=? AND author_id=? AND version_number=?", object, time.Now().Unix(), id, i.ID, v)
	}
	if err == nil {
		n, _ := res.RowsAffected()
		if n != 1 {
			err = errConflict
		}
	}
	if err != nil {
		app.vault.Exec("DELETE FROM objects WHERE id=?", object)
		fail(w, err)
		return
	}
	if r.Header.Get("Accept") == "application/json" {
		respond(w, 200, map[string]any{"id": id, "version": v + 1, "message": "私人草稿已保存"})
	} else {
		http.Redirect(w, r, "/drafts/"+pathID(id), 303)
	}
}
func (app *App) deleteDraft(w http.ResponseWriter, r *http.Request) {
	i := app.requireIdentity(w, r, false)
	if i == nil {
		return
	}
	id := chi.URLParam(r, "id")
	res, err := app.db.Exec("DELETE FROM drafts WHERE id=? AND author_id=?", id, i.ID)
	if err != nil {
		fail(w, err)
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		fail(w, sql.ErrNoRows)
		return
	}
	app.vault.Exec("DELETE FROM objects WHERE owner_id=? AND kind='draft' AND id LIKE ?", i.ID, id+":%")
	http.Redirect(w, r, "/my/drafts", 303)
}
