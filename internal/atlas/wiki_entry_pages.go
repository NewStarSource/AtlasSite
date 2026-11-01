package atlas

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

func wikiReviewState(state string) string {
	return map[string]string{"pending": "待人工审核", "accepted": "已发布", "rejected": "已退回", "withdrawn": "已撤回或限制"}[state]
}

func (app *App) wikiEntryEditor(w http.ResponseWriter, r *http.Request) {
	i := app.requireIdentity(w, r, false)
	if i == nil {
		return
	}
	id := chi.URLParam(r, "wiki")
	creating := id == ""
	v := wikiVersion{PageID: randomID(), Kind: r.URL.Query().Get("kind"), Fields: `{"status":"unknown"}`}
	if creating {
		if v.Kind == "" {
			v.Kind = "community"
		}
		if !formalWiki(v.Kind) {
			fail(w, errInput)
			return
		}
		if v.Kind == "work" {
			v.Fields = `{"status":"public"}`
		}
	} else {
		var err error
		submission := r.URL.Query().Get("submission")
		if submission != "" {
			n, e := strconv.Atoi(submission)
			if e != nil || n < 1 {
				fail(w, sql.ErrNoRows)
				return
			}
			v, err = loadWikiVersion(app.db, id, n)
			if err == nil && (v.Editor != i.ID || !submissionVisible(app.db, v, i.ID) || (v.Review != "pending" && v.Review != "rejected")) {
				err = sql.ErrNoRows
			}
		} else if wikiReadable(app.db, id) {
			v, err = loadWikiVersion(app.db, id, 0)
		} else {
			var n int
			err = app.db.QueryRow("SELECT latest_version FROM wiki_pages WHERE id=? AND creator_id=? AND state='active' AND published_version=0", id, i.ID).Scan(&n)
			if err == nil {
				v, err = loadWikiVersion(app.db, id, n)
			}
		}
		if err != nil {
			fail(w, err)
			return
		}
		if !formalWiki(v.Kind) {
			fail(w, errForbidden)
			return
		}
		if submission == "" {
			v.Base = v.Published
		}
	}
	note := ""
	if !creating && v.Base != v.Published {
		note = "这份原稿基于较旧的公开版本。请保留输入并载入最新版本，核对差异后再提交。"
	}
	app.renderWikiEntryEditor(w, r, i, v, creating, 200, note)
}

func (app *App) wikiEntryError(w http.ResponseWriter, r *http.Request, i *Identity, v wikiVersion, creating bool, err error) {
	if (err != errInput && err != errConflict) || !formalWiki(v.Kind) || strings.Contains(r.Header.Get("Accept"), "application/json") {
		fail(w, err)
		return
	}
	status, note := 400, "请检查必填字段、公开来源、正文脚注和许可确认。"
	if err == errConflict {
		status, note = 409, "已有新提交或公开版本，本次输入尚未保存。请保留输入，再载入最新版本。"
	}
	app.renderWikiEntryEditor(w, r, i, v, creating, status, note)
}

