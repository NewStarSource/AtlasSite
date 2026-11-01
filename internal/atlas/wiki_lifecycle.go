package atlas

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

func wipeWikiSQL(tx *sql.Tx, id, state, reason string, at int64) error {
	for _, q := range []string{
		"UPDATE wiki_versions SET title='',summary='',body='',fields_json='{}',sources_json='[]',edit_summary='',review_reason='',review_status='withdrawn' WHERE page_id=?",
		"UPDATE wiki_replies SET content='',status='deleted' WHERE page_id=?",
	} {
		if _, err := tx.Exec(q, id); err != nil {
			return err
		}
	}
	_, err := tx.Exec("UPDATE wiki_pages SET state=?,reason=?,published_version=0,latest_version=latest_version+1,withdrawn_at=?,updated_at=? WHERE id=?", state, reason, at, at, id)
	return err
}

func wipeWikiReplySQL(tx *sql.Tx, id string) error {
	_, err := tx.Exec(`WITH RECURSIVE targets(id) AS (SELECT id FROM wiki_replies WHERE id=? UNION ALL SELECT r.id FROM wiki_replies r JOIN targets t ON r.parent_id=t.id) UPDATE wiki_replies SET content='',status='deleted' WHERE id IN (SELECT id FROM targets)`, id)
	return err
}

func withdrawOwnWikiReplies(tx *sql.Tx, user string, retained []string) error {
	rows, err := tx.Query("SELECT id FROM wiki_replies WHERE author_id=? AND status='published'", user)
	if err != nil {
		return err
	}
	ids := []string{}
	keep := map[string]bool{}
	for _, id := range retained {
		keep[id] = true
	}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		if !keep[id] {
			ids = append(ids, id)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err = wipeWikiReplySQL(tx, id); err != nil {
			return err
		}
	}
	return nil
}

func (app *App) preserveWiki(tx *sql.Tx, id string) error {
	// A short-lived encrypted copy supports manual risk handling; it has no public route.
	rows, err := tx.Query("SELECT number,title,summary,body,fields_json,sources_json,edit_summary FROM wiki_versions WHERE page_id=?", id)
	if err != nil {
		return err
	}
	versions := []map[string]any{}
	for rows.Next() {
		var number int
		var title, summary, body, fields, sources, reason string
		if err = rows.Scan(&number, &title, &summary, &body, &fields, &sources, &reason); err != nil {
			rows.Close()
			return err
		}
		versions = append(versions, map[string]any{"number": number, "title": title, "summary": summary, "body": body, "fields": fields, "sources": sources, "edit_summary": reason})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	return app.privatePut("wiki-isolated:"+id, "", "wiki-isolated", versions, time.Now().Add(30*24*time.Hour).Unix())
}

func (app *App) withdrawWiki(w http.ResponseWriter, r *http.Request) {
	i := app.wikiRightsIdentity(w, r)
	if i == nil {
		return
	}
	if r.ParseForm() != nil {
		fail(w, errInput)
		return
	}
	version, err := strconv.Atoi(r.FormValue("version"))
	state, reason := r.FormValue("state"), r.FormValue("reason")
	if err != nil || version < 1 || (state != "deleted" && state != "hidden") || wikiReason(reason) == "" || r.FormValue("confirm") != "withdraw" {
		fail(w, errInput)
		return
	}
	id := chi.URLParam(r, "wiki")
	err = app.commitWikiWithdrawal(r, i, id, state, reason, version)
	if err != nil {
		fail(w, err)
		return
	}
	if state == "hidden" {
		http.Redirect(w, r, "/wiki", 303)
	} else {
		http.Redirect(w, r, "/wiki/"+pathID(id), 303)
	}
}

func (app *App) commitWikiWithdrawal(r *http.Request, i *Identity, id, state, reason string, version int) error {
	app.opsMu.Lock()
	defer app.opsMu.Unlock()
	unlock, err := app.journalLock()
	if err != nil {
		return err
	}
	defer unlock()
	records, err := app.readJournal()
	if err != nil {
		return err
	}
	tx, err := app.collaborationTransaction(r, i, false, false)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var creator, kind, currentState string
	var current int
	if err = tx.QueryRow("SELECT creator_id,kind,state,latest_version FROM wiki_pages WHERE id=?", id).Scan(&creator, &kind, &currentState, &current); err != nil {
		return err
	}
	if currentState != "active" || current != version {
		return errConflict
	}
	role, err := managementAccess(tx, i.ID)
	if err != nil {
		return err
	}
	team := role == "admin" || role == "member"
	if !team && (i.ID != creator || kind != "discovery" || state != "deleted") {
		return errForbidden
	}
	if team {
		var recent int64
		if tx.QueryRow("SELECT reauthenticated_at FROM sessions WHERE token_hash=? AND user_id=?", digest(sessionCookie(r)), i.ID).Scan(&recent) != nil || recent < time.Now().Add(-5*time.Minute).Unix() {
			return errForbidden
		}
	}
	if err = app.preserveWiki(tx, id); err != nil {
		return err
	}
	op := operation{EventID: randomID(), ObjectID: id, OwnerID: i.ID, Action: "wiki-withdraw", TargetKind: state, Reason: reason, CreatedAt: time.Now().Unix()}
	if err = wipeWikiSQL(tx, id, state, reason, op.CreatedAt); err != nil {
		return err
	}
	if err = managementEvent(tx, i.ID, "wiki-withdraw", id, map[string]int{"version": version}, map[string]string{"state": state, "reason": reason}); err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO operation_events(event_id,object_id,action,created_at) VALUES(?,?,?,?)", op.EventID, id, op.Action, op.CreatedAt); err != nil {
		return err
	}
	if err = app.appendOperationRecords(op, records); err == nil {
		err = tx.Commit()
	}
	if err != nil {
		tx.Rollback()
		app.db.Exec("UPDATE runtime_state SET mode='isolated' WHERE id=1")
		return err
	}
	return app.scrubBackupsLocked()
}

