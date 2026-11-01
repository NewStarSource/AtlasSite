package atlas

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

type communityTool struct {
	ID, Community, Kind, Title, Description, Content, Status, Editor, Summary string
	Version, Order, Pinned                                                    int
	Updated                                                                   int64
	Refs                                                                      []string
}

type toolQuery interface {
	managementQuery
	Query(string, ...any) (*sql.Rows, error)
}

func loadCommunityTool(q toolQuery, community, id string) (communityTool, error) {
	t := communityTool{ID: id, Community: community}
	err := q.QueryRow(`SELECT c.type,c.title,c.description,c.content,c.status,c.display_order,c.updated_at,COALESCE(d.version,1),COALESCE(d.pinned,0),COALESCE(d.editor_id,'') FROM collections c LEFT JOIN collection_details d ON d.collection_id=c.id WHERE c.id=? AND c.community_id=?`, id, community).Scan(&t.Kind, &t.Title, &t.Description, &t.Content, &t.Status, &t.Order, &t.Updated, &t.Version, &t.Pinned, &t.Editor)
	if err != nil {
		return t, err
	}
	rows, err := q.Query(`SELECT cr.post_id FROM collection_refs cr LEFT JOIN collection_positions cp ON cp.collection_id=cr.collection_id AND cp.post_id=cr.post_id JOIN posts p ON p.id=cr.post_id WHERE cr.collection_id=? ORDER BY COALESCE(cp.position,2147483647),p.created_at,p.id`, id)
	if err != nil {
		return t, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return t, err
		}
		t.Refs = append(t.Refs, id)
	}
	return t, rows.Err()
}

func toolLabel(kind string) string {
	return map[string]string{"announcement": "公告", "guide": "新人指引", "discussion": "讨论合集"}[kind]
}
func toolState(state string) string {
	return map[string]string{"draft": "草稿", "published": "已发布", "archived": "已撤下"}[state]
}
func toolBase(community string) string { return "/admin/communities/" + pathID(community) + "/tools" }

func denyToolAccess(w http.ResponseWriter) {
	respond(w, 403, map[string]string{"code": "TOOL_SCOPE_REQUIRED", "message": "该社群或工具不在当前有效授权范围内，请查看协作状态或联系代表。"})
}

func (app *App) communityToolRoutes(r *chi.Mux) {
	r.Get("/admin/communities/{id}/tools", app.communityToolsPage)
	r.Get("/admin/communities/{id}/tools/new", app.communityToolEditor)
	r.Get("/admin/communities/{id}/tools/{tool}", app.communityToolEditor)
	r.Get("/admin/communities/{id}/tools/{tool}/history", app.communityToolHistory)
	r.Post("/admin/communities/{id}/tools", app.saveCommunityTool)
	r.Post("/admin/communities/{id}/tools/{tool}", app.saveCommunityTool)
}

func (app *App) toolIdentity(w http.ResponseWriter, r *http.Request, write bool) (*Identity, string) {
	i := app.requireIdentity(w, r, write)
	if i == nil {
		return nil, ""
	}
	role, err := managementAccess(app.db, i.ID)
	if err != nil {
		fail(w, err)
		return nil, ""
	}
	var status string
	if !canMaintainAnyTool(app.db, i.ID, chi.URLParam(r, "id")) || app.db.QueryRow("SELECT status FROM communities WHERE id=?", chi.URLParam(r, "id")).Scan(&status) != nil || (status != "active" && status != "uncategorized") {
		denyToolAccess(w)
		return nil, ""
	}
	if !canManageCommunity(app.db, i.ID, chi.URLParam(r, "id")) {
		role = "organizer"
	}
	if write && !app.writeAllowed(i.ID) {
		respond(w, 503, map[string]string{"code": "READ_ONLY"})
		return nil, ""
	}
	return i, role
}

