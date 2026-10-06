package atlas

import (
	"net/url"
	"strings"
	"testing"
)

func TestCoreCommunitySubscriptionPrivacyNotificationsAndWithdrawal(t *testing.T) {
	app := coreApp(t)
	author := coreUser(t, app, "作者")
	reader := coreUser(t, app, "订阅者")
	outsider := coreUser(t, app, "其他人")
	community, topic := seedCoreCommunity(t, app)
	target := url.Values{"kind": {"community"}, "object_id": {community}, "subscribed": {"true"}, "notifications": {"true"}}
	mustStatus(t, reader.call("POST", "/api/v1/subscriptions", target, true), 303)
	mustStatus(t, reader.call("POST", "/api/v1/subscriptions", target, true), 303)
	page := outsider.call("GET", "/u/"+reader.owner+"/subscriptions", nil, true)
	mustStatus(t, page, 200)
	if !strings.Contains(page.Body.String(), "合成社群") {
		t.Fatal("relationship page missing subscribed community")
	}
	mustStatus(t, reader.call("POST", "/api/v1/settings", url.Values{"hide_relations": {"1"}, "reply_notifications": {"1"}}, true), 303)
	mustStatus(t, outsider.call("GET", "/u/"+reader.owner+"/subscriptions", nil, true), 404)
	mustStatus(t, reader.call("GET", "/my/subscriptions", nil, true), 200)
	topicTarget := url.Values{"kind": {"topic"}, "object_id": {topic}, "subscribed": {"true"}, "notifications": {"true"}}
	mustStatus(t, reader.call("POST", "/api/v1/subscriptions", topicTarget, true), 303)
	w := author.call("POST", "/api/v1/posts", url.Values{"request_id": {randomID()}, "title": {"订阅的新帖"}, "content": {"正文"}, "license": {"reserved"}, "community": {"test-community"}}, true)
	mustStatus(t, w, 303)
	post := strings.TrimPrefix(w.Header().Get("Location"), "/p/")
	mustStatus(t, author.call("POST", "/api/v1/p/"+post+"/association", url.Values{"version": {"1"}, "community": {"test-community"}, "topic_ids": {topic}}, true), 303)
	var n int
	if err := app.db.QueryRow("SELECT count(*) FROM notifications WHERE user_id=? AND kind='post' AND object_id=?", reader.owner, post).Scan(&n); err != nil || n != 1 {
		t.Fatal("duplicate subscription notification", n, err)
	}
	if !strings.Contains(reader.call("GET", "/notifications", nil, true).Body.String(), "新讨论") {
		t.Fatal("new post notification missing")
	}
	target.Set("subscribed", "false")
	mustStatus(t, reader.call("POST", "/api/v1/subscriptions", target, true), 303)
	mustStatus(t, author.call("POST", "/api/v1/p/"+post+"/delete", url.Values{"version": {"2"}}, true), 303)
	if strings.Contains(reader.call("GET", "/notifications", nil, true).Body.String(), "新讨论") {
		t.Fatal("withdrawn post still notified")
	}
	records, err := app.readJournal()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range records {
		if r.Action == "subscription-off" {
			found = true
		}
	}
	if !found {
		t.Fatal("subscription withdrawal missing from journal")
	}
}
