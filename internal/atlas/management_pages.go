package atlas

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

func managementNav(path, role string) string {
	body := `<nav class="tabs admin-tabs" aria-label="管理导航">`
	tabs := [][2]string{{"/admin", "社群"}, {"/admin/representatives", "代表"}, {"/admin/log", "记录"}}
	if role == "admin" || role == "member" {
		tabs = append(tabs[:1], append([][2]string{{"/admin/team", "团队"}}, tabs[1:]...)...)
	}
	for _, tab := range tabs {
		active := ""
		if path == tab[0] || (tab[0] == "/admin" && strings.HasPrefix(path, "/admin/communities/")) {
			active = ` class="active" aria-current="page"`
		}
		body += `<a href="` + tab[0] + `"` + active + `>` + tab[1] + `</a>`
	}
	return body + `</nav>`
}

func managementHeading(r *http.Request, i *Identity, role string) string {
	label := map[string]string{"admin": "团队管理员", "member": "团队成员", "representative": "社群代表"}[role]
	body := `<header class="admin-heading"><div><h1>管理面板</h1><p class="muted">` + label + `</p></div><a class="btn btn-secondary" href="/">返回首页</a></header>` + managementNav(r.URL.Path, role)
	if r.URL.Query().Get("saved") == "1" {
		body += `<p class="notice" role="status">已保存</p>`
	}
	if i.ReauthenticatedAt < time.Now().Add(-5*time.Minute).Unix() {
		body += `<p class="notice">修改前请<a href="/security">验证身份</a>。验证后返回此页。</p>`
	}
	return body
}

func (app *App) managementPage(w http.ResponseWriter, r *http.Request) {
	i, role := app.managementIdentity(w, r, false)
	if i == nil {
		return
	}
	if r.URL.Path == "/admin/team" && role == "representative" {
		fail(w, errForbidden)
		return
	}
	body := managementHeading(r, i, role)
	var content string
	var err error
	switch r.URL.Path {
	case "/admin/team":
		content, err = app.managementTeam(r, role)
	case "/admin/representatives":
		content, err = app.managementRepresentatives(r, i, role)
	case "/admin/log":
		content, err = app.managementLog(i, role, r)
	default:
		content, err = app.managementCommunities(i, role, r)
	}
	if err != nil {
		fail(w, err)
		return
	}
	app.renderPage(w, r, "管理面板", "admin", body+content)
}

func managementSearch(r *http.Request, placeholder string) string {
	return `<form class="admin-search" method="get"><label class="sr-only" for="admin-query">` + placeholder + `</label><input id="admin-query" name="q" type="search" maxlength="100" placeholder="` + placeholder + `" value="` + esc(r.URL.Query().Get("q")) + `"><button class="tool-icon" aria-label="搜索">` + uiIcon("search") + `</button></form>`
}

func (app *App) managementCommunities(i *Identity, role string, r *http.Request) (string, error) {
	q := r.URL.Query().Get("q")
	if !checkText(q, 100) {
		return "", errInput
	}
	query := `SELECT c.id,c.name,c.description,c.status,COALESCE(u.alias,''),COALESCE(cr.expires_at,0) FROM communities c LEFT JOIN community_representatives cr ON cr.community_id=c.id LEFT JOIN users u ON u.id=cr.user_id AND u.status='active' WHERE instr(lower(c.name),lower(?))>0`
	args := []any{q}
	if role == "representative" {
		query += ` AND c.status IN ('active','uncategorized') AND cr.user_id=? AND cr.expires_at>?`
		args = append(args, i.ID, time.Now().Unix())
	}
	query += " ORDER BY c.name,c.id LIMIT 101"
	rows, err := app.db.Query(query, args...)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	body := managementSearch(r, "搜索社群") + `<div class="admin-list">`
	n := 0
	states := map[string]string{"active": "正常", "uncategorized": "待分类", "pending": "待确认", "archived": "已归档"}
	for rows.Next() {
		var id, name, description, status, alias string
		var expires int64
		if err = rows.Scan(&id, &name, &description, &status, &alias, &expires); err != nil {
			return "", err
		}
		n++
		if n > 100 {
			body += `<p class="muted">还有更多社群，请搜索名称。</p>`
			break
		}
		representative := "暂无有效代表"
		if expires > time.Now().Unix() && alias != "" {
			representative = "代表 · " + alias
		}
		body += `<a class="admin-community-row" href="/admin/communities/` + pathID(id) + `"><div><h2>` + esc(name) + `</h2><p>` + esc(excerpt(description, 70)) + `</p><small>` + esc(representative) + `</small></div><span class="admin-badge">` + states[status] + `</span></a>`
	}
	if n == 0 {
		body += `<p class="admin-empty">暂无可管理的社群</p>`
	}
	return body + `</div>`, rows.Err()
}

