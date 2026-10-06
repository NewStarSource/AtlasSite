package atlas

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"html/template"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

//go:embed site.css
var siteCSS string

//go:embed core.js
var coreJS string

func pageHeading(kicker, title, description, actions string) string {
	return `<header class="page-heading"><div><div class="eyebrow">` + esc(kicker) + `</div><h1>` + esc(title) + `</h1><p>` + esc(description) + `</p></div><div class="action-row">` + actions + `</div></header>`
}

func emptyState(title, description, target, label string) string {
	return `<div class="empty-state"><div class="empty-symbol" aria-hidden="true">◇</div><h2>` + esc(title) + `</h2><p>` + esc(description) + `</p><a class="btn" href="` + esc(target) + `">` + esc(label) + `</a></div>`
}

func settingsNav(path string) string {
	links := [][2]string{{"/my", "我的空间"}, {"/settings", "隐私偏好"}, {"/settings/blocks", "屏蔽管理"}, {"/my/export", "数据导出"}, {"/security", "登录与安全"}, {"/settings/account", "注销与恢复"}}
	body := `<nav class="settings-tabs" aria-label="个人设置">`
	for _, link := range links {
		class := ""
		if path == link[0] {
			class = ` class="active" aria-current="page"`
		}
		body += `<a href="` + link[0] + `"` + class + `>` + link[1] + `</a>`
	}
	return body + `</nav>`
}

func (app *App) uiRoutes(router *chi.Mux) {
	router.Get("/assets/site.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.Write([]byte(siteCSS))
	})
	for _, path := range []string{"announcements", "guide", "collections", "about"} {
		router.Get("/c/{slug}/"+path, app.communityPage)
	}
	router.Get("/my", app.myPage)
	router.Get("/settings/blocks", app.settingsPage)
	router.Get("/settings/account", app.settingsPage)
}

func caseLabel(value string) string {
	labels := map[string]string{"report": "内容举报", "emergency": "紧急请求", "appeal": "处理申诉", "received": "已受理", "waiting_independent_review": "等待独立复核", "closed": "已结案", "dismissed": "不予限制", "restricted": "内容已限制", "released": "已解除对应限制", "needs-independent-review": "等待独立复核"}
	if label, ok := labels[value]; ok {
		return label
	}
	return value
}

func (app *App) helpPage(w http.ResponseWriter, r *http.Request) {
	body := pageHeading("HELP & RIGHTS", "帮助与反馈", "让表达有边界，也让每一次反馈有去处。", `<a class="btn" href="/my/cases">我的请求</a>`)
	body += `<div class="quick-links"><a class="link-card" href="/help/report"><strong>举报内容或提出申诉 ↗</strong><p>从内容详情页关联原文，提交必要说明。处理结论与申诉在我的请求中查看。</p></a><a class="link-card" href="/help/emergency"><strong>提交紧急请求 ↗</strong><p>可不登录提交。请保留受理编号，当前本地环境未提供全天值守。</p></a></div><section class="panel prose"><h2>开始使用星图</h2><p>从新星账户登录，找到感兴趣的社群，也可独立发布动态。正文支持 Markdown 和最多四张自有图片。</p><p>在编辑器上传图片后，图片会插入正文。未发布的图片仅本人可见，私人草稿也仅本人可见。</p><div class="action-row"><a class="btn" href="/discover">发现社群</a><a class="btn" href="/new">发布动态</a><a class="btn" href="/my/drafts">私人草稿</a></div></section><section class="panel prose"><h2>隐私与内容权利</h2><p>你可以隐藏社群关系、屏蔽其他身份、导出本人数据，或注销账户时选择保留内容。资料在新星账户编辑，密码始终只交给账户服务。</p><p>删除后，原帖、下级回复、图片、搜索结果与相关通知停止展示。受限旧正文一般最多保留 30 天，涉及未结争议时按案件期限冻结。</p><p>普通加密备份最长保留 12 个月，恢复前重放删除清单；私人草稿与举报说明不进入普通长期备份。</p><div class="action-row"><a class="btn" href="/settings">隐私偏好</a><a class="btn" href="/my/export">导出数据</a><a class="btn" href="/settings/account">注销与恢复</a></div></section><section class="panel"><h2>处理期限与运行状态</h2><p>紧急请求 24 小时内响应，普通举报与申诉 168 小时内初步处理。当前测试由项目负责人处理，尚未组成独立复核组；涉及处理者自身的申诉保持等待独立复核。</p><div class="action-row"><a class="btn" href="/status">运行状态</a><a class="btn" href="/config">本地配置说明</a></div></section>`
	app.renderPage(w, r, "帮助与反馈", "help", body)
}

