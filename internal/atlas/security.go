package atlas

import (
	"html/template"
	"net/http"

	"github.com/gorilla/csrf"
)

var securityPage = template.Must(template.New("security").Parse(`<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="robots" content="noindex,nofollow"><title>星图 · 登录与安全</title><style>body{font:16px system-ui;background:#f1f5f9;color:#193049;margin:0}main{max-width:760px;margin:32px auto;padding:24px;background:white;border-radius:16px}p{line-height:1.8}button{padding:12px 20px;font:inherit;margin:12px 0;background:#155f90;color:white;border:0;border-radius:6px}a{color:#155f90}.notice{padding:16px;background:#fff2da}button:focus-visible,a:focus-visible{outline:3px solid #eeaa33}</style></head><body><main><h1>已登录星图</h1><p>化名：{{.Identity.Alias}}。账户状态：{{.Identity.Status}}。</p><p>本地 S03 测试，真实邮件和部署尚未联调。</p>{{if .Degraded}}<p class="notice" role="status">新星账户服务暂时不可用。既有有效会话可继续普通访问，新的登录和敏感操作暂时不可用。</p>{{end}}<p><a href="{{.AccountOrigin}}/security">在新星账户管理登录会话</a> · <a href="/">回到首页</a></p><form method="post" action="/auth/logout">{{.CSRF}}<button>退出当前星图会话</button></form><h2>敏感操作确认</h2><p>先回新星账户重新输入密码确认身份，再撤销全部星图会话。</p><form method="post" action="/auth/reauthenticate">{{.CSRF}}<button>前往新星账户重新确认</button></form><form method="post" action="/auth/revoke-all">{{.CSRF}}<button>撤销全部星图会话</button></form></main></body></html>`))

func (app *App) renderSecurity(writer http.ResponseWriter, request *http.Request, identity *Identity, degraded bool) {
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = securityPage.Execute(writer, struct {
		Identity      *Identity
		Degraded      bool
		CSRF          template.HTML
		AccountOrigin string
	}{identity, degraded, csrf.TemplateField(request), app.config.AccountOrigin})
}

var continuePage = template.Must(template.New("continue").Parse(`<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>重新确认身份</title></head><body><main><h1>继续前往新星账户</h1><p>密码只在新星账户输入，星图不会接收密码。</p><p><a href="{{.}}">继续前往新星账户确认身份</a></p></main></body></html>`))

func renderContinue(writer http.ResponseWriter, target string) {
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = continuePage.Execute(writer, target)
}