func (app *App) createWikiReply(w http.ResponseWriter, r *http.Request) {
	i := app.contentIdentity(w, r)
	if i == nil || !app.rateLimit(w, r, "wiki-reply:"+i.ID, 100) {
		return
	}
	if r.ParseForm() != nil {
		fail(w, errInput)
		return
	}
	id, parent, body := chi.URLParam(r, "wiki"), r.FormValue("parent_id"), strings.TrimSpace(r.FormValue("content"))
	if body == "" || !checkText(body, 10000) || !checkText(parent, 100) || r.FormValue("license") != "CC BY-SA 4.0" {
		fail(w, errInput)
		return
	}
	tx, err := app.collaborationTransaction(r, i, false, true)
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback()
	if !wikiReadable(tx, id) {
		fail(w, sql.ErrNoRows)
		return
	}
	if parent != "" {
		var depth int
		if err = tx.QueryRow(`WITH RECURSIVE chain(id,parent_id,depth) AS (SELECT id,parent_id,1 FROM wiki_replies WHERE id=? AND page_id=? AND status='published' UNION ALL SELECT r.id,r.parent_id,c.depth+1 FROM wiki_replies r JOIN chain c ON r.id=c.parent_id WHERE c.depth<64 AND r.status='published') SELECT COALESCE(max(depth),0) FROM chain`, parent, id).Scan(&depth); err != nil || depth == 0 || depth >= 64 {
			fail(w, errInput)
			return
		}
	}
	reply := randomID()
	_, err = tx.Exec("INSERT INTO wiki_replies VALUES(?,?,?,?,?,?,'published',?,?)", reply, id, nullable(parent), i.ID, i.Alias, body, time.Now().Unix(), time.Now().Unix())
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		fail(w, err)
		return
	}
	http.Redirect(w, r, "/wiki/"+pathID(id)+"#wiki-reply-"+pathID(reply), 303)
}

func (app *App) deleteWikiReply(w http.ResponseWriter, r *http.Request) {
	i := app.wikiRightsIdentity(w, r)
	if i == nil {
		return
	}
	page, id := chi.URLParam(r, "wiki"), chi.URLParam(r, "reply")
	app.opsMu.Lock()
	defer app.opsMu.Unlock()
	unlock, err := app.journalLock()
	if err != nil {
		fail(w, err)
		return
	}
	defer unlock()
	records, err := app.readJournal()
	if err != nil {
		fail(w, err)
		return
	}
	tx, err := app.collaborationTransaction(r, i, false, false)
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback()
	var owner string
	if !wikiReadable(tx, page) || tx.QueryRow("SELECT author_id FROM wiki_replies WHERE id=? AND page_id=? AND status='published'", id, page).Scan(&owner) != nil {
		fail(w, sql.ErrNoRows)
		return
	}
	if owner != i.ID {
		fail(w, errForbidden)
		return
	}
	op := operation{EventID: randomID(), ObjectID: id, OwnerID: i.ID, Action: "wiki-reply-delete", CreatedAt: time.Now().Unix()}
	if err = wipeWikiReplySQL(tx, id); err == nil {
		_, err = tx.Exec("INSERT INTO operation_events(event_id,object_id,action,created_at) VALUES(?,?,?,?)", op.EventID, id, op.Action, op.CreatedAt)
	}
	if err != nil {
		fail(w, err)
		return
	}
	if err = app.appendOperationRecords(op, records); err == nil {
		err = tx.Commit()
	}
	if err != nil {
		tx.Rollback()
		app.db.Exec("UPDATE runtime_state SET mode='isolated' WHERE id=1")
		fail(w, err)
		return
	}
	if err = app.scrubBackupsLocked(); err != nil {
		fail(w, err)
		return
	}
	http.Redirect(w, r, "/wiki/"+pathID(page), 303)
}

func (app *App) wikiRightsIdentity(w http.ResponseWriter, r *http.Request) *Identity {
	i, degraded, err := app.currentIdentity(r)
	if err != nil || degraded {
		respond(w, 503, map[string]string{"code": "IDENTITY_UNAVAILABLE"})
		return nil
	}
	if i == nil {
		respond(w, 401, map[string]string{"code": "SESSION_REQUIRED"})
		return nil
	}
	return i
}

