package atlas

import (
	"archive/zip"
	"bytes"
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

func wikiEntryValues(kind, id, title, community string, version, base int) url.Values {
	v := wikiValues(id, title, community, version)
	v.Set("kind", kind)
	v.Set("base", strconv.Itoa(base))
	v.Set("public_scope", "public")
	if kind == "community" {
		v.Set("status_value", "active")
		v.Set("first_verified_at", time.Now().Add(-24*time.Hour).Format("2006-01-02"))
	} else {
		v.Set("status_value", "public")
		v.Set("work_type", "公开网站")
		v.Set("creator", "公开创作者署名")
		v.Set("public_url", "https://example.test/work")
	}
	return v
}
func entrySetup(t *testing.T) (*App, *coreClient, *coreClient, *coreClient, string) {
	t.Helper()
	app := coreApp(t)
	app.config.WikiEnabled = true
	author, editor, admin := coreUser(t, app, "条目作者"), coreUser(t, app, "建议者"), coreUser(t, app, "人工审核员")
	if err := app.BootstrapAdmin(admin.owner); err != nil {
		t.Fatal(err)
	}
	community, _ := seedCoreCommunity(t, app)
	return app, author, editor, admin, community
}
func submitEntry(t *testing.T, c *coreClient, kind, title, community string) string {
	t.Helper()
	id := randomID()
	mustStatus(t, c.call("POST", "/wiki/entries", wikiEntryValues(kind, id, title, community, 0, 0), true), 303)
	return id
}
func reviewEntry(t *testing.T, c *coreClient, id string, n int, decision, note string) {
	t.Helper()
	mustStatus(t, c.call("POST", "/admin/wiki/"+id+"/versions/"+strconv.Itoa(n)+"/review", url.Values{"decision": {decision}, "note": {note}, "confirm": {"reviewed"}}, true), 303)
}
func suggest(t *testing.T, app *App, c *coreClient, page, target, proposed, evidence string, base int) string {
	t.Helper()
	mustStatus(t, c.call("POST", "/wiki/"+page+"/suggestions", url.Values{"base": {strconv.Itoa(base)}, "target": {target}, "proposed": {proposed}, "evidence": {evidence}, "license": {"CC BY-SA 4.0"}}, true), 303)
	var id string
	if err := app.db.QueryRow("SELECT id FROM wiki_suggestions WHERE page_id=? AND author_id=? ORDER BY rowid DESC LIMIT 1", page, c.owner).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
func resolveSuggestion(t *testing.T, c *coreClient, page, id, decision, note, replacement string, version int) {
	t.Helper()
	mustStatus(t, c.call("POST", "/admin/wiki/"+page+"/suggestions/"+id+"/resolve", url.Values{"version": {strconv.Itoa(version)}, "decision": {decision}, "note": {note}, "replacement": {replacement}, "confirm": {"reviewed"}}, true), 303)
}

func TestWikiEntryReviewAndPrivateSubmissions(t *testing.T) {
	for _, kind := range []string{"community", "work"} {
		t.Run(kind, func(t *testing.T) {
			app, author, other, admin, community := entrySetup(t)
			id := submitEntry(t, author, kind, "未审核的正式资料", community)
			for _, path := range []string{"/wiki/" + id, "/wiki/" + id + "/history", "/wiki/" + id + "/versions/1", "/wiki/" + id + "/diff?from=1&to=1"} {
				mustStatus(t, other.call("GET", path, nil, true), 404)
			}
			mustStatus(t, other.call("GET", "/wiki/"+id+"/submissions/1", nil, true), 404)
			private := author.call("GET", "/wiki/"+id+"/submissions/1", nil, true)
			mustStatus(t, private, 200)
			if !strings.Contains(private.Body.String(), "待人工审核") {
				t.Fatal("private submission state missing")
			}
			if strings.Contains(other.call("GET", "/wiki?q="+url.QueryEscape("未审核的正式资料"), nil, true).Body.String(), `href="/wiki/`+id+`"`) {
				t.Fatal("pending entry indexed")
			}
			mustStatus(t, author.call("POST", "/admin/wiki/"+id+"/versions/1/review", url.Values{"decision": {"accept"}, "note": {"不能自授审核权限"}, "confirm": {"reviewed"}}, true), 403)
			mustStatus(t, admin.call("POST", "/admin/wiki/"+id+"/versions/1/review", url.Values{"decision": {"accept"}, "note": {"已人工核对公开来源"}, "confirm": {"reviewed"}}, false), 403)
			queue := admin.call("GET", "/admin/wiki", nil, true)
			mustStatus(t, queue, 200)
			if !strings.Contains(queue.Body.String(), "未审核的正式资料") {
				t.Fatal("review queue missing submission")
			}
			reviewEntry(t, admin, id, 1, "accept", "已人工核对公开来源")
			page := other.call("GET", "/wiki/"+id, nil, true)
			mustStatus(t, page, 200)
			if !strings.Contains(page.Body.String(), "人工审核发布") || !strings.Contains(page.Body.String(), wikiKind(kind)) {
				t.Fatal("approved entry template/stage missing")
			}
			if kind == "work" && !strings.Contains(page.Body.String(), "公开创作者署名") {
				t.Fatal("work fields missing")
			}
			if kind == "community" && !strings.Contains(page.Body.String(), "首次确认存在") {
				t.Fatal("community fields missing")
			}
			v, err := loadWikiVersion(app.db, id, 0)
			if err != nil || v.Reviewer != admin.owner || v.Number != 1 {
				t.Fatal("review metadata missing", err)
			}
		})
	}
}

func TestWikiEntryRejectionAndStaleApproval(t *testing.T) {
	app, author, other, admin, community := entrySetup(t)
	id := submitEntry(t, author, "community", "版本审核资料", community)
	reviewEntry(t, admin, id, 1, "reject", "来源尚不足，请补充公开依据")
	if !strings.Contains(author.call("GET", "/wiki/"+id+"/submissions/1", nil, true).Body.String(), "来源尚不足") {
		t.Fatal("rejection reason hidden from submitter")
	}
	mustStatus(t, author.call("POST", "/wiki/"+id+"/propose", wikiEntryValues("community", id, "版本审核资料", community, 1, 0), true), 303)
	reviewEntry(t, admin, id, 2, "accept", "补充后通过")
	if strings.Contains(other.call("GET", "/wiki/"+id+"/history", nil, true).Body.String(), "/diff?from=1") {
		t.Fatal("public diff links to rejected version")
	}
	a := wikiEntryValues("community", id, "未公开修订甲", community, 2, 2)
	a.Set("body", "修订甲。[^1]")
	mustStatus(t, other.call("POST", "/wiki/"+id+"/propose", a, true), 303)
	b := wikiEntryValues("community", id, "未公开修订乙", community, 3, 2)
	mustStatus(t, author.call("POST", "/wiki/"+id+"/propose", b, true), 303)
	public := other.call("GET", "/wiki/"+id, nil, true).Body.String()
	if strings.Contains(public, "未公开修订甲") || strings.Contains(public, "未公开修订乙") || !strings.Contains(public, "待人工审核") {
		t.Fatal("pending revision leaked or banner absent")
	}
	reviewEntry(t, admin, id, 3, "accept", "修订甲通过")
	mustStatus(t, admin.call("POST", "/admin/wiki/"+id+"/versions/4/review", url.Values{"decision": {"accept"}, "note": {"不能覆盖新版本"}, "confirm": {"reviewed"}}, true), 409)
	reviewEntry(t, admin, id, 4, "reject", "请基于已发布版本重新提交")
	current, err := loadWikiVersion(app.db, id, 0)
	if err != nil || current.Number != 3 || current.Title != "未公开修订甲" {
		t.Fatal("stale approval overwrote public version", err)
	}
}

func TestWikiEntryTemplateValidation(t *testing.T) {
	_, author, _, _, community := entrySetup(t)
	for _, field := range []string{"first_verified_at", "summary", "sources"} {
		v := wikiEntryValues("community", randomID(), "模板检查", community, 0, 0)
		v.Set(field, "")
		mustStatus(t, author.call("POST", "/wiki/entries", v, true), 400)
	}
	v := wikiEntryValues("work", randomID(), "不公开作品拒绝", community, 0, 0)
	v.Set("status_value", "private")
	mustStatus(t, author.call("POST", "/wiki/entries", v, true), 400)
	v = wikiEntryValues("work", randomID(), "作品类型必填", community, 0, 0)
	v.Set("work_type", "")
	mustStatus(t, author.call("POST", "/wiki/entries", v, true), 400)
	v = wikiEntryValues("community", randomID(), "正文事实脚注", community, 0, 0)
	v.Set("body", "没有脚注的事实")
	mustStatus(t, author.call("POST", "/wiki/entries", v, true), 400)
}

func TestWikiSuggestionAdoptionPartialAndRejection(t *testing.T) {
	app, author, other, admin, community := entrySetup(t)
	id := submitEntry(t, author, "community", "建议协作资料", community)
	reviewEntry(t, admin, id, 1, "accept", "通过")
	g := suggest(t, app, other, id, "summary", "采纳后的说明", "公开来源支持更清楚的说明", 1)
	mustStatus(t, other.call("POST", "/admin/wiki/"+id+"/suggestions/"+g+"/resolve", url.Values{"version": {"1"}, "decision": {"accepted"}, "note": {"越权不能处理"}, "confirm": {"reviewed"}}, true), 403)
	resolveSuggestion(t, admin, id, g, "accepted", "说明准确，予以采纳", "", 1)
	current, err := loadWikiVersion(app.db, id, 0)
	if err != nil || current.Number != 2 || current.Summary != "采纳后的说明" || current.Editor != other.owner {
		t.Fatal("suggestion adoption failed", err)
	}
	g2 := suggest(t, app, other, id, "summary", "过于绝对的说明", "补充描述依据", 2)
	resolveSuggestion(t, admin, id, g2, "partial", "保留可核实部分", "部分采纳的中性说明", 1)
	current, err = loadWikiVersion(app.db, id, 0)
	if err != nil || current.Number != 3 || current.Summary != "部分采纳的中性说明" || current.Editor != admin.owner {
		t.Fatal("partial adoption failed", err)
	}
	g3 := suggest(t, app, other, id, "summary", "依据不足的说明", "原来的依据", 3)
	resolveSuggestion(t, admin, id, g3, "rejected", "缺少公开依据，请补充来源", "", 1)
	page := author.call("GET", "/wiki/"+id, nil, true).Body.String()
	if !strings.Contains(page, "缺少公开依据") || !strings.Contains(page, "已部分采纳") {
		t.Fatal("public suggestion result missing")
	}
	v := url.Values{"base": {"3"}, "target": {"summary"}, "proposed": {"再次提交的文字"}, "evidence": {"原来的依据"}, "license": {"CC BY-SA 4.0"}}
	mustStatus(t, other.call("POST", "/wiki/"+id+"/suggestions", v, true), 400)
	mustStatus(t, other.call("POST", "/wiki/"+id+"/suggestions/"+g3+"/withdraw", url.Values{"version": {"2"}}, true), 303)
	mustStatus(t, other.call("POST", "/wiki/"+id+"/suggestions", v, true), 400)
	v.Set("evidence", "新增独立公开依据")
	mustStatus(t, other.call("POST", "/wiki/"+id+"/suggestions", v, true), 303)
}

func TestWikiSuggestionStaleConflictAndAtomicity(t *testing.T) {
	app, author, other, admin, community := entrySetup(t)
	id := submitEntry(t, author, "community", "旧建议冲突", community)
	reviewEntry(t, admin, id, 1, "accept", "通过")
	g := suggest(t, app, other, id, "summary", "旧建议不应覆盖", "最初依据", 1)
	mustStatus(t, author.call("POST", "/wiki/"+id+"/propose", wikiEntryValues("community", id, "新公开标题", community, 1, 1), true), 303)
	reviewEntry(t, admin, id, 2, "accept", "新版本通过")
	mustStatus(t, admin.call("POST", "/admin/wiki/"+id+"/suggestions/"+g+"/resolve", url.Values{"version": {"1"}, "decision": {"accepted"}, "note": {"旧建议"}, "confirm": {"reviewed"}}, true), 409)
	resolveSuggestion(t, admin, id, g, "rejected", "已有新版本，请更新依据", "", 1)
	g2 := suggest(t, app, other, id, "summary", "并发建议", "新依据", 2)
	var wg sync.WaitGroup
	results := make(chan int, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- admin.call("POST", "/admin/wiki/"+id+"/suggestions/"+g2+"/resolve", url.Values{"version": {"1"}, "decision": {"accepted"}, "note": {"一次性采纳"}, "confirm": {"reviewed"}}, true).Code
		}()
	}
	wg.Wait()
	close(results)
	accepted, denied := 0, 0
	for code := range results {
		if code == 303 {
			accepted++
		} else if code == 403 || code == 409 {
			denied++
		} else {
			t.Fatal(code)
		}
	}
	if accepted != 1 || denied != 1 {
		t.Fatal("suggestion applied twice")
	}
}

func TestWikiSubmissionWithdrawalAndBackup(t *testing.T) {
	app, author, other, _, community := entrySetup(t)
	id := submitEntry(t, author, "work", "必须清理的私有提交", community)
	backup, err := app.Backup()
	if err != nil {
		t.Fatal(err)
	}
	mustStatus(t, other.call("POST", "/wiki/"+id+"/submissions/1/withdraw", url.Values{}, true), 403)
	mustStatus(t, author.call("POST", "/wiki/"+id+"/submissions/1/withdraw", url.Values{}, true), 303)
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
	if bytes.Contains(plain, []byte("必须清理的私有提交")) {
		t.Fatal("withdrawn submission survived backup scrub")
	}
	destination := filepath.Join(t.TempDir(), "submission.db")
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
	v, err := loadWikiVersion(restored.db, id, 1)
	if err != nil || v.Review != "withdrawn" || v.Body != "" {
		t.Fatal("withdrawn submission revived", err)
	}
}

func wikiArchiveText(t *testing.T, app *App, archive string) string {
	t.Helper()
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
	return string(plain)
}

func TestWikiReviewRiskCleanupAndRecovery(t *testing.T) {
	app, author, other, admin, community := entrySetup(t)
	id := submitEntry(t, author, "community", "正式可公开资料", community)
	reviewEntry(t, admin, id, 1, "accept", "通过")
	g := suggest(t, app, other, id, "summary", "需清理的建议原文", "需清理的建议依据", 1)
	private := submitEntry(t, author, "work", "需清理的私有标题", community)
	backup, err := app.Backup()
	if err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(app.config.BackupDirectory, backup.Archive)
	mustStatus(t, admin.call("POST", "/admin/wiki/"+id+"/suggestions/"+g+"/resolve", url.Values{"version": {"1"}, "decision": {"rejected"}, "risk": {"privacy"}, "note": {"需清理的处理原文"}, "confirm": {"reviewed"}}, true), 303)
	mustStatus(t, admin.call("POST", "/admin/wiki/"+private+"/versions/1/review", url.Values{"decision": {"reject"}, "risk": {"copyright"}, "note": {"私有处理原文"}, "confirm": {"reviewed"}}, true), 303)
	for _, secret := range []string{"需清理的建议原文", "需清理的建议依据", "需清理的私有标题", "需清理的处理原文", "私有处理原文"} {
		if strings.Contains(wikiArchiveText(t, app, archive), secret) {
			t.Fatal("risk material survived archive scrub", secret)
		}
		if strings.Contains(author.call("GET", "/wiki/"+id+"?suggestion="+g, nil, true).Body.String(), secret) {
			t.Fatal("risk suggestion still public")
		}
	}
	destination := filepath.Join(t.TempDir(), "risk.db")
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
	proposal, err := loadWikiVersion(restored.db, private, 1)
	if err != nil || proposal.Review != "withdrawn" || proposal.Title != "" {
		t.Fatal("risk submission revived", err)
	}
	s, err := loadWikiSuggestion(restored.db, g)
	if err != nil || s.State != "rejected" || s.Proposed != "" || s.Evidence != "" {
		t.Fatal("risk suggestion revived", err)
	}
	var rejected int
	if err = restored.db.QueryRow("SELECT was_rejected FROM wiki_suggestions WHERE id=?", g).Scan(&rejected); err != nil || rejected != 1 {
		t.Fatal("risk rejection basis lost", err)
	}
}

func TestWikiSuggestionParagraphAndWholePageWithdrawal(t *testing.T) {
	app, author, other, admin, community := entrySetup(t)
	id := submitEntry(t, author, "community", "段落协作资料", community)
	reviewEntry(t, admin, id, 1, "accept", "通过")
	v := url.Values{"base": {"1"}, "target": {"body"}, "old_text": {"公开线索有待核实。[^1]"}, "proposed": {"新资料与原资料并列。[^1][^2]"}, "evidence": {"第二项公开依据"}, "sources": {"2 | 新来源 | https://example.test/second | " + time.Now().Format("2006-01-02")}, "license": {"CC BY-SA 4.0"}}
	mustStatus(t, other.call("POST", "/wiki/"+id+"/suggestions", v, true), 303)
	var g string
	if err := app.db.QueryRow("SELECT id FROM wiki_suggestions WHERE page_id=?", id).Scan(&g); err != nil {
		t.Fatal(err)
	}
	resolveSuggestion(t, admin, id, g, "accepted", "并列来源可核实", "", 1)
	current, err := loadWikiVersion(app.db, id, 0)
	if err != nil || len(current.Sources) != 2 || !strings.Contains(current.Body, "[^2]") {
		t.Fatal("paragraph/source adoption failed", err)
	}
	backup, err := app.Backup()
	if err != nil {
		t.Fatal(err)
	}
	mustStatus(t, author.call("POST", "/wiki/"+id+"/withdraw", url.Values{"version": {"2"}, "state": {"deleted"}, "reason": {"privacy"}, "confirm": {"withdraw"}}, true), 403)
	mustStatus(t, admin.call("POST", "/wiki/"+id+"/withdraw", url.Values{"version": {"2"}, "state": {"hidden"}, "reason": {"privacy"}, "confirm": {"withdraw"}}, true), 303)
	var proposed, evidence, note string
	if err = app.db.QueryRow("SELECT proposed,evidence,resolution FROM wiki_suggestions WHERE id=?", g).Scan(&proposed, &evidence, &note); err != nil || proposed != "" || evidence != "" || note != "" {
		t.Fatal("whole-page withdrawal left suggestion text", err)
	}
	if strings.Contains(wikiArchiveText(t, app, filepath.Join(app.config.BackupDirectory, backup.Archive)), "第二项公开依据") {
		t.Fatal("whole-page withdrawal left archive evidence")
	}
}

func TestWikiEntryReadOnlyExportAndDeactivation(t *testing.T) {
	app, author, other, admin, community := entrySetup(t)
	id := submitEntry(t, author, "community", "公开共同资料保留", community)
	reviewEntry(t, admin, id, 1, "accept", "通过")
	private := submitEntry(t, author, "work", "本人私有草稿", community)
	submitEntry(t, other, "work", "他人的私有草稿", community)
	suggest(t, app, author, id, "summary", "本人的公开建议", "本人的公开依据", 1)
	w := author.call("POST", "/api/v1/me/export", url.Values{}, true)
	mustStatus(t, w, 200)
	z, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range z.File {
		if f.Name == "own-data.json" {
			r, e := f.Open()
			if e != nil {
				t.Fatal(e)
			}
			raw, e := io.ReadAll(r)
			r.Close()
			if e != nil {
				t.Fatal(e)
			}
			found = true
			if !bytes.Contains(raw, []byte("本人私有草稿")) || !bytes.Contains(raw, []byte("本人的公开建议")) || bytes.Contains(raw, []byte("他人的私有草稿")) {
				t.Fatal("private export isolation failed")
			}
		}
	}
	if !found {
		t.Fatal("own data missing")
	}
	if err = app.SetMaintenance("readonly"); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, author.call("POST", "/wiki/entries", wikiEntryValues("work", randomID(), "只读拒绝", community, 0, 0), true), 503)
	mustStatus(t, admin.call("POST", "/admin/wiki/"+private+"/versions/1/review", url.Values{"decision": {"accept"}, "note": {"只读不能审批"}, "confirm": {"reviewed"}}, true), 503)
	mustStatus(t, author.call("POST", "/wiki/"+private+"/submissions/1/withdraw", url.Values{}, true), 303)
	if err = app.SetMaintenance("normal"); err != nil {
		t.Fatal(err)
	}
	private2 := submitEntry(t, author, "work", "注销前的待审核稿", community)
	mustStatus(t, author.call("POST", "/api/v1/me/deactivate", url.Values{"confirm": {"deactivate"}, "retain_content": {"1"}}, true), 303)
	p, err := loadWikiVersion(app.db, private2, 1)
	if err != nil || p.Review != "withdrawn" || p.Body != "" {
		t.Fatal("private unpublished material retained on deactivation", err)
	}
	if !wikiReadable(app.db, id) {
		t.Fatal("accepted collective contribution lost")
	}
}

