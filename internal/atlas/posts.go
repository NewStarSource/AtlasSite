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

type Post struct {
	ID            string
	CommunityID   string
	CommunityName string
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

func (app *App) postsRoutes(router *chi.Mux) {
	// Post detail page
	router.Get("/p/{id}", func(w http.ResponseWriter, r *http.Request) {
		postID := chi.URLParam(r, "id")
		post, err := app.getPost(postID)
		if err == sql.ErrNoRows {
			w.WriteHeader(404)
			w.Write([]byte("帖子不存在"))
			return
		}
		if err != nil {
			w.WriteHeader(503)
			w.Write([]byte("服务暂时不可用"))
			return
		}
		if post.Status != "published" {
			w.WriteHeader(404)
			w.Write([]byte("帖子不可用"))
			return
		}

		// Increment view count
		_, _ = app.db.Exec("UPDATE posts SET view_count = view_count + 1 WHERE id = ?", postID)

		replies, _ := app.listReplies(postID)
		replyTree := buildReplyTree(replies)

		identity, _, _ := app.currentIdentity(r)

		type PageData struct {
			Title    string
			Page     string
			Identity *Identity
			Content  template.HTML
			Post     *Post
		}

		var contentBuf strings.Builder
		contentBuf.WriteString(`<nav class="breadcrumb">
			<a href="/">首页</a>
			<span class="breadcrumb-sep">/</span>
			<a href="/c/`)
		contentBuf.WriteString(template.URLQueryEscaper(post.CommunityID))
		contentBuf.WriteString(`">`)
		contentBuf.WriteString(template.HTMLEscapeString(post.CommunityName))
		contentBuf.WriteString(`</a>
			<span class="breadcrumb-sep">/</span>
			<span>帖子</span>
		</nav>
		<article class="post-article">
			<div class="post-meta">
				<a href="/u/`)
		contentBuf.WriteString(template.URLQueryEscaper(post.AuthorID))
		contentBuf.WriteString(`" class="post-author">`)
		contentBuf.WriteString(template.HTMLEscapeString(post.AuthorName))
		contentBuf.WriteString(`</a>
				<span>·</span>
				<span>`)
		contentBuf.WriteString(formatTime(post.CreatedAt))
		contentBuf.WriteString(`</span>
				<span>·</span>
				<a href="/c/`)
		contentBuf.WriteString(template.URLQueryEscaper(post.CommunityID))
		contentBuf.WriteString(`" class="post-community">`)
		contentBuf.WriteString(template.HTMLEscapeString(post.CommunityName))
		contentBuf.WriteString(`</a>
			</div>
			<h1 class="post-title">`)
		contentBuf.WriteString(template.HTMLEscapeString(post.Title))
		contentBuf.WriteString(`</h1>
			<div class="post-content">`)
		contentBuf.WriteString(template.HTMLEscapeString(post.Content))
		contentBuf.WriteString(`</div>
			<div class="post-actions">
				<button class="action-btn">👍 赞</button>
				<button class="action-btn">💬 回复</button>
				<button class="action-btn">🔖 收藏</button>
				<button class="action-btn">⋯ 更多</button>
			</div>
		</article>`)

		// Reply composer
		if identity != nil {
			contentBuf.WriteString(`<div class="reply-composer">
				<h2>发表回复</h2>
				<form method="post" action="/api/v1/p/`)
			contentBuf.WriteString(template.URLQueryEscaper(postID))
			contentBuf.WriteString(`/replies">
					<textarea name="content" class="reply-textarea" placeholder="写下你的想法..." required></textarea>
					<div class="reply-actions">
						<button type="submit" class="btn">发布回复</button>
					</div>
				</form>
			</div>`)
		}

		// Replies section
		if len(replies) > 0 {
			contentBuf.WriteString(`<div class="replies-section">
				<div class="replies-header">
					<h2 class="replies-count">`)
			contentBuf.WriteString(fmt.Sprintf("%d", len(replies)))
			contentBuf.WriteString(` 条回复</h2>
				</div>
				<div class="reply-tree">`)
			renderReplyTree(&contentBuf, replyTree)
			contentBuf.WriteString(`</div>
			</div>`)
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = app.page.Execute(w, PageData{
			Title:    post.Title,
			Page:     "post",
			Identity: identity,
			Content:  template.HTML(contentBuf.String()),
			Post:     post,
		})
	})

	// New post page
	router.Get("/c/{slug}/new", func(w http.ResponseWriter, r *http.Request) {
		slug := chi.URLParam(r, "slug")
		identity, _, _ := app.currentIdentity(r)

		if identity == nil {
			w.WriteHeader(401)
			w.Write([]byte("请先登录"))
			return
		}

		community, err := app.getCommunityBySlug(slug)
		if err == sql.ErrNoRows {
			w.WriteHeader(404)
			w.Write([]byte("社群不存在"))
			return
		}
		if err != nil {
			w.WriteHeader(503)
			w.Write([]byte("服务暂时不可用"))
			return
		}

		type PageData struct {
			Title     string
			Page      string
			Identity  *Identity
			Content   template.HTML
			Community *Community
		}

		var contentBuf strings.Builder
		contentBuf.WriteString(`<nav class="breadcrumb">
			<a href="/">首页</a>
			<span class="breadcrumb-sep">/</span>
			<a href="/c/`)
		contentBuf.WriteString(template.URLQueryEscaper(slug))
		contentBuf.WriteString(`">`)
		contentBuf.WriteString(template.HTMLEscapeString(community.Name))
		contentBuf.WriteString(`</a>
			<span class="breadcrumb-sep">/</span>
			<span>发布新帖</span>
		</nav>
		<div class="post-composer">
			<h1 class="composer-title">发布新帖</h1>
			<form method="post" action="/api/v1/c/`)
		contentBuf.WriteString(template.URLQueryEscaper(slug))
		contentBuf.WriteString(`/posts">
				<div class="form-group">
					<label for="title" class="form-label">标题</label>
					<input type="text" id="title" name="title" class="form-input" placeholder="用一句话描述你想讨论的内容" required maxlength="200">
				</div>
				<div class="form-group">
					<label for="content" class="form-label">正文</label>
					<textarea id="content" name="content" class="form-textarea" placeholder="详细描述你的想法..." required></textarea>
				</div>
				<div class="form-actions">
					<button type="button" class="btn btn-secondary" onclick="history.back()">取消</button>
					<button type="submit" class="btn">发布</button>
				</div>
			</form>
		</div>`)

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = app.page.Execute(w, PageData{
			Title:     "发布新帖",
			Page:      "new-post",
			Identity:  identity,
			Content:   template.HTML(contentBuf.String()),
			Community: community,
		})
	})

	// API: Create post
	router.Post("/api/v1/c/{slug}/posts", func(w http.ResponseWriter, r *http.Request) {
		slug := chi.URLParam(r, "slug")
		identity, _, _ := app.currentIdentity(r)

		if identity == nil {
			respond(w, 401, map[string]string{"code": "UNAUTHORIZED"})
			return
		}

		community, err := app.getCommunityBySlug(slug)
		if err == sql.ErrNoRows {
			respond(w, 404, map[string]string{"code": "NOT_FOUND", "message": "社群不存在"})
			return
		}
		if err != nil {
			respond(w, 503, map[string]string{"code": "DEPENDENCY_UNAVAILABLE"})
			return
		}

		if err := r.ParseForm(); err != nil {
			respond(w, 400, map[string]string{"code": "BAD_REQUEST"})
			return
		}

		title := strings.TrimSpace(r.FormValue("title"))
		content := strings.TrimSpace(r.FormValue("content"))

		if title == "" || content == "" {
			respond(w, 400, map[string]string{"code": "BAD_REQUEST", "message": "标题和内容不能为空"})
			return
		}

		post, err := app.createPost(community.ID, identity.ID, title, content)
		if err != nil {
			respond(w, 503, map[string]string{"code": "DEPENDENCY_UNAVAILABLE"})
			return
		}

		http.Redirect(w, r, "/p/"+post.ID, http.StatusSeeOther)
	})

	// API: Create reply
	router.Post("/api/v1/p/{id}/replies", func(w http.ResponseWriter, r *http.Request) {
		postID := chi.URLParam(r, "id")
		identity, _, _ := app.currentIdentity(r)

		if identity == nil {
			respond(w, 401, map[string]string{"code": "UNAUTHORIZED"})
			return
		}

		if err := r.ParseForm(); err != nil {
			respond(w, 400, map[string]string{"code": "BAD_REQUEST"})
			return
		}

		content := strings.TrimSpace(r.FormValue("content"))
		parentID := strings.TrimSpace(r.FormValue("parent_id"))

		if content == "" {
			respond(w, 400, map[string]string{"code": "BAD_REQUEST", "message": "回复内容不能为空"})
			return
		}

		_, err := app.createReply(postID, identity.ID, content, parentID)
		if err != nil {
			respond(w, 503, map[string]string{"code": "DEPENDENCY_UNAVAILABLE"})
			return
		}

		http.Redirect(w, r, "/p/"+postID, http.StatusSeeOther)
	})
}

func (app *App) createPost(communityID, authorID, title, content string) (*Post, error) {
	id := randomID()
	now := time.Now().Unix()

	_, err := app.db.Exec(`
		INSERT INTO posts (id, community_id, author_id, title, content, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 'published', ?, ?)
	`, id, communityID, authorID, title, content, now, now)

	if err != nil {
		return nil, err
	}

	return app.getPost(id)
}

func (app *App) getPost(id string) (*Post, error) {
	var p Post
	var createdAt, updatedAt int64

	err := app.db.QueryRow(`
		SELECT p.id, p.community_id, COALESCE(c.name, ''), p.author_id, COALESCE(i.name, ''),
		       p.title, p.content, p.status, p.view_count, p.reply_count, p.bookmark_count,
		       p.created_at, p.updated_at
		FROM posts p
		LEFT JOIN communities c ON p.community_id = c.id
		LEFT JOIN identities i ON p.author_id = i.id
		WHERE p.id = ?
	`, id).Scan(&p.ID, &p.CommunityID, &p.CommunityName, &p.AuthorID, &p.AuthorName,
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
		SELECT p.id, p.community_id, COALESCE(c.name, ''), p.author_id, COALESCE(i.name, ''),
		       p.title, p.content, p.status, p.view_count, p.reply_count, p.bookmark_count,
		       p.created_at, p.updated_at
		FROM posts p
		LEFT JOIN communities c ON p.community_id = c.id
		LEFT JOIN identities i ON p.author_id = i.id
		WHERE p.community_id = ? AND p.status = 'published'
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

func (app *App) listRecentPosts(limit int) ([]Post, error) {
	query := `
		SELECT p.id, p.community_id, COALESCE(c.name, ''), p.author_id, COALESCE(i.name, ''),
		       p.title, p.content, p.status, p.view_count, p.reply_count, p.bookmark_count,
		       p.created_at, p.updated_at
		FROM posts p
		LEFT JOIN communities c ON p.community_id = c.id
		LEFT JOIN identities i ON p.author_id = i.id
		WHERE p.status = 'published'
		ORDER BY p.created_at DESC
		LIMIT ?
	`

	rows, err := app.db.Query(query, limit)
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

func (app *App) createReply(postID, authorID, content, parentID string) (*Reply, error) {
	id := randomID()
	now := time.Now().Unix()
	level := 0

	// Calculate level based on parent
	if parentID != "" {
		var parentLevel int
		err := app.db.QueryRow("SELECT level FROM replies WHERE id = ?", parentID).Scan(&parentLevel)
		if err != nil {
			return nil, err
		}
		level = parentLevel + 1
	}

	var err error
	if parentID == "" {
		_, err = app.db.Exec(`
			INSERT INTO replies (id, post_id, parent_id, author_id, content, level, status, created_at, updated_at)
			VALUES (?, ?, NULL, ?, ?, ?, 'published', ?, ?)
		`, id, postID, authorID, content, level, now, now)
	} else {
		_, err = app.db.Exec(`
			INSERT INTO replies (id, post_id, parent_id, author_id, content, level, status, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, 'published', ?, ?)
		`, id, postID, parentID, authorID, content, level, now, now)
	}

	if err != nil {
		return nil, err
	}

	// Update post reply count
	_, _ = app.db.Exec("UPDATE posts SET reply_count = reply_count + 1 WHERE id = ?", postID)

	return app.getReply(id)
}

func (app *App) getReply(id string) (*Reply, error) {
	var r Reply
	var parentID sql.NullString
	var createdAt, updatedAt int64

	err := app.db.QueryRow(`
		SELECT r.id, r.post_id, r.parent_id, r.author_id, COALESCE(i.name, ''),
		       r.content, r.level, r.status, r.created_at, r.updated_at
		FROM replies r
		LEFT JOIN identities i ON r.author_id = i.id
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
		SELECT r.id, r.post_id, r.parent_id, r.author_id, COALESCE(i.name, ''),
		       r.content, r.level, r.status, r.created_at, r.updated_at
		FROM replies r
		LEFT JOIN identities i ON r.author_id = i.id
		WHERE r.post_id = ? AND r.status = 'published'
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
