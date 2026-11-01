package atlas

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

func wikiNav() string {
	return `<nav class="action-row"><a href="/wiki">Wiki</a><a href="/wiki/new">新建发现页</a><a href="/wiki/entries/new">提交条目</a><a href="/my/wiki">我的提交</a></nav>`
}

func (app *App) wikiList(r *http.Request, community string) (string, error) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if !checkText(q, 100) {
		return "", errInput
	}
	query := `SELECT p.id,p.kind,v.title,p.updated_at FROM wiki_pages p JOIN wiki_versions v ON v.page_id=p.id AND v.number=p.published_version WHERE p.state='active' AND v.review_status='accepted' AND ` + wikiCommunityVisibleSQL
	args := []any{}
	if community != "" {
		query += " AND v.community_id=?"
		args = append(args, community)
	} else if q == "" {
		query += " AND (p.kind<>'discovery' OR v.community_id IS NOT NULL)"
	}
	if q != "" {
		query += " AND (instr(lower(v.title),lower(?))>0 OR instr(lower(v.summary),lower(?))>0 OR instr(lower(v.body),lower(?))>0 OR instr(lower(v.sources_json),lower(?))>0)"
		args = append(args, q, q, q, q)
	}
	if q != "" {
		query += " ORDER BY CASE WHEN lower(v.title)=lower(?) THEN 0 WHEN instr(lower(v.title),lower(?))>0 THEN 1 WHEN instr(lower(v.summary),lower(?))>0 THEN 2 ELSE 3 END,p.updated_at DESC,p.id"
		args = append(args, q, q, q)
	} else {
		query += " ORDER BY p.updated_at DESC,p.id"
	}
	query += " LIMIT ? OFFSET ?"
	args = append(args, pageSize+1, pageOffset(r))
	rows, err := app.db.Query(query, args...)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	body := `<div class="wiki-list">`
	n := 0
	for rows.Next() {
		var id, kind, title string
		var updated int64
		if err = rows.Scan(&id, &kind, &title, &updated); err != nil {
			return "", err
		}
		n++
		if n > pageSize {
			break
		}
		body += `<article class="wiki-card"><a href="/wiki/` + pathID(id) + `"><h2>` + esc(title) + `</h2></a><small class="muted">` + wikiKind(kind) + ` · ` + wikiTime(updated) + `</small></article>`
	}
	if err = rows.Err(); err != nil {
		return "", err
	}
	if n == 0 {
		body += `<p class="admin-empty">暂无匹配页面</p>`
	}
	return body + `</div>` + pagination(r, n > pageSize), nil
}

func (app *App) wikiIndex(w http.ResponseWriter, r *http.Request) {
	list, err := app.wikiList(r, "")
	if err != nil {
		fail(w, err)
		return
	}
	body := wikiNav() + `<header class="page-heading"><h1>Wiki</h1><span class="admin-badge">内部原型</span></header><form method="get" class="admin-search"><label for="wiki-query" class="sr-only">搜索 Wiki</label><input id="wiki-query" type="search" name="q" maxlength="100" value="` + esc(r.URL.Query().Get("q")) + `" placeholder="搜索条目或发现页…"><button class="tool-icon" aria-label="搜索 Wiki">` + uiIcon("search") + `</button></form>`
	i, _, _ := app.currentIdentity(r)
	if i != nil && wikiReviewer(app.db, i.ID) {
		body += `<p><a href="/admin/wiki">Wiki 人工审核</a></p>`
	}
	if r.URL.Query().Get("q") == "" {
		body += `<p class="muted">未关联社群的发现页可通过搜索查找。</p>`
	}
	app.renderPage(w, r, "Wiki", "wiki", body+list)
}

func (app *App) communityWikiPage(w http.ResponseWriter, r *http.Request) {
	c, err := app.readableCommunity(r)
	if err != nil {
		fail(w, err)
		return
	}
	list, err := app.wikiList(r, c.ID)
	if err != nil {
		fail(w, err)
		return
	}
	app.renderPage(w, r, c.Name+" · Wiki", "community", wikiNav()+`<nav class="breadcrumb"><a href="/c/`+pathID(c.Slug)+`">`+esc(c.Name)+`</a><span>/</span><span>Wiki 与发现页</span></nav>`+list)
}

func (app *App) wikiAttribution(v wikiVersion) string {
	var status string
	app.db.QueryRow("SELECT status FROM users WHERE id=?", v.Editor).Scan(&status)
	alias := v.Alias
	if status == "deleted" || status == "deactivated" {
		alias += "（已注销）"
	}
	return `<p class="muted">` + esc(alias) + ` · ` + wikiTime(v.Created) + ` · 版本 ` + strconv.Itoa(v.Number) + `</p>`
}

