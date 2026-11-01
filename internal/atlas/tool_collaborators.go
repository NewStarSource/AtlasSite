package atlas

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

const liveToolGrantSQL = ` FROM tool_collaborators g JOIN tool_eligibility e ON e.community_id=g.community_id AND e.user_id=g.user_id JOIN community_representatives cr ON cr.community_id=g.community_id JOIN communities c ON c.id=g.community_id JOIN users u ON u.id=g.user_id JOIN users ru ON ru.id=cr.user_id WHERE e.eligible=1 AND e.version=g.eligibility_version AND cr.user_id=g.representative_id AND cr.version=g.representative_version AND cr.expires_at>unixepoch() AND g.expires_at>unixepoch() AND u.status='active' AND ru.status='active' AND c.status IN ('active','uncategorized')`

func canMaintainTool(q managementQuery, user, community, kind string) bool {
	if toolLabel(kind) == "" {
		return false
	}
	if canManageCommunity(q, user, community) {
		return true
	}
	var n int
	return q.QueryRow("SELECT count(*)"+liveToolGrantSQL+" AND g.user_id=? AND g.community_id=? AND g.kind=? AND g.state='accepted'", user, community, kind).Scan(&n) == nil && n == 1
}

func canMaintainAnyTool(q managementQuery, user, community string) bool {
	for _, kind := range []string{"announcement", "guide", "discussion"} {
		if canMaintainTool(q, user, community, kind) {
			return true
		}
	}
	return false
}

func collaborationBase(community string) string {
	return "/admin/communities/" + pathID(community) + "/collaborators"
}

func (app *App) collaboratorRoutes(r *chi.Mux) {
	r.Get("/admin/communities/{id}/collaborators", app.collaboratorsPage)
	r.Post("/admin/communities/{id}/collaborators/eligibility", app.saveToolEligibility)
	r.Post("/admin/communities/{id}/collaborators/invite", app.inviteToolCollaborator)
	r.Post("/admin/communities/{id}/collaborators/{grant}/revoke", app.revokeToolCollaborator)
	r.Get("/my/community-tools", app.myCollaborationsPage)
	for _, action := range []string{"accept", "decline", "leave"} {
		r.Post("/my/community-tools/{grant}/"+action, app.answerCollaboration(action))
	}
}

// Verification registers an existing qualification, never creates formal membership.
func (app *App) saveToolEligibility(w http.ResponseWriter, r *http.Request) {
	i, role := app.managementIdentity(w, r, true)
	if i == nil {
		return
	}
	if role != "admin" && role != "member" {
		fail(w, errForbidden)
		return
	}
	if r.ParseForm() != nil {
		fail(w, errInput)
		return
	}
	community, user := chi.URLParam(r, "id"), r.FormValue("user_id")
	version, err := strconv.Atoi(r.FormValue("version"))
	eligible := integer(r.FormValue("eligible"))
	standard := strings.TrimSpace(r.FormValue("standard_url"))
	reason := strings.TrimSpace(r.FormValue("reason"))
	if err != nil || version < 0 || (r.FormValue("eligible") != "0" && r.FormValue("eligible") != "1") || !checkText(standard, 1000) || !safeSource(standard) || reason == "" || !checkText(reason, 300) || r.FormValue("confirm") != "verified" {
		fail(w, errInput)
		return
	}
	tx, err := app.collaborationTransaction(r, i, true, true)
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback()
	actual, err := managementAccess(tx, i.ID)
	if err != nil || (actual != "admin" && actual != "member") {
		fail(w, errForbidden)
		return
	}
	var n int
	if tx.QueryRow("SELECT count(*) FROM communities WHERE id=? AND status IN ('active','uncategorized')", community).Scan(&n) != nil || n != 1 || (eligible == 1 && !activeManagementUser(tx, user)) {
		fail(w, errInput)
		return
	}
	if tx.QueryRow("SELECT count(*) FROM users WHERE id=?", user).Scan(&n) != nil || n != 1 {
		fail(w, errInput)
		return
	}
	var old int
	err = tx.QueryRow("SELECT version FROM tool_eligibility WHERE community_id=? AND user_id=?", community, user).Scan(&old)
	if err != nil && err != sql.ErrNoRows {
		fail(w, err)
		return
	}
	if old != version {
		fail(w, errConflict)
		return
	}
	_, err = tx.Exec("INSERT INTO tool_eligibility VALUES(?,?,?,?,?,?,?) ON CONFLICT(community_id,user_id) DO UPDATE SET eligible=excluded.eligible,standard_url=excluded.standard_url,verified_by=excluded.verified_by,version=excluded.version,updated_at=excluded.updated_at", community, user, eligible, standard, i.ID, version+1, time.Now().Unix())
	if err == nil {
		err = managementEvent(tx, i.ID, "tool-eligibility", community, map[string]any{"user": user, "version": old}, map[string]any{"user": user, "version": version + 1, "eligible": eligible, "reason": reason, "standard_url": standard})
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		fail(w, err)
		return
	}
	http.Redirect(w, r, collaborationBase(community)+"?saved=1", 303)
}

