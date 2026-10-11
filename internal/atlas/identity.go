package atlas

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/gorilla/csrf"
	"golang.org/x/oauth2"
)

type Identity struct {
	ID                string `json:"id"`
	Alias             string `json:"alias"`
	Status            string `json:"status"`
	SID               string `json:"-"`
	AccountID         string `json:"-"`
	AuthTime          int64  `json:"-"`
	ReauthenticatedAt int64  `json:"-"`
	CanManage         bool   `json:"-"`
}
type tokenClaims struct {
	SubjectID string `json:"subject_id"`
	Alias     string `json:"alias"`
	Status    string `json:"status"`
	Version   int64  `json:"status_version"`
	SID       string `json:"sid"`
	Verified  bool   `json:"email_verified"`
	AuthTime  int64  `json:"auth_time"`
}

func token() string {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		panic("secure random unavailable")
	}
	return base64.RawURLEncoding.EncodeToString(bytes)
}
func (app *App) sealVerifier(value string) (string, error) {
	mac := hmac.New(sha256.New, app.csrfKey)
	mac.Write([]byte("staratlas-oidc-verifier-v1"))
	block, err := aes.NewCipher(mac.Sum(nil))
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(value), nil)), nil
}
func (app *App) openVerifier(value string) (string, error) {
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, app.csrfKey)
	mac.Write([]byte("staratlas-oidc-verifier-v1"))
	block, err := aes.NewCipher(mac.Sum(nil))
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(data) < gcm.NonceSize() {
		return "", errors.New("invalid verifier")
	}
	plain, err := gcm.Open(nil, data[:gcm.NonceSize()], data[gcm.NonceSize():], nil)
	return string(plain), err
}
func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func cookie(writer http.ResponseWriter, name, value string, age int) {
	http.SetCookie(writer, &http.Cookie{Name: name, Value: value, MaxAge: age, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode})
}
func (app *App) audit(event string) {
	_, _ = app.db.Exec("INSERT INTO security_events VALUES(?,?,?)", uuid.NewString(), event, time.Now().Unix())
}
func (app *App) protect(handler http.Handler) http.Handler {
	protected := csrf.Protect(app.csrfKey, csrf.CookieName("star_atlas_csrf"), csrf.Secure(false), csrf.HttpOnly(true), csrf.Path("/"), csrf.SameSite(csrf.SameSiteLaxMode), csrf.ErrorHandler(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		respond(writer, 403, map[string]string{"code": "CSRF_INVALID", "message": "请刷新页面再试"})
	})))(handler)
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != "GET" && request.Method != "HEAD" {
			if origin := request.Header.Get("Origin"); origin != "" && origin != app.config.Origin {
				respond(writer, 403, map[string]string{"code": "ORIGIN_INVALID"})
				return
			}
		}
		protected.ServeHTTP(writer, csrf.PlaintextHTTPRequest(request))
	})
}
func (app *App) identityRoutes(router chi.Router) {
	router.Get("/login", app.beginLogin)
	router.Get("/auth/callback", app.callback)
	router.Get("/security", func(writer http.ResponseWriter, request *http.Request) {
		identity, degraded, err := app.currentIdentity(request)
		if err != nil {
			respond(writer, 503, map[string]string{"code": "DEPENDENCY_UNAVAILABLE"})
			return
		}
		if identity == nil {
			http.Redirect(writer, request, "/login", 303)
			return
		}
		app.renderSecurity(writer, request, identity, degraded)
	})
	router.Get("/api/v1/csrf", func(writer http.ResponseWriter, request *http.Request) {
		respond(writer, 200, map[string]string{"csrf_token": csrf.Token(request)})
	})
	router.Post("/auth/logout", func(writer http.ResponseWriter, request *http.Request) {
		if current, err := request.Cookie("star_atlas_session"); err == nil {
			if _, err = app.db.ExecContext(request.Context(), "UPDATE sessions SET revoked_at=? WHERE token_hash=?", time.Now().Unix(), digest(current.Value)); err != nil {
				respond(writer, 503, map[string]string{"code": "DEPENDENCY_UNAVAILABLE"})
				return
			}
		}
		cookie(writer, "star_atlas_session", "", -1)
		http.Redirect(writer, request, "/", 303)
	})
	router.Post("/auth/reauthenticate", func(writer http.ResponseWriter, request *http.Request) { app.startFlow(writer, request, "reauth") })
	router.Post("/auth/revoke-all", func(writer http.ResponseWriter, request *http.Request) {
		identity, degraded, err := app.currentIdentity(request)
		if err != nil || degraded {
			respond(writer, 503, map[string]string{"code": "IDENTITY_UNAVAILABLE"})
			return
		}
		if identity == nil {
			respond(writer, 401, map[string]string{"code": "SESSION_REQUIRED"})
			return
		}
		if identity.ReauthenticatedAt < time.Now().Add(-5*time.Minute).Unix() {
			respond(writer, 403, map[string]string{"code": "REAUTH_REQUIRED"})
			return
		}
		if _, err = app.db.ExecContext(request.Context(), "UPDATE sessions SET revoked_at=? WHERE user_id=? AND revoked_at IS NULL", time.Now().Unix(), identity.ID); err != nil {
			respond(writer, 503, map[string]string{"code": "DEPENDENCY_UNAVAILABLE"})
			return
		}
		app.audit("sessions_revoked")
		cookie(writer, "star_atlas_session", "", -1)
		http.Redirect(writer, request, "/", 303)
	})
}
func (app *App) beginLogin(writer http.ResponseWriter, request *http.Request) {
	app.startFlow(writer, request, "login")
}
func (app *App) provider(ctx context.Context) (*oidc.Provider, *oauth2.Config, error) {
	ctx = oidc.ClientContext(ctx, app.httpClient)
	provider, err := oidc.NewProvider(ctx, app.config.AccountOrigin)
	if err != nil {
		return nil, nil, err
	}
	endpoint := provider.Endpoint()
	var discovery struct {
		JWKS string `json:"jwks_uri"`
	}
	if err = provider.Claims(&discovery); err != nil {
		return nil, nil, err
	}
	for _, endpointURL := range []string{endpoint.AuthURL, endpoint.TokenURL, discovery.JWKS} {
		parsed, err := url.Parse(endpointURL)
		if err != nil || parsed.Scheme+"://"+parsed.Host != app.config.AccountOrigin || parsed.User != nil {
			return nil, nil, errors.New("invalid endpoint")
		}
	}
	endpoint.AuthStyle = oauth2.AuthStyleInHeader
	config := &oauth2.Config{ClientID: "star-atlas", ClientSecret: app.secret, Endpoint: endpoint, RedirectURL: app.config.Origin + "/auth/callback", Scopes: []string{"openid", "atlas_identity"}}
	return provider, config, nil
}
func (app *App) startFlow(writer http.ResponseWriter, request *http.Request, purpose string) {
	if app.secret == "" {
		respond(writer, 503, map[string]string{"code": "AUTH_NOT_CONFIGURED"})
		return
	}
	host, _, _ := net.SplitHostPort(request.RemoteAddr)
	var count int
	err := app.db.QueryRowContext(request.Context(), "INSERT INTO auth_limits(key,count,reset_at) VALUES(?,1,?) ON CONFLICT(key) DO UPDATE SET count=CASE WHEN reset_at<=? THEN 1 ELSE count+1 END,reset_at=CASE WHEN reset_at<=? THEN excluded.reset_at ELSE reset_at END RETURNING count", digest(host), time.Now().Add(time.Minute).Unix(), time.Now().Unix(), time.Now().Unix()).Scan(&count)
	if err != nil || count > 30 {
		respond(writer, 429, map[string]string{"code": "RATE_LIMITED"})
		return
	}
	var sessionHash string
	if purpose == "reauth" {
		identity, degraded, err := app.currentIdentity(request)
		if err != nil || degraded {
			respond(writer, 503, map[string]string{"code": "IDENTITY_UNAVAILABLE"})
			return
		}
		if identity == nil {
			respond(writer, 401, map[string]string{"code": "SESSION_REQUIRED"})
			return
		}
		current, _ := request.Cookie("star_atlas_session")
		sessionHash = digest(current.Value)
	}
	_, config, err := app.provider(request.Context())
	if err != nil {
		respond(writer, 503, map[string]string{"code": "IDENTITY_UNAVAILABLE", "message": "账户服务暂时不可用，请稍后重试"})
		return
	}
	state, binding, nonce, verifier := token(), token(), token(), oauth2.GenerateVerifier()
	sealed, err := app.sealVerifier(verifier)
	if err != nil {
		respond(writer, 503, map[string]string{"code": "DEPENDENCY_UNAVAILABLE"})
		return
	}
	if _, err = app.db.ExecContext(request.Context(), "INSERT INTO oidc_flows VALUES(?,?,?,?,?,?,?)", digest(state), digest(binding), nonce, sealed, time.Now().Add(5*time.Minute).Unix(), purpose, sessionHash); err != nil {
		respond(writer, 503, map[string]string{"code": "DEPENDENCY_UNAVAILABLE"})
		return
	}
	cookie(writer, "star_atlas_flow", binding, 300)
	options := []oauth2.AuthCodeOption{oauth2.S256ChallengeOption(verifier), oidc.Nonce(nonce)}
	if purpose == "reauth" {
		options = append(options, oauth2.SetAuthURLParam("prompt", "login"), oauth2.SetAuthURLParam("max_age", "0"))
	}
	if purpose == "reauth" {
		app.renderContinue(writer, request, config.AuthCodeURL(state, options...))
		return
	}
	http.Redirect(writer, request, config.AuthCodeURL(state, options...), 303)
}
func (app *App) callback(writer http.ResponseWriter, request *http.Request) {
	if app.secret == "" {
		respond(writer, 503, map[string]string{"code": "AUTH_NOT_CONFIGURED"})
		return
	}
	success := false
	defer func() {
		if !success {
			app.audit("oidc_callback_rejected")
		}
	}()
	failed := func() {
		respond(writer, 400, map[string]string{"code": "AUTH_CALLBACK_INVALID", "message": "登录验证失败或链接已失效，请重新登录"})
	}
	binding, err := request.Cookie("star_atlas_flow")
	if err != nil {
		failed()
		return
	}
	var nonce, verifier, purpose, sessionHash string
	err = app.db.QueryRowContext(request.Context(), "DELETE FROM oidc_flows WHERE state_hash=? AND binding_hash=? AND expires_at>? RETURNING nonce,verifier,purpose,session_hash", digest(request.URL.Query().Get("state")), digest(binding.Value), time.Now().Unix()).Scan(&nonce, &verifier, &purpose, &sessionHash)
	if err != nil {
		failed()
		return
	}
	cookie(writer, "star_atlas_flow", "", -1)
	verifier, err = app.openVerifier(verifier)
	if err != nil {
		failed()
		return
	}
	if request.URL.Query().Get("error") != "" || request.URL.Query().Get("code") == "" {
		failed()
		return
	}
	provider, config, err := app.provider(request.Context())
	if err != nil {
		failed()
		return
	}
	ctx := oidc.ClientContext(request.Context(), app.httpClient)
	exchanged, err := config.Exchange(ctx, request.URL.Query().Get("code"), oauth2.VerifierOption(verifier))
	if err != nil {
		failed()
		return
	}
	raw, ok := exchanged.Extra("id_token").(string)
	if !ok {
		failed()
		return
	}
	verified, err := provider.Verifier(&oidc.Config{ClientID: "star-atlas", SupportedSigningAlgs: []string{"RS256"}}).Verify(ctx, raw)
	if err != nil {
		failed()
		return
	}
	if subtle.ConstantTimeCompare([]byte(verified.Nonce), []byte(nonce)) != 1 || verified.IssuedAt.After(time.Now().Add(60*time.Second)) || verified.IssuedAt.Before(time.Now().Add(-6*time.Minute)) || verified.Expiry.Before(time.Now()) || verified.Expiry.After(verified.IssuedAt.Add(6*time.Minute)) {
		failed()
		return
	}
	if verified.VerifyAccessToken(exchanged.AccessToken) != nil {
		failed()
		return
	}
	var claims tokenClaims
	if verified.Claims(&claims) != nil || !claims.Verified || claims.Status != "active" || claims.Version < 1 || claims.Alias == "" || claims.AuthTime <= 0 || claims.AuthTime > time.Now().Add(time.Minute).Unix() {
		failed()
		return
	}
	for _, value := range []string{verified.Subject, claims.SubjectID, claims.SID} {
		if _, err = uuid.Parse(value); err != nil {
			failed()
			return
		}
	}
	if purpose == "reauth" {
		current, cookieErr := request.Cookie("star_atlas_session")
		if cookieErr != nil || digest(current.Value) != sessionHash || claims.AuthTime < time.Now().Add(-time.Minute).Unix() {
			failed()
			return
		}
		var accountID string
		err = app.db.QueryRowContext(ctx, "SELECT users.account_id FROM sessions JOIN users ON users.id=sessions.user_id WHERE token_hash=? AND revoked_at IS NULL AND expires_at>?", sessionHash, time.Now().Unix()).Scan(&accountID)
		if err != nil || accountID != verified.Subject {
			failed()
			return
		}
	}
	status, err := app.remoteSession(ctx, verified.Subject, claims.SID)
	if err != nil || !status.Active || status.Version != claims.Version {
		failed()
		return
	}
	if err = app.syncIdentity(ctx); err != nil {
		failed()
		return
	}
	transaction, err := app.db.BeginTx(ctx, nil)
	if err != nil {
		failed()
		return
	}
	defer transaction.Rollback()
	if _, err = transaction.Exec("INSERT INTO consumed_tokens VALUES(?,?)", digest(raw), verified.Expiry.Unix()); err != nil {
		failed()
		return
	}
	var cachedStatus string
	var cachedVersion int64
	stateErr := transaction.QueryRow("SELECT status,version FROM account_states WHERE account_id=?", verified.Subject).Scan(&cachedStatus, &cachedVersion)
	if stateErr != nil && !errors.Is(stateErr, sql.ErrNoRows) {
		failed()
		return
	}
	if cachedVersion > claims.Version || (cachedVersion == claims.Version && cachedStatus != "active") {
		failed()
		return
	}
	var revoked int
	if err = transaction.QueryRow("SELECT count(*) FROM revoked_account_sessions WHERE sid=?", claims.SID).Scan(&revoked); err != nil || revoked != 0 {
		failed()
		return
	}
	_, err = transaction.Exec("INSERT INTO users(id,account_id,subject_id,alias,status,status_version,issuer) VALUES(?,?,?,?,?,?,?) ON CONFLICT(account_id) DO UPDATE SET alias=excluded.alias,status=excluded.status,status_version=excluded.status_version WHERE users.issuer=excluded.issuer AND users.subject_id=excluded.subject_id", uuid.NewString(), verified.Subject, claims.SubjectID, claims.Alias, claims.Status, claims.Version, app.config.AccountOrigin)
	if err != nil {
		failed()
		return
	}
	var userID string
	err = transaction.QueryRow("SELECT id FROM users WHERE account_id=? AND issuer=? AND subject_id=?", verified.Subject, app.config.AccountOrigin, claims.SubjectID).Scan(&userID)
	if err != nil {
		failed()
		return
	}
	rawSession := token()
	expiry := time.Now().Add(7 * 24 * time.Hour).Unix()
	if status.ExpiresAt < expiry {
		expiry = status.ExpiresAt
	}
	reauthenticated := int64(0)
	if purpose == "reauth" {
		reauthenticated = time.Now().Unix()
	}
	_, err = transaction.Exec("INSERT INTO sessions(id,token_hash,user_id,account_session_id,created_at,expires_at,auth_time,reauthenticated_at) VALUES(?,?,?,?,?,?,?,?)", uuid.NewString(), digest(rawSession), userID, claims.SID, time.Now().Unix(), expiry, claims.AuthTime, reauthenticated)
	if err != nil {
		failed()
		return
	}
	if old, cookieErr := request.Cookie("star_atlas_session"); cookieErr == nil {
		if _, err = transaction.Exec("UPDATE sessions SET revoked_at=? WHERE token_hash=?", time.Now().Unix(), digest(old.Value)); err != nil {
			failed()
			return
		}
	}
	if err = transaction.Commit(); err != nil {
		failed()
		return
	}
	success = true
	cookie(writer, "star_atlas_session", rawSession, int(expiry-time.Now().Unix()))
	app.audit("oidc_login_succeeded")
	http.Redirect(writer, request, "/security", 303)
}