func (app *App) wikiArticle(v wikiVersion, historical bool) string {
	label := wikiKind(v.Kind) + " · 待核实 · 内部原型"
	if formalWiki(v.Kind) {
		label = wikiKind(v.Kind) + " · " + wikiReviewState(v.Review) + " · 内部原型"
	}
	if historical {
		label += " · 历史版本"
	}
	body := `<article class="wiki-article"><span class="admin-badge">` + label + `</span><h1>` + esc(v.Title) + `</h1>` + app.wikiAttribution(v)
	for _, s := range v.Sources {
		if s.Expired {
			body += `<p class="notice">部分来源已失效，相关事实待重新核验。</p>`
			break
		}
	}
	if v.Summary != "" {
		body += `<p class="wiki-summary">` + esc(v.Summary) + `</p>`
	}
	if formalWiki(v.Kind) && v.Review == "accepted" {
		body += `<p class="muted">人工审核发布 · ` + wikiTime(v.Reviewed) + `</p>`
	}
	return body + wikiFieldsHTML(v) + `<div class="prose">` + wikiBody(v) + `</div>` + wikiSourcesHTML(v.Sources) + `<p class="muted">CC BY-SA 4.0 · 外部引用材料保留原许可边界</p></article>`
}

func (app *App) wikiPage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "wiki")
	var withdrawn int64
	var reason string
	if app.db.QueryRow("SELECT withdrawn_at,reason FROM wiki_pages WHERE id=? AND state='deleted'", id).Scan(&withdrawn, &reason) == nil {
		app.renderPage(w, r, "页面已删除", "wiki", wikiNav()+`<h1>页面已删除</h1><p>`+wikiReason(reason)+` · `+wikiTime(withdrawn)+`</p>`)
		return
	}
	if !wikiReadable(app.db, id) {
		fail(w, sql.ErrNoRows)
		return
	}
	v, err := loadWikiVersion(app.db, id, 0)
	if err != nil {
		fail(w, err)
		return
	}
	body := wikiNav()
	if formalWiki(v.Kind) {
		var pending int
		if app.db.QueryRow("SELECT count(*) FROM wiki_versions WHERE page_id=? AND review_status='pending'", id).Scan(&pending) == nil && pending > 0 {
			body += `<p class="notice">有修订待人工审核，当前仍显示已发布版本。</p>`
		}
	}
	body += app.wikiArticle(v, false)
	editPath, editLabel := "/edit", "编辑"
	if formalWiki(v.Kind) {
		editPath, editLabel = "/propose", "提交修订"
	}
	body += `<div class="action-row"><a href="/wiki/` + pathID(id) + editPath + `">` + editLabel + `</a><a href="/wiki/` + pathID(id) + `/history">版本记录</a></div>`
	i, _, _ := app.currentIdentity(r)
	if i != nil {
		role, _ := managementAccess(app.db, i.ID)
		if (i.ID == v.Creator && v.Kind == "discovery") || role == "admin" || role == "member" {
			body += `<details class="field-help"><summary>页面管理</summary>` + formStart(r, "/wiki/"+pathID(id)+"/withdraw") + field("version", strconv.Itoa(v.Latest)) + `<label>原因<select name="reason"><option value="nonexistent">对象不存在</option><option value="duplicate">重复页面</option><option value="privacy">隐私请求</option><option value="copyright">版权请求</option><option value="safety">安全限制</option></select></label><label>处理方式<select name="state"><option value="deleted">删除页面</option>`
			if role == "admin" || role == "member" {
				body += `<option value="hidden">暂时隐藏</option>`
			}
			body += `</select></label><label class="check-row"><input type="checkbox" name="confirm" value="withdraw" required><span>停止展示正文、历史和讨论</span></label><button class="btn btn-secondary">确认处理</button></form><p class="muted">团队操作前需<a href="/security">验证身份</a>。受限副本最多保留 30 天，不提供自动恢复。</p></details>`
		}
	}
	replies, err := app.wikiRepliesHTML(r, id, i)
	if err != nil {
		fail(w, err)
		return
	}
	suggestions, err := app.wikiSuggestionsHTML(r, v, i)
	if err != nil {
		fail(w, err)
		return
	}
	app.renderPage(w, r, v.Title, "wiki", body+suggestions+replies)
}

func (app *App) wikiCommunitySelect(selected string) (string, error) {
	rows, err := app.db.Query("SELECT id,name FROM communities WHERE status IN ('active','uncategorized') ORDER BY name,id LIMIT 500")
	if err != nil {
		return "", err
	}
	defer rows.Close()
	body := `<select name="community"><option value="">不关联社群</option>`
	for rows.Next() {
		var id, name string
		if err = rows.Scan(&id, &name); err != nil {
			return "", err
		}
		s := ""
		if id == selected {
			s = " selected"
		}
		body += `<option value="` + esc(id) + `"` + s + `>` + esc(name) + `</option>`
	}
	return body + `</select>`, rows.Err()
}

