package atlas

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPostCreationAndRetrieval(t *testing.T) {
	config := Config{Mode: "test", Address: "127.0.0.1:4200", Origin: "http://127.0.0.1:4200", AccountOrigin: "http://127.0.0.1:4100", Database: filepath.Join(t.TempDir(), "atlas.db")}
	app, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	// Create a test identity
	identityID := randomID()
	_, err = app.db.Exec(`
		INSERT INTO identities (id, name, email, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
	`, identityID, "测试用户", "test@example.com", time.Now().Unix(), time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}

	// Create a test community
	community, err := app.createCommunity("测试社群", "测试描述", "", "")
	if err != nil {
		t.Fatal(err)
	}

	// Create a post
	post, err := app.createPost(community.ID, identityID, "测试标题", "测试内容")
	if err != nil {
		t.Fatal(err)
	}

	if post.Title != "测试标题" {
		t.Fatalf("expected title '测试标题', got '%s'", post.Title)
	}

	if post.Content != "测试内容" {
		t.Fatalf("expected content '测试内容', got '%s'", post.Content)
	}

	if post.Status != "published" {
		t.Fatalf("expected status 'published', got '%s'", post.Status)
	}

	// Retrieve the post
	retrieved, err := app.getPost(post.ID)
	if err != nil {
		t.Fatal(err)
	}

	if retrieved.ID != post.ID {
		t.Fatalf("expected post ID '%s', got '%s'", post.ID, retrieved.ID)
	}

	if retrieved.AuthorName != "测试用户" {
		t.Fatalf("expected author name '测试用户', got '%s'", retrieved.AuthorName)
	}

	// List posts
	posts, err := app.listPosts(community.ID, 10)
	if err != nil {
		t.Fatal(err)
	}

	if len(posts) != 1 {
		t.Fatalf("expected 1 post, got %d", len(posts))
	}

	if posts[0].ID != post.ID {
		t.Fatalf("expected post ID '%s', got '%s'", post.ID, posts[0].ID)
	}
}

func TestReplyCreationAndTree(t *testing.T) {
	config := Config{Mode: "test", Address: "127.0.0.1:4200", Origin: "http://127.0.0.1:4200", AccountOrigin: "http://127.0.0.1:4100", Database: filepath.Join(t.TempDir(), "atlas.db")}
	app, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	// Create test identity
	identityID := randomID()
	_, err = app.db.Exec(`
		INSERT INTO identities (id, name, email, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
	`, identityID, "测试用户", "test@example.com", time.Now().Unix(), time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}

	// Create test community and post
	community, err := app.createCommunity("测试社群", "测试描述", "", "")
	if err != nil {
		t.Fatal(err)
	}

	post, err := app.createPost(community.ID, identityID, "测试标题", "测试内容")
	if err != nil {
		t.Fatal(err)
	}

	// Create root reply
	reply1, err := app.createReply(post.ID, identityID, "第一条回复", "")
	if err != nil {
		t.Fatal(err)
	}

	if reply1.Level != 0 {
		t.Fatalf("expected level 0, got %d", reply1.Level)
	}

	// Create nested reply
	reply2, err := app.createReply(post.ID, identityID, "嵌套回复", reply1.ID)
	if err != nil {
		t.Fatal(err)
	}

	if reply2.Level != 1 {
		t.Fatalf("expected level 1, got %d", reply2.Level)
	}

	if !reply2.ParentID.Valid || reply2.ParentID.String != reply1.ID {
		t.Fatalf("expected parent ID '%s', got '%v'", reply1.ID, reply2.ParentID)
	}

	// List replies
	replies, err := app.listReplies(post.ID)
	if err != nil {
		t.Fatal(err)
	}

	if len(replies) != 2 {
		t.Fatalf("expected 2 replies, got %d", len(replies))
	}

	// Build tree
	tree := buildReplyTree(replies)
	if len(tree) != 1 {
		t.Fatalf("expected 1 root reply, got %d", len(tree))
	}

	if len(tree[0].Children) != 1 {
		t.Fatalf("expected 1 child reply, got %d", len(tree[0].Children))
	}

	// Check post reply count was updated
	updatedPost, err := app.getPost(post.ID)
	if err != nil {
		t.Fatal(err)
	}

	if updatedPost.ReplyCount != 2 {
		t.Fatalf("expected reply count 2, got %d", updatedPost.ReplyCount)
	}
}