func TestWikiEntryMigrationFromTenAndClosedRoutes(t *testing.T) {
	app, author, _, admin, community := entrySetup(t)
	id := createDiscovery(t, author, "原发现页保留", community)
	dropSchemasAfterForTest(t, app, 10)
	upgraded, err := New(app.config)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	if !wikiReadable(upgraded.db, id) {
		t.Fatal("migration lost discovery")
	}
	var n int
	if err = upgraded.db.QueryRow("SELECT count(*) FROM wiki_suggestions").Scan(&n); err != nil || n != 0 {
		t.Fatal("migration failed", err)
	}
	app.config.WikiEnabled = false
	for _, path := range []string{"/wiki/entries/new", "/my/wiki", "/admin/wiki", "/wiki/" + id + "/propose", "/wiki/" + id + "/submissions/1"} {
		mustStatus(t, admin.call("GET", path, nil, true), 404)
	}
	for _, path := range []string{"/wiki/entries", "/wiki/" + id + "/propose", "/wiki/" + id + "/suggestions", "/wiki/" + id + "/suggestions/x/withdraw", "/admin/wiki/" + id + "/versions/1/review", "/admin/wiki/" + id + "/suggestions/x/resolve"} {
		mustStatus(t, admin.call("POST", path, url.Values{}, true), 404)
	}
}