func (app *App) myPage(w http.ResponseWriter, r *http.Request) {
	i := app.requireIdentity(w, r, false)
	if i == nil {
		return
	}
	posts, err := app.listPostsByAuthor(i.ID, 8)
	if err != nil {
		fail(w, err)
		return
	}
	body := pageHeading("YOUR SPACE", "我的空间", "内容、订阅与偏好，都可以从这里找到。", `<a class="btn btn-primary" href="/new">＋ 发布动态</a>`)
	body += `<section class="panel profile-header"><span class="avatar">我</span><div><h2>` + esc(i.Alias) + `</h2><p class="muted">星图化名 · 公开内容以此身份展示</p><a href="/u/` + pathID(i.ID) + `">查看我的公开主页 →</a></div></section><div class="quick-links">`
	for _, link := range [][4]string{{"/my/drafts", "私人草稿", "继续没有写完的想法，仅自己可见。", "SELECT count(*) FROM drafts WHERE author_id=? AND updated_at>unixepoch()-2592000"}, {"/my/bookmarks", "我的收藏", "回到想认真读完的内容。", "SELECT count(*) FROM post_bookmarks WHERE identity_id=?"}, {"/my/subscriptions", "社群与主题订阅", "管理订阅和新讨论通知。", "SELECT count(*) FROM subscriptions WHERE user_id=?"}, {"/my/cases", "举报与申诉", "查看受理状态与处理结论。", "SELECT count(*) FROM cases WHERE user_id=?"}} {
		var count int
		if err := app.db.QueryRow(link[3], i.ID).Scan(&count); err != nil {
			fail(w, err)
			return
		}
		body += `<a class="link-card" href="` + link[0] + `"><span class="card-number">` + strconv.Itoa(count) + `</span><strong>` + link[1] + `</strong><p>` + link[2] + `</p></a>`
	}
	body += `</div><section class="panel"><h2>账户与偏好</h2><div class="link-list"><a href="` + esc(app.config.AccountOrigin) + `/profile">编辑个人资料</a><a href="/settings">隐私与通知偏好</a><a href="/settings/blocks">屏蔽管理</a><a href="/my/export">导出本人数据</a><a href="/security">登录与安全</a><a href="/settings/account">注销与恢复</a></div></section><div class="section-heading"><h2>我的最近动态</h2><a href="/u/` + pathID(i.ID) + `">查看全部 →</a></div><div class="feed-list">` + app.postCards(posts, i.ID) + `</div>`
	app.renderPage(w, r, "我的空间", "my", body)
}

type browserErrorWriter struct {
	http.ResponseWriter
	status   int
	body     bytes.Buffer
	captured bool
}

func (w *browserErrorWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.captured = status >= 400 && strings.Contains(w.Header().Get("Content-Type"), "application/json")
	if !w.captured {
		w.ResponseWriter.WriteHeader(status)
	}
}
func (w *browserErrorWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	if w.captured {
		return w.body.Write(data)
	}
	return w.ResponseWriter.Write(data)
}

// HTML navigations get actionable pages; API clients keep their status and JSON contract.
func (app *App) browserErrors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept"), "text/html") || strings.HasPrefix(r.URL.Path, "/media/") || strings.HasPrefix(r.URL.Path, "/assets/") {
			next.ServeHTTP(w, r)
			return
		}
		capture := &browserErrorWriter{ResponseWriter: w}
		next.ServeHTTP(capture, r)
		if !capture.captured {
			return
		}
		var data struct{ Code, Message string }
		_ = json.Unmarshal(capture.body.Bytes(), &data)
		title, message := "暂时无法完成", "请稍后重试；如果正在编辑，先保留已输入的文字。"
		switch capture.status {
		case 400:
			title, message = "请检查提交的信息", "内容可能超过长度限制，或有必填项未完成。请返回检查后再提交。"
		case 401:
			title, message = "请先登录", "登录星图后，即可继续使用个人内容与账户功能。"
		case 403:
			title, message = "当前无法执行此操作", "请确认操作权限；导出和注销等操作需要先重新确认身份。"
		case 404:
			title, message = "这里暂时没有内容", "内容可能已删除、隐藏，或当前身份无法访问。你可以返回首页继续浏览。"
		case 409:
			title, message = "内容已在另一处更新", "为避免覆盖新内容，请保留当前文字，重新打开编辑页后再修改。"
		case 429:
			title, message = "操作稍频繁", "请稍等一会儿再试，当前已保存的内容不会受影响。"
		}
		if data.Message != "" {
			message = data.Message
		}
		body := pageHeading("NOTICE", title, message, "") + `<section class="panel"><div class="action-row"><a class="btn" href="/">回到首页</a><a class="btn" href="/my">我的空间</a><a class="btn" href="/help">帮助与反馈</a>`
		if capture.status == 401 {
			body += `<a class="btn btn-primary" href="/login">登录星图</a>`
		}
		if data.Code == "REAUTH_REQUIRED" {
			body += `<a class="btn btn-primary" href="/security">重新确认身份</a>`
		}
		body += `</div></section>`
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(capture.status)
		_ = app.page.Execute(w, struct {
			Title, Page string
			Identity    *Identity
			Content     template.HTML
		}{title, "notice", nil, template.HTML(body)})
	})
}

func licenseLabel(value string) string {
	if value == "reserved" {
		return "保留全部权利"
	}
	return value
}
