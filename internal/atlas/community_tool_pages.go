package atlas

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

func toolNav(community, role string) string {
	body := `<div class="action-row">`
	if role != "organizer" {
		body += `<a href="/admin/communities/` + pathID(community) + `">社群资料</a><a href="` + collaborationBase(community) + `">协作授权</a>`
	}
	return body + `<a href="` + toolBase(community) + `">公告、指引与合集</a></div>`
}

func (app *App) communityToolsPage(w http.ResponseWriter, r *http.Request) {
	i, role := app.toolIdentity(w, r, false)
	if i == nil {
		return
	}
	community := chi.URLParam(r, "id")
	kinds := []string{}
	args := []any{community}
	holders := []string{}
	for _, kind := range []string{"announcement", "guide", "discussion"} {
		if canMaintainTool(app.db, i.ID, community, kind) {
			kinds = append(kinds, kind)
			holders = append(holders, "?")
			args = append(args, kind)
		}
	}
	args = append(args, pageSize+1, pageOffset(r))
	rows, err := app.db.Query(`SELECT c.id,c.type,c.title,c.status,c.updated_at,COALESCE(d.pinned,0) FROM collections c LEFT JOIN collection_details d ON d.collection_id=c.id WHERE c.community_id=? AND c.type IN (`+strings.Join(holders, ",")+`) ORDER BY COALESCE(d.pinned,0) DESC,c.display_order,c.updated_at DESC,c.id LIMIT ? OFFSET ?`, args...)
	if err != nil {
		fail(w, err)
		return
	}
	defer rows.Close()
	body := managementHeading(r, i, role) + toolNav(community, role) + `<section class="admin-editor"><h2>社群工具</h2><div class="action-row">`
	for _, kind := range kinds {
		body += `<a class="btn btn-secondary" href="` + toolBase(community) + `/new?kind=` + kind + `">新建` + toolLabel(kind) + `</a>`
	}
	body += `</div></section><div class="admin-list">`
	n := 0
	for rows.Next() {
		var id, kind, title, status string
		var updated int64
		var pinned int
		if err = rows.Scan(&id, &kind, &title, &status, &updated, &pinned); err != nil {
			fail(w, err)
			return
		}
		n++
		if n > pageSize {
			break
		}
		pin := ""
		if pinned == 1 {
			pin = " · 置顶"
		}
		body += `<a class="admin-community-row" href="` + toolBase(community) + `/` + pathID(id) + `"><div><h2>` + esc(title) + `</h2><small>` + toolLabel(kind) + pin + ` · ` + time.Unix(updated, 0).Format("2006-01-02 15:04") + `</small></div><span class="admin-badge">` + toolState(status) + `</span></a>`
	}
	if err = rows.Err(); err != nil {
		fail(w, err)
		return
	}
	rows.Close()
	if n == 0 {
		body += `<p class="admin-empty">还没有内容</p>`
	}
	body += `</div>` + pagination(r, n > pageSize)
	app.renderPage(w, r, "社群工具", "admin", body)
}

func (app *App) communityToolEditor(w http.ResponseWriter, r *http.Request) {
	i, role := app.toolIdentity(w, r, false)
	if i == nil {
		return
	}
	community, id := chi.URLParam(r, "id"), chi.URLParam(r, "tool")
	t := communityTool{ID: randomID(), Community: community, Kind: r.URL.Query().Get("kind"), Status: "draft"}
	if id != "" {
		var err error
		t, err = loadCommunityTool(app.db, community, id)
		if err != nil {
			fail(w, err)
			return
		}
	}
	if toolLabel(t.Kind) == "" {
		fail(w, errInput)
		return
	}
	if !canMaintainTool(app.db, i.ID, community, t.Kind) {
		denyToolAccess(w)
		return
	}
	app.renderCommunityToolEditor(w, r, i, role, t, id != "", 200, "")
}