func (app *App) collaborationTransaction(r *http.Request, i *Identity, recent, normal bool) (*sql.Tx, error) {
	tx, err := app.db.Begin()
	if err != nil {
		return nil, err
	}
	query := `SELECT count(*) FROM sessions s JOIN users u ON u.id=s.user_id JOIN runtime_state rs ON rs.id=1 WHERE s.token_hash=? AND s.user_id=? AND s.revoked_at IS NULL AND s.expires_at>? AND u.status='active'`
	args := []any{digest(sessionCookie(r)), i.ID, time.Now().Unix()}
	if recent {
		query += " AND s.reauthenticated_at>=?"
		args = append(args, time.Now().Add(-5*time.Minute).Unix())
	}
	if normal {
		query += " AND rs.mode='normal'"
	}
	var n int
	if err = tx.QueryRow(query, args...).Scan(&n); err != nil || n != 1 {
		tx.Rollback()
		if err == nil {
			err = errForbidden
		}
		return nil, err
	}
	return tx, nil
}

func (app *App) inviteToolCollaborator(w http.ResponseWriter, r *http.Request) {
	i, _ := app.managementIdentity(w, r, true)
	if i == nil {
		return
	}
	if r.ParseForm() != nil {
		fail(w, errInput)
		return
	}
	g, err := grantInput(r, 6)
	kind := r.FormValue("kind")
	community := chi.URLParam(r, "id")
	if err != nil || toolLabel(kind) == "" || g.User == i.ID {
		fail(w, errInput)
		return
	}
	tx, err := app.collaborationTransaction(r, i, true, true)
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback()
	var repVersion, eligibilityVersion int
	var repExpires int64
	if tx.QueryRow(`SELECT cr.version,cr.expires_at FROM community_representatives cr JOIN communities c ON c.id=cr.community_id WHERE cr.community_id=? AND cr.user_id=? AND cr.expires_at>unixepoch() AND c.status IN ('active','uncategorized')`, community, i.ID).Scan(&repVersion, &repExpires) != nil {
		fail(w, errForbidden)
		return
	}
	if g.Expires > repExpires {
		fail(w, errInput)
		return
	}
	if !activeManagementUser(tx, g.User) || tx.QueryRow("SELECT version FROM tool_eligibility WHERE community_id=? AND user_id=? AND eligible=1", community, g.User).Scan(&eligibilityVersion) != nil {
		fail(w, errInput)
		return
	}
	var id string
	var old int
	err = tx.QueryRow("SELECT id,version FROM tool_collaborators WHERE community_id=? AND user_id=? AND kind=?", community, g.User, kind).Scan(&id, &old)
	if err != nil && err != sql.ErrNoRows {
		fail(w, err)
		return
	}
	if old != g.Version {
		fail(w, errConflict)
		return
	}
	if id == "" {
		id = randomID()
	}
	_, err = tx.Exec(`INSERT INTO tool_collaborators VALUES(?,?,?,?,?,?,?,?,?,'pending',?,?) ON CONFLICT(community_id,user_id,kind) DO UPDATE SET representative_id=excluded.representative_id,representative_version=excluded.representative_version,eligibility_version=excluded.eligibility_version,purpose=excluded.purpose,expires_at=excluded.expires_at,state='pending',version=excluded.version,updated_at=excluded.updated_at`, id, community, g.User, kind, i.ID, repVersion, eligibilityVersion, g.Authority, g.Expires, old+1, time.Now().Unix())
	if err == nil {
		_, err = tx.Exec(`INSERT INTO notifications(id,user_id,kind,object_id,created_at) VALUES(?,?,'tool-invitation',?,?) ON CONFLICT(user_id,kind,object_id) DO UPDATE SET read_at=NULL,created_at=excluded.created_at`, randomID(), g.User, id, time.Now().Unix())
	}
	if err == nil {
		err = managementEvent(tx, i.ID, "tool-invite", community, map[string]any{"id": id, "version": old}, map[string]any{"id": id, "version": old + 1, "kind": kind, "user": g.User, "expires_at": g.Expires})
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		fail(w, err)
		return
	}
	http.Redirect(w, r, collaborationBase(community)+"?saved=1", 303)
}

