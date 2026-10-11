package atlas

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

type managementQuery interface{ QueryRow(string, ...any) *sql.Row }

func managementAccess(q managementQuery, user string) (string, error) {
	var role string
	err := q.QueryRow(`SELECT role FROM team_members t JOIN users u ON u.id=t.user_id WHERE t.user_id=? AND u.status='active' AND t.expires_at>?`, user, time.Now().Unix()).Scan(&role)
	if err == nil {
		return role, nil
	}
	if err != sql.ErrNoRows {
		return "", err
	}
	var n int
	err = q.QueryRow(`SELECT count(*) FROM community_representatives r JOIN users u ON u.id=r.user_id JOIN communities c ON c.id=r.community_id WHERE r.user_id=? AND u.status='active' AND r.expires_at>? AND c.status IN ('active','uncategorized')`, user, time.Now().Unix()).Scan(&n)
	if n > 0 {
		return "representative", err
	}
	return "", err
}

func canManageCommunity(q managementQuery, user, community string) bool {
	role, err := managementAccess(q, user)
	if err != nil {
		return false
	}
	if role == "admin" || role == "member" {
		return true
	}
	var n int
	return q.QueryRow(`SELECT count(*) FROM community_representatives r JOIN communities c ON c.id=r.community_id WHERE r.user_id=? AND r.community_id=? AND r.expires_at>? AND c.status IN ('active','uncategorized')`, user, community, time.Now().Unix()).Scan(&n) == nil && n == 1
}

func (app *App) managementIdentity(w http.ResponseWriter, r *http.Request, write bool) (*Identity, string) {
	i := app.requireIdentity(w, r, write)
	if i == nil {
		return nil, ""
	}
	role, err := managementAccess(app.db, i.ID)
	if err != nil {
		fail(w, err)
		return nil, ""
	}
	if role == "" {
		fail(w, errForbidden)
		return nil, ""
	}
	if write && !app.writeAllowed(i.ID) {
		respond(w, 503, map[string]string{"code": "READ_ONLY"})
		return nil, ""
	}
	return i, role
}

func (app *App) managementRoutes(r *chi.Mux) {
	r.Get("/admin", app.managementPage)
	r.Get("/admin/team", app.managementPage)
	r.Get("/admin/representatives", app.managementPage)
	r.Get("/admin/log", app.managementPage)
	r.Get("/admin/communities/{id}", app.manageCommunityPage)
	r.Post("/admin/team", app.saveTeamMember)
	r.Post("/admin/team/{id}/remove", app.removeTeamMember)
	r.Post("/admin/representatives", app.saveRepresentative)
	r.Post("/admin/representatives/{id}/remove", app.removeRepresentative)
	r.Post("/admin/communities/{id}", app.saveManagedCommunity)
}

func managementEvent(tx *sql.Tx, actor, action, object string, before, after any) error {
	b, err := json.Marshal(before)
	if err != nil {
		return err
	}
	a, err := json.Marshal(after)
	if err != nil {
		return err
	}
	_, err = tx.Exec("INSERT INTO management_events VALUES(?,?,?,?,?,?,?)", randomID(), actor, action, object, string(b), string(a), time.Now().Unix())
	return err
}

// Bootstrap is a local, explicit operator command; signup never grants privileges.
func (app *App) BootstrapAdmin(identifier string) error {
	tx, err := app.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var mode string
	if tx.QueryRow("SELECT mode FROM runtime_state WHERE id=1").Scan(&mode) != nil || mode != "normal" {
		return errForbidden
	}
	var n int
	if err = tx.QueryRow("SELECT count(*) FROM team_members t JOIN users u ON u.id=t.user_id WHERE t.role='admin' AND t.expires_at>? AND u.status='active'", time.Now().Unix()).Scan(&n); err != nil {
		return err
	}
	if n != 0 {
		return errConflict
	}
	rows, err := tx.Query("SELECT id FROM users WHERE status='active' AND (id=? OR alias=?)", identifier, identifier)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(ids) != 1 {
		return errInput
	}
	now := time.Now()
	_, err = tx.Exec(`INSERT INTO team_members(user_id,role,authority,expires_at,updated_at) VALUES(?,'admin','本地管理员初始化',?,?) ON CONFLICT(user_id) DO UPDATE SET role='admin',authority=excluded.authority,expires_at=excluded.expires_at,version=team_members.version+1,updated_at=excluded.updated_at`, ids[0], now.AddDate(1, 0, 0).Unix(), now.Unix())
	if err != nil {
		return err
	}
	if err = managementEvent(tx, "local-operator", "bootstrap-admin", ids[0], nil, map[string]any{"role": "admin", "expires_at": now.AddDate(1, 0, 0).Unix()}); err != nil {
		return err
	}
	return tx.Commit()
}

