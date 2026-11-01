package atlas

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func wikiValues(id, title, community string, version int) url.Values {
	return url.Values{"page_id": {id}, "title": {title}, "summary": {"合成线索"}, "body": {"公开线索有待核实。[^1]"}, "community": {community}, "sources": {"1 | 合成公开来源 | https://example.test/source | " + time.Now().Format("2006-01-02")}, "edit_summary": {"合成记录"}, "license": {"CC BY-SA 4.0"}, "version": {strconv.Itoa(version)}}
}
func createDiscovery(t *testing.T, c *coreClient, title, community string) string {
	t.Helper()
	id := randomID()
	mustStatus(t, c.call("POST", "/wiki", wikiValues(id, title, community, 0), true), 303)
	return id
}
func wikiReply(t *testing.T, c *coreClient, page, parent, content string) string {
	t.Helper()
	w := c.call("POST", "/wiki/"+page+"/replies", url.Values{"parent_id": {parent}, "content": {content}, "license": {"CC BY-SA 4.0"}}, true)
	mustStatus(t, w, 303)
	return strings.Split(w.Header().Get("Location"), "#wiki-reply-")[1]
}

func TestWikiFeatureGateAndIsolation(t *testing.T) {
	app := coreApp(t)
	author := coreUser(t, app, "发现者")
	community, _ := seedCoreCommunity(t, app)
	if app.config.WikiEnabled {
		t.Fatal("Wiki enabled by default")
	}
	for _, path := range []string{"/wiki", "/wiki/new", "/wiki/missing", "/wiki/missing/history", "/wiki/missing/versions/1", "/wiki/missing/diff?from=1&to=2", "/c/test-community/wiki"} {
		mustStatus(t, author.call("GET", path, nil, true), 404)
	}
	mustStatus(t, author.call("POST", "/wiki", wikiValues(randomID(), "关闭时的线索", community, 0), true), 404)
	if strings.Contains(author.call("GET", "/", nil, true).Body.String(), `href="/wiki"`) {
		t.Fatal("disabled entry shown")
	}
	app.config.WikiEnabled = true
	id := createDiscovery(t, author, "独立发现页", community)
	mustStatus(t, author.call("GET", "/wiki/"+id, nil, true), 200)
	for _, path := range []string{"/", "/search?q=" + url.QueryEscape("独立发现页")} {
		if strings.Contains(author.call("GET", path, nil, true).Body.String(), `href="/wiki/`+id+`"`) {
			t.Fatal("Wiki entered common timeline/search")
		}
	}
	if !strings.Contains(author.call("GET", "/c/test-community/wiki", nil, true).Body.String(), "独立发现页") {
		t.Fatal("community Wiki entry missing")
	}
	unlinked := createDiscovery(t, author, "未关联发现页", "")
	if strings.Contains(author.call("GET", "/wiki", nil, true).Body.String(), "未关联发现页") {
		t.Fatal("unlinked page appears outside Wiki search")
	}
	if !strings.Contains(author.call("GET", "/wiki?q="+url.QueryEscape("未关联发现页"), nil, true).Body.String(), unlinked) {
		t.Fatal("Wiki search cannot find unlinked page")
	}
	app.config.WikiEnabled = false
	for _, path := range []string{"/wiki/" + id, "/wiki/" + id + "/history", "/wiki/" + id + "/versions/1"} {
		mustStatus(t, author.call("GET", path, nil, true), 404)
	}
	mustStatus(t, author.call("POST", "/wiki/"+id+"/edit", wikiValues(id, "独立发现页", community, 1), true), 404)
}