func (app *App) renderWikiEntryEditor(w http.ResponseWriter, r *http.Request, i *Identity, v wikiVersion, creating bool, status int, note string) {
	community, err := app.wikiCommunitySelect(v.Community)
	if err != nil {
		fail(w, err)
		return
	}
	path := "/wiki/entries"
	if !creating {
		path = "/wiki/" + pathID(v.PageID) + "/propose"
	}
	body := wikiNav() + `<section class="admin-editor"><h1>提交` + wikiKind(v.Kind) + `</h1><p class="muted">人工审核后发布 · 内部原型</p>`
	if creating {
		body += `<nav class="action-row"><a href="/wiki/entries/new?kind=community">社群</a><a href="/wiki/entries/new?kind=work">已公开作品</a></nav>`
	}
	if note != "" {
		body += `<p role="alert" class="notice">` + esc(note) + `</p>`
		if !creating {
			body += `<a href="/wiki/` + pathID(v.PageID) + `/propose">载入最新版本</a>`
		}
	}
	body += formStart(r, path) + field("page_id", v.PageID) + field("kind", v.Kind) + field("version", strconv.Itoa(v.Latest)) + field("base", strconv.Itoa(v.Base)) + `<label>名称<input name="title" value="` + esc(v.Title) + `" maxlength="200" required></label><label>一句话说明<input name="summary" value="` + esc(v.Summary) + `" maxlength="500" required></label>`
	f := wikiFields(v)
	if v.Kind == "community" {
		body += `<div class="metadata-grid"><label>社群状态<select name="status_value">`
		for _, state := range []string{"active", "inactive", "migrated", "unknown"} {
			selected := ""
			if state == f.Status {
				selected = " selected"
			}
			body += `<option value="` + state + `"` + selected + `>` + wikiStatusLabel(state) + `</option>`
		}
		body += `</select></label><label>首次确认存在的日期<input type="date" name="first_verified_at" value="` + esc(f.FirstVerified) + `" required></label></div>`
	} else {
		body += field("status_value", "public") + `<label>作品类型<input name="work_type" value="` + esc(f.WorkType) + `" maxlength="100" required></label><label>创作者公开署名<input name="creator" value="` + esc(f.Creator) + `" maxlength="200"></label><label>公开作品入口<input type="url" name="public_url" value="` + esc(f.PublicURL) + `" maxlength="1000"></label>`
	}
	raw := wikiSourcesText(v.Sources)
	if r.Method == "POST" {
		raw = r.FormValue("sources")
	}
	body += `<label>正文 <small>Markdown</small><textarea name="body" rows="14" maxlength="30000" required placeholder="可核查事实请使用 [^1] 引用来源。">` + esc(v.Body) + `</textarea></label><label>公开来源<textarea name="sources" rows="5" maxlength="20000" required placeholder="1 | 来源标题 | https://example.com | 2026-11-01">` + esc(raw) + `</textarea></label><details class="field-help"><summary>填写说明</summary><p>来源格式为“编号 | 标题 | 公开链接 | 访问日期”，正文用 [^编号] 引用，最多 20 项。仅收录公开、可核查的社群与已公开作品，不提交人物争议、私下证明或未公开材料。</p></details><label>关联社群` + community + `</label><label>修改说明<input name="edit_summary" value="` + esc(v.EditSummary) + `" maxlength="300" required></label><label class="check-row"><input type="checkbox" name="public_scope" value="public" required><span>仅包含本阶段允许的公开资料</span></label><label class="check-row"><input type="checkbox" name="license" value="CC BY-SA 4.0" required><span>按 CC BY-SA 4.0 提交，有权使用引用材料</span></label><button class="btn btn-primary">提交人工审核</button></form></section>`
	app.renderPageStatus(w, r, status, "提交条目", "wiki", body, "")
}

func wikiStatusLabel(status string) string {
	return map[string]string{"active": "活跃", "inactive": "已不活跃", "migrated": "已迁移", "unknown": "状态未知", "public": "已公开"}[status]
}
func wikiFieldsHTML(v wikiVersion) string {
	if !formalWiki(v.Kind) {
		return ""
	}
	f := wikiFields(v)
	body := `<dl class="wiki-fields"><dt>状态</dt><dd>` + wikiStatusLabel(f.Status) + `</dd>`
	if v.Kind == "community" {
		body += `<dt>首次确认存在</dt><dd>` + esc(f.FirstVerified) + `</dd>`
	} else {
		body += `<dt>作品类型</dt><dd>` + esc(f.WorkType) + `</dd>`
		if f.Creator != "" {
			body += `<dt>公开署名</dt><dd>` + esc(f.Creator) + `</dd>`
		}
		if safeSource(f.PublicURL) {
			body += `<dt>公开入口</dt><dd><a rel="noopener noreferrer" href="` + esc(f.PublicURL) + `">查看作品</a></dd>`
		}
	}
	return body + `</dl>`
}

func (app *App) myWikiSubmissions(w http.ResponseWriter, r *http.Request) {
	i := app.requireIdentity(w, r, false)
	if i == nil {
		return
	}
	rows, err := app.db.Query(`SELECT v.page_id,v.number,v.title,v.review_status,v.created_at FROM wiki_versions v JOIN wiki_pages p ON p.id=v.page_id WHERE v.editor_id=? AND p.kind IN ('community','work') AND p.state='active' AND `+wikiCommunityVisibleSQL+` AND (p.published_version=0 OR EXISTS(SELECT 1 FROM wiki_versions cv WHERE cv.page_id=p.id AND cv.number=p.published_version AND (cv.community_id IS NULL OR EXISTS(SELECT 1 FROM communities cc WHERE cc.id=cv.community_id AND cc.status IN ('active','uncategorized'))))) ORDER BY v.created_at DESC,v.number DESC LIMIT ? OFFSET ?`, i.ID, pageSize+1, pageOffset(r))
	if err != nil {
		fail(w, err)
		return
	}
	body := wikiNav() + `<h1>我的条目提交</h1>`
	n := 0
	for rows.Next() {
		var id, title, state string
		var number int
		var created int64
		if err = rows.Scan(&id, &number, &title, &state, &created); err != nil {
			rows.Close()
			fail(w, err)
			return
		}
		n++
		if n > pageSize {
			break
		}
		if title == "" {
			title = "已撤回或受限的提交"
		}
		body += `<article class="admin-row"><h2><a href="/wiki/` + pathID(id) + `/submissions/` + strconv.Itoa(number) + `">` + esc(title) + `</a></h2><small>` + wikiReviewState(state) + ` · 版本 ` + strconv.Itoa(number) + ` · ` + wikiTime(created) + `</small></article>`
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		fail(w, err)
		return
	}
	if n == 0 {
		body += `<p class="admin-empty">暂无条目提交</p>`
	}
	app.renderPage(w, r, "我的条目提交", "wiki", body+pagination(r, n > pageSize))
}

