package atlas

import (
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func toolSetup(t *testing.T) (*App, *coreClient, *coreClient, string) {
	t.Helper()
	app := coreApp(t)
	admin, rep := coreUser(t, app, "工具管理员"), coreUser(t, app, "工具代表")
	community, _ := seedCoreCommunity(t, app)
	if err := app.BootstrapAdmin(admin.owner); err != nil {
		t.Fatal(err)
	}
	v := grantValues(rep.owner, "", "0")
	v.Set("community_id", community)
	mustStatus(t, admin.call("POST", "/admin/representatives", v, true), 303)
	return app, admin, rep, community
}
func toolValues(kind, id string, version int) url.Values {
	return url.Values{"tool_id": {id}, "kind": {kind}, "version": {strconv.Itoa(version)}, "title": {"合成" + toolLabel(kind)}, "content": {"**合成工具正文**"}, "description": {"简短说明"}, "status": {"draft"}, "order": {"10"}, "summary": {"合成维护记录"}}
}

func TestCommunityToolPageLifecycle(t *testing.T) {
	app, admin, rep, community := toolSetup(t)
	base := toolBase(community)
	ordinary := coreUser(t, app, "普通用户")
	anonymous := &coreClient{app: app}
	for _, kind := range []string{"announcement", "guide", "discussion"} {
		mustStatus(t, rep.call("GET", base+"/new?kind="+kind, nil, true), 200)
	}
	id := randomID()
	values := toolValues("announcement", id, 0)
	mustStatus(t, ordinary.call("POST", base, values, true), 403)
	mustStatus(t, rep.call("POST", base, values, false), 403)
	mustStatus(t, rep.call("POST", base, values, true), 303)
	mustStatus(t, rep.call("POST", base, values, true), 409)
	mustStatus(t, rep.call("GET", base+"/"+id, nil, true), 200)
	if strings.Contains(anonymous.call("GET", "/c/test-community/announcements", nil, true).Body.String(), "合成工具正文") {
		t.Fatal("draft exposed")
	}
	values.Set("version", "1")
	values.Set("status", "published")
	values.Set("pinned", "1")
	mustStatus(t, rep.call("POST", base+"/"+id, values, true), 303)
	public := anonymous.call("GET", "/c/test-community/announcements", nil, true)
	mustStatus(t, public, 200)
	for _, text := range []string{"<strong>合成工具正文</strong>", "置顶", "工具代表", "更新于"} {
		if !strings.Contains(public.Body.String(), text) {
			t.Fatal("missing public tool data", text)
		}
	}
	mustStatus(t, rep.call("POST", base+"/"+id, values, true), 409)
	values.Set("version", "2")
	values.Set("status", "archived")
	values.Set("content", "撤下后的内容")
	mustStatus(t, admin.call("POST", base+"/"+id, values, true), 303)
	if strings.Contains(anonymous.call("GET", "/c/test-community/announcements", nil, true).Body.String(), "合成公告") {
		t.Fatal("withdrawn tool exposed")
	}
	history := rep.call("GET", base+"/"+id+"/history", nil, true)
	mustStatus(t, history, 200)
	for _, text := range []string{"版本 1", "版本 2", "版本 3", "工具代表", "工具管理员"} {
		if !strings.Contains(history.Body.String(), text) {
			t.Fatal("history missing", text)
		}
	}
	mustStatus(t, ordinary.call("GET", base+"/"+id+"/history", nil, true), 403)
	if !strings.Contains(rep.call("GET", "/admin/log", nil, true).Body.String(), "维护社群工具") {
		t.Fatal("audit missing")
	}
	if _, err := app.db.Exec("UPDATE community_representatives SET expires_at=0 WHERE community_id=?", community); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{base, base + "/" + id, base + "/" + id + "/history"} {
		mustStatus(t, rep.call("GET", path, nil, true), 403)
	}
	values.Set("version", "3")
	mustStatus(t, rep.call("POST", base+"/"+id, values, true), 403)
}

func TestCommunityToolReferencesAndWithdrawal(t *testing.T) {
	app, _, rep, community := toolSetup(t)
	author := coreUser(t, app, "原帖作者")
	first, second := publishFor(t, author, "第一篇原帖", "秘密原文不复制"), publishFor(t, author, "第二篇原帖", "原文仍属作者")
	for _, id := range []string{first, second} {
		if _, err := app.db.Exec("UPDATE posts SET community_id=? WHERE id=?", community, id); err != nil {
			t.Fatal(err)
		}
	}
	id := randomID()
	values := toolValues("discussion", id, 0)
	values.Set("content", "")
	values.Set("status", "published")
	values.Set("references", "/p/"+second+"\n"+app.config.Origin+"/p/"+first)
	base := toolBase(community)
	mustStatus(t, rep.call("POST", base, values, true), 303)
	view := (&coreClient{app: app}).call("GET", "/c/test-community/collections", nil, true).Body.String()
	if strings.Index(view, "第二篇原帖") > strings.Index(view, "第一篇原帖") || !strings.Contains(view, "原帖作者") || strings.Contains(view, "秘密原文不复制") {
		t.Fatal("reference order, attribution or copy boundary incorrect")
	}
	var snapshot string
	if err := app.db.QueryRow("SELECT refs_json FROM collection_versions WHERE collection_id=?", id).Scan(&snapshot); err != nil || strings.Contains(snapshot, "秘密原文") {
		t.Fatal("reference snapshot copied original", err)
	}
	if _, err := app.db.Exec("UPDATE posts SET community_id=NULL WHERE id=?", second); err != nil {
		t.Fatal(err)
	}
	view = (&coreClient{app: app}).call("GET", "/c/test-community/collections?page=2", nil, true).Body.String()
	if strings.Contains(view, "第二篇原帖") || !strings.Contains(view, "第一篇原帖") {
		t.Fatal("withdrawn association remains visible")
	}
	// A withdrawn existing reference must not prevent the maintainer from withdrawing the collection.
	values.Set("version", "1")
	values.Set("status", "archived")
	mustStatus(t, rep.call("POST", base+"/"+id, values, true), 303)
	values.Set("version", "2")
	values.Set("status", "published")
	mustStatus(t, rep.call("POST", base+"/"+id, values, true), 303)
	var version int
	app.db.QueryRow("SELECT version_number FROM posts WHERE id=?", first).Scan(&version)
	mustStatus(t, author.call("POST", "/api/v1/p/"+first+"/delete", url.Values{"version": {strconv.Itoa(version)}}, true), 303)
	view = (&coreClient{app: app}).call("GET", "/c/test-community/collections", nil, true).Body.String()
	if strings.Contains(view, "第一篇原帖") || strings.Contains(view, "第二篇原帖") {
		t.Fatal("withdrawn originals remain in collection")
	}
	history := rep.call("GET", base+"/"+id+"/history", nil, true)
	mustStatus(t, history, 200)
	if strings.Contains(history.Body.String(), "第一篇原帖") || strings.Contains(history.Body.String(), "第二篇原帖") || strings.Contains(history.Body.String(), "秘密原文不复制") {
		t.Fatal("history revived withdrawn original")
	}
	var owner, license string
	if err := app.db.QueryRow("SELECT author_id,license FROM posts WHERE id=?", second).Scan(&owner, &license); err != nil || owner != author.owner || license != "reserved" {
		t.Fatal("collection changed original rights", err)
	}
}

func TestCommunityToolScopeInputConcurrencyAndRollback(t *testing.T) {
	app, _, rep, community := toolSetup(t)
	other := randomID()
	if _, err := app.db.Exec("INSERT INTO communities(id,slug,name,description,status,created_at,updated_at) VALUES(?,'other-tool','其他社群','','active',unixepoch(),unixepoch())", other); err != nil {
		t.Fatal(err)
	}
	base := toolBase(community)
	id := randomID()
	v := toolValues("guide", id, 0)
	mustStatus(t, rep.call("GET", toolBase(other), nil, true), 403)
	mustStatus(t, rep.call("POST", toolBase(other), v, true), 403)
	v.Set("pinned", "1")
	mustStatus(t, rep.call("POST", base, v, true), 400)
	v.Del("pinned")
	v.Set("references", "not-a-post")
	mustStatus(t, rep.call("POST", base, v, true), 400)
	v.Del("references")
	mustStatus(t, rep.call("POST", base, v, true), 303)
	v.Set("version", "1")
	v.Set("status", "published")
	results := make(chan int, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); results <- rep.call("POST", base+"/"+id, v, true).Code }()
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for code := range results {
		if code == 303 {
			success++
		} else if code == 409 {
			conflict++
		} else {
			t.Fatal("unexpected concurrent response", code)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal("concurrent edit overwrote version")
	}
	if _, err := app.db.Exec("CREATE TRIGGER fail_tool BEFORE INSERT ON collection_versions BEGIN SELECT RAISE(ABORT,'synthetic failure'); END;"); err != nil {
		t.Fatal(err)
	}
	v.Set("version", "2")
	v.Set("title", "不应保存的标题")
	mustStatus(t, rep.call("POST", base+"/"+id, v, true), 503)
	tt, err := loadCommunityTool(app.db, community, id)
	if err != nil || tt.Version != 2 || tt.Title == "不应保存的标题" {
		t.Fatal("partial tool transaction", err)
	}
	if _, err = app.db.Exec("DROP TRIGGER fail_tool; UPDATE sessions SET reauthenticated_at=0 WHERE user_id=?", rep.owner); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, rep.call("POST", base+"/"+id, v, true), 403)
}

