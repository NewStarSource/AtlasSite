package atlas

import "net/http"

func (app *App) renderSecurity(w http.ResponseWriter, r *http.Request, i *Identity, degraded bool) {
	body := pageHeading("ACCOUNT SECURITY", "登录与安全", "管理当前会话，或重新确认敏感操作的身份。", "") + settingsNav("/security")
	if degraded {
		body += `<div class="notice" role="status">新星账户服务暂时不可用。既有有效会话可继续普通访问，新的登录和敏感操作暂时不可用。</div>`
	}
	body += `<section class="panel"><div class="profile-header"><span class="avatar">我</span><div><h2>` + esc(i.Alias) + `</h2><p class="muted">已登录星图 · 当前账户有效</p></div></div><a class="btn" href="` + esc(app.config.AccountOrigin) + `/security">在新星账户管理登录会话 ↗</a>` + formStart(r, "/auth/logout") + `<button>退出当前星图会话</button></form></section><section class="panel"><h2>重新确认身份</h2><p>导出、注销或撤销全部会话之前，先前往新星账户输入密码。星图不会接收密码。</p>` + formStart(r, "/auth/reauthenticate") + `<button class="btn btn-primary">前往新星账户重新确认</button></form></section><section class="panel danger-panel"><h2>撤销全部星图会话</h2><p>所有已登录的星图会话将失效，需要重新登录。</p>` + formStart(r, "/auth/revoke-all") + `<button class="btn-danger">撤销全部星图会话</button></form></section>`
	app.renderPage(w, r, "登录与安全", "security", body)
}
func (app *App) renderContinue(w http.ResponseWriter, r *http.Request, target string) {
	app.renderPage(w, r, "重新确认身份", "security", pageHeading("VERIFY IDENTITY", "继续前往新星账户", "密码只在新星账户输入，星图不会接收密码。", "")+`<section class="panel"><p>确认后返回星图，继续当前操作。</p><a class="btn btn-primary" href="`+esc(target)+`">继续前往新星账户确认身份</a></section>`)
}
