package atlas

import (
	"database/sql"
	"time"

	"github.com/go-chi/chi/v5"
)

type UserProfile struct {
	ID        string
	Name      string
	Email     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (app *App) userRoutes(router *chi.Mux) { app.registerUserRoutes(router) }

type ReplyWithPost struct {
	Reply
	PostTitle string
}

func (app *App) getIdentity(id string) (*UserProfile, error) {
	var u UserProfile
	var createdAt, updatedAt int64

	err := app.db.QueryRow(`
		SELECT u.id,u.alias,'',s.created_at,s.created_at FROM users u JOIN user_settings s ON s.user_id=u.id WHERE u.id=?
	`, id).Scan(&u.ID, &u.Name, &u.Email, &createdAt, &updatedAt)

	if err != nil {
		return nil, err
	}

	u.CreatedAt = time.Unix(createdAt, 0)
	u.UpdatedAt = time.Unix(updatedAt, 0)
	return &u, nil
}

func (app *App) listPostsByAuthor(authorID string, limit int) ([]Post, error) {
	rows, err := app.db.Query(`
		SELECT p.id, COALESCE(p.community_id,''), COALESCE(c.name, ''), COALESCE(c.slug,''),p.author_id, COALESCE(i.alias, ''),COALESCE(i.status,'deleted'),
		       p.title, p.content, p.status, p.view_count, p.reply_count, p.bookmark_count,
		       p.created_at, p.updated_at
		FROM posts p
		LEFT JOIN communities c ON p.community_id = c.id
		LEFT JOIN users i ON p.author_id = i.id
		WHERE p.author_id = ? AND `+visiblePostSQL+`
		ORDER BY p.created_at DESC
		LIMIT ?
	`, authorID, limit)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var posts []Post
	for rows.Next() {
		var p Post
		var createdAt, updatedAt int64
		if err := rows.Scan(&p.ID, &p.CommunityID, &p.CommunityName, &p.CommunitySlug, &p.AuthorID, &p.AuthorName, &p.AuthorStatus,
			&p.Title, &p.Content, &p.Status, &p.ViewCount, &p.ReplyCount, &p.BookmarkCount,
			&createdAt, &updatedAt); err != nil {
			return nil, err
		}
		p.CreatedAt = time.Unix(createdAt, 0)
		p.UpdatedAt = time.Unix(updatedAt, 0)
		posts = append(posts, p)
	}

	return posts, rows.Err()
}

func (app *App) listRepliesByAuthor(authorID string, limit int) ([]ReplyWithPost, error) {
	rows, err := app.db.Query(`
		SELECT r.id, r.post_id, r.parent_id, r.author_id, COALESCE(i.alias, ''),
		       r.content, r.level, r.status, r.created_at, r.updated_at,
		       COALESCE(p.title, '')
		FROM replies r
		LEFT JOIN users i ON r.author_id = i.id
		LEFT JOIN posts p ON r.post_id = p.id
		WHERE r.author_id = ? AND r.status = 'published' AND `+visiblePostSQL+`
		ORDER BY r.created_at DESC
		LIMIT ?
	`, authorID, limit)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var replies []ReplyWithPost
	for rows.Next() {
		var r ReplyWithPost
		var parentID sql.NullString
		var createdAt, updatedAt int64
		if err := rows.Scan(&r.ID, &r.PostID, &parentID, &r.AuthorID, &r.AuthorName,
			&r.Content, &r.Level, &r.Status, &createdAt, &updatedAt, &r.PostTitle); err != nil {
			return nil, err
		}
		r.ParentID = parentID
		r.CreatedAt = time.Unix(createdAt, 0)
		r.UpdatedAt = time.Unix(updatedAt, 0)
		replies = append(replies, r)
	}

	return replies, rows.Err()
}

func (app *App) listBookmarks(identityID string, limit int) ([]Post, error) {
	rows, err := app.db.Query(`
		SELECT p.id, COALESCE(p.community_id,''), COALESCE(c.name, ''), COALESCE(c.slug,''),p.author_id, COALESCE(i.alias, ''),COALESCE(i.status,'deleted'),
		       p.title, p.content, p.status, p.view_count, p.reply_count, p.bookmark_count,
		       p.created_at, p.updated_at
		FROM posts p
		INNER JOIN post_bookmarks b ON p.id = b.post_id
		LEFT JOIN communities c ON p.community_id = c.id
		LEFT JOIN users i ON p.author_id = i.id
		WHERE b.identity_id = ? AND `+visiblePostSQL+`
		ORDER BY b.created_at DESC
		LIMIT ?
	`, identityID, limit)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var posts []Post
	for rows.Next() {
		var p Post
		var createdAt, updatedAt int64
		if err := rows.Scan(&p.ID, &p.CommunityID, &p.CommunityName, &p.CommunitySlug, &p.AuthorID, &p.AuthorName, &p.AuthorStatus,
			&p.Title, &p.Content, &p.Status, &p.ViewCount, &p.ReplyCount, &p.BookmarkCount,
			&createdAt, &updatedAt); err != nil {
			return nil, err
		}
		p.CreatedAt = time.Unix(createdAt, 0)
		p.UpdatedAt = time.Unix(updatedAt, 0)
		posts = append(posts, p)
	}

	return posts, rows.Err()
}

func (app *App) isBookmarked(identityID, postID string) (bool, error) {
	var count int
	err := app.db.QueryRow(`
		SELECT COUNT(*) FROM post_bookmarks
		WHERE identity_id = ? AND post_id = ?
	`, identityID, postID).Scan(&count)
	return count > 0, err
}

func (app *App) addBookmark(identityID, postID string) error {
	id := randomID()
	now := time.Now().Unix()

	tx, err := app.db.Begin()
	if err != nil {
		return err
	}

	_, err = tx.Exec(`
		INSERT INTO post_bookmarks (id, identity_id, post_id, created_at)
		VALUES (?, ?, ?, ?)
	`, id, identityID, postID, now)
	if err != nil {
		tx.Rollback()
		return err
	}

	_, err = tx.Exec(`
		UPDATE posts SET bookmark_count = bookmark_count + 1
		WHERE id = ?
	`, postID)
	if err != nil {
		tx.Rollback()
		return err
	}

	return tx.Commit()
}

func (app *App) removeBookmark(identityID, postID string) error {
	tx, err := app.db.Begin()
	if err != nil {
		return err
	}

	_, err = tx.Exec(`
		DELETE FROM post_bookmarks
		WHERE identity_id = ? AND post_id = ?
	`, identityID, postID)
	if err != nil {
		tx.Rollback()
		return err
	}

	_, err = tx.Exec(`
		UPDATE posts SET bookmark_count = bookmark_count - 1
		WHERE id = ? AND bookmark_count > 0
	`, postID)
	if err != nil {
		tx.Rollback()
		return err
	}

	return tx.Commit()
}
