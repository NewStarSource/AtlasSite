package atlas

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/image/draw"
)

func (app *App) mediaRoutes(router *chi.Mux) {
	router.Post("/api/v1/media", app.uploadMedia)
	router.Post("/api/v1/media/{id}/delete", app.deleteMedia)
	router.Get("/media/{id}", app.serveMedia)
	router.Get("/media/{id}/thumbnail", app.serveMedia)
}

func (app *App) deleteMedia(w http.ResponseWriter, r *http.Request) {
	i := app.contentIdentity(w, r)
	if i == nil {
		return
	}
	id := chi.URLParam(r, "id")
	var owner string
	if err := app.db.QueryRow("SELECT owner_id FROM media WHERE id=?", id).Scan(&owner); err != nil {
		fail(w, err)
		return
	}
	if owner != i.ID {
		fail(w, errForbidden)
		return
	}
	if err := app.applyOperation(operation{EventID: randomID(), ObjectID: id, OwnerID: i.ID, Action: "delete-media", CreatedAt: time.Now().Unix()}); err != nil {
		fail(w, err)
		return
	}
	http.Redirect(w, r, "/my/drafts", 303)
}

var imageDecodeSlots = make(chan struct{}, 2)

func (app *App) uploadMedia(w http.ResponseWriter, r *http.Request) {
	i := app.contentIdentity(w, r)
	if i == nil || !app.rateLimit(w, r, i.ID, 30) {
		return
	}
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		fail(w, errInput)
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, header, err := r.FormFile("image")
	if err != nil {
		fail(w, errInput)
		return
	}
	defer file.Close()
	license := r.FormValue("license")
	if !validLicense(license) {
		fail(w, errInput)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(file, (8<<20)+1))
	if err != nil || len(raw) == 0 || len(raw) > 8<<20 {
		fail(w, errInput)
		return
	}
	detected := http.DetectContentType(raw)
	ext := strings.ToLower(filepath.Ext(header.Filename))
	if !((detected == "image/png" && ext == ".png") || (detected == "image/jpeg" && (ext == ".jpg" || ext == ".jpeg"))) {
		fail(w, errInput)
		return
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || (format != "png" && format != "jpeg") || config.Width < 1 || config.Height < 1 || config.Width > 10000 || config.Height > 10000 || int64(config.Width)*int64(config.Height) > 16000000 {
		fail(w, errInput)
		return
	}
	select {
	case imageDecodeSlots <- struct{}{}:
		defer func() { <-imageDecodeSlots }()
	default:
		respond(w, 503, map[string]string{"code": "IMAGE_BUSY"})
		return
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		fail(w, errInput)
		return
	}
	var clean bytes.Buffer
	mime := "image/png"
	if format == "jpeg" {
		mime = "image/jpeg"
		err = jpeg.Encode(&clean, img, &jpeg.Options{Quality: 85})
	} else {
		err = png.Encode(&clean, img)
	}
	if err != nil || clean.Len() > 8<<20 {
		fail(w, errInput)
		return
	}
	width, height := config.Width, config.Height
	if width > 320 || height > 320 {
		if width >= height {
			height = max(1, height*320/width)
			width = 320
		} else {
			width = max(1, width*320/height)
			height = 320
		}
	}
	thumb := image.NewNRGBA(image.Rect(0, 0, width, height))
	draw.CatmullRom.Scale(thumb, thumb.Bounds(), img, img.Bounds(), draw.Over, nil)
	var small bytes.Buffer
	if err = png.Encode(&small, thumb); err != nil {
		fail(w, err)
		return
	}
	key := make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		fail(w, err)
		return
	}
	id := randomID()
	body, err := encrypt(key, clean.Bytes(), id+":image")
	if err != nil {
		fail(w, err)
		return
	}
	thumbnail, err := encrypt(key, small.Bytes(), id+":thumbnail")
	if err != nil {
		fail(w, err)
		return
	}
	if _, err = app.vault.Exec("INSERT INTO media_keys VALUES(?,?)", id, key); err != nil {
		fail(w, err)
		return
	}
	_, err = app.db.Exec("INSERT INTO media(id,owner_id,post_id,mime,width,height,license,body,thumbnail,created_at) VALUES(?,?,NULL,?,?,?,?,?,?,?)", id, i.ID, mime, config.Width, config.Height, license, body, thumbnail, time.Now().Unix())
	if err != nil {
		app.vault.Exec("DELETE FROM media_keys WHERE id=?", id)
		fail(w, err)
		return
	}
	if r.Header.Get("Accept") == "application/json" {
		respond(w, 201, map[string]any{"id": id, "markdown": "![自有图片](/media/" + id + ")", "width": config.Width, "height": config.Height})
		return
	}
	app.renderPage(w, r, "图片已上传", "editor", `<h1>图片已上传</h1><p>把下面文字复制到正文中。发布前图片仅本人可见。</p><pre>`+esc("![自有图片](/media/"+id+")")+`</pre>`+formStart(r, "/api/v1/media/"+pathID(id)+"/delete")+`<button>撤回这张图片</button></form><p><a href="/new">返回编辑器</a></p>`)
}
func (app *App) serveMedia(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var owner string
	var post sql.NullString
	var created int64
	if app.db.QueryRow("SELECT owner_id,post_id,created_at FROM media WHERE id=? AND removed_at IS NULL", id).Scan(&owner, &post, &created) != nil {
		fail(w, sql.ErrNoRows)
		return
	}
	i, _, _ := app.currentIdentity(r)
	viewer := ""
	if i != nil {
		viewer = i.ID
	}
	if !post.Valid {
		if viewer != owner || created < time.Now().Add(-24*time.Hour).Unix() {
			fail(w, sql.ErrNoRows)
			return
		}
	} else {
		if !app.postVisible(post.String, viewer) {
			fail(w, sql.ErrNoRows)
			return
		}
		p, err := app.getPost(post.String)
		if err != nil {
			fail(w, err)
			return
		}
		ids, err := mediaIDs(p.Content)
		present := false
		for _, value := range ids {
			present = present || value == id
		}
		if err != nil || !present {
			fail(w, sql.ErrNoRows)
			return
		}
	}
	var key, encrypted []byte
	var mime string
	if app.vault.QueryRow("SELECT key FROM media_keys WHERE id=?", id).Scan(&key) != nil {
		fail(w, sql.ErrNoRows)
		return
	}
	column, aad := "body", id+":image"
	if strings.HasSuffix(r.URL.Path, "/thumbnail") {
		column, aad = "thumbnail", id+":thumbnail"
	}
	if app.db.QueryRow("SELECT "+column+",mime FROM media WHERE id=?", id).Scan(&encrypted, &mime) != nil {
		fail(w, sql.ErrNoRows)
		return
	}
	raw, err := decrypt(key, encrypted, aad)
	if err != nil {
		fail(w, sql.ErrNoRows)
		return
	}
	// Never honor a stale ETag or If-Modified-Since for revoked media.
	if strings.HasSuffix(r.URL.Path, "/thumbnail") {
		mime = "image/png"
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "no-store")
	w.Write(raw)
}
