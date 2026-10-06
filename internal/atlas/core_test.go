package atlas

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type coreClient struct {
	app                  *App
	owner, session, csrf string
	cookies              []*http.Cookie
}

func coreApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/internal/identity/events":
			respond(w, 200, map[string]any{"schema_version": 1, "events": []identityEvent{}})
		case "/internal/identity/session":
			respond(w, 200, map[string]any{"active": true, "status": "active", "status_version": 1, "expires_at": time.Now().Add(time.Hour).Unix()})
		case "/internal/identity/profile":
			respond(w, 200, publicProfile{DisplayName: "资料名称", Bio: "资料简介"})
		case "/internal/identity/deactivate":
			respond(w, 200, map[string]string{"code": "DEACTIVATED"})
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(remote.Close)
	secret := filepath.Join(dir, "secret")
	csrfPath := filepath.Join(dir, "csrf")
	key := make([]byte, 32)
	rand.Read(key)
	os.WriteFile(secret, []byte(token()), 0600)
	os.WriteFile(csrfPath, key, 0600)
	app, err := New(Config{Mode: "test", Address: "127.0.0.1:4200", Origin: "http://127.0.0.1:4200", AccountOrigin: remote.URL, Database: filepath.Join(dir, "atlas.db"), OIDCSecretFile: secret, CSRFKeyFile: csrfPath})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.Close() })
	return app
}
func coreUser(t *testing.T, app *App, name string) *coreClient {
	t.Helper()
	owner, account, sid, session := randomID(), randomID(), randomID(), token()
	_, err := app.db.Exec("INSERT INTO users VALUES(?,?,?,?, 'active',1,?)", owner, account, randomID(), name, app.config.AccountOrigin)
	if err != nil {
		t.Fatal(err)
	}
	_, err = app.db.Exec("INSERT INTO sessions(id,token_hash,user_id,account_session_id,created_at,expires_at,auth_time,reauthenticated_at) VALUES(?,?,?,?,?,?,?,?)", randomID(), digest(session), owner, sid, time.Now().Unix(), time.Now().Add(time.Hour).Unix(), time.Now().Unix(), time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	c := &coreClient{app: app, owner: owner, session: session, cookies: []*http.Cookie{{Name: "star_atlas_session", Value: session}}}
	response := c.call("GET", "/api/v1/csrf", nil, true)
	var value map[string]string
	if json.Unmarshal(response.Body.Bytes(), &value) != nil {
		t.Fatal("csrf response invalid")
	}
	c.csrf = value["csrf_token"]
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == "star_atlas_csrf" {
			c.cookies = append(c.cookies, cookie)
		}
	}
	return c
}
func (c *coreClient) call(method, path string, values url.Values, validCSRF bool) *httptest.ResponseRecorder {
	var body io.Reader
	if values != nil {
		body = strings.NewReader(values.Encode())
	}
	r := httptest.NewRequest(method, c.app.config.Origin+path, body)
	for _, cookie := range c.cookies {
		r.AddCookie(cookie)
	}
	if method == "POST" {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if strings.HasPrefix(path, "/api/v1/drafts/") && !strings.HasSuffix(path, "/delete") {
			r.Header.Set("Accept", "application/json")
		}
		r.Header.Set("Origin", c.app.config.Origin)
		if validCSRF {
			r.Header.Set("X-CSRF-Token", c.csrf)
		}
	}
	w := httptest.NewRecorder()
	c.app.Handler().ServeHTTP(w, r)
	return w
}
func mustStatus(t *testing.T, w *httptest.ResponseRecorder, want int) {
	t.Helper()
	if w.Code != want {
		t.Fatalf("status=%d want=%d body=%s", w.Code, want, w.Body.String())
	}
}
func publishFor(t *testing.T, c *coreClient, title, content string) string {
	t.Helper()
	w := c.call("POST", "/api/v1/posts", url.Values{"title": {title}, "content": {content}, "license": {"reserved"}, "request_id": {randomID()}}, true)
	mustStatus(t, w, 303)
	return strings.TrimPrefix(w.Header().Get("Location"), "/p/")
}

