package atlas

import (
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func grantValues(user, role, version string) url.Values {
	return url.Values{"user_id": {user}, "role": {role}, "version": {version}, "authority": {"合成授权记录"}, "expires": {time.Now().AddDate(0, 3, 0).UTC().Format("2006-01-02")}}
}

func TestManagementPermissionsAndLifecycle(t *testing.T) {
	app := coreApp(t)
	admin := coreUser(t, app, "lixx")
	member := coreUser(t, app, "团队成员")
	rep := coreUser(t, app, "代表")
	ordinary := coreUser(t, app, "普通用户")
	community, _ := seedCoreCommunity(t, app)
	other := randomID()
	if _, err := app.db.Exec(`INSERT INTO communities(id,slug,name,description,status,created_at,updated_at) VALUES(?,'other','另一个社群','说明','active',unixepoch(),unixepoch())`, other); err != nil {
		t.Fatal(err)
	}
	if err := app.BootstrapAdmin("missing"); err != errInput {
		t.Fatal("unknown bootstrap", err)
	}
	if err := app.BootstrapAdmin("lixx"); err != nil {
		t.Fatal(err)
	}
	if err := app.BootstrapAdmin(member.owner); err != errConflict {
		t.Fatal("repeated bootstrap", err)
	}
	anon := &coreClient{app: app}
	mustStatus(t, anon.call("GET", "/admin", nil, false), 401)
	for _, path := range []string{"/admin", "/admin/team", "/admin/representatives", "/admin/log", "/admin/communities/" + community} {
		mustStatus(t, ordinary.call("GET", path, nil, true), 403)
		mustStatus(t, admin.call("GET", path, nil, true), 200)
	}
	if !strings.Contains(admin.call("GET", "/", nil, true).Body.String(), `href="/admin"`) {
		t.Fatal("management entry absent")
	}
	if strings.Contains(ordinary.call("GET", "/", nil, true).Body.String(), `href="/admin"`) {
		t.Fatal("ordinary user entry exposed")
	}
	mustStatus(t, ordinary.call("POST", "/admin/team", grantValues(ordinary.owner, "admin", "0"), true), 403)
	mustStatus(t, admin.call("POST", "/admin/team", grantValues(member.owner, "member", "0"), false), 403)
	mustStatus(t, admin.call("POST", "/admin/team", grantValues(member.owner, "member", "0"), true), 303)
	mustStatus(t, member.call("GET", "/admin/team", nil, true), 200)
	mustStatus(t, member.call("POST", "/admin/team", grantValues(ordinary.owner, "admin", "0"), true), 403)
	mustStatus(t, member.call("POST", "/admin/team/"+admin.owner+"/remove", url.Values{"version": {"1"}}, true), 403)
	values := grantValues(rep.owner, "", "0")
	values.Set("community_id", community)
	mustStatus(t, admin.call("POST", "/admin/representatives", values, true), 303)
	mustStatus(t, admin.call("POST", "/admin/representatives", values, true), 409)
	for _, path := range []string{"/admin", "/admin/representatives", "/admin/log", "/admin/communities/" + community} {
		mustStatus(t, rep.call("GET", path, nil, true), 200)
	}
	mustStatus(t, rep.call("GET", "/admin/team", nil, true), 403)
	mustStatus(t, rep.call("GET", "/admin/communities/"+other, nil, true), 403)
	if strings.Contains(rep.call("GET", "/admin", nil, true).Body.String(), "另一个社群") {
		t.Fatal("other community exposed")
	}
	edit := url.Values{"version": {"1"}, "name": {"更新的社群"}, "description": {"<script>alert(1)</script>"}, "contact": {"站内联系"}}
	mustStatus(t, rep.call("POST", "/admin/communities/"+other, edit, true), 403)
	mustStatus(t, rep.call("POST", "/admin/communities/"+community, edit, false), 403)
	mustStatus(t, rep.call("POST", "/admin/communities/"+community, edit, true), 303)
	mustStatus(t, rep.call("POST", "/admin/communities/"+community, edit, true), 409)
	public := ordinary.call("GET", "/c/test-community/about", nil, true)
	mustStatus(t, public, 200)
	if !strings.Contains(public.Body.String(), "更新的社群") || !strings.Contains(public.Body.String(), "&lt;script&gt;") || strings.Contains(public.Body.String(), "<script>alert(1)</script>") {
		t.Fatal("updated data or escaping missing")
	}
	log := rep.call("GET", "/admin/log", nil, true).Body.String()
	if !strings.Contains(log, "更新社群资料") || strings.Contains(log, "更新团队授权") || strings.Contains(log, "初始化管理员") {
		t.Fatal("representative audit scope incorrect")
	}
	mustStatus(t, rep.call("POST", "/admin/representatives", values, true), 403)
	mustStatus(t, admin.call("POST", "/admin/team/"+admin.owner+"/remove", url.Values{"version": {"1"}}, true), 409)
	mustStatus(t, admin.call("POST", "/admin/team", grantValues(admin.owner, "member", "1"), true), 409)
	mustStatus(t, admin.call("POST", "/admin/representatives/"+community+"/remove", url.Values{"version": {"1"}}, true), 303)
	mustStatus(t, rep.call("GET", "/admin", nil, true), 403)
	mustStatus(t, rep.call("POST", "/admin/communities/"+community, edit, true), 403)
	values.Set("version", "1")
	mustStatus(t, admin.call("POST", "/admin/representatives", values, true), 409)
	values.Set("version", "2")
	mustStatus(t, admin.call("POST", "/admin/representatives", values, true), 303)
	if _, err := app.db.Exec("UPDATE community_representatives SET expires_at=unixepoch()-1 WHERE community_id=?", community); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, rep.call("GET", "/admin", nil, true), 403)
	if _, err := app.db.Exec("UPDATE sessions SET reauthenticated_at=0 WHERE user_id=?", admin.owner); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, admin.call("POST", "/admin/team", grantValues(ordinary.owner, "admin", "0"), true), 403)
	if _, err := app.db.Exec("UPDATE sessions SET reauthenticated_at=unixepoch() WHERE user_id=?", admin.owner); err != nil {
		t.Fatal(err)
	}
	if err := app.SetMaintenance("readonly"); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, admin.call("POST", "/admin/team", grantValues(ordinary.owner, "admin", "0"), true), 503)
	mustStatus(t, admin.call("GET", "/admin", nil, true), 200)
}

