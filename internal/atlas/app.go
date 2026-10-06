package atlas

import (
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
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

//go:embed 004_posts.sql
var postsSchema string

//go:embed 005_user_activity.sql
var userActivitySchema string

//go:embed 006_core.sql
var coreSchema string

type Config struct {
	Mode            string `json:"mode"`
	Address         string `json:"address"`
	Origin          string `json:"origin"`
	AccountOrigin   string `json:"account_origin"`
	Database        string `json:"database"`
	OIDCSecretFile  string `json:"oidc_secret_file"`
	CSRFKeyFile     string `json:"csrf_key_file"`
	VaultDatabase   string `json:"vault_database"`
	VaultKeyFile    string `json:"vault_key_file"`
	BackupDirectory string `json:"backup_directory"`
	BackupKeyFile   string `json:"backup_key_file"`
	OpsJournalFile  string `json:"ops_journal_file"`
	WriteUntil      string `json:"write_until,omitempty"`
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
	if config.WriteUntil != "" {
		if _, err := time.Parse(time.RFC3339, config.WriteUntil); err != nil {
			return errors.New("write_until 必须为包含时区的 RFC3339 时间")
		}
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
	db                *sql.DB
	config            Config
	page              *template.Template
	secret            string
	csrfKey           []byte
	httpClient        *http.Client
	syncMu            sync.Mutex
	vault             *sql.DB
	vaultKey          []byte
	opsMu             sync.Mutex
	jobsMu            sync.Mutex
	lastCleanup       int64
	lastBackup        int64
	lastBackupAttempt int64
}

func New(config Config) (*App, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(config.Database), 0700); err != nil {
		return nil, err
	}
	database, err := sql.Open("sqlite", config.Database+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(2000)&_pragma=synchronous(FULL)&_pragma=journal_mode(WAL)&_pragma=secure_delete(ON)")
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
	if err = transaction.QueryRow("SELECT max(version) FROM schema_migrations").Scan(&version); err != nil || version < 1 || version > 6 {
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
	if version < 4 {
		if _, err = transaction.Exec(postsSchema); err != nil {
			transaction.Rollback()
			database.Close()
			return nil, err
		}
	}
	if version < 5 {
		if _, err = transaction.Exec(userActivitySchema); err != nil {
			transaction.Rollback()
			database.Close()
			return nil, err
		}
	}
	if version < 6 {
		if _, err = transaction.Exec(coreSchema); err != nil {
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
	if err = restrictPath(config.Database, false); err != nil {
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
		if err = restrictPath(config.OIDCSecretFile, false); err != nil {
			database.Close()
			return nil, err
		}
		if err = restrictPath(config.CSRFKeyFile, false); err != nil {
			database.Close()
			return nil, err
		}
		app.secret = string(secret)
		app.csrfKey = key
	}
	if len(app.csrfKey) == 0 {
		app.csrfKey, err = restrictedKey(config.Database + ".csrf.key")
		if err != nil {
			app.Close()
			return nil, err
		}
	}
	if err := app.initVault(); err != nil {
		app.Close()
		return nil, err
	}
	if err := app.reconcileJournal(); err != nil {
		app.Close()
		return nil, err
	}
	return app, nil
}
func (app *App) Close() error {
	if app.vault != nil {
		app.vault.Close()
	}
	return app.db.Close()
}
func respond(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
func (app *App) Handler() http.Handler {
	router := chi.NewRouter()
	router.Get("/", app.homePage)
	router.Get("/search", app.searchPage)
	router.Get("/config", func(w http.ResponseWriter, r *http.Request) {
		app.renderPage(w, r, "配置清单", "config", `<h1>本地测试配置</h1><p>当前仅开放本地合成验收，真实用户尚未接入。</p><p>登录凭据由新星账户持有；图片经星图鉴权，草稿及敏感材料独立加密保存。</p>`)
	})
	router.Get("/health", func(writer http.ResponseWriter, request *http.Request) {
		if err := app.db.PingContext(request.Context()); err != nil {
			respond(writer, 503, map[string]string{"code": "DEPENDENCY_UNAVAILABLE"})
			return
		}
		respond(writer, 200, map[string]any{"ok": true, "service": "star-atlas", "stage": "core-local", "oidc_configured": app.secret != "", "integration": "本地合成；真实环境未联调", "real_users": false})
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
	app.uiRoutes(router)
	app.identityRoutes(router)
	app.communityRoutes(router)
	app.postsRoutes(router)
	app.userRoutes(router)
	app.contentRoutes(router)
	app.rightsRoutes(router)
	app.mediaRoutes(router)
	app.notificationRoutes(router)
	app.subscriptionRoutes(router)
	app.operationsRoutes(router)
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
		writer.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'; img-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		if request.Host != app.config.Address {
			respond(writer, 400, map[string]string{"code": "HOST_INVALID"})
			return
		}
		limit := int64(384 * 1024)
		if request.URL.Path == "/api/v1/media" {
			limit = (8 * 1024 * 1024) + (128 * 1024)
		}
		request.Body = http.MaxBytesReader(writer, request.Body, limit)
		app.browserErrors(app.maintenanceGate(protected)).ServeHTTP(writer, request)
	})
}
func Server(config Config, handler http.Handler) *http.Server {
	return &http.Server{Addr: config.Address, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 75 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
}
