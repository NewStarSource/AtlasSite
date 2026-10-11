package atlas

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/google/uuid"
)

func TestCallbackRejectsInvalidClaimsAndReplay(t *testing.T) {
	private, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"valid", "signature", "issuer", "audience", "nonce", "expired", "future", "unverified", "inactive", "missing_subject", "old_auth", "at_hash", "replay"} {
		t.Run(scenario, func(t *testing.T) {
			directory := t.TempDir()
			secret := token()
			keys := make([]byte, 32)
			rand.Read(keys)
			if err := os.WriteFile(filepath.Join(directory, "secret"), []byte(secret), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, "csrf"), keys, 0600); err != nil {
				t.Fatal(err)
			}
			subject, linked, sid := uuid.NewString(), uuid.NewString(), uuid.NewString()
			nonce := "expected-nonce"
			accessToken := "synthetic-access-token"
			var encoded string
			var origin string
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				switch request.URL.Path {
				case "/.well-known/openid-configuration":
					respond(writer, 200, map[string]any{"issuer": origin, "authorization_endpoint": origin + "/authorize", "token_endpoint": origin + "/token", "jwks_uri": origin + "/keys", "id_token_signing_alg_values_supported": []string{"RS256"}})
				case "/keys":
					respond(writer, 200, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &private.PublicKey, KeyID: "test", Use: "sig", Algorithm: "RS256"}}})
				case "/token":
					username, password, ok := request.BasicAuth()
					if !ok || username != "star-atlas" || password != secret {
						writer.WriteHeader(401)
						return
					}
					respond(writer, 200, map[string]any{"access_token": accessToken, "token_type": "Bearer", "id_token": encoded, "expires_in": 300})
				case "/internal/identity/session":
					respond(writer, 200, map[string]any{"active": true, "status": "active", "status_version": 1, "expires_at": time.Now().Add(time.Hour).Unix()})
				case "/internal/identity/events":
					respond(writer, 200, map[string]any{"schema_version": 1, "events": []identityEvent{}})
				default:
					writer.WriteHeader(404)
				}
			}))
			defer server.Close()
			origin = server.URL
			accessHash := sha256.Sum256([]byte(accessToken))
			claims := map[string]any{"iss": origin, "aud": []string{"star-atlas"}, "sub": subject, "nonce": nonce, "iat": time.Now().Unix(), "exp": time.Now().Add(5 * time.Minute).Unix(), "auth_time": time.Now().Unix(), "subject_id": linked, "sid": sid, "alias": "测试化名", "status": "active", "status_version": 1, "email_verified": true, "at_hash": base64.RawURLEncoding.EncodeToString(accessHash[:16])}
			switch scenario {
			case "issuer":
				claims["iss"] = "http://wrong.invalid"
			case "audience":
				claims["aud"] = []string{"another-client"}
			case "nonce":
				claims["nonce"] = "bad"
			case "expired":
				claims["exp"] = time.Now().Add(-time.Minute).Unix()
			case "future":
				claims["iat"] = time.Now().Add(2 * time.Minute).Unix()
			case "unverified":
				claims["email_verified"] = false
			case "inactive":
				claims["status"] = "suspended"
			case "missing_subject":
				claims["sub"] = ""
			case "old_auth":
				claims["auth_time"] = 0
			case "at_hash":
				claims["at_hash"] = "incorrect"
			}
			signingKey := private
			if scenario == "signature" {
				signingKey, err = rsa.GenerateKey(rand.Reader, 2048)
				if err != nil {
					t.Fatal(err)
				}
			}
			signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: &jose.JSONWebKey{Key: signingKey, KeyID: "test"}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			payload, _ := json.Marshal(claims)
			signed, err := signer.Sign(payload)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err = signed.CompactSerialize()
			if err != nil {
				t.Fatal(err)
			}
			config := Config{Mode: "test", Address: "127.0.0.1:4200", Origin: "http://127.0.0.1:4200", AccountOrigin: origin, Database: filepath.Join(directory, "atlas.db"), OIDCSecretFile: filepath.Join(directory, "secret"), CSRFKeyFile: filepath.Join(directory, "csrf")}
			app, err := New(config)
			if err != nil {
				t.Fatal(err)
			}
			defer app.Close()
			callback := func() *httptest.ResponseRecorder {
				state, binding := token(), token()
				sealed, err := app.sealVerifier(token())
				if err != nil {
					t.Fatal(err)
				}
				_, err = app.db.Exec("INSERT INTO oidc_flows VALUES(?,?,?,?,?,?,?)", digest(state), digest(binding), nonce, sealed, time.Now().Add(time.Minute).Unix(), "login", "")
				if err != nil {
					t.Fatal(err)
				}
				request := httptest.NewRequest("GET", config.Origin+"/auth/callback?state="+state+"&code=synthetic", nil)
				request.AddCookie(&http.Cookie{Name: "star_atlas_flow", Value: binding})
				response := httptest.NewRecorder()
				app.Handler().ServeHTTP(response, request)
				return response
			}
			response := callback()
			if scenario == "valid" || scenario == "replay" {
				if response.Code != 303 {
					t.Fatalf("valid callback = %d", response.Code)
				}
			} else if response.Code != 400 {
				t.Fatalf("%s accepted: %d", scenario, response.Code)
			}
			if scenario == "replay" && callback().Code != 400 {
				t.Fatal("id token replay accepted")
			}
		})
	}
}

