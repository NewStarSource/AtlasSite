package atlas

import (
	"database/sql"
	"fmt"
	"html/template"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

type Post struct {
	ID            string
	CommunityID   string
	CommunityName string
	CommunitySlug string
	AuthorStatus  string
	AuthorID      string
	AuthorName    string
	Title         string
	Content       string
	Status        string
	ViewCount     int
	ReplyCount    int
	BookmarkCount int
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type Reply struct {
	ID         string
	PostID     string
	ParentID   sql.NullString
	AuthorID   string
	AuthorName string
	Content    string
	Level      int
	Status     string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	Children   []*Reply
}

func (app *App) postsRoutes(router *chi.Mux) { app.registerPostRoutes(router) }

func (app *App) createPost(communityID, authorID, title, content string) (*Post, error) {
	id := randomID()
	now := time.Now().Unix()

	_, err := app.db.Exec(`
		INSERT INTO posts (id, community_id, author_id, title, content, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 'published', ?, ?)
	`, id, nullable(communityID), authorID, title, content, now, now)

	if err != nil {
		return nil, err
	}

	return app.getPost(id)
}

func (app *App) getPost(id string) (*Post, error) {
	var p Post
	var createdAt, updatedAt int64

	err := app.db.QueryRow(`
		SELECT p.id, COALESCE(p.community_id,''), COALESCE(c.name, ''), COALESCE(c.slug,''),p.author_id, COALESCE(i.alias, ''),COALESCE(i.status,'deleted'),
		       p.title, p.content, p.status, p.view_count, p.reply_count, p.bookmark_count,
		       p.created_at, p.updated_at
		FROM posts p
		LEFT JOIN communities c ON p.community_id = c.id
		LEFT JOIN users i ON p.author_id = i.id
		WHERE p.id = ?
	`, id).Scan(&p.ID, &p.CommunityID, &p.CommunityName, &p.CommunitySlug, &p.AuthorID, &p.AuthorName, &p.AuthorStatus,
		&p.Title, &p.Content, &p.Status, &p.ViewCount, &p.ReplyCount, &p.BookmarkCount,
		&createdAt, &updatedAt)

	if err != nil {
		return nil, err
	}

	p.CreatedAt = time.Unix(createdAt, 0)
	p.UpdatedAt = time.Unix(updatedAt, 0)
	return &p, nil
}

func (app *App) listPosts(communityID string, limit int) ([]Post, error) {
	query := `
		SELECT p.id, COALESCE(p.community_id,''), COALESCE(c.name, ''), COALESCE(c.slug,''),p.author_id, COALESCE(i.alias, ''),COALESCE(i.status,'deleted'),
		       p.title, p.content, p.status, p.view_count, p.reply_count, p.bookmark_count,
		       p.created_at, p.updated_at
		FROM posts p
		LEFT JOIN communities c ON p.community_id = c.id
		LEFT JOIN users i ON p.author_id = i.id
		WHERE p.community_id = ? AND ` + visiblePostSQL + `
		ORDER BY p.created_at DESC
		LIMIT ?
	`

	rows, err := app.db.Query(query, communityID, limit)
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

func (app *App) listRecentPosts(limit int) ([]Post, error) { return app.queryPosts("", limit) }
func (app *App) queryPosts(condition string, limit int, args ...any) ([]Post, error) {
	query := `
		SELECT p.id, COALESCE(p.community_id,''), COALESCE(c.name, ''), COALESCE(c.slug,''),p.author_id, COALESCE(i.alias, ''),COALESCE(i.status,'deleted'),
		       p.title, p.content, p.status, p.view_count, p.reply_count, p.bookmark_count,
		       p.created_at, p.updated_at
		FROM posts p
		LEFT JOIN communities c ON p.community_id = c.id
		LEFT JOIN users i ON p.author_id = i.id
		WHERE ` + visiblePostSQL + condition + `
		ORDER BY p.created_at DESC
		LIMIT ?
	`

	args = append(args, limit)
	rows, err := app.db.Query(query, args...)
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

func (app *App) createReply(postID, authorID, content, parentID string) (*Reply, error) {
	return app.saveReply(postID, authorID, content, parentID, randomID())
}

func (app *App) getReply(id string) (*Reply, error) {
	var r Reply
	var parentID sql.NullString
	var createdAt, updatedAt int64

	err := app.db.QueryRow(`
		SELECT r.id, r.post_id, r.parent_id, r.author_id, COALESCE(i.alias, ''),
		       r.content, r.level, r.status, r.created_at, r.updated_at
		FROM replies r
		LEFT JOIN users i ON r.author_id = i.id
		WHERE r.id = ?
	`, id).Scan(&r.ID, &r.PostID, &parentID, &r.AuthorID, &r.AuthorName,
		&r.Content, &r.Level, &r.Status, &createdAt, &updatedAt)

	if err != nil {
		return nil, err
	}

	r.ParentID = parentID
	r.CreatedAt = time.Unix(createdAt, 0)
	r.UpdatedAt = time.Unix(updatedAt, 0)
	return &r, nil
}

func (app *App) listReplies(postID string) ([]*Reply, error) {
	rows, err := app.db.Query(`
		SELECT r.id, r.post_id, r.parent_id, r.author_id, COALESCE(i.alias, ''),
		       r.content, r.level, r.status, r.created_at, r.updated_at
		FROM replies r
		LEFT JOIN users i ON r.author_id = i.id
		WHERE r.post_id = ? AND r.status = 'published' AND NOT EXISTS(
 WITH RECURSIVE chain(id,parent_id,status) AS (SELECT id,parent_id,status FROM replies WHERE id=r.id UNION ALL SELECT parent.id,parent.parent_id,parent.status FROM replies parent JOIN chain ch ON parent.id=ch.parent_id)
 SELECT 1 FROM chain WHERE status<>'published' OR EXISTS(SELECT 1 FROM restrictions WHERE object_id=chain.id AND removed_at IS NULL))
		ORDER BY r.created_at ASC
	`, postID)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var replies []*Reply
	for rows.Next() {
		var r Reply
		var parentID sql.NullString
		var createdAt, updatedAt int64
		if err := rows.Scan(&r.ID, &r.PostID, &parentID, &r.AuthorID, &r.AuthorName,
			&r.Content, &r.Level, &r.Status, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		r.ParentID = parentID
		r.CreatedAt = time.Unix(createdAt, 0)
		r.UpdatedAt = time.Unix(updatedAt, 0)
		replies = append(replies, &r)
	}

	return replies, rows.Err()
}

func buildReplyTree(replies []*Reply) []*Reply {
	idMap := make(map[string]*Reply)
	for _, r := range replies {
		idMap[r.ID] = r
	}

	var roots []*Reply
	for _, r := range replies {
		if !r.ParentID.Valid {
			roots = append(roots, r)
		} else if parent, ok := idMap[r.ParentID.String]; ok {
			parent.Children = append(parent.Children, r)
		}
	}

	return roots
}

func renderReplyTree(buf *strings.Builder, replies []*Reply) {
	for _, reply := range replies {
		levelClass := fmt.Sprintf("level-%d", reply.Level)
		if reply.Level >= 3 {
			levelClass = "level-3-plus"
		}

		buf.WriteString(`<div class="reply-item `)
		buf.WriteString(levelClass)
		buf.WriteString(`">
			<div class="reply-header">
				<a href="/u/`)
		buf.WriteString(template.URLQueryEscaper(reply.AuthorID))
		buf.WriteString(`" class="reply-author">`)
		buf.WriteString(template.HTMLEscapeString(reply.AuthorName))
		buf.WriteString(`</a>
				<span>·</span>
				<span class="reply-time">`)
		buf.WriteString(formatTime(reply.CreatedAt))
		buf.WriteString(`</span>
			</div>
			<div class="reply-body">`)
		buf.WriteString(template.HTMLEscapeString(reply.Content))
		buf.WriteString(`</div>`)

		if len(reply.Children) > 0 {
			buf.WriteString(`<div class="reply-children">`)
			renderReplyTree(buf, reply.Children)
			buf.WriteString(`</div>`)
		}

		buf.WriteString(`</div>`)
	}
}

func formatTime(t time.Time) string {
	now := time.Now()
	diff := now.Sub(t)

	if diff < time.Minute {
		return "刚刚"
	} else if diff < time.Hour {
		return fmt.Sprintf("%d 分钟前", int(diff.Minutes()))
	} else if diff < 24*time.Hour {
		return fmt.Sprintf("%d 小时前", int(diff.Hours()))
	} else if diff < 30*24*time.Hour {
		return fmt.Sprintf("%d 天前", int(diff.Hours()/24))
	} else {
		return t.Format("2006-01-02")
	}
}
