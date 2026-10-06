package atlas

import "database/sql"

const descendantsSQL = `SELECT r.id FROM replies r WHERE r.post_id=? OR r.id IN (WITH RECURSIVE children(id) AS (SELECT id FROM replies WHERE parent_id=? UNION ALL SELECT r.id FROM replies r JOIN children c ON r.parent_id=c.id) SELECT id FROM children)`

func subtreeIDs(tx *sql.Tx, root string) ([]string, error) {
	rows, err := tx.Query(descendantsSQL, root, root)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
func (app *App) preserveSubtree(tx *sql.Tx, root string) error {
	ids, err := subtreeIDs(tx, root)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err = app.preserveIsolation(tx, id); err != nil {
			return err
		}
	}
	return nil
}
func isolateSubtreeSQL(tx *sql.Tx, root string, now int64) error {
	ids, err := subtreeIDs(tx, root)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err = tx.Exec("INSERT OR IGNORE INTO isolated_children VALUES(?,?)", root, id); err != nil {
			return err
		}
		if _, err = tx.Exec("UPDATE replies SET status='hidden',content='',updated_at=? WHERE id=? AND status<>'deleted'", now, id); err != nil {
			return err
		}
	}
	return nil
}
func (app *App) restoreSubtree(tx *sql.Tx, root string) error {
	rows, err := tx.Query("SELECT r.id,r.author_id FROM isolated_children ic JOIN replies r ON r.id=ic.child_id WHERE ic.root_id=? AND r.status='hidden' ORDER BY r.level,r.created_at,r.id", root)
	if err != nil {
		return err
	}
	type item struct{ id, owner string }
	var items []item
	for rows.Next() {
		var value item
		if err = rows.Scan(&value.id, &value.owner); err != nil {
			rows.Close()
			return err
		}
		items = append(items, value)
	}
	rows.Close()
	for _, value := range items {
		var n int
		// Restore from parents to children, keeping every independent restriction in force.
		err = tx.QueryRow(`SELECT count(*) FROM replies r JOIN posts p ON p.id=r.post_id WHERE r.id=? AND `+visiblePostSQL+`
     AND NOT EXISTS(SELECT 1 FROM restrictions WHERE object_id=r.id AND removed_at IS NULL)
     AND NOT EXISTS(WITH RECURSIVE chain(id,parent_id,status) AS (SELECT id,parent_id,status FROM replies WHERE id=r.parent_id UNION ALL SELECT parent.id,parent.parent_id,parent.status FROM replies parent JOIN chain c ON parent.id=c.parent_id) SELECT 1 FROM chain WHERE status<>'published' OR EXISTS(SELECT 1 FROM restrictions WHERE object_id=chain.id AND removed_at IS NULL))`, value.id).Scan(&n)
		if err != nil {
			return err
		}
		if n == 0 {
			continue
		}
		var body isolatedBody
		if err = app.privateGet("isolated:"+value.id, value.owner, "isolated", &body); err == sql.ErrNoRows {
			continue
		} else if err != nil {
			return err
		}
		if _, err = tx.Exec("UPDATE replies SET status='published',content=? WHERE id=? AND status='hidden'", body.Content, value.id); err != nil {
			return err
		}
	}
	return nil
}
