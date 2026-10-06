package atlas

import (
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

type failedIdentityTransport struct{}

func (failedIdentityTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("synthetic outage")
}
func TestCoreIdentityOutageStopsContentWritesKeepsReadAndRights(t *testing.T) {
	app := coreApp(t)
	c := coreUser(t, app, "本人")
	post := publishFor(t, c, "已发表内容", "正文")
	app.httpClient = &http.Client{Transport: failedIdentityTransport{}}
	mustStatus(t, c.call("GET", "/p/"+post, nil, true), 200)
	mustStatus(t, c.call("POST", "/api/v1/posts", url.Values{}, true), 503)
	mustStatus(t, c.call("POST", "/api/v1/cases", url.Values{"kind": {"emergency"}, "request_id": {randomID()}, "detail": {"合成紧急请求"}}, true), 200)
	mustStatus(t, c.call("POST", "/api/v1/me/export", url.Values{}, true), 503)
}
func TestCoreAnonymousEmergencyCSRFWithoutOIDC(t *testing.T) {
	app, err := New(Config{Mode: "test", Address: "127.0.0.1:4200", Origin: "http://127.0.0.1:4200", AccountOrigin: "http://127.0.0.1:4100", Database: filepath.Join(t.TempDir(), "atlas.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	handler := app.Handler()
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, httptest.NewRequest("GET", app.config.Origin+"/help/emergency", nil))
	mustStatus(t, page, 200)
	match := regexp.MustCompile(`name="gorilla.csrf.Token" value="([^"]+)"`).FindStringSubmatch(page.Body.String())
	if len(match) != 2 {
		t.Fatal("anonymous form missing CSRF")
	}
	values := url.Values{"kind": {"emergency"}, "request_id": {randomID()}, "detail": {"匿名合成请求"}}
	call := func(valid bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", app.config.Origin+"/api/v1/cases", strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", app.config.Origin)
		if valid {
			r.Header.Set("X-CSRF-Token", match[1])
			for _, cookie := range page.Result().Cookies() {
				r.AddCookie(cookie)
			}
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	mustStatus(t, call(false), 403)
	mustStatus(t, call(true), 200)
}
func TestCoreEditingWithdrawsOldImageAndRestoreDoesNotReviveIt(t *testing.T) {
	app := coreApp(t)
	c := coreUser(t, app, "作者")
	media := randomID()
	key := make([]byte, 32)
	for n := range key {
		key[n] = byte(n + 1)
	}
	body, err := encrypt(key, []byte("synthetic media bytes"), media+":image")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = app.vault.Exec("INSERT INTO media_keys VALUES(?,?)", media, key); err != nil {
		t.Fatal(err)
	}
	if _, err = app.db.Exec("INSERT INTO media(id,owner_id,mime,width,height,license,body,thumbnail,created_at) VALUES(?,?,'image/png',1,1,'reserved',?,?,?)", media, c.owner, body, body, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	post := publishFor(t, c, "含图内容", "![自有图片](/media/"+media+")")
	backup, err := app.Backup()
	if err != nil {
		t.Fatal(err)
	}
	mustStatus(t, c.call("POST", "/api/v1/p/"+post+"/edit", url.Values{"version": {"1"}, "title": {"更新"}, "content": {"图片已移除"}, "license": {"reserved"}}, true), 303)
	mustStatus(t, c.call("GET", "/media/"+media, nil, true), 404)
	destination := filepath.Join(t.TempDir(), "restored.db")
	if err = RestoreBackup(app.config, filepath.Join(app.config.BackupDirectory, backup.Archive), destination); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", destination)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var removed sql.NullInt64
	if err = db.QueryRow("SELECT removed_at FROM media WHERE id=?", media).Scan(&removed); err != nil || !removed.Valid {
		t.Fatal("old image withdrawal not replayed", err)
	}
}

func TestCoreEditingAssociationKeepsOneVersionIncrement(t *testing.T) {
	app := coreApp(t)
	c := coreUser(t, app, "作者")
	community, _ := seedCoreCommunity(t, app)
	post := publishFor(t, c, "原标题", "正文")
	mustStatus(t, c.call("POST", "/api/v1/p/"+post+"/edit", url.Values{"version": {"1"}, "title": {"新标题"}, "content": {"新正文"}, "community": {"test-community"}, "license": {"reserved"}}, true), 303)
	var version int
	var actual string
	if err := app.db.QueryRow("SELECT version_number,community_id FROM posts WHERE id=?", post).Scan(&version, &actual); err != nil {
		t.Fatal(err)
	}
	if version != 2 || actual != community {
		t.Fatal("edit changed version more than once", version, actual)
	}
}