type managementGrant struct {
	User      string `json:"user"`
	Role      string `json:"role,omitempty"`
	Authority string `json:"authority"`
	Expires   int64  `json:"expires_at"`
	Version   int    `json:"version"`
}

func grantInput(r *http.Request, months int) (managementGrant, error) {
	g := managementGrant{User: r.FormValue("user_id"), Role: r.FormValue("role"), Authority: strings.TrimSpace(r.FormValue("authority"))}
	version, err := strconv.Atoi(r.FormValue("version"))
	if err != nil || version < 0 {
		return g, errInput
	}
	g.Version = version
	now := time.Now()
	date, err := time.ParseInLocation("2006-01-02", r.FormValue("expires"), now.Location())
	if err != nil {
		return g, errInput
	}
	g.Expires = date.AddDate(0, 0, 1).Add(-time.Second).Unix()
	if g.User == "" || !checkText(g.User, 100) || g.Authority == "" || !checkText(g.Authority, 300) || g.Expires <= now.Unix() || date.Format("2006-01-02") > now.AddDate(0, months, 0).Format("2006-01-02") {
		return g, errInput
	}
	return g, nil
}

func (app *App) grantTransaction(i *Identity) (*sql.Tx, error) {
	tx, err := app.db.Begin()
	if err != nil {
		return nil, err
	}
	role, err := managementAccess(tx, i.ID)
	var mode string
	if err == nil && (role != "admin" || tx.QueryRow("SELECT mode FROM runtime_state WHERE id=1").Scan(&mode) != nil || mode != "normal") {
		err = errForbidden
	}
	if err != nil {
		tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func activeManagementUser(tx *sql.Tx, id string) bool {
	var n int
	return tx.QueryRow("SELECT count(*) FROM users WHERE id=? AND status='active'", id).Scan(&n) == nil && n == 1
}
func lastAdmin(tx *sql.Tx, target string) bool {
	var n int
	return tx.QueryRow(`SELECT count(*) FROM team_members t JOIN users u ON u.id=t.user_id WHERE t.role='admin' AND t.expires_at>? AND u.status='active' AND t.user_id<>?`, time.Now().Unix(), target).Scan(&n) != nil || n == 0
}
func (app *App) saveTeamMember(w http.ResponseWriter, r *http.Request) {
	i, role := app.managementIdentity(w, r, true)
	if i == nil {
		return
	}
	if role != "admin" {
		fail(w, errForbidden)
		return
	}
	if r.ParseForm() != nil {
		fail(w, errInput)
		return
	}
	g, err := grantInput(r, 12)
	if err != nil || (g.Role != "admin" && g.Role != "member") {
		fail(w, errInput)
		return
	}
	tx, err := app.grantTransaction(i)
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback()
	if !activeManagementUser(tx, g.User) {
		fail(w, errInput)
		return
	}
	var old managementGrant
	old.User = g.User
	err = tx.QueryRow("SELECT role,authority,expires_at,version FROM team_members WHERE user_id=?", g.User).Scan(&old.Role, &old.Authority, &old.Expires, &old.Version)
	if err != nil && err != sql.ErrNoRows {
		fail(w, err)
		return
	}
	if old.Version != g.Version {
		fail(w, errConflict)
		return
	}
	if old.Role == "admin" && old.Expires > time.Now().Unix() && g.Role != "admin" && lastAdmin(tx, g.User) {
		respond(w, 409, map[string]string{"code": "LAST_ADMIN", "message": "请先安排另一位有效管理员"})
		return
	}
	if old.Version == 0 {
		_, err = tx.Exec("INSERT INTO team_members(user_id,role,authority,expires_at,updated_at) VALUES(?,?,?,?,?)", g.User, g.Role, g.Authority, g.Expires, time.Now().Unix())
	} else {
		_, err = tx.Exec("UPDATE team_members SET role=?,authority=?,expires_at=?,version=version+1,updated_at=? WHERE user_id=?", g.Role, g.Authority, g.Expires, time.Now().Unix(), g.User)
	}
	if err == nil {
		g.Version++
		err = managementEvent(tx, i.ID, "team-save", g.User, old, g)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		fail(w, err)
		return
	}
	http.Redirect(w, r, "/admin/team?saved=1", 303)
}

func (app *App) removeTeamMember(w http.ResponseWriter, r *http.Request) {
	app.removeManagementGrant(w, r, true)
}
func (app *App) removeRepresentative(w http.ResponseWriter, r *http.Request) {
	app.removeManagementGrant(w, r, false)
}
func (app *App) removeManagementGrant(w http.ResponseWriter, r *http.Request, team bool) {
	i, role := app.managementIdentity(w, r, true)
	if i == nil {
		return
	}
	if role != "admin" {
		fail(w, errForbidden)
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
	tx, err := app.grantTransaction(i)
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback()
	id := chi.URLParam(r, "id")
	var old managementGrant
	query := "SELECT user_id,role,authority,expires_at,version FROM team_members WHERE user_id=?"
	if !team {
		query = "SELECT user_id,'representative',authority,expires_at,version FROM community_representatives WHERE community_id=?"
	}
	if err = tx.QueryRow(query, id).Scan(&old.User, &old.Role, &old.Authority, &old.Expires, &old.Version); err != nil {
		fail(w, err)
		return
	}
	if old.Version != version {
		fail(w, errConflict)
		return
	}
	if team && old.Role == "admin" && old.Expires > time.Now().Unix() && lastAdmin(tx, id) {
		respond(w, 409, map[string]string{"code": "LAST_ADMIN", "message": "请先安排另一位有效管理员"})
		return
	}
	table, key, action, destination := "team_members", "user_id", "team-remove", "/admin/team"
	if !team {
		table, key, action, destination = "community_representatives", "community_id", "representative-remove", "/admin/representatives"
	}
	_, err = tx.Exec("UPDATE "+table+" SET expires_at=0,version=version+1,updated_at=? WHERE "+key+"=?", time.Now().Unix(), id)
	if err == nil {
		err = managementEvent(tx, i.ID, action, id, old, nil)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		fail(w, err)
		return
	}
	http.Redirect(w, r, destination+"?saved=1", 303)
}

func (app *App) saveRepresentative(w http.ResponseWriter, r *http.Request) {
	i, role := app.managementIdentity(w, r, true)
	if i == nil {
		return
	}
	if role != "admin" {
		fail(w, errForbidden)
		return
	}
	if r.ParseForm() != nil {
		fail(w, errInput)
		return
	}
	g, err := grantInput(r, 6)
	id := r.FormValue("community_id")
	if err != nil || id == "" || !checkText(id, 200) {
		fail(w, errInput)
		return
	}
	tx, err := app.grantTransaction(i)
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback()
	var status string
	if !activeManagementUser(tx, g.User) || tx.QueryRow("SELECT status FROM communities WHERE id=?", id).Scan(&status) != nil || (status != "active" && status != "uncategorized") {
		fail(w, errInput)
		return
	}
	var old managementGrant
	err = tx.QueryRow("SELECT user_id,authority,expires_at,version FROM community_representatives WHERE community_id=?", id).Scan(&old.User, &old.Authority, &old.Expires, &old.Version)
	if err != nil && err != sql.ErrNoRows {
		fail(w, err)
		return
	}
	if old.Version != g.Version {
		fail(w, errConflict)
		return
	}
	if old.Version == 0 {
		_, err = tx.Exec("INSERT INTO community_representatives(community_id,user_id,authority,expires_at,updated_at) VALUES(?,?,?,?,?)", id, g.User, g.Authority, g.Expires, time.Now().Unix())
	} else {
		_, err = tx.Exec("UPDATE community_representatives SET user_id=?,authority=?,expires_at=?,version=version+1,updated_at=? WHERE community_id=?", g.User, g.Authority, g.Expires, time.Now().Unix(), id)
	}
	if err == nil {
		g.Version++
		err = managementEvent(tx, i.ID, "representative-save", id, old, g)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		fail(w, err)
		return
	}
	http.Redirect(w, r, "/admin/representatives?saved=1", 303)
}