func grantDate(expires int64) string {
	if expires == 0 {
		return "已撤销"
	}
	return time.Unix(expires, 0).Format("2006-01-02")
}
func grantStatus(expires int64) string {
	if expires == 0 {
		return "已撤销"
	}
	if expires <= time.Now().Unix() {
		return "已到期"
	}
	return "有效"
}

func (app *App) managementUserOptions(selected, q string) (string, error) {
	if !checkText(q, 100) {
		return "", errInput
	}
	rows, err := app.db.Query(`SELECT id,alias FROM users WHERE status='active' AND (id=? OR instr(lower(alias),lower(?))>0 OR id=?) ORDER BY id=? DESC,alias,id LIMIT 100`, selected, q, q, selected)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	body := `<option value="">选择用户</option>`
	for rows.Next() {
		var id, alias string
		if err = rows.Scan(&id, &alias); err != nil {
			return "", err
		}
		s := ""
		if id == selected {
			s = " selected"
		}
		body += `<option value="` + esc(id) + `"` + s + `>` + esc(alias) + ` · ` + esc(excerpt(id, 8)) + `</option>`
	}
	return body, rows.Err()
}

func grantFields(g managementGrant, months int) string {
	date := grantDate(g.Expires)
	if g.Expires <= time.Now().Unix() {
		date = time.Now().AddDate(0, months, 0).Format("2006-01-02")
	}
	return field("version", strconv.Itoa(g.Version)) + `<label>授权依据<input name="authority" maxlength="300" value="` + esc(g.Authority) + `" placeholder="选举结果或临时授权记录" required></label><label>有效至<input type="date" name="expires" value="` + date + `" min="` + time.Now().Format("2006-01-02") + `" max="` + time.Now().AddDate(0, months, 0).Format("2006-01-02") + `" required></label><button class="btn btn-primary">保存</button>`
}
func teamRoleSelect(role string) string {
	selected := ""
	if role == "admin" {
		selected = " selected"
	}
	return `<label>职责<select name="role"><option value="member">团队成员 · 维护社群</option><option value="admin"` + selected + `>管理员 · 管理授权</option></select></label>`
}
func removeGrantForm(r *http.Request, path string, version int) string {
	return formStart(r, path) + field("version", strconv.Itoa(version)) + `<label class="check-row"><input type="checkbox" required><span>确认撤销授权</span></label><button class="btn btn-secondary">撤销</button></form>`
}