func (app *App) wikiSubmissionPage(w http.ResponseWriter, r *http.Request) {
	i := app.requireIdentity(w, r, false)
	if i == nil {
		return
	}
	id := chi.URLParam(r, "wiki")
	number, err := strconv.Atoi(chi.URLParam(r, "number"))
	if err != nil || number < 1 {
		fail(w, sql.ErrNoRows)
		return
	}
	v, err := loadWikiVersion(app.db, id, number)
	if err != nil || !submissionVisible(app.db, v, i.ID) {
		fail(w, sql.ErrNoRows)
		return
	}
	body := wikiNav() + `<h1>提交记录 · ` + wikiReviewState(v.Review) + `</h1>`
	if v.Review != "withdrawn" {
		body += app.wikiArticle(v, v.Review == "accepted")
	}
	if v.ReviewReason != "" {
		body += `<p class="notice">处理说明 · ` + esc(v.ReviewReason) + `</p>`
	}
	if v.Base > 0 && wikiVersionReadable(app.db, id, v.Base) && v.Review != "withdrawn" {
		base, e := loadWikiVersion(app.db, id, v.Base)
		if e == nil {
			body += `<details><summary>与提交时公开版本的差异</summary>` + wikiDiffHTML(wikiVersionText(base), wikiVersionText(v)) + `</details>`
		}
	}
	if v.Editor == i.ID && (v.Review == "pending" || v.Review == "rejected") {
		body += `<div class="action-row"><a href="/wiki/` + pathID(id) + `/propose?submission=` + strconv.Itoa(number) + `">重新提交</a></div>` + formStart(r, "/wiki/"+pathID(id)+"/submissions/"+strconv.Itoa(number)+"/withdraw") + `<button class="btn btn-secondary">撤回这次提交</button></form>`
	}
	if v.Review == "pending" && wikiReviewer(app.db, i.ID) {
		disabled := ""
		if v.Base != v.Published {
			disabled = " disabled"
			body += `<p class="notice">公开版本已变化，不能直接批准这次提交，请附理由退回。</p>`
		}
		body += `<section class="admin-editor"><h2>人工审核</h2>` + formStart(r, "/admin/wiki/"+pathID(id)+"/versions/"+strconv.Itoa(number)+"/review") + `<label>处理结果<select name="decision"><option value="accept"` + disabled + `>审核通过并发布</option><option value="reject">附理由退回</option></select></label><label>处理理由（通过时公开）<textarea name="note" maxlength="1000" required></textarea></label><details class="field-help"><summary>风险内容处理</summary><label>限制原提交<select name="risk"><option value="">无需清理</option><option value="privacy">隐私风险</option><option value="copyright">版权风险</option><option value="safety">安全风险</option></select></label><p>风险限制须选择退回，会清理提交正文及旧普通备份，仅保留必要处理结论。</p></details><label class="check-row"><input type="checkbox" name="confirm" value="reviewed" required><span>已人工核对内容、来源和本阶段范围</span></label><button class="btn btn-primary">保存审核决定</button></form></section>`
	}
	app.renderPage(w, r, "条目提交记录", "wiki", body)
}

