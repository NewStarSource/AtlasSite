package atlas

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestBrowserPagesAndAPIErrorContract(t *testing.T) {
	app := coreApp(t)
	for _, tc := range []struct{ accept, content string }{{"text/html", "text/html"}, {"application/json", "application/json"}} {
		r := httptest.NewRequest("GET", app.config.Origin+"/missing-page", nil)
		r.Header.Set("Accept", tc.accept)
		w := httptest.NewRecorder()
		app.Handler().ServeHTTP(w, r)
		if w.Code != 404 || !strings.Contains(w.Header().Get("Content-Type"), tc.content) {
			t.Fatalf("error contract %s: %d %s", tc.accept, w.Code, w.Header())
		}
		if tc.accept == "text/html" && !strings.Contains(w.Body.String(), "回到首页") {
			t.Fatal("browser lacks recovery navigation")
		}
	}
	request := httptest.NewRequest("GET", app.config.Origin+"/my", nil)
	request.Header.Set("Accept", "text/html")
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if response.Code != 303 || response.Header().Get("Location") != "/login" {
		t.Fatal("anonymous browser should reach login")
	}
}

func TestSettingsSeparationAndPostOwnerControls(t *testing.T) {
	app := coreApp(t)
	author := coreUser(t, app, "作者")
	other := coreUser(t, app, "另一人")
	for _, route := range []string{"/my", "/settings", "/settings/blocks", "/settings/account", "/my/export", "/security"} {
		mustStatus(t, author.call("GET", route, nil, true), 200)
	}
	privacy := author.call("GET", "/settings", nil, true).Body.String()
	if strings.Contains(privacy, `action="/api/v1/me/deactivate"`) {
		t.Fatal("destructive form leaked into privacy page")
	}
	account := author.call("GET", "/settings/account", nil, true).Body.String()
	if !strings.Contains(account, `action="/api/v1/me/deactivate"`) {
		t.Fatal("account form missing")
	}
	post := publishFor(t, author, "测试详情", "测试正文")
	page := other.call("GET", "/p/"+post, nil, true).Body.String()
	if strings.Contains(page, `/association"`) || strings.Contains(page, "回复对象 ID") {
		t.Fatal("foreign author controls or raw parent field visible")
	}
	ownerPage := author.call("GET", "/p/"+post, nil, true)
	mustStatus(t, ownerPage, 200)
	body := ownerPage.Body.String()
	moreStart := strings.Index(body, `id="more-overlay"`)
	association := strings.Index(body, `action="/api/v1/p/`+post+`/association"`)
	if moreStart < 0 || association < moreStart || strings.Contains(body[:moreStart], "管理社群关联与内容") {
		t.Fatal("association controls must live inside the author's more overlay")
	}
}

func TestSharedPageOverlays(t *testing.T) {
	app := coreApp(t)
	for _, route := range []string{"/", "/discover", "/search", "/help", "/missing-page"} {
		r := httptest.NewRequest("GET", app.config.Origin+route, nil)
		r.Header.Set("Accept", "text/html")
		w := httptest.NewRecorder()
		app.Handler().ServeHTTP(w, r)
		if w.Code != 200 && !(route == "/missing-page" && w.Code == 404) {
			t.Fatalf("page %s: %d", route, w.Code)
		}
		body := w.Body.String()
		for _, expected := range []string{`data-overlay-open="search-overlay"`, `data-overlay-open="more-overlay"`, `id="search-overlay"`, `id="more-overlay"`, `name="q"`, `name="type"`, `href="/help"`} {
			if !strings.Contains(body, expected) {
				t.Fatalf("%s missing shared overlay control %s", route, expected)
			}
		}
	}
}

func TestReportContentLinksAndMarkdownExcerpt(t *testing.T) {
	app := coreApp(t)
	author := coreUser(t, app, "作者")
	post := publishFor(t, author, "链接举报", "公开测试正文")
	form := url.Values{"kind": {"report"}, "request_id": {randomID()}, "object_url": {app.config.Origin + "/p/" + post}, "detail": {"合成请求"}}
	mustStatus(t, author.call("POST", "/api/v1/cases", form, true), 200)
	var source string
	if err := app.db.QueryRow("SELECT object_id FROM cases WHERE user_id=?", author.owner).Scan(&source); err != nil || source != post {
		t.Fatal("content link not associated", err)
	}
	for _, link := range []string{"https://example.com/p/" + post, "//example.com/p/" + post} {
		form.Set("request_id", randomID())
		form.Set("object_url", link)
		mustStatus(t, author.call("POST", "/api/v1/cases", form, true), 400)
	}
	summary := markdownExcerpt("## 正文\n\n**重点**与[链接](https://example.com)\n\n![图片](/media/secret-id)", 100)
	if strings.Contains(summary, "secret-id") || strings.Contains(summary, "https://") || strings.Contains(summary, "**") || !strings.Contains(summary, "重点") {
		t.Fatal("markdown source leaked into card", summary)
	}
}

func TestHomeComposerOpensEditorWithoutPublishing(t *testing.T) {
	app := coreApp(t)
	author := coreUser(t, app, "作者")
	content := "首页合成内容\n<script>alert(1)</script>"
	form := url.Values{"content": {content}}
	page := author.call("POST", "/new", form, true)
	mustStatus(t, page, 200)
	if !strings.Contains(page.Body.String(), "首页合成内容") || strings.Contains(page.Body.String(), "<script>alert(1)</script>") {
		t.Fatal("composer input was lost or rendered as unsafe HTML")
	}
	var posts, drafts int
	app.db.QueryRow("SELECT count(*) FROM posts").Scan(&posts)
	app.db.QueryRow("SELECT count(*) FROM drafts").Scan(&drafts)
	if posts != 0 || drafts != 0 {
		t.Fatal("opening the editor must not publish or save content")
	}
	mustStatus(t, author.call("POST", "/new", form, false), 403)
	form.Set("content", strings.Repeat("字", 50001))
	mustStatus(t, author.call("POST", "/new", form, true), 400)
}

func TestProfileMarkdownDoesNotLoadImages(t *testing.T) {
	profile := renderProfileMarkdown("## 关于我\n\n**编程**\n\n- 阅读\n- 创作\n\n<script>alert(1)</script>\n\n[危险](javascript:alert(1))\n\n![外站](https://example.invalid/tracker.png)\n\n![站内](/media/private-id)")
	for _, expected := range []string{"<h2>关于我</h2>", "<strong>编程</strong>", "<ul>"} {
		if !strings.Contains(profile, expected) {
			t.Fatal("Markdown profile formatting missing", expected)
		}
	}
	for _, forbidden := range []string{"<script>", "javascript:", "<img", "tracker.png", "private-id"} {
		if strings.Contains(profile, forbidden) {
			t.Fatal("unsafe or image content in profile", forbidden)
		}
	}
}