func TestWikiVersionsSourcesAndConcurrency(t *testing.T) {
	app := coreApp(t)
	app.config.WikiEnabled = true
	author, other := coreUser(t, app, "创建者"), coreUser(t, app, "协作者")
	community, _ := seedCoreCommunity(t, app)
	id := createDiscovery(t, author, "共同编辑的线索", community)
	v := wikiValues(id, "共同编辑的线索", community, 1)
	v.Set("body", "新的**公开线索**。[^1]\n\n<script>alert(1)</script>\n\n![不加载图片](https://example.test/tracker.png)")
	v.Set("sources", v.Get("sources")+" | expired")
	mustStatus(t, other.call("POST", "/wiki/"+id+"/edit", v, false), 403)
	mustStatus(t, other.call("POST", "/wiki/"+id+"/edit", v, true), 303)
	page := author.call("GET", "/wiki/"+id, nil, true)
	mustStatus(t, page, 200)
	for _, text := range []string{"<strong>公开线索</strong>", "wiki-source-1", "合成公开来源", "来源已失效", "待重新核验", "CC BY-SA 4.0"} {
		if !strings.Contains(page.Body.String(), text) {
			t.Fatal("missing source or Markdown rendering", text)
		}
	}
	if strings.Contains(page.Body.String(), "<script>alert(1)</script>") || strings.Contains(page.Body.String(), "tracker.png") {
		t.Fatal("unsafe Wiki rendering")
	}
	mustStatus(t, other.call("GET", "/wiki/"+id+"/versions/1", nil, true), 200)
	diff := other.call("GET", "/wiki/"+id+"/diff?from=1&to=2", nil, true)
	mustStatus(t, diff, 200)
	if !strings.Contains(diff.Body.String(), "<del>") || !strings.Contains(diff.Body.String(), "<ins>") || strings.Contains(diff.Body.String(), "<script>alert") {
		t.Fatal("version diff not escaped")
	}
	v.Set("version", "2")
	v.Set("title", "协作者不可重命名")
	mustStatus(t, other.call("POST", "/wiki/"+id+"/edit", v, true), 403)
	v.Set("title", "创建者重命名")
	mustStatus(t, author.call("POST", "/wiki/"+id+"/edit", v, true), 303)
	v.Set("version", "3")
	v.Set("body", "并发变更。[^1]")
	results := make(chan int, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); results <- other.call("POST", "/wiki/"+id+"/edit", v, true).Code }()
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
			t.Fatal("concurrent status", code)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal("lost concurrent Wiki edit")
	}
	if _, err := app.db.Exec("UPDATE communities SET status='archived' WHERE id=?", community); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/wiki/" + id, "/wiki/" + id + "/history", "/wiki/" + id + "/versions/1", "/wiki/" + id + "/diff?from=1&to=2"} {
		mustStatus(t, other.call("GET", path, nil, true), 404)
	}
}

func TestWikiInvalidInputAndRollback(t *testing.T) {
	app := coreApp(t)
	app.config.WikiEnabled = true
	author := coreUser(t, app, "输入检查")
	for _, source := range []string{"1 | 不安全 | javascript:alert(1) | 2026-01-01", "1 | 来源 | https://example.test | 2999-01-01", "bad id | 来源 | https://example.test | 2026-01-01", "1 | A | https://example.test | 2026-01-01\n1 | B | https://example.test | 2026-01-01"} {
		v := wikiValues(randomID(), "无效来源", "", 0)
		v.Set("sources", source)
		mustStatus(t, author.call("POST", "/wiki", v, true), 400)
	}
	v := wikiValues(randomID(), "无效脚注", "", 0)
	v.Set("body", "不能引用不存在的来源。[^2]")
	mustStatus(t, author.call("POST", "/wiki", v, true), 400)
	v.Set("body", "私写脚注。[^1]\n\n[^1]: 不允许另写的来源")
	mustStatus(t, author.call("POST", "/wiki", v, true), 400)
	v = wikiValues(randomID(), "许可确认", "", 0)
	v.Del("license")
	mustStatus(t, author.call("POST", "/wiki", v, true), 400)
	id := createDiscovery(t, author, "事务回滚线索", "")
	if _, err := app.db.Exec("CREATE TRIGGER wiki_save_fail BEFORE INSERT ON wiki_versions WHEN NEW.number=2 BEGIN SELECT RAISE(ABORT,'synthetic rollback'); END;"); err != nil {
		t.Fatal(err)
	}
	v = wikiValues(id, "事务回滚线索", "", 1)
	v.Set("body", "不应写入。[^1]")
	mustStatus(t, author.call("POST", "/wiki/"+id+"/edit", v, true), 503)
	current, err := loadWikiVersion(app.db, id, 0)
	if err != nil || current.Latest != 1 || strings.Contains(current.Body, "不应写入") {
		t.Fatal("partial Wiki transaction", err)
	}
}