func (app *App) wikiReviewQueue(w http.ResponseWriter, r *http.Request) {
	i, role := app.managementIdentity(w, r, false)
	if i == nil {
		return
	}
	if role != "admin" && role != "member" {
		fail(w, errForbidden)
		return
	}
	rows, err := app.db.Query(`SELECT v.page_id,v.number,v.title,v.editor_alias,v.created_at,v.base_version,p.published_version FROM wiki_versions v JOIN wiki_pages p ON p.id=v.page_id WHERE p.state='active' AND v.review_status='pending' AND `+wikiCommunityVisibleSQL+` AND (p.published_version=0 OR EXISTS(SELECT 1 FROM wiki_versions cv WHERE cv.page_id=p.id AND cv.number=p.published_version AND (cv.community_id IS NULL OR EXISTS(SELECT 1 FROM communities cc WHERE cc.id=cv.community_id AND cc.status IN ('active','uncategorized'))))) ORDER BY v.created_at,v.page_id,v.number LIMIT ? OFFSET ?`, pageSize+1, pageOffset(r))
	if err != nil {
		fail(w, err)
		return
	}
	body := managementHeading(r, i, role) + wikiNav() + `<p><a href="/admin/wiki/restricted">受限页面处理</a></p><h2>条目人工审核</h2>`
	n := 0
	for rows.Next() {
		var id, title, alias string
		var number, base, published int
		var created int64
		if err = rows.Scan(&id, &number, &title, &alias, &created, &base, &published); err != nil {
			rows.Close()
			fail(w, err)
			return
		}
		n++
		if n > pageSize {
			break
		}
		state := "待审核"
		if base != published {
			state = "基于较旧版本，需重提"
		}
		body += `<article class="admin-row"><h3><a href="/wiki/` + pathID(id) + `/submissions/` + strconv.Itoa(number) + `">` + esc(title) + `</a></h3><p class="muted">` + esc(alias) + ` · ` + wikiTime(created) + ` · ` + state + `</p></article>`
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		fail(w, err)
		return
	}
	if n == 0 {
		body += `<p class="admin-empty">没有待审核条目</p>`
	}
	suggestions, err := app.wikiSuggestionQueue(r)
	if err != nil {
		fail(w, err)
		return
	}
	app.renderPage(w, r, "Wiki 人工审核", "admin", body+pagination(r, n > pageSize)+suggestions)
}

func wikiFieldsText(v wikiVersion) string {
	if !formalWiki(v.Kind) {
		return ""
	}
	f := wikiFields(v)
	if v.Kind == "community" {
		return "状态 " + wikiStatusLabel(f.Status) + "\n首次确认存在 " + f.FirstVerified
	}
	return "状态 " + wikiStatusLabel(f.Status) + "\n作品类型 " + f.WorkType + "\n公开署名 " + f.Creator + "\n公开入口 " + f.PublicURL
}

func (app *App) wikiRestrictedPages(w http.ResponseWriter, r *http.Request) {
	i, role := app.managementIdentity(w, r, false)
	if i == nil {
		return
	}
	if role != "admin" && role != "member" {
		fail(w, errForbidden)
		return
	}
	rows, err := app.db.Query("SELECT id,kind,latest_version,updated_at FROM wiki_pages WHERE state='hidden' ORDER BY updated_at DESC,id LIMIT ? OFFSET ?", pageSize+1, pageOffset(r))
	if err != nil {
		fail(w, err)
		return
	}
	body := managementHeading(r, i, role) + wikiNav() + `<h2>受限页面处理</h2><p class="muted">仅列出对象编号。恢复后的旧内容不会在此展示，复核后可清理或删除。</p>`
	n := 0
	for rows.Next() {
		var id, kind string
		var version int
		var at int64
		if err = rows.Scan(&id, &kind, &version, &at); err != nil {
			rows.Close()
			fail(w, err)
			return
		}
		n++
		if n > pageSize {
			break
		}
		body += `<article class="admin-row"><h3>` + wikiKind(kind) + `</h3><p class="muted">` + esc(id) + ` · ` + wikiTime(at) + `</p><details class="admin-edit"><summary>清理受限内容</summary>` + formStart(r, "/wiki/"+pathID(id)+"/withdraw") + field("version", strconv.Itoa(version)) + `<label>结果<select name="state"><option value="hidden">清理后保持隐藏</option><option value="deleted">删除并保留空页面</option></select></label><label>原因<select name="reason"><option value="privacy">隐私风险</option><option value="copyright">版权风险</option><option value="safety">安全风险</option></select></label><label class="check-row"><input type="checkbox" name="confirm" value="withdraw" required><span>清理正文、来源、建议和讨论，旧普通备份同步清理</span></label><button class="btn btn-secondary">确认清理</button></form></details></article>`
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		fail(w, err)
		return
	}
	if n == 0 {
		body += `<p class="admin-empty">暂无受限页面</p>`
	}
	app.renderPage(w, r, "Wiki 受限页面", "admin", body+pagination(r, n > pageSize))
}
