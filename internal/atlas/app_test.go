package atlas

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBaselineAndClosedAuthentication(t *testing.T) {
	config := Config{Mode: "test", Address: "127.0.0.1:4200", Origin: "http://127.0.0.1:4200", AccountOrigin: "http://127.0.0.1:4100", Database: filepath.Join(t.TempDir(), "atlas.db")}
	app, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	for path, status := range map[string]int{"/": 302, "/config": 200, "/health": 200, "/api/v1/config": 200, "/api/v1/session": 200, "/login": 503, "/auth/callback?code=forged": 503, "/api/v1/dev/session": 503, "/api/v1/me": 404, "/robots.txt": 200} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest("GET", config.Origin+path, nil)
		app.Handler().ServeHTTP(response, request)
		if response.Code != status {
			t.Fatalf("%s = %d", path, response.Code)
		}
		if response.Header().Get("X-Robots-Tag") != "noindex, nofollow" {
			t.Fatal("indexing header absent")
		}
	}
	var schema string
	if err = app.db.QueryRow("SELECT group_concat(sql) FROM sqlite_master WHERE type='table'").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(schema, "password") {
		t.Fatal("atlas stores passwords")
	}
	if _, err = app.db.Exec("INSERT INTO sessions VALUES('id','hash','missing','sid',1,2,NULL)"); err == nil {
		t.Fatal("foreign keys disabled")
	}
	app.Close()
	reopened, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	reopened.Close()
	config.Mode = "production"
	if _, err = New(config); err == nil {
		t.Fatal("production silently starts")
	}
}
func TestMissingConfigurationAndCorruptionFailClosed(t *testing.T) {
	if _, err := LoadConfig(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("missing config accepted")
	}
	filename := filepath.Join(t.TempDir(), "broken.db")
	if err := os.WriteFile(filename, []byte("not a database"), 0600); err != nil {
		t.Fatal(err)
	}
	config := Config{Mode: "test", Address: "127.0.0.1:4200", Origin: "http://127.0.0.1:4200", AccountOrigin: "http://127.0.0.1:4100", Database: filename}
	if _, err := New(config); err == nil {
		t.Fatal("corrupt database silently replaced")
	}
}
