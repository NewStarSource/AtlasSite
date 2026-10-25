package atlas

import (
	"net/http"
	"strconv"
)

const pageSize = 50

func pageNumber(r *http.Request) int {
	n, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || n < 0 {
		return 0
	}
	if n > 10000 {
		return 10000
	}
	return n
}
func pageOffset(r *http.Request) int { return pageNumber(r) * pageSize }
func optionalOffset(values []int) int {
	if len(values) > 0 && values[0] > 0 {
		return values[0]
	}
	return 0
}
func pageItems[T any](items []T) []T {
	if len(items) > pageSize {
		return items[:pageSize]
	}
	return items
}
func pagination(r *http.Request, more bool) string {
	page := pageNumber(r)
	if page == 0 && !more {
		return ""
	}
	body := `<nav class="action-row" aria-label="分页">`
	link := func(n int, label string) string {
		u := *r.URL
		q := u.Query()
		q.Set("page", strconv.Itoa(n))
		u.RawQuery = q.Encode()
		return `<a class="btn btn-secondary" href="` + esc(u.RequestURI()) + `">` + label + `</a>`
	}
	if page > 0 {
		body += link(page-1, "上一页")
	}
	if more && page < 10000 {
		body += link(page+1, "下一页")
	}
	return body + `</nav>`
}