func (app *App) revokeToolCollaborator(w http.ResponseWriter, r *http.Request) {
	i, _ := app.managementIdentity(w, r, true)
	if i == nil {
		return
	}
	if r.ParseForm() != nil {
		fail(w, errInput)
		return
	}
	version, err := strconv.Atoi(r.FormValue("version"))
	if err != nil || version < 1 {
		fail(w, errInput)
		return
	}
	community, id := chi.URLParam(r, "id"), chi.URLParam(r, "grant")
	tx, err := app.collaborationTransaction(r, i, true, true)
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback()
	if !canManageCommunity(tx, i.ID, community) {
		fail(w, errForbidden)
		return
	}
	result, err := tx.Exec("UPDATE tool_collaborators SET state='revoked',version=version+1,updated_at=? WHERE id=? AND community_id=? AND version=?", time.Now().Unix(), id, community, version)
	if err == nil {
		var n int64
		n, err = result.RowsAffected()
		if err == nil && n != 1 {
			err = errConflict
		}
	}
	if err == nil {
		err = managementEvent(tx, i.ID, "tool-revoke", community, map[string]any{"id": id, "version": version}, map[string]any{"id": id, "version": version + 1})
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		fail(w, err)
		return
	}
	http.Redirect(w, r, collaborationBase(community)+"?saved=1", 303)
}

func (app *App) answerCollaboration(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		i := app.requireIdentity(w, r, action == "accept")
		if i == nil {
			return
		}
		if r.ParseForm() != nil {
			fail(w, errInput)
			return
		}
		version, err := strconv.Atoi(r.FormValue("version"))
		if err != nil || version < 1 {
			fail(w, errInput)
			return
		}
		id := chi.URLParam(r, "grant")
		tx, err := app.collaborationTransaction(r, i, action == "accept", action == "accept")
		if err != nil {
			fail(w, err)
			return
		}
		defer tx.Rollback()
		var community, state string
		var current int
		if err = tx.QueryRow("SELECT community_id,state,version FROM tool_collaborators WHERE id=? AND user_id=?", id, i.ID).Scan(&community, &state, &current); err != nil {
			fail(w, err)
			return
		}
		if current != version {
			fail(w, errConflict)
			return
		}
		next := ""
		switch action {
		case "accept":
			var n int
			if state != "pending" || r.FormValue("confirm") != "accept" || tx.QueryRow("SELECT count(*)"+liveToolGrantSQL+" AND g.id=? AND g.user_id=?", id, i.ID).Scan(&n) != nil || n != 1 {
				fail(w, errForbidden)
				return
			}
			next = "accepted"
		case "decline":
			if state != "pending" {
				fail(w, errConflict)
				return
			}
			next = "declined"
		case "leave":
			if state != "accepted" {
				fail(w, errConflict)
				return
			}
			next = "left"
		}
		_, err = tx.Exec("UPDATE tool_collaborators SET state=?,version=version+1,updated_at=? WHERE id=? AND user_id=?", next, time.Now().Unix(), id, i.ID)
		if err == nil {
			err = managementEvent(tx, i.ID, "tool-"+action, community, map[string]any{"id": id, "version": version}, map[string]any{"id": id, "version": version + 1})
		}
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			fail(w, err)
			return
		}
		http.Redirect(w, r, "/my/community-tools?saved=1", 303)
	}
}