func TestIdentityEventIdempotencyAndStaleStatus(t *testing.T) {
	directory := t.TempDir()
	config := Config{Mode: "test", Address: "127.0.0.1:4200", Origin: "http://127.0.0.1:4200", AccountOrigin: "http://127.0.0.1:4100", Database: filepath.Join(directory, "atlas.db")}
	app, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	accountID, subject, sid, userID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err = app.db.Exec("INSERT INTO users VALUES(?,?,?,?,?,?,?)", userID, accountID, subject, "别名", "active", 1, config.AccountOrigin); err != nil {
		t.Fatal(err)
	}
	if _, err = app.db.Exec("INSERT INTO sessions(id,token_hash,user_id,account_session_id,created_at,expires_at) VALUES(?,?,?,?,?,?)", uuid.NewString(), digest("local"), userID, sid, time.Now().Unix(), time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		username, password, ok := request.BasicAuth()
		if !ok || username != "star-atlas" || password != "secret" {
			writer.WriteHeader(401)
			return
		}
		if request.URL.Query().Get("after") == "0" {
			respond(writer, 200, map[string]any{"schema_version": 1, "events": []identityEvent{{Sequence: 1, AccountID: accountID, SID: sid, Type: "session_revoked", Status: "active", Version: 1}, {Sequence: 2, AccountID: accountID, Type: "status_changed", Status: "suspended", Version: 3}, {Sequence: 3, AccountID: accountID, Type: "status_changed", Status: "active", Version: 2}}})
		} else {
			respond(writer, 200, map[string]any{"schema_version": 1, "events": []identityEvent{}})
		}
	}))
	defer server.Close()
	app.config.AccountOrigin = server.URL
	app.secret = "secret"
	for index := 0; index < 2; index++ {
		if err = app.syncIdentity(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	var status string
	var version, count int
	if err = app.db.QueryRow("SELECT status,status_version FROM users WHERE id=?", userID).Scan(&status, &version); err != nil || status != "suspended" || version != 3 {
		t.Fatal("stale state applied")
	}
	if err = app.db.QueryRow("SELECT count(*) FROM sessions WHERE revoked_at IS NULL").Scan(&count); err != nil || count != 0 {
		t.Fatal("revocation lost")
	}
}

func TestMigrationFromVersionOnePreservesData(t *testing.T) {
	directory := t.TempDir()
	config := Config{Mode: "test", Address: "127.0.0.1:4200", Origin: "http://127.0.0.1:4200", AccountOrigin: "http://127.0.0.1:4100", Database: filepath.Join(directory, "atlas.db")}
	legacy, err := sql.Open("sqlite", config.Database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = legacy.Exec(schema); err != nil {
		t.Fatal(err)
	}
	if _, err = legacy.Exec("INSERT INTO users VALUES('legacy','account','subject','旧化名','active',1)"); err != nil {
		t.Fatal(err)
	}
	legacy.Close()
	app, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	request := httptest.NewRequest("GET", config.Origin+"/auth/callback", nil)
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if response.Code != 503 {
		t.Fatal("unconfigured auth accepted")
	}
	var version int
	app.db.QueryRow("SELECT max(version) FROM schema_migrations").Scan(&version)
	if version != currentSchema {
		t.Fatal("migration missing")
	}
	var alias string
	if err = app.db.QueryRow("SELECT alias FROM users WHERE id='legacy'").Scan(&alias); err != nil || alias != "旧化名" {
		t.Fatal("migration lost data")
	}
}
