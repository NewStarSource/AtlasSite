package atlas

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUserActivityPage(t *testing.T) {
	config := Config{Mode: "test", Address: "127.0.0.1:4200", Origin: "http://127.0.0.1:4200", AccountOrigin: "http://127.0.0.1:4100", Database: filepath.Join(t.TempDir(), "atlas.db")}
	app, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	// Create test identity
	identityID := randomID()
	_, err = app.db.Exec(`
		INSERT INTO users(id,alias,account_id,subject_id,status,status_version,issuer)
 VALUES(?,?,?,? ,'active',1,'')
	`, identityID, "测试用户", "test@example.com", randomID())
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

	// Create test reply
	_, err = app.createReply(post.ID, identityID, "测试回复", "")
	if err != nil {
		t.Fatal(err)
	}

	// Test user activity page
	response := httptest.NewRecorder()
	request := httptest.NewRequest("GET", config.Origin+"/u/"+identityID, nil)
	app.Handler().ServeHTTP(response, request)

	if response.Code != 200 {
		t.Fatalf("expected 200, got %d", response.Code)
	}

	body := response.Body.String()
	if !strings.Contains(body, "测试用户") {
		t.Fatal("user name not found in response")
	}

	if !strings.Contains(body, "测试标题") {
		t.Fatal("post title not found in response")
	}

	if !strings.Contains(body, "测试回复") {
		t.Fatal("reply content not found in response")
	}

	if !strings.Contains(body, "查看完整资料") {
		t.Fatal("profile link not found in response")
	}

	// Test non-existent user returns 404
	response = httptest.NewRecorder()
	request = httptest.NewRequest("GET", config.Origin+"/u/nonexistent", nil)
	app.Handler().ServeHTTP(response, request)

	if response.Code != 404 {
		t.Fatalf("expected 404 for non-existent user, got %d", response.Code)
	}
}

func TestBookmarkFunctionality(t *testing.T) {
	config := Config{Mode: "test", Address: "127.0.0.1:4200", Origin: "http://127.0.0.1:4200", AccountOrigin: "http://127.0.0.1:4100", Database: filepath.Join(t.TempDir(), "atlas.db")}
	app, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	// Create test identity
	identityID := randomID()
	_, err = app.db.Exec(`
		INSERT INTO users(id,alias,account_id,subject_id,status,status_version,issuer)
 VALUES(?,?,?,? ,'active',1,'')
	`, identityID, "测试用户", "test@example.com", randomID())
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

	// Test isBookmarked (should be false initially)
	isBookmarked, err := app.isBookmarked(identityID, post.ID)
	if err != nil {
		t.Fatal(err)
	}
	if isBookmarked {
		t.Fatal("post should not be bookmarked initially")
	}

	// Test addBookmark
	err = app.addBookmark(identityID, post.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Verify bookmark was added
	isBookmarked, err = app.isBookmarked(identityID, post.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !isBookmarked {
		t.Fatal("post should be bookmarked after adding")
	}

	// Verify bookmark_count was incremented
	updatedPost, err := app.getPost(post.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updatedPost.BookmarkCount != 1 {
		t.Fatalf("expected bookmark_count 1, got %d", updatedPost.BookmarkCount)
	}

	// Test listBookmarks
	bookmarks, err := app.listBookmarks(identityID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(bookmarks) != 1 {
		t.Fatalf("expected 1 bookmark, got %d", len(bookmarks))
	}
	if bookmarks[0].ID != post.ID {
		t.Fatalf("expected post ID %s, got %s", post.ID, bookmarks[0].ID)
	}

	// Test removeBookmark
	err = app.removeBookmark(identityID, post.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Verify bookmark was removed
	isBookmarked, err = app.isBookmarked(identityID, post.ID)
	if err != nil {
		t.Fatal(err)
	}
	if isBookmarked {
		t.Fatal("post should not be bookmarked after removing")
	}

	// Verify bookmark_count was decremented
	updatedPost, err = app.getPost(post.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updatedPost.BookmarkCount != 0 {
		t.Fatalf("expected bookmark_count 0, got %d", updatedPost.BookmarkCount)
	}
}

func TestListPostsByAuthor(t *testing.T) {
	config := Config{Mode: "test", Address: "127.0.0.1:4200", Origin: "http://127.0.0.1:4200", AccountOrigin: "http://127.0.0.1:4100", Database: filepath.Join(t.TempDir(), "atlas.db")}
	app, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	// Create test identities
	author1ID := randomID()
	author2ID := randomID()
	_, err = app.db.Exec(`
		INSERT INTO users(id,alias,account_id,subject_id,status,status_version,issuer)
 VALUES(?,?,?,? ,'active',1,'')
	`, author1ID, "作者1", "author1@example.com", randomID())
	if err != nil {
		t.Fatal(err)
	}

	// Use a different randomID to avoid collision
	time.Sleep(1 * time.Millisecond)
	author2ID = randomID()

	_, err = app.db.Exec(`
		INSERT INTO users(id,alias,account_id,subject_id,status,status_version,issuer)
 VALUES(?,?,?,? ,'active',1,'')
	`, author2ID, "作者2", "author2@example.com", randomID())
	if err != nil {
		t.Fatal(err)
	}

	// Create test community
	community, err := app.createCommunity("测试社群", "测试描述", "", "")
	if err != nil {
		t.Fatal(err)
	}

	// Create posts by author1
	_, err = app.createPost(community.ID, author1ID, "帖子1", "内容1")
	if err != nil {
		t.Fatal(err)
	}
	_, err = app.createPost(community.ID, author1ID, "帖子2", "内容2")
	if err != nil {
		t.Fatal(err)
	}

	// Create post by author2
	_, err = app.createPost(community.ID, author2ID, "帖子3", "内容3")
	if err != nil {
		t.Fatal(err)
	}

	// List posts by author1
	posts, err := app.listPostsByAuthor(author1ID, 10)
	if err != nil {
		t.Fatal(err)
	}

	if len(posts) != 2 {
		t.Fatalf("expected 2 posts by author1, got %d", len(posts))
	}

	// Verify posts are by author1
	for _, post := range posts {
		if post.AuthorID != author1ID {
			t.Fatalf("expected author ID %s, got %s", author1ID, post.AuthorID)
		}
	}
}