func (app *App) toolRefs(raw string) ([]string, error) {
	refs := strings.Fields(raw)
	if len(refs) > 50 {
		return nil, errInput
	}
	seen := map[string]bool{}
	for n, value := range refs {
		if strings.HasPrefix(value, "/p/") {
			value = strings.TrimPrefix(value, "/p/")
		} else if strings.Contains(value, "://") {
			u, err := url.Parse(value)
			if err != nil || u.Scheme+"://"+u.Host != app.config.Origin || u.RawQuery != "" || u.Fragment != "" || !strings.HasPrefix(u.Path, "/p/") {
				return nil, errInput
			}
			value = strings.TrimPrefix(u.Path, "/p/")
		}
		if value == "" || !checkText(value, 100) || strings.ContainsAny(value, "/?#") || seen[value] {
			return nil, errInput
		}
		seen[value] = true
		refs[n] = value
	}
	return refs, nil
}

func (app *App) saveCommunityTool(w http.ResponseWriter, r *http.Request) {
	i, role := app.toolIdentity(w, r, true)
	if i == nil {
		return
	}
	if r.ParseForm() != nil {
		fail(w, errInput)
		return
	}
	t := communityTool{ID: chi.URLParam(r, "tool"), Community: chi.URLParam(r, "id"), Kind: r.FormValue("kind"), Title: strings.TrimSpace(r.FormValue("title")), Description: strings.TrimSpace(r.FormValue("description")), Content: strings.TrimSpace(r.FormValue("content")), Status: r.FormValue("status"), Summary: strings.TrimSpace(r.FormValue("summary")), Editor: i.ID}
	if toolLabel(t.Kind) != "" && !canMaintainTool(app.db, i.ID, t.Community, t.Kind) {
		denyToolAccess(w)
		return
	}
	creating := t.ID == ""
	if creating {
		t.ID = r.FormValue("tool_id")
	}
	showError := func(err error) {
		if (err == errInput || err == errConflict) && toolLabel(t.Kind) != "" && !strings.Contains(r.Header.Get("Accept"), "application/json") {
			status, message := 400, "请检查必填内容、排序和原帖链接；只可新增本社群当前可见的原帖。"
			if err == errConflict {
				status, message = 409, "已有较新版本，当前输入尚未保存。请先保留输入，再载入最新版本。"
			}
			app.renderCommunityToolEditor(w, r, i, role, t, !creating, status, message)
		} else {
			fail(w, err)
		}
	}
	var err error
	t.Version, err = strconv.Atoi(r.FormValue("version"))
	if err != nil || t.Version < 0 || creating != (t.Version == 0) || t.ID == "" || !checkText(t.ID, 100) || strings.ContainsAny(t.ID, "/?#") {
		showError(errInput)
		return
	}
	t.Order, err = strconv.Atoi(r.FormValue("order"))
	if err != nil || t.Order < 0 || t.Order > 9999 || toolLabel(t.Kind) == "" || toolState(t.Status) == "" || t.Title == "" || t.Summary == "" || !checkText(t.Title, 200) || !checkText(t.Description, 2000) || !checkText(t.Content, 30000) || !checkText(t.Summary, 300) || (t.Kind != "discussion" && t.Content == "") {
		showError(errInput)
		return
	}
	if r.FormValue("pinned") == "1" {
		t.Pinned = 1
	}
	if t.Kind != "announcement" && t.Pinned != 0 {
		showError(errInput)
		return
	}
	t.Refs, err = app.toolRefs(r.FormValue("references"))
	if err != nil || (t.Kind != "discussion" && len(t.Refs) != 0) || (t.Kind == "discussion" && t.Content != "") {
		showError(errInput)
		return
	}
	input := t
	if err = app.commitCommunityTool(r, i, &t, creating); err != nil {
		t = input
		showError(err)
		return
	}
	http.Redirect(w, r, toolBase(t.Community)+"/"+pathID(t.ID)+"?saved=1", 303)
}