func TestCommunityToolRestoreDoesNotRepublishOldVersion(t *testing.T) {
	app, _, rep, community := toolSetup(t)
	id := randomID()
	v := toolValues("announcement", id, 0)
	v.Set("status", "published")
	mustStatus(t, rep.call("POST", toolBase(community), v, true), 303)
	backup, err := app.Backup()
	if err != nil {
		t.Fatal(err)
	}
	v.Set("version", "1")
	v.Set("status", "archived")
	mustStatus(t, rep.call("POST", toolBase(community)+"/"+id, v, true), 303)
	destination := filepath.Join(t.TempDir(), "restored.db")
	if err = RestoreBackup(app.config, filepath.Join(app.config.BackupDirectory, backup.Archive), destination); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", destination)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var status string
	var version int
	if err = db.QueryRow("SELECT c.status,d.version FROM collections c JOIN collection_details d ON d.collection_id=c.id WHERE c.id=?", id).Scan(&status, &version); err != nil || status != "archived" || version != 3 {
		t.Fatal("backup republished stale tool", status, version, err)
	}
	raw, err := os.ReadFile(app.config.OpsJournalFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "合成工具正文") {
		t.Fatal("journal copied tool text")
	}
}

func TestCommunityToolsMigrationPreservesLegacyData(t *testing.T) {
	app, _, _, community := toolSetup(t)
	id := randomID()
	if _, err := app.db.Exec("INSERT INTO collections VALUES(?,?,'guide','旧指引','legacy','','旧内容',2,'published',1,2)", id, community); err != nil {
		t.Fatal(err)
	}
	// Produce an actual version 7 snapshot, then let New run the migration.
	if _, err := app.db.Exec("DROP TABLE collection_positions; DROP TABLE collection_versions; DROP TABLE collection_details; DELETE FROM schema_migrations WHERE version=8;"); err != nil {
		t.Fatal(err)
	}
	backup, err := app.Backup()
	if err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(app.config.BackupDirectory, backup.Archive)
	backup.Schema = 7
	raw, _ := json.Marshal(backup)
	if err = os.WriteFile(archive+".json", raw, 0600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "migrated.db")
	if err = RestoreBackup(app.config, archive, destination); err != nil {
		t.Fatal(err)
	}
	config := app.config
	config.Database = destination
	restored, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	tt, err := loadCommunityTool(restored.db, community, id)
	if err != nil || tt.Content != "旧内容" || tt.Version != 1 || tt.Order != 2 {
		t.Fatal("migration lost old data", err)
	}
	var count int
	if err = restored.db.QueryRow("SELECT count(*) FROM collection_versions WHERE collection_id=?", id).Scan(&count); err != nil || count != 1 {
		t.Fatal("legacy baseline not preserved", err)
	}
}