func TestWikiSuggestionRecoveryDecisionAndRestrictedCleanup(t *testing.T) {
	for _, decision := range []string{"accepted", "rejected"} {
		t.Run(decision, func(t *testing.T) {
			app, author, other, admin, community := entrySetup(t)
			id := submitEntry(t, author, "community", "恢复后不能直接公开的旧标题", community)
			reviewEntry(t, admin, id, 1, "accept", "通过")
			g := suggest(t, app, other, id, "summary", "备份之后处理的建议", "退回后必须更新依据", 1)
			backup, err := app.Backup()
			if err != nil {
				t.Fatal(err)
			}
			resolveSuggestion(t, admin, id, g, decision, "明确的人工处理结论", "", 1)
			destination := filepath.Join(t.TempDir(), "older-decision.db")
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
			s, err := loadWikiSuggestion(restored.db, g)
			if err != nil || s.State != decision {
				t.Fatal("old pending suggestion revived", err)
			}
			if decision == "rejected" {
				var rejected int
				if err = restored.db.QueryRow("SELECT was_rejected FROM wiki_suggestions WHERE id=?", g).Scan(&rejected); err != nil || rejected != 1 {
					t.Fatal("rejection basis lost", err)
				}
				if !wikiReadable(restored.db, id) {
					t.Fatal("rejection hid unchanged public entry")
				}
				return
			}
			if wikiReadable(restored.db, id) {
				t.Fatal("older accepted version republished")
			}
			// Restore strips old authority; grant a current reviewer in the isolated recovery instance.
			if err = restored.SetMaintenance("normal"); err != nil {
				t.Fatal(err)
			}
			newAdmin := coreUser(t, restored, "恢复清理人员")
			if err = restored.BootstrapAdmin(newAdmin.owner); err != nil {
				t.Fatal(err)
			}
			ordinary := coreUser(t, restored, "无权读取受限页")
			mustStatus(t, ordinary.call("GET", "/admin/wiki/restricted", nil, true), 403)
			list := newAdmin.call("GET", "/admin/wiki/restricted", nil, true)
			mustStatus(t, list, 200)
			if strings.Contains(list.Body.String(), "恢复后不能直接公开的旧标题") || strings.Contains(list.Body.String(), "退回后必须更新依据") || !strings.Contains(list.Body.String(), id) {
				t.Fatal("restricted queue exposed old text or omitted object")
			}
			var latest int
			if err = restored.db.QueryRow("SELECT latest_version FROM wiki_pages WHERE id=?", id).Scan(&latest); err != nil {
				t.Fatal(err)
			}
			if err = restored.SetMaintenance("readonly"); err != nil {
				t.Fatal(err)
			}
			mustStatus(t, newAdmin.call("POST", "/wiki/"+id+"/withdraw", url.Values{"version": {strconv.Itoa(latest)}, "state": {"deleted"}, "reason": {"privacy"}, "confirm": {"withdraw"}}, true), 303)
			old, err := loadWikiVersion(restored.db, id, 1)
			if err != nil || old.Body != "" || old.Review != "withdrawn" {
				t.Fatal("hidden recovery content not cleared", err)
			}
		})
	}
}
