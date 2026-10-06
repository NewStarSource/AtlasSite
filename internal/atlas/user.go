package atlas

import (
	"database/sql"
	"fmt"
	"html/template"
	"net/http"
	"strings"
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

func (app *App) userRoutes(router *chi.Mux) {
	// User activity page
	router.Get("/u/{id}", func(w http.ResponseWriter, r *http.Request) {
		userID := chi.URLParam(r, "id")
		identity, _, _ := app.currentIdentity(r)

		// Get user info
		user, err := app.getIdentity(userID)
		if err == sql.ErrNoRows {
			w.WriteHeader(404)
			w.Write([]byte("用户不存在"))
			return
		}
		if err != nil {
			w.WriteHeader(503)
			w.Write([]byte("服务暂时不可用"))
			return
		}

		// Get user's posts
		posts, _ := app.listPostsByAuthor(userID, 20)

		// Get user's replies
		replies, _ := app.listRepliesByAuthor(userID, 20)

		// Calculate stats
		postCount := len(posts)
		replyCount := len(replies)

		type PageData struct {
			Title    string
			Page     string
			Identity *Identity
			Content  template.HTML
		}

		var contentBuf strings.Builder
		contentBuf.WriteString(`<div class="user-profile">
			<div class="user-header">
				<div class="user-info">
					<h1 class="user-name">`)
		contentBuf.WriteString(template.HTMLEscapeString(user.Name))
		contentBuf.WriteString(`</h1>
					<div class="user-meta">
						<span>加入于 `)
		contentBuf.WriteString(formatTime(user.CreatedAt))
		contentBuf.WriteString(`</span>
					</div>
				</div>
				<a href="`)
		contentBuf.WriteString(template.HTMLEscapeString(app.config.AccountOrigin))
		contentBuf.WriteString(`/profile/`)
		contentBuf.WriteString(template.URLQueryEscaper(userID))
		contentBuf.WriteString(`" class="btn btn-secondary" target="_blank">查看完整资料</a>
			</div>
			<div class="user-stats">
				<div class="stat-item">
					<span class="stat-value">`)
		contentBuf.WriteString(fmt.Sprintf("%d", postCount))
		contentBuf.WriteString(`</span>
					<span class="stat-label">帖子</span>
				</div>
				<div class="stat-item">
					<span class="stat-value">`)
		contentBuf.WriteString(fmt.Sprintf("%d", replyCount))
		contentBuf.WriteString(`</span>
					<span class="stat-label">回复</span>
				</div>
			</div>
		</div>`)

		// Posts section
		if len(posts) > 0 {
			contentBuf.WriteString(`<div class="content-section">
				<h2 class="section-title">最近的帖子</h2>
				<div class="feed-list">`)
			for _, post := range posts {
				contentBuf.WriteString(`<div class="feed-item">
					<div class="feed-meta">
						<a href="/c/`)
				contentBuf.WriteString(template.URLQueryEscaper(post.CommunityID))
				contentBuf.WriteString(`" class="feed-community">`)
				contentBuf.WriteString(template.HTMLEscapeString(post.CommunityName))
				contentBuf.WriteString(`</a>
						<span>·</span>
						<span>`)
				contentBuf.WriteString(formatTime(post.CreatedAt))
				contentBuf.WriteString(`</span>
					</div>
					<h3 class="feed-title"><a href="/p/`)
				contentBuf.WriteString(template.URLQueryEscaper(post.ID))
				contentBuf.WriteString(`">`)
				contentBuf.WriteString(template.HTMLEscapeString(post.Title))
				contentBuf.WriteString(`</a></h3>
					<p class="feed-excerpt">`)
				excerpt := post.Content
				if len(excerpt) > 150 {
					excerpt = excerpt[:150] + "..."
				}
				contentBuf.WriteString(template.HTMLEscapeString(excerpt))
				contentBuf.WriteString(`</p>
					<div class="feed-stats">
						<span>👁 `)
				contentBuf.WriteString(fmt.Sprintf("%d", post.ViewCount))
				contentBuf.WriteString(`</span>
						<span>💬 `)
				contentBuf.WriteString(fmt.Sprintf("%d", post.ReplyCount))
				contentBuf.WriteString(`</span>
					</div>
				</div>`)
			}
			contentBuf.WriteString(`</div></div>`)
		}

		// Replies section
		if len(replies) > 0 {
			contentBuf.WriteString(`<div class="content-section">
				<h2 class="section-title">最近的回复</h2>
				<div class="reply-list">`)
			for _, reply := range replies {
				contentBuf.WriteString(`<div class="reply-card">
					<div class="reply-card-meta">
						<span>回复于</span>
						<a href="/p/`)
				contentBuf.WriteString(template.URLQueryEscaper(reply.PostID))
				contentBuf.WriteString(`">`)
				contentBuf.WriteString(template.HTMLEscapeString(reply.PostTitle))
				contentBuf.WriteString(`</a>
						<span>·</span>
						<span>`)
				contentBuf.WriteString(formatTime(reply.CreatedAt))
				contentBuf.WriteString(`</span>
					</div>
					<div class="reply-card-content">`)
				contentBuf.WriteString(template.HTMLEscapeString(reply.Content))
				contentBuf.WriteString(`</div>
				</div>`)
			}
			contentBuf.WriteString(`</div></div>`)
		}

		if len(posts) == 0 && len(replies) == 0 {
			contentBuf.WriteString(`<div class="empty-state">
				<p>该用户还没有发布任何内容</p>
			</div>`)
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = app.page.Execute(w, PageData{
			Title:    user.Name,
			Page:     "user",
			Identity: identity,
			Content:  template.HTML(contentBuf.String()),
		})
	})

	// My bookmarks page
	router.Get("/my/bookmarks", func(w http.ResponseWriter, r *http.Request) {
		identity, _, _ := app.currentIdentity(r)
		if identity == nil {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}

		bookmarks, _ := app.listBookmarks(identity.ID, 50)

		type PageData struct {
			Title    string
			Page     string
			Identity *Identity
			Content  template.HTML
		}

		var contentBuf strings.Builder
		contentBuf.WriteString(`<div class="page-header">
			<h1 class="page-title">我的收藏</h1>
		</div>`)

		if len(bookmarks) > 0 {
			contentBuf.WriteString(`<div class="feed-list">`)
			for _, post := range bookmarks {
				contentBuf.WriteString(`<div class="feed-item">
					<div class="feed-meta">
						<a href="/u/`)
				contentBuf.WriteString(template.URLQueryEscaper(post.AuthorID))
				contentBuf.WriteString(`" class="feed-author">`)
				contentBuf.WriteString(template.HTMLEscapeString(post.AuthorName))
				contentBuf.WriteString(`</a>
						<span>·</span>
						<a href="/c/`)
				contentBuf.WriteString(template.URLQueryEscaper(post.CommunityID))
				contentBuf.WriteString(`" class="feed-community">`)
				contentBuf.WriteString(template.HTMLEscapeString(post.CommunityName))
				contentBuf.WriteString(`</a>
						<span>·</span>
						<span>`)
				contentBuf.WriteString(formatTime(post.CreatedAt))
				contentBuf.WriteString(`</span>
					</div>
					<h3 class="feed-title"><a href="/p/`)
				contentBuf.WriteString(template.URLQueryEscaper(post.ID))
				contentBuf.WriteString(`">`)
				contentBuf.WriteString(template.HTMLEscapeString(post.Title))
				contentBuf.WriteString(`</a></h3>
					<p class="feed-excerpt">`)
				excerpt := post.Content
				if len(excerpt) > 150 {
					excerpt = excerpt[:150] + "..."
				}
				contentBuf.WriteString(template.HTMLEscapeString(excerpt))
				contentBuf.WriteString(`</p>
					<div class="feed-stats">
						<span>👁 `)
				contentBuf.WriteString(fmt.Sprintf("%d", post.ViewCount))
				contentBuf.WriteString(`</span>
						<span>💬 `)
				contentBuf.WriteString(fmt.Sprintf("%d", post.ReplyCount))
				contentBuf.WriteString(`</span>
					</div>
				</div>`)
			}
			contentBuf.WriteString(`</div>`)
		} else {
			contentBuf.WriteString(`<div class="empty-state">
				<p>还没有收藏任何帖子</p>
				<a href="/discover" class="btn">去发现社群</a>
			</div>`)
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = app.page.Execute(w, PageData{
			Title:    "我的收藏",
			Page:     "bookmarks",
			Identity: identity,
			Content:  template.HTML(contentBuf.String()),
		})
	})

	// API: Toggle bookmark
	router.Post("/api/v1/p/{id}/bookmark", func(w http.ResponseWriter, r *http.Request) {
		postID := chi.URLParam(r, "id")
		identity, _, _ := app.currentIdentity(r)

		if identity == nil {
			respond(w, 401, map[string]string{"code": "UNAUTHORIZED"})
			return
		}

		// Check if post exists
		_, err := app.getPost(postID)
		if err == sql.ErrNoRows {
			respond(w, 404, map[string]string{"code": "NOT_FOUND"})
			return
		}
		if err != nil {
			respond(w, 503, map[string]string{"code": "DEPENDENCY_UNAVAILABLE"})
			return
		}

		// Toggle bookmark
		isBookmarked, err := app.isBookmarked(identity.ID, postID)
		if err != nil {
			respond(w, 503, map[string]string{"code": "DEPENDENCY_UNAVAILABLE"})
			return
		}

		if isBookmarked {
			err = app.removeBookmark(identity.ID, postID)
		} else {
			err = app.addBookmark(identity.ID, postID)
		}

		if err != nil {
			respond(w, 503, map[string]string{"code": "DEPENDENCY_UNAVAILABLE"})
			return
		}

		respond(w, 200, map[string]any{"bookmarked": !isBookmarked})
	})
}

type ReplyWithPost struct {
	Reply
	PostTitle string
}

func (app *App) getIdentity(id string) (*UserProfile, error) {
	var u UserProfile
	var createdAt, updatedAt int64

	err := app.db.QueryRow(`
		SELECT id, name, email, created_at, updated_at
		FROM identities
		WHERE id = ?
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
		SELECT p.id, p.community_id, COALESCE(c.name, ''), p.author_id, COALESCE(i.name, ''),
		       p.title, p.content, p.status, p.view_count, p.reply_count, p.bookmark_count,
		       p.created_at, p.updated_at
		FROM posts p
		LEFT JOIN communities c ON p.community_id = c.id
		LEFT JOIN identities i ON p.author_id = i.id
		WHERE p.author_id = ? AND p.status = 'published'
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
		if err := rows.Scan(&p.ID, &p.CommunityID, &p.CommunityName, &p.AuthorID, &p.AuthorName,
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
		SELECT r.id, r.post_id, r.parent_id, r.author_id, COALESCE(i.name, ''),
		       r.content, r.level, r.status, r.created_at, r.updated_at,
		       COALESCE(p.title, '')
		FROM replies r
		LEFT JOIN identities i ON r.author_id = i.id
		LEFT JOIN posts p ON r.post_id = p.id
		WHERE r.author_id = ? AND r.status = 'published'
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
		SELECT p.id, p.community_id, COALESCE(c.name, ''), p.author_id, COALESCE(i.name, ''),
		       p.title, p.content, p.status, p.view_count, p.reply_count, p.bookmark_count,
		       p.created_at, p.updated_at
		FROM posts p
		INNER JOIN post_bookmarks b ON p.id = b.post_id
		LEFT JOIN communities c ON p.community_id = c.id
		LEFT JOIN identities i ON p.author_id = i.id
		WHERE b.identity_id = ? AND p.status = 'published'
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
		if err := rows.Scan(&p.ID, &p.CommunityID, &p.CommunityName, &p.AuthorID, &p.AuthorName,
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
