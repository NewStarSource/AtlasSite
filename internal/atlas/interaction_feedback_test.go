package atlas

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestInteractionJSONFeedbackAndFormFallback(t *testing.T) {
	app := coreApp(t)
	reader := coreUser(t, app, "读者")
	author := coreUser(t, app, "作者")
	post := publishFor(t, author, "合成内容", "正文")
	community, _ := seedCoreCommunity(t, app)
	call := func(path string, data url.Values, csrf bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", app.config.Origin+path, strings.NewReader(data.Encode()))
		r.Header.Set("Accept", "application/json")
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", app.config.Origin)
		if csrf {
			r.Header.Set("X-CSRF-Token", reader.csrf)
		}
		for _, c := range reader.cookies {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		app.Handler().ServeHTTP(w, r)
		return w
	}
	bookmark := url.Values{"bookmarked": {"true"}}
	mustStatus(t, call("/api/v1/p/"+post+"/bookmark", bookmark, false), 403)
	for _, desired := range []string{"true", "true", "false"} {
		bookmark.Set("bookmarked", desired)
		w := call("/api/v1/p/"+post+"/bookmark", bookmark, true)
		mustStatus(t, w, 200)
		var result struct {
			Bookmarked bool
			Count      int
		}
		if json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Bookmarked != (desired == "true") || result.Count != map[bool]int{true: 1, false: 0}[result.Bookmarked] {
			t.Fatal("incorrect bookmark feedback", w.Body.String())
		}
	}
	bookmark.Set("bookmarked", "true")
	mustStatus(t, reader.call("POST", "/api/v1/p/"+post+"/bookmark", bookmark, true), 303)
	page := reader.call("GET", "/", nil, true).Body.String()
	if !strings.Contains(page, `name="bookmarked" value="false"`) || !strings.Contains(page, `aria-label="已收藏"`) || !strings.Contains(page, "gorilla.csrf.Token") {
		t.Fatal("server-rendered bookmark state or form token missing")
	}
	subscription := url.Values{"kind": {"community"}, "object_id": {community}, "subscribed": {"true"}}
	w := call("/api/v1/subscriptions", subscription, true)
	mustStatus(t, w, 200)
	if !strings.Contains(w.Body.String(), `"subscribed":true`) {
		t.Fatal("subscription response missing")
	}
	subscription.Set("subscribed", "false")
	mustStatus(t, reader.call("POST", "/api/v1/subscriptions", subscription, true), 303)
	mustStatus(t, author.call("POST", "/api/v1/p/"+post+"/delete", url.Values{"version": {"1"}}, true), 303)
	mustStatus(t, call("/api/v1/p/"+post+"/bookmark", bookmark, true), 404)
}
