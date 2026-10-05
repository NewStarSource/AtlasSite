package atlas

import (
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"
)

//go:embed page.html
var pageHTML string

//go:embed 001_init.sql
var schema string

//go:embed 002_identity.sql
var identitySchema string

//go:embed 003_communities.sql
var communitiesSchema string

type Config struct {
	Mode           string `json:"mode"`
	Address        string `json:"address"`
	Origin         string `json:"origin"`
	AccountOrigin  string `json:"account_origin"`
	Database       string `json:"database"`
	OIDCSecretFile string `json:"oidc_secret_file"`
	CSRFKeyFile    string `json:"csrf_key_file"`
}

func (config Config) Validate() error {
	if config.Mode != "development" && config.Mode != "test" {
		return errors.New("当前阶段仅开放 development/test；生产需要后续身份与上线验收")
	}
	host, _, err := net.SplitHostPort(config.Address)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return errors.New("当前阶段只允许绑定回环地址")
	}
	origin, err := url.Parse(config.Origin)
	if err != nil || origin.Scheme != "http" || origin.Host != config.Address || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" || origin.User != nil {
		return errors.New("origin 必须与回环 HTTP 地址一致")
	}
	account, err := url.Parse(config.AccountOrigin)
	if err != nil || account.Scheme != "http" || net.ParseIP(account.Hostname()) == nil || !net.ParseIP(account.Hostname()).IsLoopback() || account.Path != "" || account.RawQuery != "" || account.User != nil || account.Fragment != "" {
		return errors.New("账户地址必须为回环 HTTP origin")
	}
	if config.Database == "" || strings.ContainsAny(config.Database, "?\x00") {
		return errors.New("database 配置无效")
	}
	if (config.OIDCSecretFile == "") != (config.CSRFKeyFile == "") {
		return errors.New("OIDC 与 CSRF 配置必须同时提供")
	}
	return nil
}
func LoadConfig(filename string) (Config, error) {
	var config Config
	content, err := os.ReadFile(filename)
	if err != nil {
		return config, errors.New("缺少配置，先运行 init-dev")
	}
	if err = json.Unmarshal(content, &config); err != nil {
		return config, errors.New("配置 JSON 无效")
	}
	return config, config.Validate()
}
func InitDevelopment() error {
	if err := os.MkdirAll(".local", 0700); err != nil {
		return err
	}
	config := Config{Mode: "development", Address: "127.0.0.1:4200", Origin: "http://127.0.0.1:4200", AccountOrigin: "http://127.0.0.1:4100", Database: ".local/development.db"}
	content, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.OpenFile(".local/development.json", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = file.Write(content)
	closeErr := file.Close()
	if err != nil {
		return err
	}
	return closeErr
}

type App struct {
	db         *sql.DB
	config     Config
	page       *template.Template
	secret     string
	csrfKey    []byte
	httpClient *http.Client
	syncMu     sync.Mutex
}

func New(config Config) (*App, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(config.Database), 0700); err != nil {
		return nil, err
	}
	database, err := sql.Open("sqlite", config.Database+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(2000)&_pragma=synchronous(FULL)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	database.SetMaxOpenConns(1)
	transaction, err := database.Begin()
	if err != nil {
		database.Close()
		return nil, err
	}
	if _, err = transaction.Exec(schema); err != nil {
		transaction.Rollback()
		database.Close()
		return nil, err
	}
	var version int
	if err = transaction.QueryRow("SELECT max(version) FROM schema_migrations").Scan(&version); err != nil || version < 1 || version > 3 {
		transaction.Rollback()
		database.Close()
		return nil, errors.New("unsupported schema version")
	}
	if version == 1 {
		if _, err = transaction.Exec(identitySchema); err != nil {
			transaction.Rollback()
			database.Close()
			return nil, err
		}
	}
	if version < 3 {
		if _, err = transaction.Exec(communitiesSchema); err != nil {
			transaction.Rollback()
			database.Close()
			return nil, err
		}
	}
	if err = transaction.Commit(); err != nil {
		database.Close()
		return nil, err
	}
	var foreignKeys, synchronous, busy int
	var journal string
	for query, target := range map[string]any{"PRAGMA foreign_keys": &foreignKeys, "PRAGMA synchronous": &synchronous, "PRAGMA busy_timeout": &busy, "PRAGMA journal_mode": &journal} {
		if err = database.QueryRow(query).Scan(target); err != nil {
			database.Close()
			return nil, err
		}
	}
	if foreignKeys != 1 || synchronous != 2 || busy != 2000 || journal != "wal" {
		database.Close()
		return nil, errors.New("sqlite safety settings unavailable")
	}
	if err = os.Chmod(config.Database, 0600); err != nil {
		database.Close()
		return nil, err
	}
	page, err := template.New("page").Parse(pageHTML)
	if err != nil {
		database.Close()
		return nil, err
	}
	app := &App{db: database, config: config, page: page, httpClient: &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(request *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}}
	if config.OIDCSecretFile != "" {
		secret, secretErr := os.ReadFile(config.OIDCSecretFile)
		key, keyErr := os.ReadFile(config.CSRFKeyFile)
		if secretErr != nil || keyErr != nil || len(secret) != 43 || len(key) != 32 {
			database.Close()
			return nil, errors.New("身份配置密钥不可用")
		}
		app.secret = string(secret)
		app.csrfKey = key
	}
	return app, nil
}
func (app *App) Close() error { return app.db.Close() }
func respond(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
func (app *App) Handler() http.Handler {
	router := chi.NewRouter()
	router.Get("/", func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		identity, _, _ := app.currentIdentity(request)

		type PageData struct {
			Title    string
			Page     string
			Identity *Identity
			Content  template.HTML
		}

		var contentBuf strings.Builder
		contentBuf.WriteString(`<div class="page-header">
			<h1 class="page-title">欢迎来到星图</h1>
			<p class="page-subtitle">探索互联网社群文化的公共空间</p>
		</div>
		<div class="empty-state">
			<p>动态流功能即将上线，敬请期待。</p>
			<p style="margin-top: var(--space-4);">
				<a href="/discover" style="color: var(--accent-primary);">现在就去发现社群 →</a>
			</p>
		</div>`)

		_ = app.page.Execute(writer, PageData{
			Title:    "星图 AtlasSite",
			Page:     "home",
			Identity: identity,
			Content:  template.HTML(contentBuf.String()),
		})
	})
	router.Get("/config", func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		identity, _, identityErr := app.currentIdentity(request)
		if identityErr != nil {
			respond(writer, 503, map[string]string{"code": "DEPENDENCY_UNAVAILABLE"})
			return
		}
		_ = app.page.Execute(writer, struct {
			Config
			Identity *Identity
			Enabled  bool
		}{app.config, identity, app.secret != ""})
	})
	router.Get("/discover", func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		identity, _, _ := app.currentIdentity(request)
		domains, err := app.listDomains()
		if err != nil {
			respond(writer, 503, map[string]string{"code": "DEPENDENCY_UNAVAILABLE"})
			return
		}

		type PageData struct {
			Title    string
			Page     string
			Identity *Identity
			Content  template.HTML
		}

		var contentBuf strings.Builder
		contentBuf.WriteString(`<div class="page-header">
			<h1 class="page-title">发现社群</h1>
			<p class="page-subtitle">探索由领域专家和爱好者维护的社群空间</p>
		</div>
		<div class="category-grid">`)

		for _, domain := range domains {
			directions, _ := app.listDirections(domain.ID)

			contentBuf.WriteString(`<section class="category-section">
				<div class="category-header">
					<h2 class="category-title">`)
			contentBuf.WriteString(template.HTMLEscapeString(domain.Name))
			contentBuf.WriteString(`</h2>
					<p class="category-desc">`)
			contentBuf.WriteString(template.HTMLEscapeString(domain.Description))
			contentBuf.WriteString(`</p>
				</div>`)

			if len(directions) > 0 {
				contentBuf.WriteString(`<div class="direction-list">`)
				for _, direction := range directions {
					subcategories, _ := app.listSubcategories(direction.ID)

					contentBuf.WriteString(`<div class="direction-item">
						<div class="direction-name">`)
					contentBuf.WriteString(template.HTMLEscapeString(direction.Name))
					contentBuf.WriteString(`</div>
						<div class="direction-desc">`)
					contentBuf.WriteString(template.HTMLEscapeString(direction.Description))
					contentBuf.WriteString(`</div>`)

					if len(subcategories) > 0 {
						contentBuf.WriteString(`<div class="subcategory-list">`)
						for _, subcategory := range subcategories {
							communities, _ := app.listCommunitiesBySubcategory(subcategory.ID)
							communityCount := len(communities)

							contentBuf.WriteString(`<span class="subcategory-tag" title="`)
							contentBuf.WriteString(template.HTMLEscapeString(subcategory.Description))
							contentBuf.WriteString(`">`)
							contentBuf.WriteString(template.HTMLEscapeString(subcategory.Name))
							if communityCount > 0 {
								contentBuf.WriteString(` (`)
								contentBuf.WriteString(fmt.Sprintf("%d", communityCount))
								contentBuf.WriteString(`)`)
							}
							contentBuf.WriteString(`</span>`)
						}
						contentBuf.WriteString(`</div>`)
					}

					contentBuf.WriteString(`</div>`)
				}
				contentBuf.WriteString(`</div>`)
			}

			contentBuf.WriteString(`</section>`)
		}

		contentBuf.WriteString(`</div>`)

		_ = app.page.Execute(writer, PageData{
			Title:    "发现社群",
			Page:     "discover",
			Identity: identity,
			Content:  template.HTML(contentBuf.String()),
		})
	})
	router.Get("/search", func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		identity, _, _ := app.currentIdentity(request)
		query := request.URL.Query().Get("q")

		type PageData struct {
			Title    string
			Page     string
			Identity *Identity
			Content  template.HTML
			Query    string
		}

		var contentBuf strings.Builder
		contentBuf.WriteString(`<div class="search-header">
			<div class="search-input-container">
				<svg class="search-icon" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
					<circle cx="11" cy="11" r="8"/>
					<path d="m21 21-4.35-4.35"/>
				</svg>
				<form method="get" action="/search">
					<input type="search" name="q" class="search-input" placeholder="搜索社群、内容或用户…" value="`)
		contentBuf.WriteString(template.HTMLEscapeString(query))
		contentBuf.WriteString(`" autofocus>
				</form>
			</div>
		</div>`)

		if query == "" {
			contentBuf.WriteString(`<div class="empty-state">
				<p>输入关键词开始搜索</p>
			</div>`)
		} else {
			// 搜索社群
			communities, err := app.searchCommunities(query)
			if err != nil || len(communities) == 0 {
				contentBuf.WriteString(`<div class="empty-state">
					<p>没有找到匹配"`)
				contentBuf.WriteString(template.HTMLEscapeString(query))
				contentBuf.WriteString(`"的结果</p>
				</div>`)
			} else {
				contentBuf.WriteString(`<div class="result-group">
					<div class="result-group-header">
						<h2 class="result-group-title">社群</h2>
						<span class="result-group-count">`)
				contentBuf.WriteString(fmt.Sprintf("%d", len(communities)))
				contentBuf.WriteString(` 条结果</span>
					</div>
					<div style="display: grid; grid-template-columns: repeat(auto-fill, minmax(200px, 1fr)); gap: var(--space-4);">`)

				for _, community := range communities {
					contentBuf.WriteString(`<a href="/c/`)
					contentBuf.WriteString(template.URLQueryEscaper(community.Slug))
					contentBuf.WriteString(`" class="community-card">
							<div class="community-name">`)
					contentBuf.WriteString(template.HTMLEscapeString(community.Name))
					if community.Verified {
						contentBuf.WriteString(` <span class="status-badge verified">✓</span>`)
					}
					contentBuf.WriteString(`</div>
							<div class="community-desc">`)
					contentBuf.WriteString(template.HTMLEscapeString(community.Description))
					contentBuf.WriteString(`</div>
						</a>`)
				}

				contentBuf.WriteString(`</div>
				</div>`)
			}
		}

		_ = app.page.Execute(writer, PageData{
			Title:    "搜索",
			Page:     "search",
			Identity: identity,
			Content:  template.HTML(contentBuf.String()),
			Query:    query,
		})
	})
	router.Get("/health", func(writer http.ResponseWriter, request *http.Request) {
		if err := app.db.PingContext(request.Context()); err != nil {
			respond(writer, 503, map[string]string{"code": "DEPENDENCY_UNAVAILABLE"})
			return
		}
		respond(writer, 200, map[string]any{"ok": true, "service": "star-atlas", "stage": "S03", "oidc_configured": app.secret != "", "integration": "本地合成；真实环境未联调", "real_users": false})
	})
	router.Get("/api/v1/config", func(writer http.ResponseWriter, request *http.Request) {
		respond(writer, 200, map[string]any{"service": "star-atlas", "password_owner": "star-account", "account_origin": app.config.AccountOrigin, "oidc_configured": app.secret != "", "real_users": false})
	})
	router.Get("/api/v1/session", func(writer http.ResponseWriter, request *http.Request) {
		identity, degraded, err := app.currentIdentity(request)
		if err != nil {
			respond(writer, 503, map[string]string{"code": "DEPENDENCY_UNAVAILABLE"})
			return
		}
		respond(writer, 200, map[string]any{"authenticated": identity != nil, "user": identity, "identity_service_unavailable": degraded})
	})
	for _, route := range []string{"/api/v1/dev/session"} {
		router.HandleFunc(route, func(writer http.ResponseWriter, request *http.Request) {
			respond(writer, 503, map[string]string{"code": "AUTH_NOT_CONFIGURED", "message": "星图单点登录尚未接入，请稍后再试"})
		})
	}
	app.identityRoutes(router)
	app.communityRoutes(router)
	router.Get("/robots.txt", func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		io.WriteString(writer, "User-agent: *\nDisallow: /\n")
	})
	router.NotFound(func(writer http.ResponseWriter, request *http.Request) {
		respond(writer, 404, map[string]string{"code": "NOT_FOUND"})
	})
	protected := app.protect(router)
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("X-Robots-Tag", "noindex, nofollow")
		writer.Header().Set("Referrer-Policy", "strict-origin")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("X-Frame-Options", "DENY")
		writer.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		if request.Host != app.config.Address {
			respond(writer, 400, map[string]string{"code": "HOST_INVALID"})
			return
		}
		request.Body = http.MaxBytesReader(writer, request.Body, 8192)
		protected.ServeHTTP(writer, request)
	})
}
func Server(config Config, handler http.Handler) *http.Server {
	return &http.Server{Addr: config.Address, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
}