func (app *App) wikiEditor(w http.ResponseWriter, r *http.Request) {
	i := app.requireIdentity(w, r, false)
	if i == nil {
		return
	}
	id := chi.URLParam(r, "wiki")
	v := wikiVersion{PageID: randomID(), Kind: "discovery", Creator: i.ID}
	if id != "" {
		if !wikiReadable(app.db, id) {
			fail(w, sql.ErrNoRows)
			return
		}
		var err error
		v, err = loadWikiVersion(app.db, id, 0)
		if err != nil {
			fail(w, err)
			return
		}
		if v.Kind != "discovery" {
			http.Redirect(w, r, "/wiki/"+pathID(id)+"/propose", 303)
			return
		}
	}
	app.renderWikiEditor(w, r, i, v, id == "", 200, "")
}

func (app *App) wikiEditorError(w http.ResponseWriter, r *http.Request, i *Identity, v wikiVersion, creating bool, err error) {
	if (err != errInput && err != errConflict) || strings.Contains(r.Header.Get("Accept"), "application/json") {
		fail(w, err)
		return
	}
	status, message := 400, "请检查必填内容、来源格式和脚注；正文中的脚注须能对应下方来源编号。"
	if err == errConflict {
		status, message = 409, "页面已有较新版本，本次输入尚未保存。请先保留输入，再载入最新版本。"
	}
	if !creating && wikiReadable(app.db, v.PageID) {
		old, e := loadWikiVersion(app.db, v.PageID, 0)
		if e == nil {
			v.Creator = old.Creator
		}
	}
	app.renderWikiEditor(w, r, i, v, creating, status, message)
}

func (app *App) renderWikiEditor(w http.ResponseWriter, r *http.Request, i *Identity, v wikiVersion, creating bool, status int, message string) {
	communities, err := app.wikiCommunitySelect(v.Community)
	if err != nil {
		fail(w, err)
		return
	}
	action := "/wiki"
	if !creating {
		action += "/" + pathID(v.PageID) + "/edit"
	}
	body := wikiNav() + `<section class="admin-editor"><h1>编辑发现页</h1>`
	if message != "" {
		body += `<p class="notice" role="alert">` + esc(message) + `</p>`
		if !creating {
			body += `<a href="` + action + `">载入最新版本</a>`
		}
	}
	readonly := ""
	if !creating && v.Creator != i.ID {
		readonly = " readonly"
	}
	raw := wikiSourcesText(v.Sources)
	if r.Method == "POST" {
		raw = r.FormValue("sources")
	}
	body += formStart(r, action) + field("page_id", v.PageID) + field("version", strconv.Itoa(v.Latest)) + `<label>标题<input name="title" value="` + esc(v.Title) + `" maxlength="200" required` + readonly + `></label><label>简短说明<input name="summary" value="` + esc(v.Summary) + `" maxlength="500"></label><label>正文 <small>Markdown</small><textarea name="body" rows="14" maxlength="30000" required placeholder="记录线索，使用 [^1] 引用来源。">` + esc(v.Body) + `</textarea></label><label>公开来源<textarea name="sources" rows="5" maxlength="20000" required placeholder="1 | 来源标题 | https://example.com | ` + time.Now().Format("2006-01-02") + `">` + esc(raw) + `</textarea></label><details class="field-help"><summary>来源和协作说明</summary><p>每行填写“编号 | 标题 | 链接 | 访问日期”，最多 20 项。正文用 [^编号] 引用，脚注由系统生成。失效来源可在行末添加 | expired。</p><p>本原型只记录公开、可核查的资料，发现页内容仍待核实。暂不承接人物争议、未公开作品、私下证明和图片上传。重命名由创建者办理。</p></details><label>关联社群` + communities + `</label><label>修改说明<input name="edit_summary" value="` + esc(v.EditSummary) + `" maxlength="300" required></label><label class="check-row"><input type="checkbox" name="license" value="CC BY-SA 4.0" required><span>按 CC BY-SA 4.0 发布，有权使用引用材料</span></label><button class="btn btn-primary">保存发现页</button></form></section>`
	app.renderPageStatus(w, r, status, "编辑发现页", "wiki", body, "")
}

