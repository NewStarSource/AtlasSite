package atlas

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

type operation struct {
	Sequence      int64    `json:"sequence"`
	EventID       string   `json:"event_id"`
	ObjectID      string   `json:"object_id"`
	Action        string   `json:"action"`
	OwnerID       string   `json:"owner_id,omitempty"`
	Retained      []string `json:"retained,omitempty"`
	CommunityID   string   `json:"community_id,omitempty"`
	TopicIDs      []string `json:"topic_ids,omitempty"`
	TargetKind    string   `json:"target_kind,omitempty"`
	Notify        bool     `json:"notify,omitempty"`
	VersionNumber int      `json:"version_number,omitempty"`
	CreatedAt     int64    `json:"created_at"`
	Previous      string   `json:"previous"`
	MAC           string   `json:"mac"`
}

func operationMAC(key []byte, op operation) string {
	op.MAC = ""
	raw, _ := json.Marshal(op)
	mac := hmac.New(sha256.New, key)
	mac.Write(raw)
	return hex.EncodeToString(mac.Sum(nil))
}
func (app *App) readJournal() ([]operation, error) {
	f, err := os.Open(app.config.OpsJournalFile)
	if os.IsNotExist(err) {
		entries, e := os.ReadDir(app.config.OpsJournalFile + ".anchors")
		if e == nil && len(entries) > 0 {
			return nil, errors.New("operation journal missing")
		}
		var count int
		if app.db.QueryRow("SELECT count(*) FROM operation_events").Scan(&count) != nil || count > 0 {
			return nil, errors.New("operation journal missing")
		}
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 4096), 64*1024)
	result := []operation{}
	previous := ""
	for scan.Scan() {
		var op operation
		if json.Unmarshal(scan.Bytes(), &op) != nil || op.Sequence != int64(len(result)+1) || op.Previous != previous || !hmac.Equal([]byte(op.MAC), []byte(operationMAC(app.vaultKey, op))) {
			return nil, errors.New("operation journal integrity failure")
		}
		anchor, err := os.ReadFile(filepath.Join(app.config.OpsJournalFile+".anchors", fmt.Sprintf("%020d", op.Sequence)))
		if err != nil || string(anchor) != op.MAC {
			return nil, errors.New("operation anchor mismatch")
		}
		result = append(result, op)
		previous = op.MAC
	}
	if scan.Err() != nil {
		return nil, scan.Err()
	}
	entries, err := os.ReadDir(app.config.OpsJournalFile + ".anchors")
	if err != nil || len(entries) != len(result) {
		return nil, errors.New("operation journal truncated")
	}
	return result, nil
}
func (app *App) journalLock() (func(), error) {
	path := app.config.OpsJournalFile + ".lock"
	deadline := time.Now().Add(2 * time.Second)
	for {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err == nil {
			fmt.Fprintf(f, "%d", os.Getpid())
			f.Close()
			return func() { os.Remove(path) }, nil
		}
		if !os.IsExist(err) || time.Now().After(deadline) {
			return nil, errors.New("independent journal busy; inspect owner before removing lock")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
func (app *App) appendOperation(op operation) error {
	unlock, err := app.journalLock()
	if err != nil {
		return err
	}
	defer unlock()
	return app.appendOperationLocked(op)
}
func (app *App) appendOperationLocked(op operation) error {
	records, err := app.readJournal()
	if err != nil {
		return err
	}
	for _, record := range records {
		if record.EventID == op.EventID {
			return nil
		}
	}
	op.Sequence = int64(len(records) + 1)
	if len(records) > 0 {
		op.Previous = records[len(records)-1].MAC
	}
	op.MAC = operationMAC(app.vaultKey, op)
	raw, err := json.Marshal(op)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(app.config.OpsJournalFile), 0700); err != nil {
		return err
	}
	anchors := app.config.OpsJournalFile + ".anchors"
	if err = os.MkdirAll(anchors, 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(app.config.OpsJournalFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(append(raw, '\n'))
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	a, err := os.OpenFile(filepath.Join(anchors, fmt.Sprintf("%020d", op.Sequence)), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = a.Write([]byte(op.MAC))
	if err == nil {
		err = a.Sync()
	}
	closeErr = a.Close()
	if err != nil {
		return err
	}
	return closeErr
}
func applyOperationSQL(tx *sql.Tx, op operation) error {
	var applied int
	if err := tx.QueryRow("SELECT count(*) FROM operation_events WHERE event_id=?", op.EventID).Scan(&applied); err != nil {
		return err
	}
	if applied > 0 {
		return nil
	}
	var query string
	var args []any
	switch op.Action {
	case "subscription-on", "subscription-off":
		if op.Action == "subscription-off" {
			query = "DELETE FROM subscriptions WHERE user_id=? AND kind=? AND object_id=?"
			args = []any{op.OwnerID, op.TargetKind, op.ObjectID}
		} else {
			query = "INSERT INTO subscriptions SELECT ?,?,?,?,? FROM users WHERE id=? ON CONFLICT(user_id,kind,object_id) DO UPDATE SET notifications=excluded.notifications"
			args = []any{op.OwnerID, op.TargetKind, op.ObjectID, op.Notify, op.CreatedAt, op.OwnerID}
		}
	case "association", "association-state":
		if op.Action == "association-state" {
			if _, err := tx.Exec("UPDATE posts SET community_id=?,version_number=max(version_number,?),updated_at=? WHERE id=? AND status='published'", nullable(op.CommunityID), op.VersionNumber, op.CreatedAt, op.ObjectID); err != nil {
				return err
			}
		} else {
			if _, err := tx.Exec("UPDATE posts SET community_id=?,version_number=version_number+1,updated_at=? WHERE id=? AND status='published'", nullable(op.CommunityID), op.CreatedAt, op.ObjectID); err != nil {
				return err
			}
		}
		if _, err := tx.Exec("DELETE FROM post_topics WHERE post_id=?", op.ObjectID); err != nil {
			return err
		}
		for _, topic := range op.TopicIDs {
			if _, err := tx.Exec("INSERT OR IGNORE INTO post_topics SELECT p.id,t.id FROM posts p JOIN topics t ON t.id=? AND t.community_id=p.community_id AND t.status='active' WHERE p.id=?", topic, op.ObjectID); err != nil {
				return err
			}
		}
		if err := notifySubscribers(tx, op.ObjectID, op.OwnerID, op.CreatedAt); err != nil {
			return err
		}
		query = "SELECT 1"
	case "delete-media":
		query = "UPDATE media SET removed_at=? WHERE id=? AND removed_at IS NULL"
		args = []any{op.CreatedAt, op.ObjectID}
	case "delete-post":
		if err := isolateSubtreeSQL(tx, op.ObjectID, op.CreatedAt); err != nil {
			return err
		}
		query = "UPDATE posts SET status='deleted',title='',content='',version_number=version_number+1,updated_at=? WHERE id=? AND status<>'deleted'"
		args = []any{op.CreatedAt, op.ObjectID}
	case "delete-reply":
		if err := isolateSubtreeSQL(tx, op.ObjectID, op.CreatedAt); err != nil {
			return err
		}
		query = "UPDATE replies SET status='deleted',content='',version_number=version_number+1,updated_at=? WHERE id=? AND status<>'deleted'"
		args = []any{op.CreatedAt, op.ObjectID}
	case "restrict":
		if err := isolateSubtreeSQL(tx, op.ObjectID, op.CreatedAt); err != nil {
			return err
		}
		if _, err := tx.Exec("UPDATE posts SET title='',content='' WHERE id=?", op.ObjectID); err != nil {
			return err
		}
		if _, err := tx.Exec("UPDATE replies SET content='' WHERE id=?", op.ObjectID); err != nil {
			return err
		}
		query = "INSERT OR IGNORE INTO restrictions(id,object_id,reason_code,created_at) VALUES(?,?,'case',?)"
		args = []any{op.EventID, op.ObjectID, op.CreatedAt}
	case "release-restriction":
		query = "UPDATE restrictions SET removed_at=? WHERE id=? AND removed_at IS NULL"
		args = []any{op.CreatedAt, op.ObjectID}
	case "bookmark-on", "bookmark-off":
		if op.Action == "bookmark-on" {
			if _, err := tx.Exec("INSERT OR IGNORE INTO post_bookmarks SELECT ?,p.id,?,? FROM posts p JOIN users u ON u.id=? WHERE p.id=?", op.EventID, op.OwnerID, op.CreatedAt, op.OwnerID, op.ObjectID); err != nil {
				return err
			}
		} else {
			if _, err := tx.Exec("DELETE FROM post_bookmarks WHERE post_id=? AND identity_id=?", op.ObjectID, op.OwnerID); err != nil {
				return err
			}
		}
		query = "UPDATE posts SET bookmark_count=(SELECT count(*) FROM post_bookmarks WHERE post_id=?) WHERE id=?"
		args = []any{op.ObjectID, op.ObjectID}
	case "like-on", "like-off":
		if op.Action == "like-on" {
			query = "INSERT OR IGNORE INTO post_likes SELECT u.id,p.id,? FROM users u JOIN posts p ON p.id=? WHERE u.id=?"
			args = []any{op.CreatedAt, op.ObjectID, op.OwnerID}
		} else {
			query = "DELETE FROM post_likes WHERE post_id=? AND user_id=?"
			args = []any{op.ObjectID, op.OwnerID}
		}
	case "block-on", "block-off":
		if op.Action == "block-on" {
			query = "INSERT OR IGNORE INTO blocks SELECT u.id,t.id,? FROM users u JOIN users t ON t.id=? WHERE u.id=?"
			args = []any{op.CreatedAt, op.ObjectID, op.OwnerID}
		} else {
			query = "DELETE FROM blocks WHERE target_id=? AND owner_id=?"
			args = []any{op.ObjectID, op.OwnerID}
		}
	case "delete-notification":
		query = "DELETE FROM notifications WHERE id=? AND user_id=?"
		args = []any{op.ObjectID, op.OwnerID}
	case "relations-private", "relations-public":
		query = "UPDATE user_settings SET hide_relations=? WHERE user_id=?"
		v := 0
		if op.Action == "relations-private" {
			v = 1
		}
		args = []any{v, op.ObjectID}
	case "deactivate-user", "retain-user":
		if _, err := tx.Exec("DELETE FROM retained_objects WHERE user_id=?", op.ObjectID); err != nil {
			return err
		}
		for _, id := range op.Retained {
			if _, err := tx.Exec("INSERT OR IGNORE INTO retained_objects SELECT ?,? FROM users WHERE id=?", op.ObjectID, id, op.ObjectID); err != nil {
				return err
			}
		}
		if _, err := tx.Exec("UPDATE users SET status='deactivated' WHERE id=?", op.ObjectID); err != nil {
			return err
		}
		if _, err := tx.Exec("UPDATE sessions SET revoked_at=? WHERE user_id=? AND revoked_at IS NULL", op.CreatedAt, op.ObjectID); err != nil {
			return err
		}
		if op.Action == "deactivate-user" {
			if _, err := tx.Exec("UPDATE posts SET status='deleted',title='',content='',updated_at=?,version_number=version_number+1 WHERE author_id=? AND status<>'deleted' AND id NOT IN (SELECT object_id FROM retained_objects WHERE user_id=?)", op.CreatedAt, op.ObjectID, op.ObjectID); err != nil {
				return err
			}
			if _, err := tx.Exec("UPDATE replies SET status='deleted',content='',updated_at=?,version_number=version_number+1 WHERE author_id=? AND status<>'deleted' AND id NOT IN (SELECT object_id FROM retained_objects WHERE user_id=?)", op.CreatedAt, op.ObjectID, op.ObjectID); err != nil {
				return err
			}
		}
		if op.Action == "deactivate-user" {
			roots, err := tx.Query("SELECT id FROM posts WHERE author_id=? AND status='deleted' UNION SELECT id FROM replies WHERE author_id=? AND status='deleted'", op.ObjectID, op.ObjectID)
			if err != nil {
				return err
			}
			var ids []string
			for roots.Next() {
				var id string
				roots.Scan(&id)
				ids = append(ids, id)
			}
			roots.Close()
			for _, id := range ids {
				if err = isolateSubtreeSQL(tx, id, op.CreatedAt); err != nil {
					return err
				}
			}
		}
		for _, q := range []string{"DELETE FROM post_bookmarks WHERE identity_id=?", "DELETE FROM post_likes WHERE user_id=?", "DELETE FROM blocks WHERE owner_id=?", "DELETE FROM drafts WHERE author_id=?", "DELETE FROM subscriptions WHERE user_id=?"} {
			if _, err := tx.Exec(q, op.ObjectID); err != nil {
				return err
			}
		}
		query = "UPDATE user_settings SET retain_content=? WHERE user_id=?"
		retain := 0
		if op.Action == "retain-user" {
			retain = 1
		}
		args = []any{retain, op.ObjectID}
	default:
		return errors.New("unknown operation")
	}
	if _, err := tx.Exec(query, args...); err != nil {
		return err
	}
	_, err := tx.Exec("INSERT OR IGNORE INTO operation_events(event_id,object_id,action,created_at) VALUES(?,?,?,?)", op.EventID, op.ObjectID, op.Action, op.CreatedAt)
	return err
}
func (app *App) applyOperation(op operation, expectedVersion ...int) error {
	app.opsMu.Lock()
	defer app.opsMu.Unlock()
	unlock, err := app.journalLock()
	if err != nil {
		return err
	}
	defer unlock()
	if len(expectedVersion) > 0 {
		table := "posts"
		if op.Action == "delete-reply" {
			table = "replies"
		}
		var v int
		if err := app.db.QueryRow("SELECT version_number FROM "+table+" WHERE id=?", op.ObjectID).Scan(&v); err != nil {
			return err
		}
		if v != expectedVersion[0] {
			return errConflict
		}
	}
	// Retain only a restricted short-lived copy, never the deleted text in the primary DB.
	if op.Action == "delete-post" {
		p, err := app.getPost(op.ObjectID)
		if err != nil {
			return err
		}
		if p.Status != "deleted" {
			if err = app.privatePut("deleted:"+op.EventID, p.AuthorID, "revision:"+p.ID, p, time.Now().Add(30*24*time.Hour).Unix()); err != nil {
				return err
			}
		}
	}
	if op.Action == "delete-reply" {
		reply, err := app.getReply(op.ObjectID)
		if err != nil {
			return err
		}
		if reply.Status != "deleted" {
			if err = app.privatePut("deleted:"+op.EventID, reply.AuthorID, "revision:"+reply.ID, reply, time.Now().Add(30*24*time.Hour).Unix()); err != nil {
				return err
			}
		}
	}
	tx, err := app.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if op.OwnerID != "" {
		var n int
		if err = tx.QueryRow("SELECT count(*) FROM users WHERE id=? AND status='active'", op.OwnerID).Scan(&n); err != nil || n != 1 {
			return errForbidden
		}
		if op.Action == "association" {
			if err = tx.QueryRow("SELECT count(*) FROM posts p WHERE p.id=? AND p.author_id=? AND "+visiblePostSQL, op.ObjectID, op.OwnerID).Scan(&n); err != nil || n != 1 {
				return errForbidden
			}
		}
	}
	if op.Action == "restrict" || op.Action == "delete-post" || op.Action == "delete-reply" {
		if err = app.preserveSubtree(tx, op.ObjectID); err != nil {
			return err
		}
	}
	if op.Action == "restrict" {
		if err = app.preserveIsolation(tx, op.ObjectID); err != nil {
			return err
		}
	}
	if op.Action == "deactivate-user" {
		if err = app.preserveUserWithdrawal(tx, op); err != nil {
			return err
		}
	}
	if err = app.appendOperationLocked(op); err != nil {
		tx.Rollback()
		app.db.Exec("UPDATE runtime_state SET mode='isolated' WHERE id=1")
		return err
	}
	if err = applyOperationSQL(tx, op); err != nil {
		return err
	}
	if op.Action == "release-restriction" {
		if err = app.restoreIsolation(tx, op.ObjectID); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if op.Action == "restrict" || op.Action == "delete-post" || op.Action == "delete-reply" || op.Action == "deactivate-user" {
		if err = app.scrubBackupsLocked(); err != nil {
			app.db.Exec("UPDATE runtime_state SET mode='isolated' WHERE id=1")
			return err
		}
	}
	return nil
}
func (app *App) reconcileJournal() error {
	unlock, lockErr := app.journalLock()
	if lockErr != nil {
		return lockErr
	}
	defer unlock()
	records, err := app.readJournal()
	if err != nil {
		return err
	}
	tx, err := app.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, op := range records {
		var count int
		if err = tx.QueryRow("SELECT count(*) FROM operation_events WHERE event_id=?", op.EventID).Scan(&count); err != nil {
			return err
		}
		if count == 0 {

			if err = applyOperationSQL(tx, op); err != nil {
				return err
			}
			if op.Action == "release-restriction" {
				if err = app.restoreIsolation(tx, op.ObjectID); err != nil {
					return err
				}
			}
		}
	}
	return tx.Commit()
}
func (app *App) SetMaintenance(mode string) error {
	if mode != "normal" && mode != "readonly" && mode != "isolated" {
		return errInput
	}
	if mode == "normal" {
		if err := app.reconcileJournal(); err != nil {
			return err
		}
	}
	_, err := app.db.Exec("UPDATE runtime_state SET mode=?,updated_at=? WHERE id=1", mode, time.Now().Unix())
	return err
}
func (app *App) maintenanceGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" || r.Method == "HEAD" {
			next.ServeHTTP(w, r)
			return
		}
		var mode string
		if app.db.QueryRow("SELECT mode FROM runtime_state WHERE id=1").Scan(&mode) != nil {
			respond(w, 503, map[string]string{"code": "DATABASE_UNAVAILABLE"})
			return
		}
		if app.config.WriteUntil != "" {
			until, _ := time.Parse(time.RFC3339, app.config.WriteUntil)
			if time.Now().After(until) && mode == "normal" {
				app.SetMaintenance("readonly")
				mode = "readonly"
			}
		}
		if mode != "normal" {
			allowed := r.URL.Path == "/api/v1/cases" || strings.HasPrefix(r.URL.Path, "/auth/") || strings.HasPrefix(r.URL.Path, "/api/v1/security/") || r.URL.Path == "/api/v1/me/deactivate" || (mode == "readonly" && r.URL.Path == "/api/v1/me/export")
			if !allowed {
				respond(w, 503, map[string]string{"code": "READ_ONLY"})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
func (app *App) operationsRoutes(router *chi.Mux) {
	router.Get("/status", func(w http.ResponseWriter, r *http.Request) {
		var mode string
		err := app.db.QueryRow("SELECT mode FROM runtime_state WHERE id=1").Scan(&mode)
		if err != nil {
			mode = "isolated"
		}
		app.renderPage(w, r, "运行状态", "status", `<h1>运行状态</h1><p>当前模式 `+esc(mode)+`。</p><p>本地合成测试，真实用户尚未接入。暂停投稿时仍保留紧急求助、举报申诉及退出入口。</p><p><a href="/help/emergency">紧急请求</a> · <a href="/help">帮助</a></p>`)
	})
}

type BackupManifest struct {
	CreatedAt        int64  `json:"created_at"`
	Schema           int    `json:"schema"`
	Tier             string `json:"tier"`
	ScrubbedSequence int64  `json:"scrubbed_sequence"`
	Hash             string `json:"sha256"`
	JournalMAC       string `json:"journal_mac"`
	JournalSequence  int64  `json:"journal_sequence"`
	Archive          string `json:"archive"`
}

func (app *App) Backup() (BackupManifest, error) {
	app.opsMu.Lock()
	defer app.opsMu.Unlock()
	var manifest BackupManifest
	unlock, err := app.journalLock()
	if err != nil {
		return manifest, err
	}
	defer unlock()
	records, err := app.readJournal()
	if err != nil {
		return manifest, err
	}
	if len(records) > 0 {
		manifest.JournalMAC = records[len(records)-1].MAC
		manifest.JournalSequence = records[len(records)-1].Sequence
	}
	var integrity string
	if err = app.db.QueryRow("PRAGMA quick_check").Scan(&integrity); err != nil || integrity != "ok" {
		return manifest, errors.New("database integrity unavailable")
	}
	key, err := restrictedKey(app.config.BackupKeyFile)
	if err != nil {
		return manifest, err
	}
	dir, err := os.MkdirTemp(filepath.Dir(app.config.Database), "backup-snapshot-")
	if err != nil {
		return manifest, err
	}
	defer os.RemoveAll(dir)
	if err = restrictPath(dir, true); err != nil {
		return manifest, err
	}
	snapshot := filepath.Join(dir, "snapshot.db")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err = app.db.ExecContext(ctx, "VACUUM INTO ?", snapshot); err != nil {
		return manifest, err
	}
	db, err := sql.Open("sqlite", snapshot)
	if err != nil {
		return manifest, err
	}
	_, err = db.Exec("DELETE FROM security_events; DELETE FROM auth_limits; DELETE FROM drafts; DELETE FROM media WHERE post_id IS NULL; DELETE FROM sessions; DELETE FROM oidc_flows; DELETE FROM consumed_tokens; UPDATE posts SET title='',content='' WHERE id IN (SELECT object_id FROM restrictions WHERE removed_at IS NULL); UPDATE replies SET content='' WHERE id IN (SELECT object_id FROM restrictions WHERE removed_at IS NULL); VACUUM;")
	closeErr := db.Close()
	if err != nil {
		return manifest, err
	}
	if closeErr != nil {
		return manifest, closeErr
	}
	raw, err := os.ReadFile(snapshot)
	if err != nil {
		return manifest, err
	}
	encrypted, err := encrypt(key, raw, "staratlas-backup-v1")
	if err != nil {
		return manifest, err
	}
	manifest.CreatedAt = time.Now().Unix()
	manifest.ScrubbedSequence = manifest.JournalSequence
	manifest.Schema = currentSchema
	manifest.Tier = "hourly"
	sum := sha256.Sum256(encrypted)
	manifest.Hash = hex.EncodeToString(sum[:])
	name := fmt.Sprintf("%d-%s.backup", time.Now().UnixNano(), randomID())
	manifest.Archive = name
	if err = os.MkdirAll(app.config.BackupDirectory, 0700); err != nil {
		return manifest, err
	}
	if err = exclusiveFile(filepath.Join(app.config.BackupDirectory, name), encrypted); err != nil {
		return manifest, err
	}
	meta, _ := json.MarshalIndent(manifest, "", "  ")
	if err = exclusiveFile(filepath.Join(app.config.BackupDirectory, name+".json"), meta); err != nil {
		return manifest, err
	}
	if err = app.rotateBackups(manifest, encrypted); err != nil {
		return manifest, err
	}
	return manifest, nil
}
func exclusiveFile(path string, raw []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err = restrictPath(path, false); err != nil {
		f.Close()
		return err
	}
	_, err = f.Write(raw)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}

// Restore only creates a new isolated database; the operator's source DB is never overwritten.
func RestoreBackup(config Config, archive, destination string) error {
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		return errors.New("restore destination must not exist")
	}
	raw, err := os.ReadFile(archive)
	if err != nil {
		return err
	}
	meta, err := os.ReadFile(archive + ".json")
	if err != nil {
		return err
	}
	var manifest BackupManifest
	if json.Unmarshal(meta, &manifest) != nil || (manifest.Schema != 6 && manifest.Schema != currentSchema) {
		return errors.New("invalid backup manifest")
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != manifest.Hash {
		return errors.New("backup hash mismatch")
	}
	keyPath := config.BackupKeyFile
	if keyPath == "" {
		keyPath = config.Database + ".backup.key"
	}
	key, err := os.ReadFile(keyPath)
	if err != nil {
		return err
	}
	plain, err := decrypt(key, raw, "staratlas-backup-v1")
	if err != nil {
		return err
	}
	if config.VaultKeyFile == "" {
		config.VaultKeyFile = config.Database + ".vault.key"
	}
	if config.OpsJournalFile == "" {
		config.OpsJournalFile = config.Database + ".operations.jsonl"
	}
	if config.VaultDatabase == "" {
		config.VaultDatabase = config.Database + ".private.db"
	}
	journal := config.OpsJournalFile
	if _, err = os.Stat(journal); err != nil && manifest.JournalSequence > 0 {
		return errors.New("restoration requires independent operation journal")
	}
	if err = os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return err
	}
	if err = exclusiveFile(destination, plain); err != nil {
		return err
	}
	// Read-only/isolation is set before New can expose any handler.
	db, err := sql.Open("sqlite", destination)
	if err != nil {
		return err
	}
	_, err = db.Exec("UPDATE runtime_state SET mode='isolated'; UPDATE sessions SET revoked_at=unixepoch();")
	db.Close()
	if err != nil {
		return err
	}
	config.Database = destination
	app, err := New(config)
	if err != nil {
		return err
	}
	defer app.Close()
	// A snapshot must not resurrect a revoked grant. Re-authorize after recovery.
	if _, err = app.db.Exec("DELETE FROM team_members; DELETE FROM community_representatives;"); err != nil {
		return err
	}
	records, err := app.readJournal()
	if err != nil {
		return err
	}
	if manifest.JournalSequence > 0 {
		if int64(len(records)) < manifest.JournalSequence || records[manifest.JournalSequence-1].MAC != manifest.JournalMAC {
			return errors.New("restoration journal is stale")
		}
	}
	if _, err = app.db.Exec("DELETE FROM drafts; DELETE FROM notifications WHERE created_at<unixepoch()-180*86400;"); err != nil {
		return err
	}
	var check string
	if app.db.QueryRow("PRAGMA integrity_check").Scan(&check) != nil || check != "ok" {
		return errors.New("restoration integrity failure")
	}
	return app.SetMaintenance("readonly")
}
func (app *App) CleanupCore() error {
	app.opsMu.Lock()
	defer app.opsMu.Unlock()
	unlock, lockErr := app.journalLock()
	if lockErr != nil {
		return lockErr
	}
	defer unlock()
	now := time.Now().Unix()
	for _, query := range []string{"DELETE FROM drafts WHERE updated_at<?", "DELETE FROM notifications WHERE created_at<?"} {
		cutoff := now - 30*86400
		if strings.Contains(query, "notifications") {
			cutoff = now - 180*86400
		}
		if _, err := app.db.Exec(query, cutoff); err != nil {
			return err
		}
	}
	// Unfinished uploads and deleted attachments lose their independent decryption keys.
	rows, err := app.db.Query("SELECT m.id FROM media m LEFT JOIN posts p ON p.id=m.post_id WHERE ((m.post_id IS NULL AND m.created_at<?) OR (p.status='deleted' AND p.updated_at<?) OR m.removed_at<unixepoch()-30*86400) AND NOT EXISTS(SELECT 1 FROM cases c WHERE (c.object_id=p.id OR c.object_id IN (SELECT id FROM replies WHERE post_id=p.id)) AND (c.closed_at IS NULL OR c.closed_at>unixepoch()-30*86400))", now-86400, now-30*86400)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		if _, err = app.vault.Exec("DELETE FROM media_keys WHERE id=?", id); err != nil {
			return err
		}
		if _, err = app.db.Exec("DELETE FROM media WHERE id=?", id); err != nil {
			return err
		}
	}
	// Keep only evidence tied to active disputes. Closed evidence expires thirty days after closure.
	rows, err = app.db.Query("SELECT id,object_id,closed_at FROM cases")
	if err != nil {
		return err
	}
	type retention struct {
		id, object string
		closed     sql.NullInt64
	}
	var cases []retention
	for rows.Next() {
		var c retention
		if err = rows.Scan(&c.id, &c.object, &c.closed); err != nil {
			rows.Close()
			return err
		}
		cases = append(cases, c)
	}
	rows.Close()
	deadlines := map[string]int64{}
	for _, c := range cases {
		expiry := int64(0)
		if c.closed.Valid {
			expiry = c.closed.Int64 + 30*86400
		}
		if _, err = app.vault.Exec("UPDATE objects SET expires_at=? WHERE id=? AND kind='case'", expiry, "case:"+c.id); err != nil {
			return err
		}
		if c.object != "" {
			old, ok := deadlines[c.object]
			if !ok || expiry == 0 || (old != 0 && expiry > old) {
				deadlines[c.object] = expiry
			}
		}
	}
	for root, expiry := range deadlines {
		rows, e := app.db.Query("SELECT child_id FROM isolated_children WHERE root_id=?", root)
		if e != nil {
			return e
		}
		var children []string
		for rows.Next() {
			var id string
			rows.Scan(&id)
			children = append(children, id)
		}
		rows.Close()
		for _, id := range children {
			old, ok := deadlines[id]
			if !ok || expiry == 0 || (old != 0 && expiry > old) {
				deadlines[id] = expiry
			}
		}
	}
	for object, expiry := range deadlines {
		if _, err = app.vault.Exec("UPDATE objects SET expires_at=? WHERE id=? OR kind=?", expiry, "isolated:"+object, "revision:"+object); err != nil {
			return err
		}
	}
	_, err = app.vault.Exec("DELETE FROM objects WHERE expires_at<>0 AND expires_at<=?", now)
	if err != nil {
		return err
	}
	return app.scrubBackupsLocked()
}

func (app *App) Diagnostics() (map[string]any, error) {
	result := map[string]any{}
	var quick, mode string
	if err := app.db.QueryRow("PRAGMA quick_check").Scan(&quick); err != nil {
		return nil, err
	}
	app.db.QueryRow("SELECT mode FROM runtime_state WHERE id=1").Scan(&mode)
	result["integrity"], result["mode"] = quick, mode
	for name, query := range map[string]string{"failed_jobs": "SELECT count(*) FROM jobs WHERE status='failed'", "overdue_cases": "SELECT count(*) FROM cases WHERE closed_at IS NULL AND due_at<unixepoch()", "queue_backlog": "SELECT count(*) FROM jobs WHERE status IN ('pending','running') AND created_at<unixepoch()-3600"} {
		var n int
		if err := app.db.QueryRow(query).Scan(&n); err != nil {
			return nil, err
		}
		result[name] = n
	}
	manifests, invalid := app.validBackups()
	var latest int64
	for _, m := range manifests {
		if m.CreatedAt > latest {
			latest = m.CreatedAt
		}
	}
	result["invalid_backups"] = invalid
	_, journalErr := app.readJournal()
	result["journal_valid"] = journalErr == nil
	result["latest_backup_at"] = latest
	result["backup_stale"] = latest == 0 || time.Now().Unix()-latest > 3600
	if stat, err := os.Stat(app.config.Database + "-wal"); err == nil {
		result["wal_bytes"] = stat.Size()
	}
	return result, nil
}
func (app *App) maintenanceTasks() {
	if err := app.processJobs(); err != nil {
		app.audit("job_processing_failed")
	}
	// Bounded hourly snapshot, daily cleanup; a failure records a redacted alert.
	now := time.Now().Unix()
	if now-app.lastCleanup >= 86400 {
		if err := app.CleanupCore(); err != nil {
			app.audit("lifecycle_cleanup_failed")
		} else {
			app.lastCleanup = now
		}
	}
	if now-app.lastBackup >= 3600 && now-app.lastBackupAttempt >= 60 {
		app.lastBackupAttempt = now
		if _, err := app.Backup(); err != nil {
			app.audit("backup_failed")
		} else {
			app.lastBackup = now
		}
	}
	var emergencies int
	app.db.QueryRow("SELECT count(*) FROM cases WHERE kind='emergency' AND status='received' AND received_at<?", now-1800).Scan(&emergencies)
	if emergencies > 0 {
		app.SetMaintenance("readonly")
	}
}
