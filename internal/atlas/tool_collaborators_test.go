package atlas

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func eligibilityValues(user string, version int) url.Values {
	return url.Values{"user_id": {user}, "version": {strconv.Itoa(version)}, "eligible": {"1"}, "standard_url": {"https://example.test/public-member-standard"}, "confirm": {"verified"}, "reason": {"合成既有资格核验"}}
}
func collaboratorInvite(user, kind string, version int) url.Values {
	v := grantValues(user, "", strconv.Itoa(version))
	v.Set("kind", kind)
	v.Set("expires", time.Now().AddDate(0, 1, 0).Format("2006-01-02"))
	return v
}
func createCollaboration(t *testing.T, app *App, admin, rep, user *coreClient, community, kind string) string {
	t.Helper()
	mustStatus(t, admin.call("POST", collaborationBase(community)+"/eligibility", eligibilityValues(user.owner, 0), true), 303)
	mustStatus(t, rep.call("POST", collaborationBase(community)+"/invite", collaboratorInvite(user.owner, kind, 0), true), 303)
	var id string
	if err := app.db.QueryRow("SELECT id FROM tool_collaborators WHERE community_id=? AND user_id=? AND kind=?", community, user.owner, kind).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
func acceptCollaboration(t *testing.T, user *coreClient, id string, version int) {
	t.Helper()
	mustStatus(t, user.call("POST", "/my/community-tools/"+id+"/accept", url.Values{"version": {strconv.Itoa(version)}, "confirm": {"accept"}}, true), 303)
}

func TestToolCollaborationConsentAndScope(t *testing.T) {
	app, admin, rep, community := toolSetup(t)
	user, other := coreUser(t, app, "自愿整理者"), coreUser(t, app, "其他用户")
	id := createCollaboration(t, app, admin, rep, user, community, "guide")
	base := toolBase(community)
	guideID, annID := randomID(), randomID()
	guide, announcement := toolValues("guide", guideID, 0), toolValues("announcement", annID, 0)
	announcement.Set("title", "未获授权公告草稿")
	mustStatus(t, rep.call("POST", base, guide, true), 303)
	mustStatus(t, rep.call("POST", base, announcement, true), 303)
	mustStatus(t, user.call("GET", base, nil, true), 403)
	mustStatus(t, user.call("POST", base+"/"+guideID, toolValues("guide", guideID, 1), true), 403)
	if !strings.Contains(user.call("GET", "/my", nil, true).Body.String(), "社群协作") || !strings.Contains(user.call("GET", "/notifications", nil, true).Body.String(), "社群协作邀请已更新") {
		t.Fatal("invitation entry missing")
	}
	if strings.Contains(other.call("GET", "/my/community-tools", nil, true).Body.String(), "自愿整理者") || strings.Contains(other.call("GET", "/notifications", nil, true).Body.String(), "社群协作邀请已更新") {
		t.Fatal("another user's invitation exposed")
	}
	mustStatus(t, other.call("POST", "/my/community-tools/"+id+"/accept", url.Values{"version": {"1"}, "confirm": {"accept"}}, true), 404)
	mustStatus(t, user.call("POST", "/my/community-tools/"+id+"/accept", url.Values{"version": {"1"}}, true), 403)
	mustStatus(t, user.call("POST", "/my/community-tools/"+id+"/accept", url.Values{"version": {"1"}, "confirm": {"accept"}}, false), 403)
	acceptCollaboration(t, user, id, 1)
	page := user.call("GET", base, nil, true)
	mustStatus(t, page, 200)
	if !strings.Contains(page.Body.String(), "合成新人指引") || strings.Contains(page.Body.String(), "未获授权公告草稿") || strings.Contains(page.Body.String(), "新建公告") || strings.Contains(page.Body.String(), "协作授权") {
		t.Fatal("tool scope leaked in list/navigation")
	}
	for _, path := range []string{base + "/" + annID, base + "/" + annID + "/history", base + "/new?kind=announcement", collaborationBase(community), "/admin/team", "/admin/representatives", "/admin/log", "/admin/communities/" + community} {
		mustStatus(t, user.call("GET", path, nil, true), 403)
	}
	edit := toolValues("guide", guideID, 1)
	edit.Set("status", "published")
	mustStatus(t, user.call("POST", base+"/"+guideID, edit, true), 303)
	mustStatus(t, user.call("GET", base+"/"+guideID+"/history", nil, true), 200)
	mustStatus(t, user.call("POST", base, announcement, true), 403)
	mustStatus(t, user.call("POST", collaborationBase(community)+"/invite", collaboratorInvite(other.owner, "guide", 0), true), 403)
	post := publishFor(t, rep, "他人的原帖", "原帖不可由整理者编辑")
	mustStatus(t, user.call("POST", "/api/v1/p/"+post+"/edit", url.Values{"version": {"1"}, "title": {"越权修改"}, "content": {"不应保存"}}, true), 403)
	r := httptest.NewRequest("GET", app.config.Origin+base+"/new?kind=announcement", nil)
	r.Header.Set("Accept", "text/html")
	for _, cookie := range user.cookies {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	app.Handler().ServeHTTP(w, r)
	mustStatus(t, w, 403)
	if !strings.Contains(w.Body.String(), "查看协作状态") || !strings.Contains(w.Body.String(), "不在当前有效授权范围内") {
		t.Fatal("native scope error has no recovery path")
	}
	if strings.Contains(user.call("GET", "/", nil, true).Body.String(), `href="/admin"`) {
		t.Fatal("organizer acquired general management entry")
	}
	// Scope or term changes always require fresh consent.
	mustStatus(t, rep.call("POST", collaborationBase(community)+"/invite", collaboratorInvite(user.owner, "guide", 2), true), 303)
	mustStatus(t, user.call("GET", base, nil, true), 403)
	mustStatus(t, user.call("POST", "/my/community-tools/"+id+"/accept", url.Values{"version": {"1"}, "confirm": {"accept"}}, true), 409)
	acceptCollaboration(t, user, id, 3)
	mustStatus(t, user.call("POST", "/my/community-tools/"+id+"/leave", url.Values{"version": {"4"}}, true), 303)
	mustStatus(t, user.call("GET", base, nil, true), 403)
}

func TestToolCollaborationEligibilityAndTerm(t *testing.T) {
	app, admin, rep, community := toolSetup(t)
	user := coreUser(t, app, "待核验用户")
	base := collaborationBase(community)
	mustStatus(t, rep.call("POST", base+"/invite", collaboratorInvite(user.owner, "guide", 0), true), 400)
	mustStatus(t, rep.call("POST", base+"/eligibility", eligibilityValues(user.owner, 0), true), 403)
	mustStatus(t, admin.call("POST", base+"/eligibility", eligibilityValues(user.owner, 0), false), 403)
	mustStatus(t, admin.call("POST", base+"/eligibility", eligibilityValues(user.owner, 0), true), 303)
	mustStatus(t, admin.call("POST", base+"/eligibility", eligibilityValues(user.owner, 0), true), 409)
	mustStatus(t, admin.call("POST", base+"/invite", collaboratorInvite(user.owner, "guide", 0), true), 403)
	v := collaboratorInvite(user.owner, "guide", 0)
	v.Set("expires", time.Now().AddDate(0, 4, 0).Format("2006-01-02"))
	mustStatus(t, rep.call("POST", base+"/invite", v, true), 400)
	v.Set("expires", time.Now().AddDate(0, 1, 0).Format("2006-01-02"))
	v.Set("kind", "team")
	mustStatus(t, rep.call("POST", base+"/invite", v, true), 400)
	v.Set("kind", "discussion")
	mustStatus(t, rep.call("POST", base+"/invite", v, true), 303)
	var id string
	app.db.QueryRow("SELECT id FROM tool_collaborators WHERE user_id=?", user.owner).Scan(&id)
	mustStatus(t, user.call("POST", "/my/community-tools/"+id+"/decline", url.Values{"version": {"1"}}, true), 303)
	mustStatus(t, user.call("GET", toolBase(community), nil, true), 403)
	if _, err := app.db.Exec("UPDATE sessions SET reauthenticated_at=0 WHERE user_id=?", rep.owner); err != nil {
		t.Fatal(err)
	}
	v.Set("version", "2")
	mustStatus(t, rep.call("POST", base+"/invite", v, true), 403)
}

func TestToolCollaborationInvalidation(t *testing.T) {
	for _, scenario := range []string{"revoke", "expiry", "representative-expiry", "representative-renewal", "representative-replacement", "eligibility-change", "recipient-status", "representative-status", "community-status"} {
		t.Run(scenario, func(t *testing.T) {
			app, admin, rep, community := toolSetup(t)
			user := coreUser(t, app, "整理者")
			id := createCollaboration(t, app, admin, rep, user, community, "guide")
			acceptCollaboration(t, user, id, 1)
			mustStatus(t, user.call("GET", toolBase(community), nil, true), 200)
			var err error
			switch scenario {
			case "revoke":
				mustStatus(t, rep.call("POST", collaborationBase(community)+"/"+id+"/revoke", url.Values{"version": {"2"}}, true), 303)
			case "expiry":
				_, err = app.db.Exec("UPDATE tool_collaborators SET expires_at=unixepoch() WHERE id=?", id)
			case "representative-expiry":
				_, err = app.db.Exec("UPDATE community_representatives SET expires_at=unixepoch() WHERE community_id=?", community)
			case "representative-renewal":
				v := grantValues(rep.owner, "", "1")
				v.Set("community_id", community)
				mustStatus(t, admin.call("POST", "/admin/representatives", v, true), 303)
			case "representative-replacement":
				v := grantValues(admin.owner, "", "1")
				v.Set("community_id", community)
				mustStatus(t, admin.call("POST", "/admin/representatives", v, true), 303)
			case "eligibility-change":
				v := eligibilityValues(user.owner, 1)
				v.Set("eligible", "0")
				mustStatus(t, admin.call("POST", collaborationBase(community)+"/eligibility", v, true), 303)
				v.Set("eligible", "1")
				v.Set("version", "2")
				mustStatus(t, admin.call("POST", collaborationBase(community)+"/eligibility", v, true), 303)
			case "recipient-status":
				_, err = app.db.Exec("UPDATE users SET status='suspended' WHERE id=?; UPDATE users SET status='active' WHERE id=?", user.owner, user.owner)
			case "representative-status":
				_, err = app.db.Exec("UPDATE users SET status='suspended' WHERE id=?; UPDATE users SET status='active' WHERE id=?", rep.owner, rep.owner)
			case "community-status":
				_, err = app.db.Exec("UPDATE communities SET status='archived' WHERE id=?; UPDATE communities SET status='active' WHERE id=?", community, community)
			}
			if err != nil {
				t.Fatal(err)
			}
			mustStatus(t, user.call("GET", toolBase(community), nil, true), 403)
			v := toolValues("guide", randomID(), 0)
			mustStatus(t, user.call("POST", toolBase(community), v, true), 403)
			if canMaintainTool(app.db, user.owner, community, "guide") {
				t.Fatal("stale grant still active")
			}
		})
	}
}

func TestToolCollaborationAtomicityAndReadOnly(t *testing.T) {
	app, admin, rep, community := toolSetup(t)
	user := coreUser(t, app, "并发整理者")
	id := createCollaboration(t, app, admin, rep, user, community, "announcement")
	var wg sync.WaitGroup
	results := make(chan int, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- user.call("POST", "/my/community-tools/"+id+"/accept", url.Values{"version": {"1"}, "confirm": {"accept"}}, true).Code
		}()
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
			t.Fatal(code)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal("consent raced")
	}
	if _, err := app.db.Exec("CREATE TRIGGER collaboration_fail BEFORE INSERT ON management_events WHEN NEW.action='tool-revoke' BEGIN SELECT RAISE(ABORT,'synthetic rollback'); END;"); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, rep.call("POST", collaborationBase(community)+"/"+id+"/revoke", url.Values{"version": {"2"}}, true), 503)
	if !canMaintainTool(app.db, user.owner, community, "announcement") {
		t.Fatal("failed transaction partially revoked")
	}
	if err := app.SetMaintenance("readonly"); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, user.call("POST", toolBase(community), toolValues("announcement", randomID(), 0), true), 503)
	mustStatus(t, user.call("POST", "/my/community-tools/"+id+"/accept", url.Values{"version": {"2"}, "confirm": {"accept"}}, true), 503)
	// Withdrawing consent remains available in read-only mode.
	mustStatus(t, user.call("POST", "/my/community-tools/"+id+"/leave", url.Values{"version": {"2"}}, true), 303)
	if canMaintainTool(app.db, user.owner, community, "announcement") {
		t.Fatal("read-only prevented withdrawal")
	}
}

