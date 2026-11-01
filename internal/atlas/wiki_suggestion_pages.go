package atlas

import (
	"database/sql"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

func (app *App) wikiSuggestionsHTML(r *http.Request, v wikiVersion, i *Identity) (string, error) {
	if !formalWiki(v.Kind) {
		return "", nil
	}
	query := `SELECT s.id FROM wiki_suggestions s JOIN wiki_versions bv ON bv.page_id=s.page_id AND bv.number=s.base_version WHERE s.page_id=? AND bv.review_status='accepted' AND (bv.community_id IS NULL OR EXISTS(SELECT 1 FROM communities c WHERE c.id=bv.community_id AND c.status IN ('active','uncategorized')))`
	args := []any{v.PageID}
	selected := r.URL.Query().Get("suggestion")
	if selected != "" {
		query += " AND s.id=?"
		args = append(args, selected)
	}
	query += " ORDER BY s.created_at DESC,s.id LIMIT ? OFFSET ?"
	offset := pageNumberFor(r, "suggestion_page") * pageSize
	if selected != "" {
		offset = 0
	}
	args = append(args, pageSize+1, offset)
	rows, err := app.db.Query(query, args...)
	if err != nil {
		return "", err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return "", err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return "", err
	}
	body := `<section class="wiki-suggestions"><h2>建议修改</h2>`
	for _, id := range pageItems(ids) {
		g, e := loadWikiSuggestion(app.db, id)
		if e != nil {
			return "", e
		}
		alias := g.Alias
		var status string
		app.db.QueryRow("SELECT status FROM users WHERE id=?", g.Author).Scan(&status)
		if status == "deactivated" || status == "deleted" {
			alias += "（已注销）"
		}
		state := suggestionState(g.State)
		if g.State == "pending" && time.Now().Unix()-g.Created > 7*86400 {
			state += " · 超过 7 天，人工队列优先处理"
		}
		body += `<article class="admin-row" id="suggestion-` + pathID(id) + `"><div class="admin-row-heading"><h3>` + suggestionTarget(g.Target) + `</h3><span class="admin-badge">` + state + `</span></div><p class="muted">` + esc(alias) + ` · ` + wikiTime(g.Created) + ` · 基于版本 ` + strconv.Itoa(g.Base) + `</p>`
		if g.State != "withdrawn" && g.Proposed != "" {
			body += `<details><summary>建议内容与依据</summary>`
			if g.Old != "" {
				body += `<h4>原段落</h4><pre class="wiki-diff">` + esc(g.Old) + `</pre>`
			}
			body += `<h4>建议内容</h4><pre class="wiki-diff">` + esc(g.Proposed) + `</pre><p>` + esc(g.Evidence) + `</p>`
			if len(g.Sources) > 0 {
				body += `<ul>`
				for _, source := range g.Sources {
					body += `<li><a rel="noopener noreferrer" href="` + esc(source.URL) + `">` + esc(source.Title) + `</a> · ` + esc(source.Accessed) + `</li>`
				}
				body += `</ul>`
			}
			body += `</details>`
		}
		if g.Resolution != "" {
			body += `<p>` + esc(g.Resolution) + `</p>`
		}
		if g.Result > 0 && wikiVersionReadable(app.db, v.PageID, g.Result) {
			body += `<p><a href="/wiki/` + pathID(v.PageID) + `/versions/` + strconv.Itoa(g.Result) + `">对应版本 ` + strconv.Itoa(g.Result) + `</a></p>`
		}
		if i != nil && g.Author == i.ID && g.State != "withdrawn" {
			body += formStart(r, "/wiki/"+pathID(v.PageID)+"/suggestions/"+pathID(id)+"/withdraw") + field("version", strconv.Itoa(g.Version)) + `<button class="btn btn-secondary">撤回建议</button></form>`
		}
		if i != nil && wikiReviewer(app.db, i.ID) && g.State == "pending" {
			disabled := ""
			if g.Base != v.Number {
				disabled = " disabled"
				body += `<p class="notice">公开版本已变化，只可附理由退回，不能直接采纳。</p>`
			}
			body += `<details class="admin-edit"><summary>人工处理</summary>` + formStart(r, "/admin/wiki/"+pathID(v.PageID)+"/suggestions/"+pathID(id)+"/resolve") + field("version", strconv.Itoa(g.Version)) + `<label>结果<select name="decision"><option value="accepted"` + disabled + `>采纳并发布</option><option value="partial"` + disabled + `>部分采纳并发布</option><option value="rejected">附理由退回</option></select></label><label>部分采纳的替换内容<textarea name="replacement" maxlength="30000"></textarea></label><label>处理说明（发布或处理建议时公开）<textarea name="note" maxlength="1000" required></textarea></label><label>风险限制<select name="risk"><option value="">无需清理</option><option value="privacy">隐私风险</option><option value="copyright">版权风险</option><option value="safety">安全风险</option></select></label><label class="check-row"><input type="checkbox" name="confirm" value="reviewed" required><span>已人工核对内容、来源和范围</span></label><button class="btn btn-primary">保存处理结果</button></form></details>`
		}
		body += `</article>`
	}
	if len(ids) == 0 {
		body += `<p class="muted">暂无修改建议</p>`
	}
	if selected == "" {
		body += paginationFor(r, len(ids) > pageSize, "suggestion_page")
	} else {
		body += `<p><a href="/wiki/` + pathID(v.PageID) + `">查看全部建议与讨论</a></p>`
	}
	if i != nil {
		body += wikiSuggestionFormHTML(r, v, nil)
	}
	return body + `</section>`, nil
}

func wikiSuggestionFormHTML(r *http.Request, v wikiVersion, values url.Values) string {
	base, open := strconv.Itoa(v.Number), ""
	if values != nil {
		base, open = values.Get("base"), " open"
	}
	body := `<details class="admin-add" id="wiki-suggestion-form"` + open + `><summary>提出修改建议</summary>` + formStart(r, "/wiki/"+pathID(v.PageID)+"/suggestions") + field("base", base) + `<label>位置<select name="target">`
	for _, target := range []string{"title", "summary", "body", "status", "first_verified_at", "work_type", "creator", "public_url"} {
		if v.Kind == "community" && (target == "work_type" || target == "creator" || target == "public_url") {
			continue
		}
		if v.Kind == "work" && (target == "status" || target == "first_verified_at") {
			continue
		}
		selected := ""
		if values.Get("target") == target {
			selected = " selected"
		}
		body += `<option value="` + target + `"` + selected + `>` + suggestionTarget(target) + `</option>`
	}
	body += `</select></label><label>原段落（仅段落替换时填写）<textarea name="old_text" maxlength="30000">` + esc(values.Get("old_text")) + `</textarea></label><label>建议内容<textarea name="proposed" maxlength="30000" required>` + esc(values.Get("proposed")) + `</textarea></label><label>依据与说明<textarea name="evidence" maxlength="2000" required>` + esc(values.Get("evidence")) + `</textarea></label><details class="field-help"><summary>新增来源与填写说明</summary><label>新增公开来源<textarea name="sources" maxlength="20000" placeholder="2 | 新来源标题 | https://example.com | 2026-11-01">` + esc(values.Get("sources")) + `</textarea></label><p>正文仍使用脚注，新增编号不能与当前来源重复。正文位置填入原段落时只替换该段，否则建议替换整篇正文。新建议公开显示，退回后重提须提供新依据。撤回建议不自动撤销已发布版本。</p></details><label class="check-row"><input type="checkbox" name="license" value="CC BY-SA 4.0" required><span>按 CC BY-SA 4.0 提交，仅使用可公开材料</span></label><button class="btn btn-primary">提交建议</button></form></details>`
	return body
}

func (app *App) wikiSuggestionError(w http.ResponseWriter, r *http.Request, err error, note string) {
	if (err != errInput && err != errConflict) || strings.Contains(r.Header.Get("Accept"), "application/json") {
		fail(w, err)
		return
	}
	id := chi.URLParam(r, "wiki")
	if !wikiReadable(app.db, id) {
		fail(w, sql.ErrNoRows)
		return
	}
	v, e := loadWikiVersion(app.db, id, 0)
	if e != nil {
		fail(w, e)
		return
	}
	status := 400
	if err == errConflict {
		status = 409
		note = "公开版本已变化或原段落无法唯一匹配。本次输入尚未保存，请保留输入并核对最新版本。"
	}
	if note == "" {
		note = "请检查建议内容、依据、脚注、新来源编号与许可确认。"
	}
	body := wikiNav() + `<h1>修改建议 · ` + esc(v.Title) + `</h1><p role="alert" class="notice">` + esc(note) + `</p><p><a href="/wiki/` + pathID(id) + `#wiki-suggestion-form">载入最新版本</a></p>` + wikiSuggestionFormHTML(r, v, r.PostForm)
	app.renderPageStatus(w, r, status, "修改建议", "wiki", body, "")
}

func (app *App) wikiSuggestionQueue(r *http.Request) (string, error) {
	rows, err := app.db.Query(`SELECT s.page_id,s.id,v.title,s.created_at FROM wiki_suggestions s JOIN wiki_pages p ON p.id=s.page_id JOIN wiki_versions v ON v.page_id=p.id AND v.number=p.published_version JOIN wiki_versions bv ON bv.page_id=s.page_id AND bv.number=s.base_version WHERE s.state='pending' AND p.state='active' AND v.review_status='accepted' AND `+wikiCommunityVisibleSQL+` AND bv.review_status='accepted' AND (bv.community_id IS NULL OR EXISTS(SELECT 1 FROM communities bc WHERE bc.id=bv.community_id AND bc.status IN ('active','uncategorized'))) ORDER BY s.created_at,s.id LIMIT ? OFFSET ?`, pageSize+1, pageNumberFor(r, "suggestion_page")*pageSize)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	body := `<h2>建议人工处理</h2>`
	n := 0
	for rows.Next() {
		var page, id, title string
		var created int64
		if err = rows.Scan(&page, &id, &title, &created); err != nil {
			return "", err
		}
		n++
		if n > pageSize {
			break
		}
		label := "待处理"
		if time.Now().Unix()-created > 7*86400 {
			label = "超过 7 天，优先处理"
		}
		body += `<article class="admin-row"><a href="/wiki/` + pathID(page) + `?suggestion=` + pathID(id) + `#suggestion-` + pathID(id) + `">` + esc(title) + `</a><p class="muted">` + label + ` · ` + wikiTime(created) + `</p></article>`
	}
	if err = rows.Err(); err != nil {
		return "", err
	}
	if n == 0 {
		body += `<p class="muted">暂无待处理建议</p>`
	}
	return body + paginationFor(r, n > pageSize, "suggestion_page"), nil
}

func wikiSubmissionRecoveryReason(reason string) string {
	if reason == "reviewed" {
		return "备份之后已有审核决定，旧提交不能重新发布。"
	}
	if wikiReason(reason) != "" {
		return wikiReason(reason) + "，提交原文已限制展示"
	}
	return ""
}
