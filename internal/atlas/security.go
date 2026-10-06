package atlas

import "net/http"

func (app *App) renderSecurity(w http.ResponseWriter, r *http.Request, i *Identity, degraded bool) {
	body := pageHeading("ACCOUNT SECURITY", "安全", "", "") + settingsNav("/security")
	if degraded {
		body += `<div class="notice" role="status">新星账户服务暂时不可用。既有有效会话可继续普通访问，新的登录和敏感操作暂时不可用。</div>`
	}
	body += `<section class="panel"><div class="profile-header"><span class="avatar">我</span><div><h2>` + esc(i.Alias) + `</h2><p class="muted">已登录</p></div></div><a class="btn" href="` + esc(app.config.AccountOrigin) + `/security">账户会话</a>` + formStart(r, "/auth/logout") + `<button>退出登录</button></form></section><section class="panel"><h2>验证身份</h2><p>导出、注销或撤销会话前需验证身份。</p>` + formStart(r, "/auth/reauthenticate") + `<button class="btn btn-primary">验证身份</button></form></section><section class="panel danger-panel"><h2>撤销全部星图会话</h2><p>所有会话将退出。</p>` + formStart(r, "/auth/revoke-all") + `<button class="btn-danger">撤销全部星图会话</button></form></section>`
	app.renderPage(w, r, "安全", "security", body)
}
func (app *App) renderContinue(w http.ResponseWriter, r *http.Request, target string) {
	app.renderPage(w, r, "验证身份", "security", pageHeading("VERIFY IDENTITY", "继续前往新星账户", "", "")+`<section class="panel"><p></p><a class="btn btn-primary" href="`+esc(target)+`">前往账户验证</a></section>`)
}
