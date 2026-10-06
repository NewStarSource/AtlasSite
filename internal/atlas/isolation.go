package atlas

import (
	"database/sql"
	"time"
)

type isolatedBody struct{ Kind, Owner, Title, Content string }

func (app *App) preserveUserWithdrawal(tx *sql.Tx, op operation) error {
	rows, err := tx.Query("SELECT id,title,content FROM posts WHERE author_id=? AND status<>'deleted' UNION ALL SELECT id,'',content FROM replies WHERE author_id=? AND status<>'deleted'", op.ObjectID, op.ObjectID)
	if err != nil {
		return err
	}
	type item struct{ id, title, content string }
	var items []item
	keep := map[string]bool{}
	for _, id := range op.Retained {
		keep[id] = true
	}
	for rows.Next() {
		var value item
		if err = rows.Scan(&value.id, &value.title, &value.content); err != nil {
			rows.Close()
			return err
		}
		if !keep[value.id] && value.content != "" {
			items = append(items, value)
		}
	}
	rows.Close()
	for _, value := range items {
		if err = app.preserveSubtree(tx, value.id); err != nil {
			return err
		}
	}
	for _, value := range items {
		if err = app.privatePut("withdrawn:"+op.EventID+":"+value.id, op.ObjectID, "revision:"+value.id, map[string]string{"title": value.title, "content": value.content}, op.CreatedAt+30*86400); err != nil {
			return err
		}
	}
	return nil
}

// Risk originals are separate from the ordinary DB and its long-lived backups.
func (app *App) preserveIsolation(tx *sql.Tx, object string) error {
	var body isolatedBody
	body.Kind = "post"
	err := tx.QueryRow("SELECT author_id,title,content FROM posts WHERE id=?", object).Scan(&body.Owner, &body.Title, &body.Content)
	if err == sql.ErrNoRows {
		body.Kind = "reply"
		err = tx.QueryRow("SELECT author_id,content FROM replies WHERE id=?", object).Scan(&body.Owner, &body.Content)
	}
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	var existing int
	if err = app.vault.QueryRow("SELECT count(*) FROM objects WHERE id=? AND (expires_at=0 OR expires_at>?)", "isolated:"+object, time.Now().Unix()).Scan(&existing); err != nil {
		return err
	}
	if existing > 0 {
		return nil
	}
	if body.Content == "" {
		return nil
	}
	return app.privatePut("isolated:"+object, body.Owner, "isolated", body, time.Now().Add(30*24*time.Hour).Unix())
}
func (app *App) restoreIsolation(tx *sql.Tx, restriction string) error {
	var object string
	err := tx.QueryRow("SELECT object_id FROM restrictions WHERE id=?", restriction).Scan(&object)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	var remaining int
	if err = tx.QueryRow("SELECT count(*) FROM restrictions WHERE object_id=? AND removed_at IS NULL", object).Scan(&remaining); err != nil || remaining > 0 {
		return err
	}
	var owner string
	kind := "post"
	err = tx.QueryRow("SELECT author_id FROM posts WHERE id=? AND status<>'deleted'", object).Scan(&owner)
	if err == sql.ErrNoRows {
		kind = "reply"
		err = tx.QueryRow("SELECT author_id FROM replies WHERE id=? AND status<>'deleted'", object).Scan(&owner)
	}
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if kind == "reply" {
		var visible int
		err = tx.QueryRow(`SELECT count(*) FROM replies r JOIN posts p ON p.id=r.post_id WHERE r.id=? AND `+visiblePostSQL+` AND NOT EXISTS(WITH RECURSIVE chain(id,parent_id,status) AS (SELECT id,parent_id,status FROM replies WHERE id=r.parent_id UNION ALL SELECT parent.id,parent.parent_id,parent.status FROM replies parent JOIN chain c ON parent.id=c.parent_id) SELECT 1 FROM chain WHERE status<>'published' OR EXISTS(SELECT 1 FROM restrictions WHERE object_id=chain.id AND removed_at IS NULL))`, object).Scan(&visible)
		if err != nil || visible == 0 {
			return err
		}
	}
	var body isolatedBody
	if err = app.privateGet("isolated:"+object, owner, "isolated", &body); err != nil {
		if kind == "post" {
			_, err = tx.Exec("UPDATE posts SET status='hidden' WHERE id=? AND content=''", object)
		} else {
			_, err = tx.Exec("UPDATE replies SET status='hidden' WHERE id=? AND content=''", object)
		}
		return err
	}
	if kind == "post" {
		_, err = tx.Exec("UPDATE posts SET title=?,content=? WHERE id=? AND status<>'deleted'", body.Title, body.Content, object)
	} else {
		_, err = tx.Exec("UPDATE replies SET content=?,status='published' WHERE id=? AND status<>'deleted'", body.Content, object)
	}
	if err == nil {
		err = app.restoreSubtree(tx, object)
	}
	return err
}
