package atlas

import (
	"strings"
	"testing"
)

func TestCoreSubtreeOriginalsIsolatedAndIndependentRelease(t *testing.T) {
	app := coreApp(t)
	author := coreUser(t, app, "作者")
	replier := coreUser(t, app, "回复作者")
	reporter := coreUser(t, app, "举报人")
	post := publishFor(t, author, "主帖", "主帖正文")
	parent, err := app.saveReply(post, replier.owner, "父回复正文", "", randomID())
	if err != nil {
		t.Fatal(err)
	}
	child, err := app.saveReply(post, author.owner, "子回复正文", parent.ID, randomID())
	if err != nil {
		t.Fatal(err)
	}
	first := reportFor(t, reporter, post, "")
	second := reportFor(t, reporter, parent.ID, "")
	if err = app.ResolveCase(first, "reviewer-a", "restricted"); err != nil {
		t.Fatal(err)
	}
	if err = app.ResolveCase(second, "reviewer-b", "restricted"); err != nil {
		t.Fatal(err)
	}
	var content string
	for _, id := range []string{parent.ID, child.ID} {
		if err = app.db.QueryRow("SELECT content FROM replies WHERE id=?", id).Scan(&content); err != nil || content != "" {
			t.Fatal("subtree risk original retained in primary DB", id, err)
		}
	}
	appeal := reportFor(t, author, post, first)
	if err = app.ResolveCase(appeal, "reviewer-c", "released"); err != nil {
		t.Fatal(err)
	}
	page := author.call("GET", "/p/"+post, nil, true)
	mustStatus(t, page, 200)
	if strings.Contains(page.Body.String(), "父回复正文") || strings.Contains(page.Body.String(), "子回复正文") {
		t.Fatal("independent reply restriction released with parent")
	}
	appeal = reportFor(t, replier, parent.ID, second)
	if err = app.ResolveCase(appeal, "reviewer-c", "released"); err != nil {
		t.Fatal(err)
	}
	page = author.call("GET", "/p/"+post, nil, true)
	mustStatus(t, page, 200)
	for _, text := range []string{"父回复正文", "子回复正文"} {
		if !strings.Contains(page.Body.String(), text) {
			t.Fatal("released subtree not recovered", text)
		}
	}
}