func TestCommunityToolPaginationAndFormErrors(t *testing.T) {
	app, _, rep, community := toolSetup(t)
	base := toolBase(community)
	for n := 0; n < pageSize+1; n++ {
		id := randomID()
		if _, err := app.db.Exec("INSERT INTO collections VALUES(?,?,'guide',? ,?,'','内容',0,'draft',1,1)", id, community, "分页指引"+strconv.Itoa(n), id); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{base, base + "?page=1"} {
		mustStatus(t, rep.call("GET", path, nil, true), 200)
	}
	id := randomID()
	v := toolValues("announcement", id, 0)
	v.Set("summary", "")
	w := rep.call("POST", base, v, true)
	mustStatus(t, w, 400)
	if !strings.Contains(w.Body.String(), `role="alert"`) || !strings.Contains(w.Body.String(), "**合成工具正文**") || !strings.Contains(w.Body.String(), `name="version" value="0"`) {
		t.Fatal("input failure lost draft")
	}
	v.Set("summary", "新建")
	mustStatus(t, rep.call("POST", base, v, true), 303)
	v.Set("version", "1")
	v.Set("status", "published")
	mustStatus(t, rep.call("POST", base+"/"+id, v, true), 303)
	v.Set("title", "<script>未保存的标题</script>")
	w = rep.call("POST", base+"/"+id, v, true)
	mustStatus(t, w, 409)
	if !strings.Contains(w.Body.String(), "重新载入最新版本") || !strings.Contains(w.Body.String(), "&lt;script&gt;") || strings.Contains(w.Body.String(), "<script>未保存") {
		t.Fatal("conflict failure lost or executed input")
	}
	// Ensure history pagination releases its database cursor before rendering the page.
	for n := 3; n < pageSize+4; n++ {
		if _, err := app.db.Exec("INSERT INTO collection_versions VALUES(?,?,'标题','','内容','draft',0,0,?,1,'维护','[]')", id, n, rep.owner); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{base + "/" + id + "/history", base + "/" + id + "/history?page=1"} {
		mustStatus(t, rep.call("GET", path, nil, true), 200)
	}
}