func TestToolCollaborationRestoreClearsAuthority(t *testing.T) {
	app, admin, rep, community := toolSetup(t)
	user := coreUser(t, app, "备份整理者")
	id := createCollaboration(t, app, admin, rep, user, community, "discussion")
	acceptCollaboration(t, user, id, 1)
	backup, err := app.Backup()
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "restored.db")
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
	var n int
	if err = restored.db.QueryRow("SELECT (SELECT count(*) FROM tool_collaborators)+(SELECT count(*) FROM tool_eligibility)").Scan(&n); err != nil || n != 0 {
		t.Fatal("restored stale collaboration authority", err)
	}
	if canMaintainTool(restored.db, user.owner, community, "discussion") {
		t.Fatal("restored stale scope")
	}
}

func TestToolCollaborationOwnExport(t *testing.T) {
	app, admin, rep, community := toolSetup(t)
	user, other := coreUser(t, app, "本人导出"), coreUser(t, app, "其他整理者")
	createCollaboration(t, app, admin, rep, user, community, "guide")
	createCollaboration(t, app, admin, rep, other, community, "discussion")
	w := user.call("POST", "/api/v1/me/export", url.Values{}, true)
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
			for _, name := range []string{"tool_collaborations", "tool_eligibility"} {
				var rows []map[string]any
				if err = json.Unmarshal(data[name], &rows); err != nil || len(rows) != 1 {
					t.Fatal("own collaboration export incomplete", name, err)
				}
			}
			if strings.Contains(string(raw), other.owner) {
				t.Fatal("export includes other collaborator")
			}
			return
		}
	}
	t.Fatal("own data missing from export")
}

func TestToolCollaborationMigrationFromEight(t *testing.T) {
	app, _, rep, community := toolSetup(t)
	id := randomID()
	v := toolValues("guide", id, 0)
	v.Set("status", "published")
	mustStatus(t, rep.call("POST", toolBase(community), v, true), 303)
	if _, err := app.db.Exec("DROP TRIGGER tool_collaborator_account_inactive; DROP TRIGGER tool_collaborator_community_inactive; DROP TABLE tool_collaborators; DROP TABLE tool_eligibility; DELETE FROM schema_migrations WHERE version=9;"); err != nil {
		t.Fatal(err)
	}
	backup, err := app.Backup()
	if err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(app.config.BackupDirectory, backup.Archive)
	backup.Schema = 8
	raw, err := json.Marshal(backup)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(archive+".json", raw, 0600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "from-eight.db")
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
	if err != nil || tt.Version != 1 || tt.Status != "published" || tt.Content != "**合成工具正文**" {
		t.Fatal("version 8 migration changed existing tool", err)
	}
	var n int
	if err = restored.db.QueryRow("SELECT count(*) FROM tool_collaborators").Scan(&n); err != nil || n != 0 {
		t.Fatal("version 9 collaboration migration failed", err)
	}
}
