package atlas

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

type toolCollaboration struct {
	ID, Community, Name, CommunityStatus, User, Alias, Kind, Purpose, State string
	Version                                                                 int
	Expires                                                                 int64
}

func (app *App) collaborationItems(user, community string, offset int) ([]toolCollaboration, error) {
	query := `SELECT g.id,g.community_id,c.name,c.status,g.user_id,u.alias,g.kind,g.purpose,g.state,g.version,g.expires_at FROM tool_collaborators g JOIN communities c ON c.id=g.community_id JOIN users u ON u.id=g.user_id WHERE `
	args := []any{}
	if user != "" {
		query += "g.user_id=?"
		args = append(args, user)
	} else {
		query += "g.community_id=?"
		args = append(args, community)
	}
	query += " ORDER BY g.updated_at DESC,g.id LIMIT ? OFFSET ?"
	args = append(args, pageSize+1, offset)
	rows, err := app.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []toolCollaboration
	for rows.Next() {
		var g toolCollaboration
		if err = rows.Scan(&g.ID, &g.Community, &g.Name, &g.CommunityStatus, &g.User, &g.Alias, &g.Kind, &g.Purpose, &g.State, &g.Version, &g.Expires); err != nil {
			return nil, err
		}
		items = append(items, g)
	}
	return items, rows.Err()
}

func (app *App) liveCollaboration(g toolCollaboration) bool {
	var n int
	return app.db.QueryRow("SELECT count(*)"+liveToolGrantSQL+" AND g.id=?", g.ID).Scan(&n) == nil && n == 1
}

func (app *App) collaborationLabel(g toolCollaboration) string {
	if (g.State == "pending" || g.State == "accepted") && !app.liveCollaboration(g) {
		return "已失效"
	}
	return map[string]string{"pending": "待本人接受", "accepted": "协作中", "declined": "已谢绝", "left": "已退出", "revoked": "已撤销"}[g.State]
}

func collaborationFormFields(g managementGrant, kind string, repExpires int64) string {
	expires := g.Expires
	if expires <= time.Now().Unix() {
		expires = time.Now().AddDate(0, 1, 0).Unix()
	}
	if expires > repExpires {
		expires = repExpires
	}
	kindField := ""
	if kind != "" {
		kindField = field("kind", kind)
	}
	return field("version", strconv.Itoa(g.Version)) + kindField + `<label>协作说明<input name="authority" value="` + esc(g.Authority) + `" maxlength="300" required></label><label>有效至<input type="date" name="expires" min="` + time.Now().Format("2006-01-02") + `" max="` + grantDate(repExpires) + `" value="` + grantDate(expires) + `" required></label><button class="btn btn-primary">发送邀请</button>`
}

