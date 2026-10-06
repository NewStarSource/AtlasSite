package atlas

import (
	"net/url"
	"testing"
	"time"
)

func TestCoreJobRetryLeaseAndNotificationWithdrawalReplay(t *testing.T) {
	app := coreApp(t)
	a := coreUser(t, app, "作者")
	b := coreUser(t, app, "回复者")
	post := publishFor(t, a, "讨论", "正文")
	mustStatus(t, b.call("POST", "/api/v1/p/"+post+"/replies", url.Values{"request_id": {randomID()}, "content": {"通知正文"}}, true), 303)
	for n := 0; n < 2; n++ {
		if err := app.processJobs(); err != nil {
			t.Fatal(err)
		}
	}
	var state string
	if err := app.db.QueryRow("SELECT status FROM jobs WHERE kind='reply'").Scan(&state); err != nil || state != "done" {
		t.Fatal("outbox not completed", state, err)
	}
	id := randomID()
	if _, err := app.db.Exec("INSERT INTO jobs(id,business_key,kind,object_id,available_at,created_at) VALUES(?,?,'unknown','',?,?)", id, id, time.Now().Unix(), time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= 4; attempt++ {
		if _, err := app.db.Exec("UPDATE jobs SET available_at=unixepoch()-1 WHERE id=?", id); err != nil {
			t.Fatal(err)
		}
		if err := app.processJobs(); err != nil {
			t.Fatal(err)
		}
		var tries int
		var lease int64
		if err := app.db.QueryRow("SELECT status,attempts,lease_until FROM jobs WHERE id=?", id).Scan(&state, &tries, &lease); err != nil {
			t.Fatal(err)
		}
		if tries != attempt || lease != 0 {
			t.Fatal("retry state", tries, lease)
		}
		if attempt == 4 && state != "failed" {
			t.Fatal("retry never entered failed state")
		}
	}
	var notification string
	if err := app.db.QueryRow("SELECT id FROM notifications WHERE user_id=? AND kind='reply'", a.owner).Scan(&notification); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, b.call("POST", "/api/v1/notifications/"+notification+"/delete", url.Values{}, true), 404)
	mustStatus(t, a.call("POST", "/api/v1/notifications/"+notification+"/delete", url.Values{}, true), 303)
	records, err := app.readJournal()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) == 0 || records[len(records)-1].Action != "delete-notification" {
		t.Fatal("notification withdrawal not journaled")
	}
}
