package atlas

import (
	"fmt"
	"net/http/httptest"
	"net/url"
	"sort"
	"sync"
	"testing"
	"time"
)

func TestCoreBoundedConcurrentReadSample(t *testing.T) {
	app := coreApp(t)
	tx, err := app.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 100; n++ {
		if _, err = tx.Exec("INSERT INTO users VALUES(?,?,?,?, 'active',1,'synthetic')", fmt.Sprintf("fixture-%d", n), fmt.Sprintf("account-%d", n), fmt.Sprintf("subject-%d", n), fmt.Sprintf("合成作者%d", n)); err != nil {
			t.Fatal(err)
		}
	}
	for n := 0; n < 2000; n++ {
		if _, err = tx.Exec("INSERT INTO posts(id,author_id,title,content,created_at,updated_at) VALUES(?,?,?,?,unixepoch(),unixepoch())", fmt.Sprintf("post-%d", n), fmt.Sprintf("fixture-%d", n%100), fmt.Sprintf("中文关键词样本%d", n), "合成数据，测试后全部清除"); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	handler := app.Handler()
	start := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	var durations []float64
	failures := 0
	began := time.Now()
	for n := 0; n < 100; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for _, path := range []string{"/", "/search?type=post&q=%E4%B8%AD%E6%96%87%E5%85%B3%E9%94%AE%E8%AF%8D"} {
				at := time.Now()
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, httptest.NewRequest("GET", app.config.Origin+path, nil))
				elapsed := float64(time.Since(at).Microseconds()) / 1000
				mu.Lock()
				durations = append(durations, elapsed)
				if w.Code != 200 {
					failures++
				}
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()
	sort.Float64s(durations)
	if failures != 0 {
		t.Fatal("concurrent read failures", failures)
	}
	t.Logf("BOUNDED_SAMPLE accounts=100 posts=2000 simultaneous_clients=100 requests=%d elapsed_ms=%d p50_ms=%.2f p95_ms=%.2f p99_ms=%.2f; handler sample, not a 30-minute capacity acceptance", len(durations), time.Since(began).Milliseconds(), durations[len(durations)/2], durations[len(durations)*95/100], durations[len(durations)*99/100])
}
func TestCoreExpiredWriteWindowPreservesRights(t *testing.T) {
	app := coreApp(t)
	c := coreUser(t, app, "本人")
	app.config.WriteUntil = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	mustStatus(t, c.call("POST", "/api/v1/posts", nil, true), 503)
	mustStatus(t, c.call("GET", "/help/emergency", nil, true), 200)
	mustStatus(t, c.call("POST", "/api/v1/cases", url.Values{"kind": {"emergency"}, "request_id": {randomID()}, "detail": {"合成紧急请求"}}, true), 200)
}