func (app *App) collaboratorsPage(w http.ResponseWriter, r *http.Request) {
	i, role := app.managementIdentity(w, r, false)
	if i == nil {
		return
	}
	community := chi.URLParam(r, "id")
	if !canManageCommunity(app.db, i.ID, community) {
		fail(w, errForbidden)
		return
	}
	items, err := app.collaborationItems("", community, pageOffset(r))
	if err != nil {
		fail(w, err)
		return
	}
	var repExpires int64
	app.db.QueryRow("SELECT expires_at FROM community_representatives WHERE community_id=? AND user_id=? AND expires_at>unixepoch()", community, i.ID).Scan(&repExpires)
	body := managementHeading(r, i, role) + toolNav(community, role) + `<h2>协作授权</h2><div class="admin-list">`
	for _, g := range pageItems(items) {
		body += `<article class="admin-row"><div class="admin-row-heading"><h3>` + esc(g.Alias) + ` · ` + toolLabel(g.Kind) + `</h3><span class="admin-badge">` + app.collaborationLabel(g) + `</span></div><p>` + esc(g.Purpose) + `</p><small class="muted">有效至 ` + grantDate(g.Expires) + `</small>`
		if g.State != "revoked" {
			body += formStart(r, collaborationBase(community)+"/"+pathID(g.ID)+"/revoke") + field("version", strconv.Itoa(g.Version)) + `<button class="btn btn-secondary">撤销协作</button></form>`
		}
		if repExpires > 0 {
			body += `<details class="admin-edit"><summary>重新邀请</summary>` + formStart(r, collaborationBase(community)+"/invite") + field("user_id", g.User) + collaborationFormFields(managementGrant{Version: g.Version, Authority: g.Purpose, Expires: g.Expires}, g.Kind, repExpires) + `</form><p class="field-help">再次邀请会重置接受状态，需本人重新同意。</p></details>`
		}
		body += `</article>`
	}
	if len(items) == 0 {
		body += `<p class="admin-empty">还没有协作邀请</p>`
	}
	body += `</div>` + pagination(r, len(items) > pageSize)
	q := r.URL.Query().Get("q")
	if !checkText(q, 100) {
		fail(w, errInput)
		return
	}
	rows, err := app.db.Query(`SELECT e.user_id,u.alias,e.eligible,e.standard_url,e.version FROM tool_eligibility e JOIN users u ON u.id=e.user_id WHERE e.community_id=? AND u.status='active' AND instr(lower(u.alias),lower(?))>0 ORDER BY u.alias,e.user_id LIMIT 101`, community, q)
	if err != nil {
		fail(w, err)
		return
	}
	type eligibility struct {
		User, Alias, URL  string
		Eligible, Version int
	}
	var records []eligibility
	for rows.Next() {
		var e eligibility
		if err = rows.Scan(&e.User, &e.Alias, &e.Eligible, &e.URL, &e.Version); err != nil {
			rows.Close()
			fail(w, err)
			return
		}
		records = append(records, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		fail(w, err)
		return
	}
	options := `<option value="">选择已核验成员</option>`
	for n, e := range records {
		if n == 100 {
			break
		}
		if e.Eligible == 1 && e.User != i.ID {
			options += `<option value="` + esc(e.User) + `">` + esc(e.Alias) + `</option>`
		}
	}
	if repExpires > 0 {
		body += `<details class="admin-add"><summary>邀请整理者</summary>` + strings.ReplaceAll(managementSearch(r, "搜索已核验成员"), "admin-query", "collaborator-query") + formStart(r, collaborationBase(community)+"/invite") + `<label>成员<select name="user_id" required>` + options + `</select></label><label>工具范围<select name="kind"><option value="announcement">公告</option><option value="guide">新人指引</option><option value="discussion">讨论合集</option></select></label>`
		fields := collaborationFormFields(managementGrant{}, "", repExpires)
		body += fields + `</form><p class="field-help">每次邀请限定一种工具，期限不超过代表授权。受邀者可谢绝或随时退出。</p></details>`
	}
	if role == "admin" || role == "member" {
		body += `<details class="admin-add"><summary>工具协作资格核验</summary><p class="field-help">核对既有正式成员资格，只登记结论和公开标准入口，不上传证明原件。本登记不创建正式成员或投票资格。</p>` + managementSearch(r, "搜索用户化名或 ID")
		for n, e := range records {
			if n == 100 {
				body += `<p class="muted">还有更多记录，请搜索。</p>`
				break
			}
			label := "已核验"
			if e.Eligible == 0 {
				label = "已撤销核验"
			}
			body += `<details class="admin-edit"><summary>` + esc(e.Alias) + ` · ` + label + `</summary>` + eligibilityForm(r, community, e.User, e.URL, e.Version, e.Eligible) + `</details>`
		}
		options, err := app.managementUserOptions("", q)
		if err != nil {
			fail(w, err)
			return
		}
		body += `<h3>登记已核验成员</h3>` + formStart(r, collaborationBase(community)+"/eligibility") + `<label>用户<select name="user_id" required>` + options + `</select></label>` + eligibilityFields("", 0, 1) + `</form></details>`
	}
	app.renderPage(w, r, "协作授权", "admin", body)
}

func eligibilityFields(standard string, version, eligible int) string {
	selected := ""
	if eligible == 0 {
		selected = " selected"
	}
	return field("version", strconv.Itoa(version)) + `<label>公开成员标准入口<input type="url" name="standard_url" value="` + esc(standard) + `" maxlength="1000" required></label><label>核验结论<select name="eligible"><option value="1">既有正式成员资格已核验</option><option value="0"` + selected + `>撤销工具协作资格核验</option></select></label><label>变更说明<input name="reason" maxlength="300" required></label><label class="check-row"><input type="checkbox" name="confirm" value="verified" required><span>已按公开标准核对，未在此登记敏感证明</span></label><button class="btn btn-primary">保存核验结论</button>`
}
func eligibilityForm(r *http.Request, community, user, standard string, version, eligible int) string {
	return formStart(r, collaborationBase(community)+"/eligibility") + field("user_id", user) + eligibilityFields(standard, version, eligible) + `</form>`
}

func (app *App) myCollaborationsPage(w http.ResponseWriter, r *http.Request) {
	i := app.requireIdentity(w, r, false)
	if i == nil {
		return
	}
	items, err := app.collaborationItems(i.ID, "", pageOffset(r))
	if err != nil {
		fail(w, err)
		return
	}
	body := pageHeading("", "社群协作", "", `<a href="/my">返回我的</a>`)
	if r.URL.Query().Get("saved") == "1" {
		body += `<p role="status" class="notice">已更新</p>`
	}
	for _, g := range pageItems(items) {
		name := g.Name
		if g.CommunityStatus != "active" && g.CommunityStatus != "uncategorized" {
			name = "社群当前不可用"
		}
		body += `<article class="admin-row"><div class="admin-row-heading"><h2>` + esc(name) + ` · ` + toolLabel(g.Kind) + `</h2><span class="admin-badge">` + app.collaborationLabel(g) + `</span></div><p>` + esc(g.Purpose) + `</p><p class="muted">有效至 ` + grantDate(g.Expires) + `</p>`
		if g.State == "pending" {
			if app.liveCollaboration(g) {
				body += formStart(r, "/my/community-tools/"+pathID(g.ID)+"/accept") + field("version", strconv.Itoa(g.Version)) + `<label class="check-row"><input type="checkbox" name="confirm" value="accept" required><span>我愿意在此范围和期限内协助维护</span></label><button class="btn btn-primary">接受邀请</button></form><p class="field-help">接受前需在<a href="/security">安全页确认身份</a>，之后返回本页。</p>`
			}
			body += formStart(r, "/my/community-tools/"+pathID(g.ID)+"/decline") + field("version", strconv.Itoa(g.Version)) + `<button class="btn btn-secondary">谢绝邀请</button></form>`
		} else if g.State == "accepted" {
			if app.liveCollaboration(g) {
				body += `<a class="btn btn-primary" href="` + toolBase(g.Community) + `">进入维护</a>`
			}
			body += formStart(r, "/my/community-tools/"+pathID(g.ID)+"/leave") + field("version", strconv.Itoa(g.Version)) + `<button class="btn btn-secondary">退出协作</button></form>`
		}
		body += `</article>`
	}
	if len(items) == 0 {
		body += `<p class="admin-empty">暂无协作邀请</p>`
	}
	body += `<details class="field-help"><summary>协作权限说明</summary><p>只可维护获授权的公告、指引或合集，不能管理人员、修改社群资料或编辑他人原帖。代表授权、资格核验或期限变化后，旧授权失效，重新邀请仍需本人接受。</p></details>`
	app.renderPage(w, r, "社群协作", "my", body+pagination(r, len(items) > pageSize))
}
