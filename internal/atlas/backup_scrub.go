package atlas

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// Withdrawn originals must not remain readable in earlier ordinary archives.
// This only removes text; restoration still replays the independent journal.
func (app *App) scrubBackupsLocked() error {
	records, err := app.readJournal()
	if err != nil {
		return err
	}
	sequence := int64(len(records))
	manifests, invalid := app.validBackups()
	if invalid > 0 {
		return errors.New("backup integrity failure requires operator attention")
	}
	if len(manifests) == 0 {
		return nil
	}
	rows, err := app.db.Query("SELECT id FROM posts WHERE status='deleted' UNION SELECT id FROM replies WHERE status IN ('deleted','hidden') UNION SELECT object_id FROM restrictions WHERE removed_at IS NULL")
	if err != nil {
		return err
	}
	var objects []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		objects = append(objects, id)
	}
	rows.Close()
	key, err := os.ReadFile(app.config.BackupKeyFile)
	if err != nil {
		return err
	}
	for _, m := range manifests {
		if m.ScrubbedSequence >= sequence {
			continue
		}
		path := filepath.Join(app.config.BackupDirectory, m.Archive)
		encrypted, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		plain, err := decrypt(key, encrypted, "staratlas-backup-v1")
		if err != nil {
			return err
		}
		dir, err := os.MkdirTemp(filepath.Dir(app.config.Database), "backup-scrub-")
		if err != nil {
			return err
		}
		err = func() error {
			defer os.RemoveAll(dir)
			if err := restrictPath(dir, true); err != nil {
				return err
			}
			snapshot := filepath.Join(dir, "snapshot.db")
			if err := exclusiveFile(snapshot, plain); err != nil {
				return err
			}
			db, err := sql.Open("sqlite", snapshot)
			if err != nil {
				return err
			}
			defer db.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			defer tx.Rollback()
			for _, id := range objects {
				if _, err = tx.ExecContext(ctx, "UPDATE posts SET title='',content='' WHERE id=?", id); err != nil {
					return err
				}
				if _, err = tx.ExecContext(ctx, "UPDATE replies SET content='' WHERE id=?", id); err != nil {
					return err
				}
			}
			if err = tx.Commit(); err != nil {
				return err
			}
			if _, err = db.ExecContext(ctx, "VACUUM"); err != nil {
				return err
			}
			if err = db.Close(); err != nil {
				return err
			}
			raw, err := os.ReadFile(snapshot)
			if err != nil {
				return err
			}
			raw, err = encrypt(key, raw, "staratlas-backup-v1")
			if err != nil {
				return err
			}
			sum := sha256.Sum256(raw)
			m.Hash = hex.EncodeToString(sum[:])
			m.ScrubbedSequence = sequence
			// Atomic single-file replacement; an interrupted two-file update fails hash validation.
			temp := path + ".rewrite"
			if err = exclusiveFile(temp, raw); err != nil {
				return err
			}
			if err = os.Rename(temp, path); err != nil {
				return err
			}
			meta, _ := json.MarshalIndent(m, "", "  ")
			temp = path + ".json.rewrite"
			if err = exclusiveFile(temp, meta); err != nil {
				return err
			}
			return os.Rename(temp, path+".json")
		}()
		if err != nil {
			return err
		}
	}
	return nil
}