func (app *App) commitCommunityTool(r *http.Request, i *Identity, t *communityTool, creating bool) error {
	app.opsMu.Lock()
	defer app.opsMu.Unlock()
	unlock, err := app.journalLock()
	if err != nil {
		return err
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
	var n int
	// Recheck authority and the concrete browser session inside the write transaction.
	if !canMaintainTool(tx, i.ID, t.Community, t.Kind) || tx.QueryRow(`SELECT count(*) FROM sessions s JOIN users u ON u.id=s.user_id JOIN communities c ON c.id=? JOIN runtime_state rs ON rs.id=1 WHERE s.token_hash=? AND s.user_id=? AND s.revoked_at IS NULL AND s.expires_at>? AND s.reauthenticated_at>=? AND u.status='active' AND c.status IN ('active','uncategorized') AND rs.mode='normal'`, t.Community, digest(sessionCookie(r)), i.ID, time.Now().Unix(), time.Now().Add(-5*time.Minute).Unix()).Scan(&n) != nil || n != 1 {
		return errForbidden
	}
	old, err := loadCommunityTool(tx, t.Community, t.ID)
	if creating {
		if err != sql.ErrNoRows {
			if err == nil {
				return errConflict
			}
			return err
		}
	} else if err != nil {
		return err
	} else if old.Version != t.Version || old.Kind != t.Kind {
		return errConflict
	}
	for _, id := range t.Refs {
		if err = tx.QueryRow("SELECT count(*) FROM posts p WHERE p.id=? AND p.community_id=? AND "+visiblePostSQL, id, t.Community).Scan(&n); err != nil {
			return err
		}
		if n != 1 {
			existing := false
			for _, prior := range old.Refs {
				if prior == id {
					existing = true
					break
				}
			}
			if !existing {
				return errInput
			}
		}
	}
	t.Updated = time.Now().Unix()
	t.Version++
	if creating {
		_, err = tx.Exec("INSERT INTO collections(id,community_id,type,title,slug,description,content,display_order,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)", t.ID, t.Community, t.Kind, t.Title, t.ID, t.Description, t.Content, t.Order, t.Status, t.Updated, t.Updated)
	} else {
		_, err = tx.Exec("UPDATE collections SET title=?,description=?,content=?,display_order=?,status=?,updated_at=? WHERE id=? AND community_id=?", t.Title, t.Description, t.Content, t.Order, t.Status, t.Updated, t.ID, t.Community)
	}
	if err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO collection_details VALUES(?,?,?,?) ON CONFLICT(collection_id) DO UPDATE SET version=excluded.version,pinned=excluded.pinned,editor_id=excluded.editor_id", t.ID, t.Version, t.Pinned, i.ID); err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM collection_positions WHERE collection_id=?", t.ID); err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM collection_refs WHERE collection_id=?", t.ID); err != nil {
		return err
	}
	for position, id := range t.Refs {
		if _, err = tx.Exec("INSERT INTO collection_refs VALUES(?,?)", t.ID, id); err != nil {
			return err
		}
		if _, err = tx.Exec("INSERT INTO collection_positions VALUES(?,?,?)", t.ID, id, position); err != nil {
			return err
		}
	}
	refs, _ := json.Marshal(t.Refs)
	if _, err = tx.Exec("INSERT INTO collection_versions VALUES(?,?,?,?,?,?,?,?,?,?,?,?)", t.ID, t.Version, t.Title, t.Description, t.Content, t.Status, t.Order, t.Pinned, i.ID, t.Updated, t.Summary, string(refs)); err != nil {
		return err
	}
	// The audit log links to restricted versions; it never copies original post text.
	if err = managementEvent(tx, i.ID, "collection-save", t.Community, map[string]any{"id": t.ID, "version": old.Version}, map[string]any{"id": t.ID, "version": t.Version, "status": t.Status}); err != nil {
		return err
	}
	op := operation{EventID: randomID(), ObjectID: t.ID, OwnerID: i.ID, Action: "collection-changed", VersionNumber: t.Version, CreatedAt: t.Updated}
	if _, err = tx.Exec("INSERT INTO operation_events(event_id,object_id,action,created_at) VALUES(?,?,?,?)", op.EventID, op.ObjectID, op.Action, op.CreatedAt); err != nil {
		return err
	}
	if err = app.appendOperationRecords(op, records); err == nil {
		err = tx.Commit()
	}
	if err != nil {
		tx.Rollback()
		app.db.Exec("UPDATE runtime_state SET mode='isolated' WHERE id=1")
	}
	return err
}

func sessionCookie(r *http.Request) string {
	c, err := r.Cookie("star_atlas_session")
	if err != nil {
		return ""
	}
	return c.Value
}