func TestWikiThreadedRepliesWithdrawalAndPagination(t *testing.T) {
	app := coreApp(t)
	app.config.WikiEnabled = true
	a, b := coreUser(t, app, "讨论者甲"), coreUser(t, app, "讨论者乙")
	id := createDiscovery(t, a, "讨论线索", "")
	root := wikiReply(t, a, id, "", "根讨论")
	child := wikiReply(t, b, id, root, "第二层讨论")
	wikiReply(t, a, id, child, "第三层讨论")
	view := b.call("GET", "/wiki/"+id, nil, true)
	mustStatus(t, view, 200)
	for _, text := range []string{"根讨论", "第二层讨论", "第三层讨论"} {
		if !strings.Contains(view.Body.String(), text) {
			t.Fatal("missing reply tree", text)
		}
	}
	otherPage := createDiscovery(t, b, "其他讨论页", "")
	mustStatus(t, b.call("POST", "/wiki/"+otherPage+"/replies", url.Values{"parent_id": {child}, "content": {"跨页面父回复"}, "license": {"CC BY-SA 4.0"}}, true), 400)
	mustStatus(t, a.call("POST", "/wiki/"+id+"/replies/"+child+"/delete", url.Values{}, true), 403)
	mustStatus(t, b.call("POST", "/wiki/"+id+"/replies/"+child+"/delete", url.Values{}, true), 303)
	view = a.call("GET", "/wiki/"+id, nil, true)
	if strings.Contains(view.Body.String(), "第二层讨论") || strings.Contains(view.Body.String(), "第三层讨论") || !strings.Contains(view.Body.String(), "根讨论") {
		t.Fatal("reply subtree withdrawal failed")
	}
	for n := 0; n < pageSize; n++ {
		if _, err := app.db.Exec("INSERT INTO wiki_replies VALUES(?,?,NULL,?,? ,?,'published',?,?)", randomID(), id, a.owner, "讨论者甲", "分页讨论"+strconv.Itoa(n), time.Now().Unix()+int64(n+1), time.Now().Unix()); err != nil {
			t.Fatal(err)
		}
	}
	if !strings.Contains(a.call("GET", "/wiki/"+id, nil, true).Body.String(), "下一页") || !strings.Contains(a.call("GET", "/wiki/"+id+"?page=1", nil, true).Body.String(), "分页讨论49") {
		t.Fatal("thread pagination omitted root")
	}
}

func TestWikiWithdrawalScrubsHistoryAndBackups(t *testing.T) {
	for _, state := range []string{"hidden", "deleted"} {
		t.Run(state, func(t *testing.T) {
			app := coreApp(t)
			app.config.WikiEnabled = true
			owner, admin, ordinary := coreUser(t, app, "Wiki 创建者"), coreUser(t, app, "Wiki 审核员"), coreUser(t, app, "普通用户")
			community, _ := seedCoreCommunity(t, app)
			if err := app.BootstrapAdmin(admin.owner); err != nil {
				t.Fatal(err)
			}
			id := createDiscovery(t, owner, "必须清除的旧标题", community)
			v := wikiValues(id, "必须清除的旧标题", community, 1)
			v.Set("body", "必须清除的历史正文。[^1]")
			mustStatus(t, owner.call("POST", "/wiki/"+id+"/edit", v, true), 303)
			wikiReply(t, ordinary, id, "", "必须清除的讨论")
			backup, err := app.Backup()
			if err != nil {
				t.Fatal(err)
			}
			withdraw := url.Values{"version": {"2"}, "state": {state}, "reason": {"privacy"}, "confirm": {"withdraw"}}
			mustStatus(t, ordinary.call("POST", "/wiki/"+id+"/withdraw", withdraw, true), 403)
			mustStatus(t, admin.call("POST", "/wiki/"+id+"/withdraw", withdraw, false), 403)
			mustStatus(t, admin.call("POST", "/wiki/"+id+"/withdraw", withdraw, true), 303)
			anon := &coreClient{app: app}
			for _, path := range []string{"/wiki/" + id + "/history", "/wiki/" + id + "/versions/1", "/wiki/" + id + "/diff?from=1&to=2", "/wiki/" + id + "/edit"} {
				mustStatus(t, owner.call("GET", path, nil, true), 404)
			}
			page := anon.call("GET", "/wiki/"+id, nil, true)
			if state == "hidden" {
				mustStatus(t, page, 404)
			} else {
				mustStatus(t, page, 200)
				if !strings.Contains(page.Body.String(), "页面已删除") {
					t.Fatal("deleted tombstone missing")
				}
			}
			for _, path := range []string{"/wiki?q=" + url.QueryEscape("必须清除"), "/c/test-community/wiki"} {
				result := anon.call("GET", path, nil, true).Body.String()
				if strings.Contains(result, `href="/wiki/`+id+`"`) || strings.Contains(result, "必须清除的旧标题") || strings.Contains(result, "必须清除的历史正文") {
					t.Fatal("withdrawn Wiki leaked through list")
				}
			}
			archive := filepath.Join(app.config.BackupDirectory, backup.Archive)
			raw, err := os.ReadFile(archive)
			if err != nil {
				t.Fatal(err)
			}
			key, err := os.ReadFile(app.config.BackupKeyFile)
			if err != nil {
				t.Fatal(err)
			}
			plain, err := decrypt(key, raw, "staratlas-backup-v1")
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(plain, []byte("必须清除的旧标题")) || bytes.Contains(plain, []byte("必须清除的历史正文")) || bytes.Contains(plain, []byte("必须清除的讨论")) {
				t.Fatal("ordinary backup retained withdrawn Wiki text")
			}
			destination := filepath.Join(t.TempDir(), "wiki-restored.db")
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
			if wikiReadable(restored.db, id) {
				t.Fatal("restore resurrected withdrawn Wiki")
			}
		})
	}
}

