package atlas

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPublicListsCanReachOlderContent(t *testing.T) {
	app := coreApp(t)
	c := coreUser(t, app, "pager")
	for n := 0; n < 55; n++ {
		p, err := app.createPost("", c.owner, fmt.Sprintf("Paging item %02d", n), "Paging body")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = app.db.Exec("INSERT INTO post_bookmarks(id,identity_id,post_id,created_at) VALUES(?,?,?,?)", randomID(), c.owner, p.ID, 1000+n); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"/", "/u/" + c.owner, "/my/bookmarks", "/search?q=Paging&type=post"} {
		first := c.call("GET", path, nil, true)
		mustStatus(t, first, 200)
		if !strings.Contains(first.Body.String(), "下一页") {
			t.Fatal("missing next", path)
		}
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		second := c.call("GET", path+sep+"page=1", nil, true)
		mustStatus(t, second, 200)
		if !strings.Contains(second.Body.String(), "Paging item") || !strings.Contains(second.Body.String(), "上一页") || strings.Contains(second.Body.String(), "下一页") {
			t.Fatal("older content unreachable", path)
		}
	}
	first, err := app.queryPostsPage("", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := app.queryPostsPage("", 50, 50)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, p := range first {
		seen[p.ID] = true
	}
	for _, p := range second {
		if seen[p.ID] {
			t.Fatal("unstable tie pagination")
		}
	}
	request := httptest.NewRequest("GET", "http://example.test/search?q=a%26b&type=post&page=1", nil)
	nav := pagination(request, true)
	if !strings.Contains(nav, "q=a%26b") || !strings.Contains(nav, "type=post") {
		t.Fatal("filters lost", nav)
	}
}