func TestPostDetailPage(t *testing.T) {
	config := Config{Mode: "test", Address: "127.0.0.1:4200", Origin: "http://127.0.0.1:4200", AccountOrigin: "http://127.0.0.1:4100", Database: filepath.Join(t.TempDir(), "atlas.db")}
	app, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	// Create test data
	identityID := randomID()
	_, err = app.db.Exec(`
		INSERT INTO identities (id, name, email, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
	`, identityID, "测试用户", "test@example.com", time.Now().Unix(), time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}

	community, err := app.createCommunity("测试社群", "测试描述", "", "")
	if err != nil {
		t.Fatal(err)
	}

	post, err := app.createPost(community.ID, identityID, "测试标题", "测试内容")
	if err != nil {
		t.Fatal(err)
	}

	// Test post detail page
	response := httptest.NewRecorder()
	request := httptest.NewRequest("GET", config.Origin+"/p/"+post.ID, nil)
	app.Handler().ServeHTTP(response, request)

	if response.Code != 200 {
		t.Fatalf("expected 200, got %d", response.Code)
	}

	body := response.Body.String()
	if !strings.Contains(body, "测试标题") {
		t.Fatal("post title not found in response")
	}

	if !strings.Contains(body, "测试内容") {
		t.Fatal("post content not found in response")
	}

	if !strings.Contains(body, "测试用户") {
		t.Fatal("author name not found in response")
	}

	// Test non-existent post returns 404
	response = httptest.NewRecorder()
	request = httptest.NewRequest("GET", config.Origin+"/p/nonexistent", nil)
	app.Handler().ServeHTTP(response, request)

	if response.Code != 404 {
		t.Fatalf("expected 404 for non-existent post, got %d", response.Code)
	}
}

func TestHomepageFeed(t *testing.T) {
	config := Config{Mode: "test", Address: "127.0.0.1:4200", Origin: "http://127.0.0.1:4200", AccountOrigin: "http://127.0.0.1:4100", Database: filepath.Join(t.TempDir(), "atlas.db")}
	app, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	// Create test data
	identityID := randomID()
	_, err = app.db.Exec(`
		INSERT INTO identities (id, name, email, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
	`, identityID, "测试用户", "test@example.com", time.Now().Unix(), time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}

	community, err := app.createCommunity("测试社群", "测试描述", "", "")
	if err != nil {
		t.Fatal(err)
	}

	post, err := app.createPost(community.ID, identityID, "测试标题", "测试内容")
	if err != nil {
		t.Fatal(err)
	}

	// Test homepage shows post
	response := httptest.NewRecorder()
	request := httptest.NewRequest("GET", config.Origin+"/", nil)
	app.Handler().ServeHTTP(response, request)

	if response.Code != 200 {
		t.Fatalf("expected 200, got %d", response.Code)
	}

	body := response.Body.String()
	if !strings.Contains(body, "首页动态") {
		t.Fatal("homepage title not found")
	}

	if !strings.Contains(body, post.Title) {
		t.Fatal("post title not found on homepage")
	}
}

func TestFormatTime(t *testing.T) {
	now := time.Now()

	if formatTime(now.Add(-30*time.Second)) != "刚刚" {
		t.Fatal("expected '刚刚' for recent time")
	}

	if formatTime(now.Add(-5*time.Minute)) != "5 分钟前" {
		t.Fatalf("expected '5 分钟前', got '%s'", formatTime(now.Add(-5*time.Minute)))
	}

	if formatTime(now.Add(-2*time.Hour)) != "2 小时前" {
		t.Fatalf("expected '2 小时前', got '%s'", formatTime(now.Add(-2*time.Hour)))
	}

	if formatTime(now.Add(-3*24*time.Hour)) != "3 天前" {
		t.Fatalf("expected '3 天前', got '%s'", formatTime(now.Add(-3*24*time.Hour)))
	}

	oldDate := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if formatTime(oldDate) != "2020-01-01" {
		t.Fatalf("expected '2020-01-01', got '%s'", formatTime(oldDate))
	}
}