func TestManagementInvalidGrantsAndConcurrentVersions(t *testing.T) {
	app := coreApp(t)
	admin := coreUser(t, app, "admin")
	second := coreUser(t, app, "second")
	rep := coreUser(t, app, "rep")
	community, _ := seedCoreCommunity(t, app)
	if err := app.BootstrapAdmin(admin.owner); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, admin.call("POST", "/admin/team", grantValues(second.owner, "admin", "0"), true), 303)
	for _, change := range []struct{ key, value string }{{"role", "superuser"}, {"version", "oops"}, {"authority", ""}, {"expires", "2099-01-01"}, {"expires", "2020-01-01"}, {"user_id", randomID()}} {
		v := grantValues(rep.owner, "member", "0")
		v.Set(change.key, change.value)
		mustStatus(t, admin.call("POST", "/admin/team", v, true), 400)
	}
	var before int
	app.db.QueryRow("SELECT count(*) FROM management_events").Scan(&before)
	var wg sync.WaitGroup
	statuses := make(chan int, 2)
	for _, client := range []*coreClient{admin, second} {
		wg.Add(1)
		go func(c *coreClient) {
			defer wg.Done()
			v := grantValues(rep.owner, "", "0")
			v.Set("community_id", community)
			statuses <- c.call("POST", "/admin/representatives", v, true).Code
		}(client)
	}
	wg.Wait()
	close(statuses)
	counts := map[int]int{}
	for s := range statuses {
		counts[s]++
	}
	if counts[303] != 1 || counts[409] != 1 {
		t.Fatal("concurrent grant results", counts)
	}
	var after int
	app.db.QueryRow("SELECT count(*) FROM management_events").Scan(&after)
	if after != before+1 {
		t.Fatal("audit not atomic", before, after)
	}
	mustStatus(t, second.call("POST", "/admin/team/"+admin.owner+"/remove", url.Values{"version": {"1"}}, true), 303)
	mustStatus(t, admin.call("GET", "/admin", nil, true), 403)
	mustStatus(t, second.call("POST", "/admin/team/"+second.owner+"/remove", url.Values{"version": {"1"}}, true), 409)
}

func TestManagementRestoreDoesNotResurrectGrants(t *testing.T) {
	app := coreApp(t)
	admin := coreUser(t, app, "admin")
	rep := coreUser(t, app, "rep")
	community, _ := seedCoreCommunity(t, app)
	if err := app.BootstrapAdmin(admin.owner); err != nil {
		t.Fatal(err)
	}
	v := grantValues(rep.owner, "", "0")
	v.Set("community_id", community)
	mustStatus(t, admin.call("POST", "/admin/representatives", v, true), 303)
	backup, err := app.Backup()
	if err != nil {
		t.Fatal(err)
	}
	if backup.Schema != currentSchema {
		t.Fatal("backup schema", backup.Schema)
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
	if err = restored.db.QueryRow("SELECT (SELECT count(*) FROM team_members)+(SELECT count(*) FROM community_representatives)").Scan(&n); err != nil || n != 0 {
		t.Fatal("restored stale grants", n, err)
	}
	if err = restored.BootstrapAdmin(admin.owner); err != errForbidden {
		t.Fatal("readonly bootstrap", err)
	}
	var name string
	if err = restored.db.QueryRow("SELECT name FROM communities WHERE id=?", community).Scan(&name); err != nil || name != "合成社群" {
		t.Fatal("restore lost community", name, err)
	}
}

func TestManagementSeededCommunityKeys(t *testing.T) {
	app := coreApp(t)
	admin := coreUser(t, app, "admin")
	rep := coreUser(t, app, "rep")
	if err := app.BootstrapAdmin(admin.owner); err != nil {
		t.Fatal(err)
	}
	if _, err := app.db.Exec(`INSERT INTO communities(id,slug,name,description,status,created_at,updated_at) VALUES('react-cn','react-cn','React 中文社区','说明','active',unixepoch(),unixepoch())`); err != nil {
		t.Fatal(err)
	}
	v := grantValues(rep.owner, "", "0")
	v.Set("community_id", "react-cn")
	mustStatus(t, admin.call("POST", "/admin/representatives", v, true), 303)
	mustStatus(t, rep.call("GET", "/admin/communities/react-cn", nil, true), 200)
	mustStatus(t, rep.call("POST", "/admin/communities/react-cn", url.Values{"version": {"1"}, "name": {"React 中文社区"}, "description": {"已维护"}}, true), 303)
}