type remoteState struct {
	Active     bool   `json:"active"`
	Status     string `json:"status"`
	Version    int64  `json:"status_version"`
	OccurredAt int64  `json:"occurred_at"`
	ExpiresAt  int64  `json:"expires_at"`
}

func (app *App) getInternal(ctx context.Context, path string, output any) error {
	request, err := http.NewRequestWithContext(ctx, "GET", app.config.AccountOrigin+path, nil)
	if err != nil {
		return err
	}
	request.SetBasicAuth("star-atlas", app.secret)
	response, err := app.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return errors.New("identity unavailable")
	}
	return json.NewDecoder(io.LimitReader(response.Body, 1024*1024)).Decode(output)
}
func (app *App) remoteSession(ctx context.Context, accountID, sid string) (remoteState, error) {
	var output remoteState
	err := app.getInternal(ctx, "/internal/identity/session?account_id="+url.QueryEscape(accountID)+"&sid="+url.QueryEscape(sid), &output)
	return output, err
}

type identityEvent struct {
	Sequence   int64  `json:"sequence"`
	ID         string `json:"event_id"`
	AccountID  string `json:"account_id"`
	SID        string `json:"sid"`
	Type       string `json:"event_type"`
	Status     string `json:"status"`
	Version    int64  `json:"status_version"`
	OccurredAt int64  `json:"occurred_at"`
}