func TestCoreCanonicalIdentityCSRFAndIdempotency(t *testing.T) {
	app := coreApp(t)
	c := coreUser(t, app, "登录作者")
	form := c.call("GET", "/new", nil, true)
	mustStatus(t, form, 200)
	if !strings.Contains(form.Body.String(), "gorilla.csrf.Token") || !strings.Contains(form.Body.String(), `src="/assets/core.js"`) {
		t.Fatal("CSRF form or external script missing")
	}
	values := url.Values{"title": {"独立动态"}, "content": {"无需社群"}, "license": {"reserved"}, "request_id": {randomID()}}
	mustStatus(t, c.call("POST", "/api/v1/posts", values, false), 403)
	w := c.call("POST", "/api/v1/posts", values, true)
	mustStatus(t, w, 303)
	id := strings.TrimPrefix(w.Header().Get("Location"), "/p/")
	mustStatus(t, c.call("POST", "/api/v1/posts", values, true), 303)
	var count int
	app.db.QueryRow("SELECT count(*) FROM posts").Scan(&count)
	if count != 1 {
		t.Fatal("duplicate publish")
	}
	values.Set("content", "不同正文")
	mustStatus(t, c.call("POST", "/api/v1/posts", values, true), 409)
	mustStatus(t, c.call("GET", "/u/"+c.owner, nil, true), 200)
	mustStatus(t, c.call("GET", "/u/"+c.owner+"/profile", nil, true), 200)
	for n := 0; n < 2; n++ {
		mustStatus(t, c.call("POST", "/api/v1/p/"+id+"/bookmark", url.Values{"bookmarked": {"true"}}, true), 303)
	}
	app.db.QueryRow("SELECT bookmark_count FROM posts WHERE id=?", id).Scan(&count)
	if count != 1 {
		t.Fatal("duplicate bookmark count")
	}
	for n := 0; n < 2; n++ {
		mustStatus(t, c.call("POST", "/api/v1/p/"+id+"/bookmark", url.Values{"bookmarked": {"false"}}, true), 303)
	}
	app.db.QueryRow("SELECT bookmark_count FROM posts WHERE id=?", id).Scan(&count)
	if count != 0 {
		t.Fatal("bookmark removal not idempotent")
	}
}
func TestCorePermissionsVersionsDraftsAndBlocks(t *testing.T) {
	app := coreApp(t)
	a, b := coreUser(t, app, "作者A"), coreUser(t, app, "作者B")
	id := publishFor(t, a, "原题", "原文")
	edit := url.Values{"title": {"新题"}, "content": {"新文"}, "license": {"reserved"}, "version": {"1"}}
	mustStatus(t, b.call("POST", "/api/v1/p/"+id+"/edit", edit, true), 403)
	mustStatus(t, a.call("POST", "/api/v1/p/"+id+"/edit", edit, true), 303)
	mustStatus(t, a.call("POST", "/api/v1/p/"+id+"/edit", edit, true), 409)
	draft := randomID()
	payload := url.Values{"title": {"私人标题"}, "content": {"私人正文唯一"}, "license": {"reserved"}, "version": {"0"}}
	mustStatus(t, a.call("POST", "/api/v1/drafts/"+draft, payload, true), 200)
	mustStatus(t, b.call("GET", "/drafts/"+draft, nil, true), 404)
	mustStatus(t, a.call("GET", "/drafts/"+draft, nil, true), 200)
	mustStatus(t, a.call("POST", "/api/v1/drafts/"+draft, payload, true), 409)
	var raw []byte
	app.vault.QueryRow("SELECT body FROM objects WHERE kind='draft' AND owner_id=?", a.owner).Scan(&raw)
	if bytes.Contains(raw, []byte("私人正文唯一")) {
		t.Fatal("draft stored unencrypted")
	}
	var tables string
	app.db.QueryRow("SELECT group_concat(sql) FROM sqlite_master").Scan(&tables)
	if strings.Contains(tables, "私人正文唯一") {
		t.Fatal("draft in main DB")
	}
	mustStatus(t, a.call("POST", "/api/v1/blocks/"+b.owner, url.Values{"blocked": {"true"}}, true), 303)
	mustStatus(t, b.call("POST", "/api/v1/p/"+id+"/replies", url.Values{"content": {"越过屏蔽"}, "request_id": {randomID()}}, true), 404)
	mustStatus(t, a.call("POST", "/api/v1/drafts/"+draft+"/delete", url.Values{}, true), 303)
	mustStatus(t, a.call("GET", "/drafts/"+draft, nil, true), 404)
}
func TestCoreReplyOwnershipAndHiddenAncestors(t *testing.T) {
	app := coreApp(t)
	a, b := coreUser(t, app, "A"), coreUser(t, app, "B")
	p1, p2 := publishFor(t, a, "帖子一", "正文"), publishFor(t, a, "帖子二", "正文")
	parent, err := app.saveReply(p1, a.owner, "父回复", "", randomID())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = app.saveReply(p2, b.owner, "跨帖回复", parent.ID, randomID()); err == nil {
		t.Fatal("cross-post parent accepted")
	}
	child, err := app.saveReply(p1, b.owner, "子回复", parent.ID, randomID())
	if err != nil {
		t.Fatal(err)
	}
	if err = app.applyOperation(operation{EventID: randomID(), ObjectID: parent.ID, Action: "delete-reply", CreatedAt: time.Now().Unix()}); err != nil {
		t.Fatal(err)
	}
	if app.replyVisible(child.ID, a.owner) {
		t.Fatal("child remained visible after parent delete")
	}
	detail := a.call("GET", "/p/"+p1, nil, true)
	mustStatus(t, detail, 200)
	if strings.Contains(detail.Body.String(), "子回复") {
		t.Fatal("hidden child text rendered")
	}
}
func TestCoreImagesDeletionSearchNotificationsAndETag(t *testing.T) {
	app := coreApp(t)
	a, b := coreUser(t, app, "A"), coreUser(t, app, "B")
	var imageData bytes.Buffer
	png.Encode(&imageData, image.NewRGBA(image.Rect(0, 0, 4, 6)))
	upload := func(filename string, data []byte) *httptest.ResponseRecorder {
		var body bytes.Buffer
		m := multipart.NewWriter(&body)
		f, _ := m.CreateFormFile("image", filename)
		f.Write(data)
		m.WriteField("license", "reserved")
		m.Close()
		r := httptest.NewRequest("POST", app.config.Origin+"/api/v1/media", &body)
		r.Header.Set("Content-Type", m.FormDataContentType())
		r.Header.Set("Accept", "application/json")
		r.Header.Set("X-CSRF-Token", a.csrf)
		r.Header.Set("Origin", app.config.Origin)
		for _, c := range a.cookies {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		app.Handler().ServeHTTP(w, r)
		return w
	}
	mustStatus(t, upload("bad.jpg", imageData.Bytes()), 400)
	mustStatus(t, upload("bad.png", []byte("broken")), 400)
	w := upload("valid.png", imageData.Bytes())
	mustStatus(t, w, 201)
	var result map[string]any
	json.Unmarshal(w.Body.Bytes(), &result)
	media := result["id"].(string)
	mustStatus(t, b.call("GET", "/media/"+media, nil, true), 404)
	mustStatus(t, a.call("GET", "/media/"+media, nil, true), 200)
	id := publishFor(t, a, "图片检索唯一", "![自有图](/media/"+media+")")
	mustStatus(t, b.call("GET", "/media/"+media+"/thumbnail", nil, true), 200)
	_, err := app.saveReply(id, b.owner, "回复唯一", "", randomID())
	if err != nil {
		t.Fatal(err)
	}
	n := a.call("GET", "/notifications", nil, true)
	if !strings.Contains(n.Body.String(), "新回复") {
		t.Fatal("reply notification missing")
	}
	mustStatus(t, a.call("POST", "/api/v1/p/"+id+"/delete", url.Values{"version": {"1"}}, true), 303)
	mustStatus(t, a.call("GET", "/p/"+id, nil, true), 404)
	mustStatus(t, b.call("GET", "/media/"+media, nil, true), 404)
	mustStatus(t, b.call("GET", "/media/"+media+"/thumbnail", nil, true), 404)
	for _, path := range []string{"/search?q=图片检索唯一", "/notifications", "/u/" + b.owner} {
		w := a.call("GET", path, nil, true)
		if strings.Contains(w.Body.String(), `href="/p/`+id+`"`) || strings.Contains(w.Body.String(), "回复唯一") || strings.Contains(w.Body.String(), "新回复") {
			t.Fatalf("hidden text leaked in %s", path)
		}
	}
}
func TestCoreOwnExportCasesAndReadOnly(t *testing.T) {
	app := coreApp(t)
	a, b := coreUser(t, app, "A"), coreUser(t, app, "B")
	publishFor(t, a, "本人内容", "本人正文")
	publishFor(t, b, "他人私人标记", "他人正文")
	request := url.Values{"kind": {"report"}, "request_id": {randomID()}, "detail": {"受限举报原件唯一"}}
	w := a.call("POST", "/api/v1/cases", request, true)
	mustStatus(t, w, 200)
	var id string
	app.db.QueryRow("SELECT id FROM cases WHERE user_id=?", a.owner).Scan(&id)
	mustStatus(t, b.call("GET", "/my/cases/"+id, nil, true), 404)
	exported := a.call("POST", "/api/v1/me/export", url.Values{}, true)
	mustStatus(t, exported, 200)
	reader, err := zip.NewReader(bytes.NewReader(exported.Body.Bytes()), int64(exported.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	file, err := reader.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(file)
	file.Close()
	if !bytes.Contains(raw, []byte("本人正文")) || bytes.Contains(raw, []byte("他人正文")) || bytes.Contains(raw, []byte("受限举报原件唯一")) {
		t.Fatal("export scope violated")
	}
	app.SetMaintenance("readonly")
	mustStatus(t, a.call("POST", "/api/v1/posts", url.Values{}, true), 503)
	mustStatus(t, a.call("POST", "/api/v1/me/export", url.Values{}, true), 200)
	request.Set("request_id", randomID())
	mustStatus(t, a.call("POST", "/api/v1/cases", request, true), 200)
}
func TestCoreEncryptedBackupRestoreReplaysDeletionAndRevokesSessions(t *testing.T) {
	app := coreApp(t)
	a := coreUser(t, app, "A")
	id := publishFor(t, a, "恢复前的标题", "恢复前的正文")
	manifest, err := app.Backup()
	if err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(app.config.BackupDirectory, manifest.Archive)
	raw, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("恢复前的正文")) {
		t.Fatal("unencrypted backup")
	}
	if err = app.applyOperation(operation{EventID: randomID(), ObjectID: id, Action: "delete-post", CreatedAt: time.Now().Unix()}); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "restored.db")
	restoredAt := time.Now()
	if err = RestoreBackup(app.config, archive, destination); err != nil {
		t.Fatal(err)
	}
	t.Logf("ISOLATED_RESTORE elapsed_ms=%d", time.Since(restoredAt).Milliseconds())
	db, err := sql.Open("sqlite", destination)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var status, content, mode string
	db.QueryRow("SELECT status,content FROM posts WHERE id=?", id).Scan(&status, &content)
	if status != "deleted" || content != "" {
		t.Fatal("deletion resurrected")
	}
	db.QueryRow("SELECT mode FROM runtime_state").Scan(&mode)
	if mode != "readonly" {
		t.Fatal("restore writable")
	}
	var sessions int
	db.QueryRow("SELECT count(*) FROM sessions WHERE revoked_at IS NULL").Scan(&sessions)
	if sessions != 0 {
		t.Fatal("session resurrected")
	}
	if err = RestoreBackup(app.config, archive, destination); err == nil {
		t.Fatal("restore overwrote an existing database")
	}
}
func TestCoreMigrationPreservesLegacyContentAndForeignKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, migration := range []string{schema, identitySchema, communitiesSchema, postsSchema, userActivitySchema} {
		if _, err = db.Exec(migration); err != nil {
			t.Fatal(err)
		}
	}
	_, err = db.Exec("INSERT INTO identities VALUES('old','旧作者','old@example.test',1,1); INSERT INTO communities VALUES('c','old-c','旧社群','说明',NULL,'active','','','',0,1,1); INSERT INTO posts VALUES('p','c','old','旧题','旧正文','published',0,2,1,1,1); INSERT INTO replies VALUES('r1','p',NULL,'old','回复一',0,'published',1,1); INSERT INTO replies VALUES('r2','p','r1','old','回复二',1,'published',1,1);INSERT INTO post_bookmarks VALUES('b','p','old',1);")
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	app, err := New(Config{Mode: "test", Address: "127.0.0.1:4200", Origin: "http://127.0.0.1:4200", AccountOrigin: "http://127.0.0.1:4100", Database: path})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	p, err := app.getPost("p")
	if err != nil || p.Content != "旧正文" || p.AuthorName != "旧作者" {
		t.Fatal("legacy post lost")
	}
	replies, err := app.listReplies("p")
	if err != nil || len(replies) != 2 {
		t.Fatal("legacy replies lost")
	}
	bookmarks, err := app.listBookmarks("old", 10)
	if err != nil || len(bookmarks) != 1 {
		t.Fatal("legacy bookmark lost")
	}
	rows, err := app.db.Query("PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("migration foreign key failure")
	}
}
