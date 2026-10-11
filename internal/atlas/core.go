package atlas

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/gorilla/csrf"
)

const visiblePostSQL = `p.status='published' AND NOT EXISTS(SELECT 1 FROM restrictions x WHERE x.object_id=p.id AND x.removed_at IS NULL) AND (p.community_id IS NULL OR EXISTS(SELECT 1 FROM communities c0 WHERE c0.id=p.community_id AND c0.status IN ('active','uncategorized'))) AND EXISTS(SELECT 1 FROM users u0 JOIN user_settings s0 ON s0.user_id=u0.id WHERE u0.id=p.author_id AND (u0.status='active' OR (u0.status IN ('deactivated','deleted') AND (s0.retain_content=1 OR EXISTS(SELECT 1 FROM retained_objects ro WHERE ro.user_id=u0.id AND ro.object_id=p.id)))))`

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}
func esc(value string) string    { return template.HTMLEscapeString(value) }
func pathID(value string) string { return url.PathEscape(value) }
func integer(value string) int   { n, _ := strconv.Atoi(value); return n }
func validLicense(value string) bool {
	return value == "reserved" || value == "CC BY 4.0" || value == "CC BY-SA 4.0"
}
func checkText(value string, limit int) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= limit && !strings.ContainsRune(value, 0)
}
func (app *App) requireIdentity(w http.ResponseWriter, r *http.Request, sensitive bool) *Identity {
	identity, degraded, err := app.currentIdentity(r)
	if err != nil || (sensitive && degraded) {
		respond(w, 503, map[string]string{"code": "IDENTITY_UNAVAILABLE"})
		return nil
	}
	if identity == nil {
		if r.Method == "GET" && strings.Contains(r.Header.Get("Accept"), "text/html") {
			http.Redirect(w, r, "/login", 303)
			return nil
		}
		respond(w, 401, map[string]string{"code": "SESSION_REQUIRED"})
		return nil
	}
	if sensitive && identity.ReauthenticatedAt < time.Now().Add(-5*time.Minute).Unix() {
		respond(w, 403, map[string]string{"code": "REAUTH_REQUIRED", "message": "请先在登录与安全页面重新确认身份"})
		return nil
	}
	return identity
}
func (app *App) writeAllowed(owner string) bool {
	var mode, status string
	if app.db.QueryRow("SELECT mode FROM runtime_state WHERE id=1").Scan(&mode) != nil || mode != "normal" {
		return false
	}
	return app.db.QueryRow("SELECT status FROM users WHERE id=?", owner).Scan(&status) == nil && status == "active"
}
func (app *App) contentIdentity(w http.ResponseWriter, r *http.Request) *Identity {
	i, degraded, err := app.currentIdentity(r)
	if err != nil || degraded {
		respond(w, 503, map[string]string{"code": "IDENTITY_UNAVAILABLE", "message": "账户服务暂时不可用，投稿与互动暂停"})
		return nil
	}
	if i == nil {
		respond(w, 401, map[string]string{"code": "SESSION_REQUIRED"})
		return nil
	}
	if !app.writeAllowed(i.ID) {
		respond(w, 503, map[string]string{"code": "READ_ONLY", "message": "当前暂停投稿，举报、申诉和退出入口继续可用"})
		return nil
	}
	return i
}
func (app *App) blocked(a, b string) bool {
	if a == "" || b == "" || a == b {
		return false
	}
	var n int
	if app.db.QueryRow("SELECT count(*) FROM blocks WHERE (owner_id=? AND target_id=?) OR (owner_id=? AND target_id=?)", a, b, b, a).Scan(&n) != nil {
		return true
	}
	return n > 0
}
func (app *App) postVisible(id, viewer string) bool {
	var owner string
	if app.db.QueryRow("SELECT p.author_id FROM posts p WHERE p.id=? AND "+visiblePostSQL, id).Scan(&owner) != nil {
		return false
	}
	return !app.blocked(viewer, owner)
}
func (app *App) renderPage(w http.ResponseWriter, r *http.Request, title, page, content string) {
	app.renderPageWithMore(w, r, title, page, content, "")
}
func (app *App) renderPageWithMore(w http.ResponseWriter, r *http.Request, title, page, content, more string) {
	identity, _, err := app.currentIdentity(r)
	if err != nil {
		respond(w, 503, map[string]string{"code": "DEPENDENCY_UNAVAILABLE"})
		return
	}
	var buf bytes.Buffer
	err = app.page.Execute(&buf, struct {
		Title, Page string
		Identity    *Identity
		Content     template.HTML
		More        template.HTML
	}{title, page, identity, template.HTML(content), template.HTML(more)})
	if err != nil {
		respond(w, 503, map[string]string{"code": "RENDER_FAILED"})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(buf.Bytes())
}
func formStart(r *http.Request, action string) string {
	return `<form method="post" action="` + esc(action) + `">` + string(csrf.TemplateField(r))
}
func field(name, value string) string {
	return `<input type="hidden" name="` + esc(name) + `" value="` + esc(value) + `">`
}
func (app *App) homePage(w http.ResponseWriter, r *http.Request) {
	posts, err := app.listRecentPosts(50)
	if err != nil {
		respond(w, 503, map[string]string{"code": "DEPENDENCY_UNAVAILABLE"})
		return
	}
	i, _, _ := app.currentIdentity(r)
	viewer := ""
	body := `<h1 class="sr-only">最新发布</h1>`
	if i != nil {
		viewer = i.ID
		body += strings.Replace(formStart(r, "/new"), `<form `, `<form class="composer" `, 1) + `<label for="composer-input" class="sr-only">发布内容</label><textarea id="composer-input" class="composer-textarea" name="content" maxlength="50000" placeholder="分享你的想法、创作或观察…" aria-label="发布内容"></textarea><div class="composer-toolbar"><div class="composer-meta"><a class="meta-tag" href="/new#post-community">＋ 关联社群</a><a class="meta-tag" href="/new#post-image">＋ 上传图片</a><a class="meta-tag" href="/my/drafts">草稿</a></div><button class="btn" type="submit">继续编辑</button></div></form>`
	} else {
		body += `<div class="composer"><p>分享你的想法、创作或观察…</p><div class="composer-toolbar"><a class="btn" href="/login">登录后发布</a></div></div>`
	}
	body += `<div class="feed" role="feed">` + app.postCards(posts, viewer, r) + `</div>`
	app.renderPage(w, r, "最新发布", "home", body)
}

func (app *App) postCards(posts []Post, viewer string, r *http.Request) string {
	blockedAuthors := map[string]bool{}
	if viewer != "" {
		rows, err := app.db.Query("SELECT CASE WHEN owner_id=? THEN target_id ELSE owner_id END FROM blocks WHERE owner_id=? OR target_id=?", viewer, viewer, viewer)
		if err != nil {
			return `<p>内容暂时不可用。</p>`
		}
		for rows.Next() {
			var id string
			rows.Scan(&id)
			blockedAuthors[id] = true
		}
		rows.Close()
	}
	bookmarks := map[string]bool{}
	if viewer != "" && len(posts) > 0 {
		args := []any{viewer}
		for _, p := range posts {
			args = append(args, p.ID)
		}
		rows, err := app.db.Query("SELECT post_id FROM post_bookmarks WHERE identity_id=? AND post_id IN ("+strings.TrimSuffix(strings.Repeat("?,", len(posts)), ",")+")", args...)
		if err != nil {
			return `<p>内容暂时不可用。</p>`
		}
		for rows.Next() {
			var id string
			if rows.Scan(&id) != nil {
				rows.Close()
				return `<p>内容暂时不可用。</p>`
			}
			bookmarks[id] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return `<p>内容暂时不可用。</p>`
		}
	}
	var b strings.Builder
	for _, p := range posts {
		if blockedAuthors[p.AuthorID] || p.Status != "published" {
			continue
		}
		b.WriteString(`<article class="post"><header class="post-header"><span class="avatar avatar-small" aria-hidden="true">` + esc(avatarInitial(p.AuthorName)) + `</span><span class="post-author">` + publicAuthor(p) + `</span>`)
		if p.CommunityID != "" {
			b.WriteString(`<span class="post-meta">发布于</span><a class="post-community" href="/c/` + pathID(p.CommunitySlug) + `">` + esc(p.CommunityName) + `</a>`)
		}
		b.WriteString(`<time class="post-meta post-time" datetime="` + p.CreatedAt.UTC().Format(time.RFC3339) + `" title="` + esc(p.CreatedAt.Format("2006-01-02 15:04:05 -07:00")) + `">` + formatTime(p.CreatedAt) + `</time></header><h2 class="post-title"><a href="/p/` + pathID(p.ID) + `">` + esc(p.Title) + `</a></h2><p class="post-content">` + esc(markdownExcerpt(p.Content, 180)) + `</p><div class="post-actions"><a class="action-btn" aria-label="查看回复" href="/p/` + pathID(p.ID) + `#reply-composer">` + uiIcon("reply") + `<span>` + strconv.Itoa(p.ReplyCount) + `</span></a>`)
		if viewer != "" {
			b.WriteString(stateForm(r, "/api/v1/p/"+pathID(p.ID)+"/bookmark", "bookmarked", bookmarks[p.ID], "收藏", "取消收藏", uiIcon("bookmark")+`<span data-action-count>`+strconv.Itoa(p.BookmarkCount)+`</span>`, "action-btn"))
		} else {
			b.WriteString(`<a class="action-btn" aria-label="登录后收藏" title="登录后收藏" href="/login">` + uiIcon("bookmark") + `<span>` + strconv.Itoa(p.BookmarkCount) + `</span></a>`)
		}
		b.WriteString(`<a class="action-btn" aria-label="打开内容" href="/p/` + pathID(p.ID) + `">` + uiIcon("more") + `</a></div></article>`)
	}
	if b.Len() == 0 {
		return emptyState("这里还没有可显示的内容", "发布一条动态，或者先去发现感兴趣的社群。", "/discover", "发现社群")
	}
	return b.String()
}
func excerpt(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func restrictedKey(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err == nil {
		if err = restrictPath(path, false); err != nil {
			return nil, err
		}
		if len(b) != 32 {
			return nil, errors.New("invalid restricted key")
		}
		return b, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	b = make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	if err = restrictPath(path, false); err != nil {
		f.Close()
		return nil, err
	}
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return nil, err
	}
	return b, closeErr
}
func encrypt(key, data []byte, aad string) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	return g.Seal(nonce, nonce, data, []byte(aad)), nil
}
func decrypt(key, data []byte, aad string) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(data) < g.NonceSize() {
		return nil, errors.New("invalid encrypted object")
	}
	return g.Open(nil, data[:g.NonceSize()], data[g.NonceSize():], []byte(aad))
}
func (app *App) initVault() error {
	if app.config.VaultKeyFile == "" {
		app.config.VaultKeyFile = app.config.Database + ".vault.key"
	}
	if app.config.OpsJournalFile == "" {
		app.config.OpsJournalFile = app.config.Database + ".operations.jsonl"
	}
	if app.config.BackupDirectory == "" {
		app.config.BackupDirectory = app.config.Database + ".backups"
	}
	if app.config.BackupKeyFile == "" {
		app.config.BackupKeyFile = app.config.Database + ".backup.key"
	}
	key, err := restrictedKey(app.config.VaultKeyFile)
	if err != nil {
		return err
	}
	app.vaultKey = key
	if app.config.VaultDatabase == "" {
		app.config.VaultDatabase = app.config.Database + ".private.db"
	}
	app.vault, err = sql.Open("sqlite", app.config.VaultDatabase+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(2000)&_pragma=synchronous(FULL)&_pragma=journal_mode(WAL)&_pragma=secure_delete(ON)")
	if err != nil {
		return err
	}
	app.vault.SetMaxOpenConns(1)
	_, err = app.vault.Exec(`CREATE TABLE IF NOT EXISTS objects(id TEXT PRIMARY KEY,owner_id TEXT NOT NULL,kind TEXT NOT NULL,body BLOB NOT NULL,expires_at INTEGER NOT NULL);CREATE TABLE IF NOT EXISTS media_keys(id TEXT PRIMARY KEY,key BLOB NOT NULL);`)
	if err != nil {
		return err
	}
	if err = restrictPath(app.config.VaultDatabase, false); err != nil {
		return err
	}
	if _, e := os.Stat(app.config.OpsJournalFile); os.IsNotExist(e) {
		var n int
		if app.db.QueryRow("SELECT count(*) FROM operation_events").Scan(&n) != nil || n > 0 {
			return errors.New("operation journal missing")
		}
		if err = os.MkdirAll(filepath.Dir(app.config.OpsJournalFile), 0700); err != nil {
			return err
		}
		if err = exclusiveFile(app.config.OpsJournalFile, []byte{}); err != nil {
			return err
		}
		if err = os.MkdirAll(app.config.OpsJournalFile+".anchors", 0700); err != nil {
			return err
		}
	}
	return nil
}
func (app *App) privatePut(id, owner, kind string, value any, expires int64) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	sealed, err := encrypt(app.vaultKey, raw, id+":"+owner+":"+kind)
	if err != nil {
		return err
	}
	_, err = app.vault.Exec("INSERT INTO objects VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body,expires_at=excluded.expires_at WHERE objects.owner_id=excluded.owner_id AND objects.kind=excluded.kind", id, owner, kind, sealed, expires)
	return err
}
func (app *App) privateGet(id, owner, kind string, value any) error {
	var raw []byte
	err := app.vault.QueryRow("SELECT body FROM objects WHERE id=? AND owner_id=? AND kind=? AND (expires_at=0 OR expires_at>?)", id, owner, kind, time.Now().Unix()).Scan(&raw)
	if err != nil {
		return err
	}
	raw, err = decrypt(app.vaultKey, raw, id+":"+owner+":"+kind)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, value)
}
func fail(w http.ResponseWriter, err error) {
	code, status := "DEPENDENCY_UNAVAILABLE", 503
	if errors.Is(err, sql.ErrNoRows) {
		code, status = "NOT_FOUND", 404
	}
	if errors.Is(err, errConflict) {
		code, status = "VERSION_CONFLICT", 409
	}
	if errors.Is(err, errInput) {
		code, status = "INPUT_INVALID", 400
	}
	if errors.Is(err, errForbidden) {
		code, status = "FORBIDDEN", 403
	}
	respond(w, status, map[string]string{"code": code})
}