func TestWikiDeactivationRetention(t *testing.T) {
	for _, retain := range []bool{false, true} {
		t.Run(strconv.FormatBool(retain), func(t *testing.T) {
			app := coreApp(t)
			app.config.WikiEnabled = true
			owner, other := coreUser(t, app, "退出讨论者"), coreUser(t, app, "共同编辑者")
			id := createDiscovery(t, owner, "注销后保留共同资料", "")
			reply := wikiReply(t, owner, id, "", "我的普通讨论")
			v := url.Values{"confirm": {"deactivate"}}
			if retain {
				v.Set("retain_ids", reply)
			}
			mustStatus(t, owner.call("POST", "/api/v1/me/deactivate", v, true), 303)
			page := other.call("GET", "/wiki/"+id, nil, true)
			mustStatus(t, page, 200)
			if !strings.Contains(page.Body.String(), "注销后保留共同资料") || !strings.Contains(page.Body.String(), "已注销") {
				t.Fatal("Wiki contribution lost after account deactivation")
			}
			if strings.Contains(page.Body.String(), "我的普通讨论") != retain {
				t.Fatal("Wiki discussion retention choice ignored")
			}
		})
	}
}

func TestWikiReadOnlyOutageAndRateLimit(t *testing.T) {
	app := coreApp(t)
	app.config.WikiEnabled = true
	owner := coreUser(t, app, "运行边界")
	id := createDiscovery(t, owner, "运行边界线索", "")
	previous := app.httpClient.Transport
	app.httpClient.Transport = failedIdentityTransport{}
	mustStatus(t, owner.call("POST", "/wiki/"+id+"/edit", wikiValues(id, "运行边界线索", "", 1), true), 503)
	mustStatus(t, owner.call("POST", "/wiki/"+id+"/withdraw", url.Values{"version": {"1"}, "state": {"deleted"}, "reason": {"privacy"}, "confirm": {"withdraw"}}, true), 503)
	app.httpClient.Transport = previous
	if _, err := app.db.Exec("INSERT INTO auth_limits VALUES(?,60,?) ON CONFLICT(key) DO UPDATE SET count=60,reset_at=excluded.reset_at", "content:wiki:"+owner.owner, time.Now().Add(15*time.Minute).Unix()); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, owner.call("POST", "/wiki/"+id+"/edit", wikiValues(id, "运行边界线索", "", 1), true), 429)
	if err := app.SetMaintenance("readonly"); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, owner.call("POST", "/wiki/"+id+"/replies", url.Values{"content": {"只读时不写入"}, "license": {"CC BY-SA 4.0"}}, true), 503)
	mustStatus(t, owner.call("POST", "/wiki/"+id+"/withdraw", url.Values{"version": {"1"}, "state": {"deleted"}, "reason": {"privacy"}, "confirm": {"withdraw"}}, true), 303)
	if err := app.SetMaintenance("isolated"); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, owner.call("GET", "/wiki", nil, true), 503)
}