func (app *App) wikiRepliesHTML(r *http.Request, page string, i *Identity) (string, error) {
	viewer := ""
	if i != nil {
		viewer = i.ID
	}
	roots, err := app.db.Query(`SELECT r.id FROM wiki_replies r JOIN users u ON u.id=r.author_id WHERE r.page_id=? AND r.parent_id IS NULL AND r.status='published' AND u.status IN ('active','deactivated','deleted') AND NOT EXISTS(SELECT 1 FROM blocks b WHERE (b.owner_id=? AND b.target_id=r.author_id) OR (b.owner_id=r.author_id AND b.target_id=?)) ORDER BY r.created_at,r.id LIMIT ? OFFSET ?`, page, viewer, viewer, pageSize+1, pageOffset(r))
	if err != nil {
		return "", err
	}
	ids := []string{}
	for roots.Next() {
		var id string
		if err = roots.Scan(&id); err != nil {
			roots.Close()
			return "", err
		}
		ids = append(ids, id)
	}
	err = roots.Err()
	roots.Close()
	if err != nil {
		return "", err
	}
	more := len(ids) > pageSize
	ids = pageItems(ids)
	holders := []string{}
	args := []any{}
	for _, id := range ids {
		holders = append(holders, "?")
		args = append(args, id)
	}
	if len(holders) == 0 {
		holders = append(holders, "NULL")
	}
	args = append(args, page)
	rows, err := app.db.Query(`WITH RECURSIVE tree(id,page_id) AS (SELECT id,page_id FROM wiki_replies WHERE id IN (`+strings.Join(holders, ",")+`) AND page_id=? AND status='published' UNION ALL SELECT r.id,r.page_id FROM wiki_replies r JOIN tree t ON r.parent_id=t.id AND r.page_id=t.page_id WHERE r.status='published') SELECT r.id,COALESCE(r.parent_id,''),r.author_id,r.author_alias,r.content,r.created_at,u.status FROM wiki_replies r JOIN users u ON u.id=r.author_id WHERE r.id IN (SELECT id FROM tree) ORDER BY r.created_at,r.id`, args...)
	if err != nil {
		return "", err
	}
	type item struct {
		id, parent, owner, alias, body, status string
		created                                int64
	}
	items := []item{}
	for rows.Next() {
		var v item
		if err = rows.Scan(&v.id, &v.parent, &v.owner, &v.alias, &v.body, &v.created, &v.status); err != nil {
			rows.Close()
			return "", err
		}
		items = append(items, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return "", err
	}
	body := `<section class="wiki-discussion"><h2>讨论</h2>`
	children := map[string][]item{}
	for _, v := range items {
		children[v.parent] = append(children[v.parent], v)
	}
	var render func(string, int) string
	render = func(parent string, depth int) string {
		if depth > 64 {
			return ""
		}
		out := ""
		for _, v := range children[parent] {
			if v.status != "active" && v.status != "deactivated" && v.status != "deleted" {
				continue
			}
			if i != nil && app.blocked(i.ID, v.owner) {
				continue
			}
			alias := v.alias
			if v.status == "deleted" || v.status == "deactivated" {
				alias += "（已注销）"
			}
			out += `<article class="wiki-reply" id="wiki-reply-` + pathID(v.id) + `"><p class="muted">` + esc(alias) + ` · ` + wikiTime(v.created) + `</p><div class="prose">` + renderProfileMarkdown(v.body) + `</div>`
			if i != nil {
				out += `<details><summary>回复</summary>` + formStart(r, "/wiki/"+pathID(page)+"/replies") + field("parent_id", v.id) + `<label class="sr-only" for="reply-` + pathID(v.id) + `">回复内容</label><textarea id="reply-` + pathID(v.id) + `" name="content" maxlength="10000" required></textarea><label class="check-row"><input type="checkbox" name="license" value="CC BY-SA 4.0" required><span>按 CC BY-SA 4.0 发布</span></label><button class="btn btn-secondary">发表回复</button></form></details>`
				if i.ID == v.owner {
					out += formStart(r, "/wiki/"+pathID(page)+"/replies/"+pathID(v.id)+"/delete") + `<button class="btn btn-secondary">删除回复及下游</button></form>`
				}
			}
			out += render(v.id, depth+1) + `</article>`
		}
		return out
	}
	body += render("", 0)
	if i != nil {
		body += formStart(r, "/wiki/"+pathID(page)+"/replies") + `<label>发表讨论<textarea name="content" maxlength="10000" required></textarea></label><label class="check-row"><input type="checkbox" name="license" value="CC BY-SA 4.0" required><span>按 CC BY-SA 4.0 发布</span></label><button class="btn btn-primary">发表讨论</button></form>`
	} else {
		body += `<p><a href="/login">登录后参与讨论</a></p>`
	}
	return body + pagination(r, more) + `</section>`, nil
}
