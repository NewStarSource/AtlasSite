package atlas

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommunityRoutes(t *testing.T) {
	config := Config{
		Mode:          "test",
		Address:       "127.0.0.1:4200",
		Origin:        "http://127.0.0.1:4200",
		AccountOrigin: "http://127.0.0.1:4100",
		Database:      filepath.Join(t.TempDir(), "test.db"),
	}

	app, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	// Seed test data
	now := int64(1696500000)
	tx, _ := app.db.Begin()
	tx.Exec("INSERT INTO domains VALUES('test-domain','测试领域','测试用',1,?)", now)
	tx.Exec("INSERT INTO directions VALUES('test-dir','test-domain','测试方向','测试用',1,?)", now)
	tx.Exec("INSERT INTO subcategories VALUES('test-sub','test-dir','测试子类','测试用',1,?)", now)
	tx.Exec(`INSERT INTO communities VALUES('comm1','test-comm','测试社群','社群描述','test-sub','active','','','CC BY',1,?,?)`, now, now)
	tx.Exec("INSERT INTO topics VALUES('topic1','comm1','general','综合讨论','综合主题',1,'active',?)", now)
	tx.Exec(`INSERT INTO collections VALUES('coll1','comm1','announcement','欢迎','welcome','欢迎语','内容',1,'published',?,?)`, now, now)
	tx.Commit()

	handler := app.Handler()
	t.Run("ClassificationAndUncategorizedVisibility", func(t *testing.T) {
		if _, err := app.db.Exec(`INSERT INTO communities VALUES('uncat','uncat-comm','待分类测试','描述',NULL,'uncategorized','','','synthetic',0,?,?)`, now, now); err != nil {
			t.Fatal(err)
		}
		if _, err := app.db.Exec(`INSERT INTO communities VALUES('hidden','hidden-comm','隐藏测试','描述',NULL,'pending','','','synthetic',0,?,?)`, now, now); err != nil {
			t.Fatal(err)
		}
		for _, route := range []string{"/discover", "/c/test-comm", "/c/uncat-comm", "/c/uncat-comm/about"} {
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest("GET", "http://127.0.0.1:4200"+route, nil))
			if w.Code != 200 || strings.Contains(w.Body.String(), "隐藏测试") {
				t.Fatalf("classification visibility %s: %d %s", route, w.Code, w.Body.String())
			}
			if route == "/discover" && !strings.Contains(w.Body.String(), "/c/uncat-comm") {
				t.Fatal("uncategorized entry absent")
			}
			if route == "/c/test-comm" && !strings.Contains(w.Body.String(), "/discover#direction-test-dir") {
				t.Fatal("classification breadcrumb absent")
			}
			if route == "/c/uncat-comm/about" && !strings.Contains(w.Body.String(), "来源待核实") {
				t.Fatal("verification status absent")
			}
		}
	})

	t.Run("GetCommunityBySlug", func(t *testing.T) {
		req := httptest.NewRequest("GET", "http://127.0.0.1:4200/api/v1/c/test-comm", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != 200 {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}

		var result map[string]interface{}
		if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}

		t.Logf("Response: %+v", result)

		community, ok := result["community"].(map[string]interface{})
		if !ok {
			t.Fatalf("community field not found or wrong type: %+v", result)
		}

		if community["Name"] != "测试社群" {
			t.Errorf("expected name=测试社群, got %v", community["Name"])
		}
		if community["Status"] != "active" {
			t.Errorf("expected status=active, got %v", community["Status"])
		}

		topics := result["topics"].([]interface{})
		if len(topics) != 1 {
			t.Errorf("expected 1 topic, got %d", len(topics))
		}

		collections := result["collections"].([]interface{})
		if len(collections) != 1 {
			t.Errorf("expected 1 collection, got %d", len(collections))
		}
	})

	t.Run("CommunityNotFound", func(t *testing.T) {
		req := httptest.NewRequest("GET", "http://127.0.0.1:4200/c/nonexistent", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != 404 {
			t.Fatalf("expected 404, got %d", w.Code)
		}
	})

	t.Run("InactiveCommunityNotAccessible", func(t *testing.T) {
		tx, _ := app.db.Begin()
		tx.Exec(`INSERT INTO communities VALUES('comm2','inactive-comm','未激活','描述','test-sub','pending','','','',0,?,?)`, now, now)
		tx.Commit()

		req := httptest.NewRequest("GET", "http://127.0.0.1:4200/c/inactive-comm", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != 404 {
			t.Fatalf("expected 404 for inactive community, got %d", w.Code)
		}
	})

	t.Run("ListDomains", func(t *testing.T) {
		req := httptest.NewRequest("GET", "http://127.0.0.1:4200/api/v1/discover", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != 200 {
			t.Fatalf("expected 200, got %d", w.Code)
		}

		var result map[string]interface{}
		if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}

		domains := result["domains"].([]interface{})
		if len(domains) == 0 {
			t.Error("expected at least one domain")
		}
	})
}

func TestCommunityCreation(t *testing.T) {
	config := Config{
		Mode:          "test",
		Address:       "127.0.0.1:4200",
		Origin:        "http://127.0.0.1:4200",
		AccountOrigin: "http://127.0.0.1:4100",
		Database:      filepath.Join(t.TempDir(), "test.db"),
	}

	app, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	t.Run("CreateCommunity", func(t *testing.T) {
		community, err := app.createCommunity(
			"新社群",
			"这是一个新的测试社群",
			"https://example.com/source",
			"CC BY-SA 4.0",
		)
		if err != nil {
			t.Fatal(err)
		}

		t.Logf("Created community: %+v", community)

		if community.Name != "新社群" {
			t.Errorf("expected name=新社群, got %s", community.Name)
		}
		if community.Status != "uncategorized" {
			t.Errorf("expected status=uncategorized, got %s", community.Status)
		}
		if community.Slug == "" {
			t.Errorf("expected non-empty slug, got empty string")
		} else {
			t.Logf("Generated slug: %s", community.Slug)
		}
	})

	t.Run("DuplicateSlugError", func(t *testing.T) {
		// Use English name so slugify produces consistent slug
		app.createCommunity("Test Community", "First one", "", "")
		_, err := app.createCommunity("Test Community", "Second one", "", "")
		if err != ErrDuplicateSlug {
			t.Errorf("expected ErrDuplicateSlug, got %v", err)
		}
	})
}

func TestSlugify(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Hello World", "hello-world"},
		{"Test 123", "test-123"},
		{"Special!@#$%Characters", "specialcharacters"},
		{"多个-连续--符号", "---"},
	}

	for _, tt := range tests {
		result := slugify(tt.input)
		if result != tt.expected {
			t.Errorf("slugify(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}