func TestWikiRestoredOlderEditRemainsHidden(t *testing.T) {
	app := coreApp(t)
	app.config.WikiEnabled = true
	owner := coreUser(t, app, "恢复复核")
	id := createDiscovery(t, owner, "恢复前的记录", "")
	backup, err := app.Backup()
	if err != nil {
		t.Fatal(err)
	}
	v := wikiValues(id, "恢复前的记录", "", 1)
	v.Set("body", "备份后更新的事实。[^1]")
	mustStatus(t, owner.call("POST", "/wiki/"+id+"/edit", v, true), 303)
	destination := filepath.Join(t.TempDir(), "older-wiki.db")
	if err = RestoreBackup(app.config, filepath.Join(app.config.BackupDirectory, backup.Archive), destination); err != nil {
		t.Fatal(err)
	}
	config := app.config
	config.Database = destination
	restored, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if wikiReadable(restored.db, id) || wikiVersionReadable(restored.db, id, 1) {
		t.Fatal("older Wiki snapshot republished without review")
	}
}

func TestWikiOwnExportVisibility(t *testing.T) {
	app := coreApp(t)
	app.config.WikiEnabled = true
	owner, other := coreUser(t, app, "导出贡献者"), coreUser(t, app, "其他贡献者")
	visible := createDiscovery(t, owner, "本人可见贡献", "")
	hidden := createDiscovery(t, owner, "已撤回的本人贡献", "")
	createDiscovery(t, other, "其他人的独立贡献", "")
	wikiReply(t, owner, visible, "", "本人可见讨论")
	mustStatus(t, owner.call("POST", "/wiki/"+hidden+"/withdraw", url.Values{"version": {"1"}, "state": {"deleted"}, "reason": {"privacy"}, "confirm": {"withdraw"}}, true), 303)
	w := owner.call("POST", "/api/v1/me/export", url.Values{}, true)
	mustStatus(t, w, 200)
	z, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range z.File {
		if f.Name == "own-data.json" {
			r, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			raw, err := io.ReadAll(r)
			r.Close()
			if err != nil {
				t.Fatal(err)
			}
			var data map[string]json.RawMessage
			if err = json.Unmarshal(raw, &data); err != nil {
				t.Fatal(err)
			}
			var edits, replies []map[string]any
			if json.Unmarshal(data["wiki_edits"], &edits) != nil || len(edits) != 1 || edits[0]["page_id"] != visible || json.Unmarshal(data["wiki_replies"], &replies) != nil || len(replies) != 1 {
				t.Fatal("own Wiki export incorrect")
			}
			if strings.Contains(string(raw), "已撤回的本人贡献") || strings.Contains(string(raw), "其他人的独立贡献") {
				t.Fatal("Wiki export leaked hidden or other contributions")
			}
			return
		}
	}
	t.Fatal("own export missing")
}

func TestWikiMigrationFromNine(t *testing.T) {
	app, admin, rep, community := toolSetup(t)
	user := coreUser(t, app, "原有整理者")
	grant := createCollaboration(t, app, admin, rep, user, community, "guide")
	acceptCollaboration(t, user, grant, 1)
	id := randomID()
	v := toolValues("guide", id, 0)
	v.Set("status", "published")
	mustStatus(t, rep.call("POST", toolBase(community), v, true), 303)
	if _, err := app.db.Exec("DROP TABLE wiki_replies; DROP TABLE wiki_versions; DROP TABLE wiki_pages; DELETE FROM schema_migrations WHERE version=10;"); err != nil {
		t.Fatal(err)
	}
	backup, err := app.Backup()
	if err != nil {
		t.Fatal(err)
	}
	backup.Schema = 9
	archive := filepath.Join(app.config.BackupDirectory, backup.Archive)
	raw, err := json.Marshal(backup)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(archive+".json", raw, 0600); err != nil {
		t.Fatal(err)
	}
	upgraded, err := New(app.config)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	if upgraded.config.WikiEnabled || !canMaintainTool(upgraded.db, user.owner, community, "guide") {
		t.Fatal("migration enabled Wiki or lost existing collaborator authority")
	}
	current, err := loadCommunityTool(upgraded.db, community, id)
	if err != nil || current.Content != "**合成工具正文**" {
		t.Fatal("migration lost community tool", err)
	}
	destination := filepath.Join(t.TempDir(), "from-nine.db")
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
	var n int
	if err = restored.db.QueryRow("SELECT count(*) FROM wiki_pages").Scan(&n); err != nil || n != 0 {
		t.Fatal("version 9 restore migration failed", err)
	}
	if canMaintainTool(restored.db, user.owner, community, "guide") {
		t.Fatal("restore revived stale authority")
	}
}