func (app *App) syncIdentity(ctx context.Context) error {
	app.syncMu.Lock()
	defer app.syncMu.Unlock()
	app.opsMu.Lock()
	defer app.opsMu.Unlock()
	var cursor int64
	if err := app.db.QueryRowContext(ctx, "SELECT value FROM identity_cursor WHERE id=1").Scan(&cursor); err != nil {
		return err
	}
	for batch := 0; batch < 20; batch++ {
		var response struct {
			Events  []identityEvent `json:"events"`
			Version int             `json:"schema_version"`
		}
		if err := app.getInternal(ctx, "/internal/identity/events?after="+strconv.FormatInt(cursor, 10), &response); err != nil {
			return err
		}
		if response.Version != 1 {
			return errors.New("invalid event schema")
		}
		transaction, err := app.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		for _, event := range response.Events {
			if event.Sequence <= cursor || event.AccountID == "" || event.Version < 1 {
				transaction.Rollback()
				return errors.New("invalid event")
			}
			if event.Type == "session_revoked" {
				_, err = transaction.Exec("INSERT OR IGNORE INTO revoked_account_sessions VALUES(?,?)", event.SID, time.Now().Add(30*24*time.Hour).Unix())
				if err == nil {
					_, err = transaction.Exec("UPDATE sessions SET revoked_at=? WHERE account_session_id=? AND revoked_at IS NULL", time.Now().Unix(), event.SID)
				}
			} else if event.Type == "status_changed" {
				var result sql.Result
				result, err = transaction.Exec("INSERT INTO account_states VALUES(?,?,?) ON CONFLICT(account_id) DO UPDATE SET status=excluded.status,version=excluded.version WHERE excluded.version>account_states.version", event.AccountID, event.Status, event.Version)
				applied := int64(0)
				if err == nil {
					applied, _ = result.RowsAffected()
				}
				if err == nil {
					_, err = transaction.Exec("UPDATE users SET status=?,status_version=? WHERE account_id=? AND status_version<?", event.Status, event.Version, event.AccountID, event.Version)
				}
				if err == nil && applied > 0 && (event.Status == "deactivated" || event.Status == "deleted") {
					var localID string
					var retain int
					e := transaction.QueryRow("SELECT u.id,s.retain_content FROM users u JOIN user_settings s ON s.user_id=u.id WHERE u.account_id=?", event.AccountID).Scan(&localID, &retain)
					if e == nil {
						action := "deactivate-user"
						if retain == 1 {
							action = "retain-user"
						}
						var retained []string
						rows, e := transaction.Query("SELECT object_id FROM retained_objects WHERE user_id=?", localID)
						if e != nil {
							transaction.Rollback()
							return e
						}
						for rows.Next() {
							var id string
							rows.Scan(&id)
							retained = append(retained, id)
						}
						rows.Close()
						op := operation{EventID: event.ID, ObjectID: localID, Action: action, Retained: retained, CreatedAt: event.OccurredAt}
						if op.CreatedAt <= 0 || op.CreatedAt > time.Now().Unix()+60 {
							op.CreatedAt = time.Now().Unix()
						}
						if op.EventID == "" {
							op.EventID = randomID()
						}
						if op.Action == "deactivate-user" {
							err = app.preserveUserWithdrawal(transaction, op)
						}
						if err == nil {
							err = app.appendOperation(op)
						}
						if err == nil {
							err = applyOperationSQL(transaction, op)
						}
						if err == nil {
							_, err = transaction.Exec("UPDATE users SET status=? WHERE id=?", event.Status, localID)
						}
					} else if e != sql.ErrNoRows {
						err = e
					}
				}
				if err == nil && applied > 0 && event.Status != "active" {
					_, err = transaction.Exec("UPDATE sessions SET revoked_at=? WHERE user_id IN (SELECT id FROM users WHERE account_id=?) AND revoked_at IS NULL", time.Now().Unix(), event.AccountID)
				}
			} else {
				err = errors.New("unknown event")
			}
			if err != nil {
				transaction.Rollback()
				return err
			}
			cursor = event.Sequence
		}
		if _, err = transaction.Exec("UPDATE identity_cursor SET value=? WHERE id=1", cursor); err != nil {
			transaction.Rollback()
			return err
		}
		if err = transaction.Commit(); err != nil {
			return err
		}
		if len(response.Events) < 200 {
			return nil
		}
	}
	return errors.New("identity backlog")
}
func (app *App) currentIdentity(request *http.Request) (*Identity, bool, error) {
	if app.secret == "" {
		return nil, false, nil
	}
	current, err := request.Cookie("star_atlas_session")
	if err != nil {
		return nil, false, nil
	}
	degraded := app.syncIdentity(request.Context()) != nil
	identity := &Identity{}
	err = app.db.QueryRowContext(request.Context(), "SELECT users.id,users.alias,users.status,users.account_id,sessions.account_session_id,sessions.auth_time,sessions.reauthenticated_at FROM sessions JOIN users ON users.id=sessions.user_id WHERE token_hash=? AND revoked_at IS NULL AND expires_at>? AND users.status='active'", digest(current.Value), time.Now().Unix()).Scan(&identity.ID, &identity.Alias, &identity.Status, &identity.AccountID, &identity.SID, &identity.AuthTime, &identity.ReauthenticatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, degraded, nil
	}
	if err != nil {
		return nil, degraded, err
	}
	if !degraded {
		status, err := app.remoteSession(request.Context(), identity.AccountID, identity.SID)
		if err != nil {
			degraded = true
		} else if !status.Active {
			_, err = app.db.ExecContext(request.Context(), "UPDATE sessions SET revoked_at=? WHERE token_hash=?", time.Now().Unix(), digest(current.Value))
			return nil, false, err
		}
	}
	access, err := managementAccess(app.db, identity.ID)
	if err != nil {
		return nil, degraded, err
	}
	identity.CanManage = access != ""
	return identity, degraded, nil
}
func (app *App) Worker(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if app.secret != "" {
				_ = app.syncIdentity(ctx)
			}
			app.maintenanceTasks()
			for query, cutoff := range map[string]int64{"DELETE FROM auth_limits WHERE reset_at<=?": time.Now().Unix(), "DELETE FROM oidc_flows WHERE expires_at<=?": time.Now().Unix(), "DELETE FROM consumed_tokens WHERE expires_at<=?": time.Now().Unix(), "DELETE FROM revoked_account_sessions WHERE expires_at<=?": time.Now().Unix(), "DELETE FROM security_events WHERE created_at<?": time.Now().Add(-30 * 24 * time.Hour).Unix(), "DELETE FROM sessions WHERE COALESCE(revoked_at,expires_at)<?": time.Now().Add(-30 * 24 * time.Hour).Unix()} {
				_, _ = app.db.ExecContext(ctx, query, cutoff)
			}
		}
	}
}