func (app *App) wikiHistory(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "wiki")
	if !wikiReadable(app.db, id) {
		fail(w, sql.ErrNoRows)
		return
	}
	rows, err := app.db.Query(`SELECT v.number,v.editor_alias,v.edit_summary,v.created_at,u.status,COALESCE(v.reviewed_at,0),v.review_reason,COALESCE((SELECT max(pv.number) FROM wiki_versions pv WHERE pv.page_id=v.page_id AND pv.number<v.number AND pv.review_status='accepted' AND (pv.community_id IS NULL OR EXISTS(SELECT 1 FROM communities pc WHERE pc.id=pv.community_id AND pc.status IN ('active','uncategorized')))),0) FROM wiki_versions v JOIN users u ON u.id=v.editor_id WHERE v.page_id=? AND v.review_status='accepted' AND `+wikiCommunityVisibleSQL+` ORDER BY v.number DESC LIMIT ? OFFSET ?`, id, pageSize+1, pageOffset(r))
	if err != nil {
		fail(w, err)
		return
	}
	body := wikiNav() + `<h1>版本记录</h1><p><a href="/wiki/` + pathID(id) + `">返回当前页面</a></p>`
	n := 0
	for rows.Next() {
		var version, previous int
		var alias, summary, status, note string
		var created, reviewed int64
		if err = rows.Scan(&version, &alias, &summary, &created, &status, &reviewed, &note, &previous); err != nil {
			rows.Close()
			fail(w, err)
			return
		}
		n++
		if n > pageSize {
			break
		}
		if status == "deleted" || status == "deactivated" {
			alias += "（已注销）"
		}
		body += `<article class="admin-row"><h2><a href="/wiki/` + pathID(id) + `/versions/` + strconv.Itoa(version) + `">版本 ` + strconv.Itoa(version) + `</a></h2><p>` + esc(summary) + `</p><small class="muted">` + esc(alias) + ` · ` + wikiTime(created) + `</small>`
		if reviewed > 0 {
			body += `<p class="muted">人工审核 · ` + wikiTime(reviewed) + ` · ` + esc(note) + `</p>`
		}
		if previous > 0 {
			body += `<p><a href="/wiki/` + pathID(id) + `/diff?from=` + strconv.Itoa(previous) + `&amp;to=` + strconv.Itoa(version) + `">查看差异</a></p>`
		}
		body += `</article>`
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		fail(w, err)
		return
	}
	app.renderPage(w, r, "Wiki 版本记录", "wiki", body+pagination(r, n > pageSize))
}

func (app *App) wikiHistoricalPage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "wiki")
	number, err := strconv.Atoi(chi.URLParam(r, "number"))
	if err != nil || number < 1 || !wikiVersionReadable(app.db, id, number) {
		fail(w, sql.ErrNoRows)
		return
	}
	v, err := loadWikiVersion(app.db, id, number)
	if err != nil {
		fail(w, err)
		return
	}
	app.renderPage(w, r, v.Title+" · 历史", "wiki", wikiNav()+`<p><a href="/wiki/`+pathID(id)+`">返回当前页面</a></p>`+app.wikiArticle(v, true))
}

func wikiDiffHTML(a, b string) string {
	old, next := strings.Split(a, "\n"), strings.Split(b, "\n")
	prefix, suffix := 0, 0
	for prefix < len(old) && prefix < len(next) && old[prefix] == next[prefix] {
		prefix++
	}
	for suffix < len(old)-prefix && suffix < len(next)-prefix && old[len(old)-1-suffix] == next[len(next)-1-suffix] {
		suffix++
	}
	body := `<pre class="wiki-diff">` + esc(strings.Join(old[:prefix], "\n")) + "\n"
	if prefix < len(old)-suffix {
		body += `<del>` + esc(strings.Join(old[prefix:len(old)-suffix], "\n")) + `</del>` + "\n"
	}
	if prefix < len(next)-suffix {
		body += `<ins>` + esc(strings.Join(next[prefix:len(next)-suffix], "\n")) + `</ins>` + "\n"
	}
	return body + esc(strings.Join(old[len(old)-suffix:], "\n")) + `</pre>`
}

func (app *App) wikiDiff(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "wiki")
	from, e1 := strconv.Atoi(r.URL.Query().Get("from"))
	to, e2 := strconv.Atoi(r.URL.Query().Get("to"))
	if e1 != nil || e2 != nil || from < 1 || to < 1 || !wikiVersionReadable(app.db, id, from) || !wikiVersionReadable(app.db, id, to) {
		fail(w, sql.ErrNoRows)
		return
	}
	a, err := loadWikiVersion(app.db, id, from)
	if err != nil {
		fail(w, err)
		return
	}
	b, err := loadWikiVersion(app.db, id, to)
	if err != nil {
		fail(w, err)
		return
	}
	app.renderPage(w, r, "Wiki 版本差异", "wiki", wikiNav()+`<h1>版本 `+strconv.Itoa(from)+` → `+strconv.Itoa(to)+`</h1><p><a href="/wiki/`+pathID(id)+`/history">返回版本记录</a></p>`+wikiDiffHTML(wikiVersionText(a), wikiVersionText(b)))
}