var errConflict = errors.New("version conflict")
var errInput = errors.New("invalid input")
var errForbidden = errors.New("forbidden")

func uuidRequest(s string) bool { _, err := uuid.Parse(s); return err == nil }
func (app *App) rateLimit(w http.ResponseWriter, r *http.Request, owner string, limit int) bool {
	key := "content:" + owner
	now := time.Now().Unix()
	_, err := app.db.Exec(`INSERT INTO auth_limits(key,count,reset_at) VALUES(?,1,?) ON CONFLICT(key) DO UPDATE SET count=CASE WHEN reset_at<=? THEN 1 ELSE count+1 END,reset_at=CASE WHEN reset_at<=? THEN excluded.reset_at ELSE reset_at END`, key, now+900, now, now)
	var count int
	if err != nil || app.db.QueryRow("SELECT count FROM auth_limits WHERE key=?", key).Scan(&count) != nil {
		fail(w, errors.New("limit unavailable"))
		return false
	}
	if count > limit {
		w.Header().Set("Retry-After", "900")
		respond(w, 429, map[string]string{"code": "RATE_LIMITED"})
		return false
	}
	return true
}
func versionField(n int) string { return field("version", fmt.Sprint(n)) }

func publicAuthor(p Post) string {
	if p.AuthorStatus == "deactivated" || p.AuthorStatus == "deleted" {
		return esc(p.AuthorName) + "（已注销）"
	}
	return `<a href="/u/` + pathID(p.AuthorID) + `">` + esc(p.AuthorName) + `</a>`
}