func (app *App) renderCommunityToolEditor(w http.ResponseWriter, r *http.Request, i *Identity, role string, t communityTool, existing bool, status int, message string) {
	community, id := t.Community, t.ID
	path := toolBase(community)
	if existing {
		path += "/" + pathID(id)
	}
	body := managementHeading(r, i, role) + toolNav(community, role) + `<section class="admin-editor"><div class="admin-row-heading"><h2>` + toolLabel(t.Kind) + `</h2>`
	if existing {
		body += `<a href="` + path + `/history">修改记录</a>`
	}
	body += `</div>`
	if message != "" {
		body += `<p class="notice" role="alert">` + esc(message) + `</p>`
		if existing {
			body += `<p><a href="` + path + `">重新载入最新版本</a></p>`
		}
	}
	body += formStart(r, path) + field("kind", t.Kind) + field("tool_id", t.ID) + field("version", strconv.Itoa(t.Version)) + `<label>标题<input name="title" maxlength="200" value="` + esc(t.Title) + `" required></label><label>说明<textarea name="description" maxlength="2000" rows="3">` + esc(t.Description) + `</textarea></label>`
	if t.Kind == "discussion" {
		refs := make([]string, len(t.Refs))
		for n, id := range t.Refs {
			refs[n] = "/p/" + id
		}
		raw := strings.Join(refs, "\n")
		if r.Method == "POST" {
			raw = r.FormValue("references")
		}
		body += `<label>原帖链接<textarea name="references" rows="8" placeholder="每行一个本社群原帖链接，按展示顺序填写">` + esc(raw) + `</textarea></label><p class="field-help">最多 50 条。原帖撤回、隐藏或移出社群后停止展示。</p>`
	} else {
		body += `<label>正文 <small>Markdown</small><textarea name="content" maxlength="30000" rows="12" required>` + esc(t.Content) + `</textarea></label>`
	}
	body += `<div class="metadata-grid"><label>状态<select name="status">`
	for _, status := range []string{"draft", "published", "archived"} {
		selected := ""
		if t.Status == status {
			selected = " selected"
		}
		body += `<option value="` + status + `"` + selected + `>` + toolState(status) + `</option>`
	}
	body += `</select></label><label>排序<input type="number" name="order" min="0" max="9999" value="` + strconv.Itoa(t.Order) + `" required></label></div>`
	if t.Kind == "announcement" {
		checked := ""
		if t.Pinned == 1 {
			checked = " checked"
		}
		body += `<label class="check-row"><input type="checkbox" name="pinned" value="1"` + checked + `><span>置顶公告</span></label>`
	}
	body += `<label>修改说明<input name="summary" value="` + esc(t.Summary) + `" maxlength="300" placeholder="简要说明这次调整" required></label><div class="action-row"><button class="btn btn-primary">保存</button><a class="btn btn-secondary" href="` + toolBase(community) + `">返回列表</a></div></form><details class="field-help"><summary>维护说明</summary><p>内容对所有访客公开。请勿填入私密入口或内部材料。修改公告不会替代社群规则所需的正式确认。排序数值越小越靠前。</p></details></section>`
	app.renderPageStatus(w, r, status, "维护"+toolLabel(t.Kind), "admin", body, "")
}

func (app *App) communityToolHistory(w http.ResponseWriter, r *http.Request) {
	i, role := app.toolIdentity(w, r, false)
	if i == nil {
		return
	}
	community, id := chi.URLParam(r, "id"), chi.URLParam(r, "tool")
	t, err := loadCommunityTool(app.db, community, id)
	if err != nil {
		fail(w, err)
		return
	}
	if !canMaintainTool(app.db, i.ID, community, t.Kind) {
		denyToolAccess(w)
		return
	}
	rows, err := app.db.Query(`SELECT v.version,v.title,v.description,v.content,v.status,v.display_order,v.pinned,v.changed_at,v.summary,COALESCE(u.alias,'历史维护者'),v.refs_json FROM collection_versions v LEFT JOIN users u ON u.id=v.editor_id AND u.status='active' WHERE v.collection_id=? ORDER BY v.version DESC LIMIT ? OFFSET ?`, id, pageSize+1, pageOffset(r))
	if err != nil {
		fail(w, err)
		return
	}
	type entry struct {
		version, order, pinned                                    int
		title, description, content, status, summary, alias, refs string
		changed                                                   int64
	}
	var entries []entry
	for rows.Next() {
		var e entry
		if err = rows.Scan(&e.version, &e.title, &e.description, &e.content, &e.status, &e.order, &e.pinned, &e.changed, &e.summary, &e.alias, &e.refs); err != nil {
			rows.Close()
			fail(w, err)
			return
		}
		entries = append(entries, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		fail(w, err)
		return
	}
	body := managementHeading(r, i, role) + toolNav(community, role) + `<div class="admin-row-heading"><h2>修改记录 · ` + esc(t.Title) + `</h2><a href="` + toolBase(community) + `/` + pathID(id) + `">返回编辑</a></div>`
	for n, e := range entries {
		if n == pageSize {
			break
		}
		pin := ""
		if e.pinned == 1 {
			pin = " · 置顶"
		}
		body += `<article class="admin-row"><h3>版本 ` + strconv.Itoa(e.version) + ` · ` + esc(e.summary) + `</h3><p class="muted">` + esc(e.alias) + ` · ` + time.Unix(e.changed, 0).Format("2006-01-02 15:04") + ` · ` + toolState(e.status) + pin + ` · 排序 ` + strconv.Itoa(e.order) + `</p><details><summary>查看内容</summary><h4>` + esc(e.title) + `</h4><p>` + esc(e.description) + `</p><div class="prose">` + renderMarkdown(e.content) + `</div>`
		if t.Kind == "discussion" {
			var refs []string
			if json.Unmarshal([]byte(e.refs), &refs) != nil {
				fail(w, errInput)
				return
			}
			body += `<ol class="collection-history-refs">`
			for _, ref := range refs {
				p, err := app.getPost(ref)
				if err != nil || p.CommunityID != community || !app.postVisible(ref, i.ID) {
					continue
				}
				body += `<li><a href="/p/` + pathID(ref) + `">` + esc(p.Title) + `</a></li>`
			}
			body += `</ol><p class="muted">按该版本引用顺序显示，已撤回或隐藏的原帖不展示。</p>`
		}
		body += `</details></article>`
	}
	app.renderPage(w, r, "工具修改记录", "admin", body+pagination(r, len(entries) > pageSize))
}