func (app *App) managementTeam(r *http.Request, role string) (string, error) {
	rows, err := app.db.Query(`SELECT t.user_id,t.role,t.authority,t.expires_at,t.version,u.alias,u.status FROM team_members t JOIN users u ON u.id=t.user_id ORDER BY t.expires_at>unixepoch() DESC,u.alias LIMIT 201`)
	if err != nil {
		return "", err
	}
	type item struct {
		g             managementGrant
		alias, status string
	}
	var items []item
	for rows.Next() {
		var v item
		if err = rows.Scan(&v.g.User, &v.g.Role, &v.g.Authority, &v.g.Expires, &v.g.Version, &v.alias, &v.status); err != nil {
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
	body := `<section class="admin-list">`
	for n, v := range items {
		if n == 200 {
			body += `<p class="muted">仅显示最近 200 条授权。</p>`
			break
		}
		label := map[string]string{"admin": "管理员", "member": "团队成员"}[v.g.Role]
		state := grantStatus(v.g.Expires)
		if v.status != "active" {
			state = "账户不可用"
		}
		body += `<article class="admin-row"><div class="admin-row-heading"><h2>` + esc(v.alias) + `</h2><span class="admin-badge">` + state + `</span></div><p>` + label + ` · ` + grantDate(v.g.Expires) + `</p><p class="muted">` + esc(v.g.Authority) + `</p>`
		if role == "admin" {
			body += `<details class="admin-edit"><summary>编辑授权</summary>` + formStart(r, "/admin/team") + field("user_id", v.g.User) + teamRoleSelect(v.g.Role) + grantFields(v.g, 12) + `</form>` + removeGrantForm(r, "/admin/team/"+pathID(v.g.User)+"/remove", v.g.Version) + `</details>`
		}
		body += `</article>`
	}
	if len(items) == 0 {
		body += `<p class="admin-empty">暂无团队成员</p>`
	}
	body += `</section>`
	if role == "admin" {
		options, e := app.managementUserOptions("", r.URL.Query().Get("q"))
		if e != nil {
			return "", e
		}
		body += `<details class="admin-add"><summary>添加团队成员</summary>` + managementSearch(r, "搜索用户化名或 ID") + formStart(r, "/admin/team") + `<label>用户<select name="user_id" required>` + options + `</select></label>` + teamRoleSelect("member") + grantFields(managementGrant{}, 12) + `</form><p class="field-help">已在列表中的用户请使用编辑授权。</p></details>`
	}
	return body, nil
}

func (app *App) managementRepresentatives(r *http.Request, i *Identity, role string) (string, error) {
	query := `SELECT cr.community_id,c.name,cr.user_id,u.alias,u.status,cr.authority,cr.expires_at,cr.version FROM community_representatives cr JOIN communities c ON c.id=cr.community_id JOIN users u ON u.id=cr.user_id`
	var args []any
	if role == "representative" {
		query += " WHERE cr.user_id=? AND cr.expires_at>? AND c.status IN ('active','uncategorized')"
		args = []any{i.ID, time.Now().Unix()}
	}
	query += " ORDER BY cr.expires_at>unixepoch() DESC,c.name LIMIT 201"
	rows, err := app.db.Query(query, args...)
	if err != nil {
		return "", err
	}
	type item struct {
		id, name, alias, status string
		g                       managementGrant
	}
	var items []item
	for rows.Next() {
		var v item
		if err = rows.Scan(&v.id, &v.name, &v.g.User, &v.alias, &v.status, &v.g.Authority, &v.g.Expires, &v.g.Version); err != nil {
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
	body := `<section class="admin-list">`
	for n, v := range items {
		if n == 200 {
			body += `<p class="muted">仅显示最近 200 条授权。</p>`
			break
		}
		state := grantStatus(v.g.Expires)
		if v.status != "active" {
			state = "账户不可用"
		}
		body += `<article class="admin-row"><div class="admin-row-heading"><h2>` + esc(v.name) + `</h2><span class="admin-badge">` + state + `</span></div><p>` + esc(v.alias) + ` · ` + grantDate(v.g.Expires) + `</p><p class="muted">` + esc(v.g.Authority) + `</p>`
		if role == "admin" {
			options, e := app.managementUserOptions(v.g.User, r.URL.Query().Get("q"))
			if e != nil {
				return "", e
			}
			body += `<details class="admin-edit"><summary>编辑授权</summary>` + formStart(r, "/admin/representatives") + field("community_id", v.id) + `<label>代表<select name="user_id" required>` + options + `</select></label>` + grantFields(v.g, 6) + `</form>` + removeGrantForm(r, "/admin/representatives/"+pathID(v.id)+"/remove", v.g.Version) + `</details>`
		}
		body += `</article>`
	}
	if len(items) == 0 {
		body += `<p class="admin-empty">暂无代表授权</p>`
	}
	body += `</section>`
	if role == "admin" {
		options, e := app.managementUserOptions("", r.URL.Query().Get("q"))
		if e != nil {
			return "", e
		}
		rows, e := app.db.Query(`SELECT c.id,c.name FROM communities c WHERE c.status IN ('active','uncategorized') AND NOT EXISTS(SELECT 1 FROM community_representatives cr WHERE cr.community_id=c.id) ORDER BY c.name`)
		if e != nil {
			return "", e
		}
		communities := `<option value="">选择社群</option>`
		for rows.Next() {
			var id, name string
			if e = rows.Scan(&id, &name); e != nil {
				rows.Close()
				return "", e
			}
			communities += `<option value="` + esc(id) + `">` + esc(name) + `</option>`
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return "", e
		}
		body += `<details class="admin-add"><summary>登记社群代表</summary>` + managementSearch(r, "搜索用户化名或 ID") + formStart(r, "/admin/representatives") + `<label>社群<select name="community_id" required>` + communities + `</select></label><label>代表<select name="user_id" required>` + options + `</select></label>` + grantFields(managementGrant{}, 6) + `</form><p class="field-help">每个社群一名代表，授权最长六个月。已有授权请在上方编辑。</p></details>`
	}
	return body, nil
}

func (app *App) managementLog(i *Identity, role string, r *http.Request) (string, error) {
	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if r.URL.Query().Get("page") == "" {
		page = 0
		err = nil
	}
	if err != nil || page < 0 || page > 10000 {
		return "", errInput
	}
	query := `SELECT e.actor_id,COALESCE(u.alias,'本地操作员'),e.action,e.object_id,COALESCE(c.name,t.alias,e.object_id),e.created_at,e.before_json,e.after_json FROM management_events e LEFT JOIN users u ON u.id=e.actor_id LEFT JOIN communities c ON c.id=e.object_id LEFT JOIN users t ON t.id=e.object_id`
	args := []any{}
	if role == "representative" {
		query += ` WHERE e.action='community-save' AND EXISTS(SELECT 1 FROM community_representatives cr JOIN communities c0 ON c0.id=cr.community_id WHERE cr.community_id=e.object_id AND cr.user_id=? AND cr.expires_at>? AND c0.status IN ('active','uncategorized'))`
		args = append(args, i.ID, time.Now().Unix())
	}
	query += " ORDER BY e.created_at DESC,e.id DESC LIMIT 51 OFFSET ?"
	args = append(args, page*50)
	rows, err := app.db.Query(query, args...)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	labels := map[string]string{"bootstrap-admin": "初始化管理员", "team-save": "更新团队授权", "team-remove": "撤销团队授权", "representative-save": "更新代表授权", "representative-remove": "撤销代表授权", "community-save": "更新社群资料"}
	body := `<div class="admin-list">`
	n := 0
	for rows.Next() {
		var actor, alias, action, object, name, before, after string
		var created int64
		if err = rows.Scan(&actor, &alias, &action, &object, &name, &created, &before, &after); err != nil {
			return "", err
		}
		n++
		if n > 50 {
			body += `<a class="btn btn-secondary" href="/admin/log?page=` + strconv.Itoa(page+1) + `">更早记录</a>`
			break
		}
		body += `<article class="admin-row"><h2>` + esc(labels[action]) + `</h2><p>` + esc(name) + `</p><small class="muted">` + esc(alias) + ` · ` + time.Unix(created, 0).Format("2006-01-02 15:04") + `</small><details class="admin-edit"><summary>变更内容</summary><div class="admin-change"><section><h3>变更前</h3>` + managementChange(action, before) + `</section><section><h3>变更后</h3>` + managementChange(action, after) + `</section></div></details></article>`
	}
	if n == 0 {
		body += `<p class="admin-empty">暂无操作记录</p>`
	}
	if page > 0 {
		body += `<a class="btn btn-secondary" href="/admin/log?page=` + strconv.Itoa(page-1) + `">较新记录</a>`
	}
	return body + `</div>`, rows.Err()
}

type managedCommunity struct {
	Name, Description, Contact string
	Version                    int
}

func managementChange(action, raw string) string {
	if raw == "null" {
		return `<p class="muted">无</p>`
	}
	if action == "community-save" {
		var c managedCommunity
		if json.Unmarshal([]byte(raw), &c) != nil {
			return ""
		}
		return `<dl><dt>名称</dt><dd>` + esc(c.Name) + `</dd><dt>简介</dt><dd>` + esc(c.Description) + `</dd><dt>联系说明</dt><dd>` + esc(c.Contact) + `</dd></dl>`
	}
	var g managementGrant
	if json.Unmarshal([]byte(raw), &g) != nil {
		return ""
	}
	if g.Role == "" && g.Authority == "" {
		return `<p class="muted">无授权</p>`
	}
	label := map[string]string{"admin": "管理员", "member": "团队成员", "representative": "社群代表"}[g.Role]
	if label == "" {
		label = "社群代表"
	}
	return `<dl><dt>职责</dt><dd>` + label + `</dd><dt>用户 ID</dt><dd>` + esc(g.User) + `</dd><dt>授权依据</dt><dd>` + esc(g.Authority) + `</dd><dt>有效至</dt><dd>` + grantDate(g.Expires) + `</dd></dl>`
}

func (app *App) manageCommunityPage(w http.ResponseWriter, r *http.Request) {
	i, role := app.managementIdentity(w, r, false)
	if i == nil {
		return
	}
	id := chi.URLParam(r, "id")
	if !canManageCommunity(app.db, i.ID, id) {
		fail(w, errForbidden)
		return
	}
	var c managedCommunity
	var slug string
	if err := app.db.QueryRow("SELECT name,description,contact_method,management_version,slug FROM communities WHERE id=?", id).Scan(&c.Name, &c.Description, &c.Contact, &c.Version, &slug); err != nil {
		fail(w, err)
		return
	}
	body := managementHeading(r, i, role) + `<section class="admin-editor"><div class="admin-row-heading"><h2>社群资料</h2><a href="/c/` + pathID(slug) + `">查看社群 ↗</a></div>` + formStart(r, "/admin/communities/"+pathID(id)) + field("version", strconv.Itoa(c.Version)) + `<label>名称<input name="name" value="` + esc(c.Name) + `" maxlength="100" required></label><label>简介<textarea name="description" maxlength="4000" rows="6" required>` + esc(c.Description) + `</textarea></label><label>联系说明<textarea name="contact" maxlength="1000" rows="3">` + esc(c.Contact) + `</textarea></label><div class="action-row"><button class="btn btn-primary">保存</button><a class="btn btn-secondary" href="/admin">返回列表</a></div></form></section>`
	app.renderPage(w, r, "管理 · "+c.Name, "admin", body)
}

func (app *App) saveManagedCommunity(w http.ResponseWriter, r *http.Request) {
	i, _ := app.managementIdentity(w, r, true)
	if i == nil {
		return
	}
	if r.ParseForm() != nil {
		fail(w, errInput)
		return
	}
	version, err := strconv.Atoi(r.FormValue("version"))
	c := managedCommunity{strings.TrimSpace(r.FormValue("name")), strings.TrimSpace(r.FormValue("description")), strings.TrimSpace(r.FormValue("contact")), version}
	if err != nil || version < 1 || c.Name == "" || c.Description == "" || !checkText(c.Name, 100) || !checkText(c.Description, 4000) || !checkText(c.Contact, 1000) {
		fail(w, errInput)
		return
	}
	tx, err := app.db.Begin()
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback()
	id := chi.URLParam(r, "id")
	if !canManageCommunity(tx, i.ID, id) {
		fail(w, errForbidden)
		return
	}
	var mode string
	if tx.QueryRow("SELECT mode FROM runtime_state WHERE id=1").Scan(&mode) != nil || mode != "normal" {
		fail(w, errForbidden)
		return
	}
	var old managedCommunity
	if err = tx.QueryRow("SELECT name,description,contact_method,management_version FROM communities WHERE id=?", id).Scan(&old.Name, &old.Description, &old.Contact, &old.Version); err != nil {
		fail(w, err)
		return
	}
	if old.Version != version {
		fail(w, errConflict)
		return
	}
	_, err = tx.Exec("UPDATE communities SET name=?,description=?,contact_method=?,management_version=management_version+1,updated_at=? WHERE id=?", c.Name, c.Description, c.Contact, time.Now().Unix(), id)
	if err == nil {
		c.Version++
		err = managementEvent(tx, i.ID, "community-save", id, old, c)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		fail(w, err)
		return
	}
	http.Redirect(w, r, "/admin/communities/"+pathID(id)+"?saved=1", 303)
}
