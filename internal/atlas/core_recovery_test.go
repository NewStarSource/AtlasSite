package atlas

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func seedCoreCommunity(t *testing.T, app *App) (string, string) {
	t.Helper()
	c, topic := randomID(), randomID()
	now := time.Now().Unix()
	for _, q := range []string{"INSERT INTO domains VALUES('domain','领域','',1,unixepoch())", "INSERT INTO directions VALUES('direction','domain','方向','',1,unixepoch())", "INSERT INTO subcategories VALUES('sub','direction','分类','',1,unixepoch())"} {
		if _, err := app.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := app.db.Exec("INSERT INTO communities VALUES(?,'test-community','合成社群','社群说明','sub','active','','javascript:alert(1)','synthetic',0,?,?,1)", c, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := app.db.Exec("INSERT INTO topics VALUES(?,?,'topic','专题','说明',1,'active',?)", topic, c, now); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"announcement", "guide", "discussion"} {
		if _, err := app.db.Exec("INSERT INTO collections VALUES(?,?,?,?,'slug','','合集文字',1,'published',?,?)", randomID(), c, kind, kind, now, now); err != nil {
			t.Fatal(err)
		}
	}
	return c, topic
}
func TestCoreCommunityTopicAssociationAndCollectionVisibility(t *testing.T) {
	app := coreApp(t)
	author := coreUser(t, app, "作者")
	viewer := coreUser(t, app, "读者")
	c, topic := seedCoreCommunity(t, app)
	id := publishFor(t, author, "引用的讨论", "讨论内容")
	form := url.Values{"version": {"1"}, "community": {"test-community"}, "topic_ids": {topic}}
	mustStatus(t, viewer.call("POST", "/api/v1/p/"+id+"/association", form, true), 403)
	mustStatus(t, author.call("POST", "/api/v1/p/"+id+"/association", form, true), 303)
	var collection string
	app.db.QueryRow("SELECT id FROM collections WHERE community_id=? AND type='discussion'", c).Scan(&collection)
	if _, err := app.db.Exec("INSERT INTO collection_refs VALUES(?,?)", collection, id); err != nil {
		t.Fatal(err)
	}
	page := viewer.call("GET", "/c/test-community", nil, true)
	mustStatus(t, page, 200)
	for _, route := range []struct{ path, text string }{{"/announcements", "announcement"}, {"/guide", "guide"}, {"/collections", "引用的讨论"}} {
		result := viewer.call("GET", "/c/test-community"+route.path, nil, true)
		mustStatus(t, result, 200)
		if !strings.Contains(result.Body.String(), route.text) {
			t.Fatal("missing collection or reference", route.path)
		}
	}

	if strings.Contains(page.Body.String(), `href="javascript:`) {
		t.Fatal("unsafe source URL rendered")
	}
	page = viewer.call("GET", "/c/test-community/t/topic", nil, true)
	mustStatus(t, page, 200)
	if !strings.Contains(page.Body.String(), "引用的讨论") {
		t.Fatal("topic association absent")
	}
	mustStatus(t, viewer.call("POST", "/api/v1/blocks/"+author.owner, url.Values{"blocked": {"true"}}, true), 303)
	if strings.Contains(viewer.call("GET", "/c/test-community/collections", nil, true).Body.String(), "引用的讨论") {
		t.Fatal("blocked author leaked through collection")
	}
	form = url.Values{"version": {"2"}, "community": {""}}
	mustStatus(t, author.call("POST", "/api/v1/p/"+id+"/association", form, true), 303)
	if strings.Contains(author.call("GET", "/c/test-community/collections", nil, true).Body.String(), "引用的讨论") {
		t.Fatal("detached discussion leaked through reference")
	}
	if _, err := app.db.Exec("INSERT INTO community_paths VALUES('old',?)", c); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, author.call("GET", "/communities/old", nil, true), 301)
}
func reportFor(t *testing.T, c *coreClient, object, parent string) string {
	t.Helper()
	mustStatus(t, c.call("POST", "/api/v1/cases", url.Values{"request_id": {randomID()}, "kind": {"report"}, "object_id": {object}, "parent_id": {parent}, "detail": {"合成测试证据，只允许本地处理"}}, true), 200)
	var id string
	if err := c.app.db.QueryRow("SELECT id FROM cases WHERE user_id=? ORDER BY received_at DESC,rowid DESC LIMIT 1", c.owner).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
func TestCoreAppealOwnershipMultipleRestrictionsAndEvidenceIsolation(t *testing.T) {
	app := coreApp(t)
	author := coreUser(t, app, "作者")
	reporter := coreUser(t, app, "举报人")
	other := coreUser(t, app, "其他人")
	post := publishFor(t, author, "需要复核", "受限正文特殊标记")
	original, err := app.Backup()
	if err != nil {
		t.Fatal(err)
	}
	first := reportFor(t, reporter, post, "")
	second := reportFor(t, other, post, "")
	mustStatus(t, author.call("GET", "/my/cases/"+first, nil, true), 404)
	if err = app.ResolveCase(first, "reviewer-a", "restricted"); err != nil {
		t.Fatal(err)
	}
	if err = app.ResolveCase(second, "reviewer-b", "restricted"); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, author.call("GET", "/p/"+post, nil, true), 404)
	visible := author.call("GET", "/my/cases/"+first, nil, true)
	mustStatus(t, visible, 200)
	if strings.Contains(visible.Body.String(), "合成测试证据") || strings.Contains(visible.Body.String(), reporter.owner) {
		t.Fatal("reporter evidence leaked to subject")
	}
	mustStatus(t, other.call("GET", "/my/cases/"+first, nil, true), 404)
	appeal := reportFor(t, author, post, first)
	if err = app.ResolveCase(appeal, "reviewer-a", "released"); err != errForbidden {
		t.Fatal("same reviewer accepted appeal", err)
	}
	if err = app.ResolveCase(appeal, "reviewer-c", "released"); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, author.call("GET", "/p/"+post, nil, true), 404)
	appeal = reportFor(t, author, post, second)
	if err = app.ResolveCase(appeal, "reviewer-c", "released"); err != nil {
		t.Fatal(err)
	}
	page := author.call("GET", "/p/"+post, nil, true)
	mustStatus(t, page, 200)
	if !strings.Contains(page.Body.String(), "受限正文特殊标记") {
		t.Fatal("isolation original not restored")
	}
	archive, err := os.ReadFile(filepath.Join(app.config.BackupDirectory, original.Archive))
	if err != nil {
		t.Fatal(err)
	}
	key, _ := os.ReadFile(app.config.BackupKeyFile)
	plain, err := decrypt(key, archive, "staratlas-backup-v1")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(plain, []byte("受限正文特殊标记")) {
		t.Fatal("risk original persisted in earlier ordinary backup")
	}
	// Expired isolated evidence must never be reconstructed from an older snapshot.
	if _, err = app.vault.Exec("DELETE FROM objects WHERE kind='isolated'"); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "restore.db")
	if err = RestoreBackup(app.config, filepath.Join(app.config.BackupDirectory, original.Archive), dest); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dest)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var status, body string
	if err = db.QueryRow("SELECT status,content FROM posts WHERE id=?", post).Scan(&status, &body); err != nil {
		t.Fatal(err)
	}
	if status != "hidden" || body != "" {
		t.Fatal("restore resurrected expired risk original", status, body)
	}
}
func TestCoreJournalTruncationBackupTamperAndRotation(t *testing.T) {
	app := coreApp(t)
	c := coreUser(t, app, "作者")
	post := publishFor(t, c, "标题", "正文")
	mustStatus(t, c.call("POST", "/api/v1/p/"+post+"/bookmark", url.Values{"bookmarked": {"true"}}, true), 303)
	backup, err := app.Backup()
	if err != nil {
		t.Fatal(err)
	}
	manifests, invalid := app.validBackups()
	if invalid != 0 || len(manifests) != 3 {
		t.Fatal("hourly/daily/monthly tiers missing", len(manifests), invalid)
	}
	archive := filepath.Join(app.config.BackupDirectory, backup.Archive)
	raw, _ := os.ReadFile(archive)
	raw[len(raw)-1] ^= 1
	if err = os.WriteFile(archive, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err = RestoreBackup(app.config, archive, filepath.Join(t.TempDir(), "restore.db")); err == nil {
		t.Fatal("tampered archive accepted")
	}
	diagnosis, err := app.Diagnostics()
	if err != nil {
		t.Fatal(err)
	}
	if diagnosis["invalid_backups"] != 1 {
		t.Fatal("corrupt backup not detected", diagnosis)
	}
	if err = os.WriteFile(app.config.OpsJournalFile, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = app.readJournal(); err == nil {
		t.Fatal("truncated journal accepted")
	}
	if err = app.SetMaintenance("normal"); err == nil {
		t.Fatal("invalid journal allowed normal mode")
	}
}
func TestCorePrivateExportDraftsAndRetention(t *testing.T) {
	app := coreApp(t)
	c := coreUser(t, app, "本人")
	draft := randomID()
	mustStatus(t, c.call("POST", "/api/v1/drafts/"+draft, url.Values{"version": {"0"}, "title": {"私人草稿"}, "content": {"草稿正文"}, "license": {"reserved"}}, true), 200)
	response := c.call("POST", "/api/v1/me/export", url.Values{}, true)
	mustStatus(t, response, 200)
	reader, err := zip.NewReader(bytes.NewReader(response.Body.Bytes()), int64(response.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	stream, _ := reader.File[0].Open()
	raw, _ := io.ReadAll(stream)
	stream.Close()
	if !bytes.Contains(raw, []byte("草稿正文")) {
		t.Fatal("private draft missing from own export")
	}
	backup, err := app.Backup()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(filepath.Join(app.config.BackupDirectory, backup.Archive))
	key, _ := os.ReadFile(app.config.BackupKeyFile)
	raw, err = decrypt(key, raw, "staratlas-backup-v1")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("草稿正文")) {
		t.Fatal("draft leaked into ordinary backup")
	}
	if _, err = app.db.Exec("UPDATE drafts SET updated_at=unixepoch()-31*86400"); err != nil {
		t.Fatal(err)
	}
	if _, err = app.vault.Exec("UPDATE objects SET expires_at=unixepoch()-1 WHERE kind='draft'"); err != nil {
		t.Fatal(err)
	}
	if err = app.CleanupCore(); err != nil {
		t.Fatal(err)
	}
	var count int
	app.db.QueryRow("SELECT count(*) FROM drafts").Scan(&count)
	if count != 0 {
		t.Fatal("expired draft retained")
	}
}
func TestCoreMarkdownUnsafeHTMLLinksAndUnicodeLimits(t *testing.T) {
	source := `<script>alert(1)</script>
 [danger](javascript:alert(1))
 ![remote](https://example.invalid/tracker.png)
 <javascript:alert(1)>
 **安全中文**`
	rendered := renderMarkdown(source)
	for _, bad := range []string{"<script>", `href="javascript:`, `src="https://`} {
		if strings.Contains(rendered, bad) {
			t.Fatal("unsafe Markdown output", rendered)
		}
	}
	if !strings.Contains(rendered, "<strong>安全中文</strong>") {
		t.Fatal("safe Markdown failed")
	}
	if !checkText(strings.Repeat("汉", 200), 200) || checkText(strings.Repeat("汉", 201), 200) {
		t.Fatal("limits count bytes")
	}
	if _, err := mediaIDs("![bad](/media/not-a-uuid)"); err == nil {
		t.Fatal("bad media ID accepted")
	}
}
