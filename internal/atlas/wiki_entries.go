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

type wikiEntryFields struct {
	Status        string `json:"status"`
	FirstVerified string `json:"first_verified_at,omitempty"`
	WorkType      string `json:"work_type,omitempty"`
	Creator       string `json:"creator,omitempty"`
	PublicURL     string `json:"public_url,omitempty"`
}

func wikiReviewer(q managementQuery, user string) bool {
	role, err := managementAccess(q, user)
	return err == nil && (role == "admin" || role == "member")
}
func formalWiki(kind string) bool { return kind == "community" || kind == "work" }
func wikiRisk(code string) bool   { return code == "privacy" || code == "copyright" || code == "safety" }
func wikiFields(v wikiVersion) wikiEntryFields {
	var f wikiEntryFields
	_ = json.Unmarshal([]byte(v.Fields), &f)
	return f
}

func validateWikiEntry(v wikiVersion) error {
	if !formalWiki(v.Kind) || v.Title == "" || v.Summary == "" || v.Body == "" || !checkText(v.Title, 200) || !checkText(v.Summary, 500) || !checkText(v.Body, 30000) || v.EditSummary == "" || !checkText(v.EditSummary, 300) || len(v.Sources) == 0 || len(v.Sources) > 20 || !validWikiFootnotes(v.Body, v.Sources) || len(wikiFootnotes.FindAllString(v.Body, -1)) == 0 {
		return errInput
	}
	f := wikiFields(v)
	if v.Kind == "community" {
		if f.Status != "active" && f.Status != "inactive" && f.Status != "migrated" && f.Status != "unknown" {
			return errInput
		}
		date, err := time.Parse("2006-01-02", f.FirstVerified)
		if err != nil || date.Format("2006-01-02") > time.Now().In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("2006-01-02") {
			return errInput
		}
	} else if f.Status != "public" || f.WorkType == "" || !checkText(f.WorkType, 100) || !checkText(f.Creator, 200) || !checkText(f.PublicURL, 1000) || (f.PublicURL != "" && !safeSource(f.PublicURL)) {
		return errInput
	}
	return nil
}

func (app *App) wikiEntryRoutes(r chi.Router) {
	r.Get("/wiki/entries/new", app.wikiEntryEditor)
	r.Post("/wiki/entries", app.submitWikiEntry)
	r.Get("/wiki/{wiki}/propose", app.wikiEntryEditor)
	r.Post("/wiki/{wiki}/propose", app.submitWikiEntry)
	r.Get("/my/wiki", app.myWikiSubmissions)
	r.Get("/wiki/{wiki}/submissions/{number}", app.wikiSubmissionPage)
	r.Post("/wiki/{wiki}/submissions/{number}/withdraw", app.withdrawWikiSubmission)
	r.Get("/admin/wiki", app.wikiReviewQueue)
	r.Get("/admin/wiki/restricted", app.wikiRestrictedPages)
	r.Post("/admin/wiki/{wiki}/versions/{number}/review", app.reviewWikiEntry)
	r.Post("/wiki/{wiki}/suggestions", app.submitWikiSuggestion)
	r.Post("/wiki/{wiki}/suggestions/{suggestion}/withdraw", app.withdrawWikiSuggestion)
	r.Post("/admin/wiki/{wiki}/suggestions/{suggestion}/resolve", app.resolveWikiSuggestion)
}

func submissionVisible(q managementQuery, v wikiVersion, user string) bool {
	if !formalWiki(v.Kind) || v.State != "active" || (v.Editor != user && !wikiReviewer(q, user)) {
		return false
	}
	if v.Published > 0 && !wikiReadable(q, v.PageID) {
		return false
	}
	if v.Community != "" {
		var n int
		return q.QueryRow("SELECT count(*) FROM communities WHERE id=? AND status IN ('active','uncategorized')", v.Community).Scan(&n) == nil && n == 1
	}
	return true
}

func insertWikiVersion(tx *sql.Tx, v wikiVersion, reviewer, reason string, reviewed int64) error {
	sources, _ := json.Marshal(v.Sources)
	_, err := tx.Exec(`INSERT INTO wiki_versions(page_id,number,base_version,title,summary,body,fields_json,sources_json,community_id,editor_id,editor_alias,edit_summary,review_status,reviewer_id,review_reason,reviewed_at,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, v.PageID, v.Number, v.Base, v.Title, v.Summary, v.Body, v.Fields, string(sources), nullable(v.Community), v.Editor, v.Alias, v.EditSummary, v.Review, nullable(reviewer), reason, nullableTime(reviewed), v.Created)
	return err
}
func nullableTime(t int64) any {
	if t == 0 {
		return nil
	}
	return t
}

// Caller holds the operation and journal locks and has validated the mutation in tx.
func (app *App) finishWikiOperation(tx *sql.Tx, op operation, records []operation) error {
	_, err := tx.Exec("INSERT INTO operation_events(event_id,object_id,action,created_at) VALUES(?,?,?,?)", op.EventID, op.ObjectID, op.Action, op.CreatedAt)
	if err != nil {
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

func (app *App) submitWikiEntry(w http.ResponseWriter, r *http.Request) {
	i := app.contentIdentity(w, r)
	if i == nil || !app.rateLimit(w, r, "wiki:"+i.ID, 60) {
		return
	}
	if r.ParseForm() != nil {
		fail(w, errInput)
		return
	}
	id := chi.URLParam(r, "wiki")
	creating := id == ""
	if creating {
		id = r.FormValue("page_id")
	}
	v := wikiVersion{PageID: id, Kind: r.FormValue("kind"), Title: strings.TrimSpace(r.FormValue("title")), Summary: strings.TrimSpace(r.FormValue("summary")), Body: strings.TrimSpace(r.FormValue("body")), Community: r.FormValue("community"), Editor: i.ID, Alias: i.Alias, EditSummary: strings.TrimSpace(r.FormValue("edit_summary")), Review: "pending"}
	f := wikiEntryFields{Status: r.FormValue("status_value"), FirstVerified: r.FormValue("first_verified_at"), WorkType: strings.TrimSpace(r.FormValue("work_type")), Creator: strings.TrimSpace(r.FormValue("creator")), PublicURL: strings.TrimSpace(r.FormValue("public_url"))}
	raw, _ := json.Marshal(f)
	v.Fields = string(raw)
	var e1, e2, errorSource error
	v.Latest, e1 = strconv.Atoi(r.FormValue("version"))
	v.Base, e2 = strconv.Atoi(r.FormValue("base"))
	v.Sources, errorSource = parseWikiSources(r.FormValue("sources"))
	if e1 != nil || e2 != nil || errorSource != nil || v.Latest < 0 || v.Base < 0 || creating != (v.Latest == 0) || id == "" || !checkText(id, 100) || strings.ContainsAny(id, "/?#") || validateWikiEntry(v) != nil || r.FormValue("license") != "CC BY-SA 4.0" || r.FormValue("public_scope") != "public" {
		app.wikiEntryError(w, r, i, v, creating, errInput)
		return
	}
	if err := app.commitWikiEntry(r, i, &v, creating); err != nil {
		app.wikiEntryError(w, r, i, v, creating, err)
		return
	}
	http.Redirect(w, r, "/wiki/"+pathID(id)+"/submissions/"+strconv.Itoa(v.Number), 303)
}

func (app *App) commitWikiEntry(r *http.Request, i *Identity, v *wikiVersion, creating bool) error {
	app.opsMu.Lock()
	defer app.opsMu.Unlock()
	tx, err := app.collaborationTransaction(r, i, false, true)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if v.Community != "" {
		var n int
		if tx.QueryRow("SELECT count(*) FROM communities WHERE id=? AND status IN ('active','uncategorized')", v.Community).Scan(&n) != nil || n != 1 {
			return errInput
		}
	}
	if creating {
		var n int
		if err = tx.QueryRow("SELECT count(*) FROM wiki_pages WHERE id=?", v.PageID).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			return errConflict
		}
		if v.Base != 0 {
			return errInput
		}
	} else {
		var kind, state, creator string
		var latest, published int
		if err = tx.QueryRow("SELECT kind,state,creator_id,latest_version,published_version FROM wiki_pages WHERE id=?", v.PageID).Scan(&kind, &state, &creator, &latest, &published); err != nil {
			return err
		}
		if state != "active" || !formalWiki(kind) || kind != v.Kind || (published == 0 && creator != i.ID) || (published > 0 && !wikiReadable(tx, v.PageID)) {
			return errForbidden
		}
		if latest != v.Latest || published != v.Base {
			return errConflict
		}
	}
	v.Number = v.Latest + 1
	v.Created = time.Now().Unix()
	if creating {
		_, err = tx.Exec("INSERT INTO wiki_pages(id,kind,creator_id,latest_version,created_at,updated_at) VALUES(?,?,?,?,?,?)", v.PageID, v.Kind, i.ID, v.Number, v.Created, v.Created)
	} else {
		_, err = tx.Exec("UPDATE wiki_pages SET latest_version=? WHERE id=?", v.Number, v.PageID)
	}
	if err == nil {
		err = insertWikiVersion(tx, *v, "", "", 0)
	}
	if err == nil {
		err = managementEvent(tx, i.ID, "wiki-submit", v.PageID, nil, map[string]int{"version": v.Number, "base": v.Base})
	}
	if err == nil {
		err = tx.Commit()
	}
	return err
}

func (app *App) reviewWikiEntry(w http.ResponseWriter, r *http.Request) {
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
	number, err := strconv.Atoi(chi.URLParam(r, "number"))
	decision, note := r.FormValue("decision"), strings.TrimSpace(r.FormValue("note"))
	risk := r.FormValue("risk")
	if err != nil || number < 1 || (decision != "accept" && decision != "reject") || note == "" || !checkText(note, 1000) || r.FormValue("confirm") != "reviewed" || (risk != "" && (decision != "reject" || !wikiRisk(risk))) {
		fail(w, errInput)
		return
	}
	err = app.commitWikiReview(r, i, chi.URLParam(r, "wiki"), number, decision, note, risk)
	if err != nil {
		fail(w, err)
		return
	}
	http.Redirect(w, r, "/admin/wiki?saved=1", 303)
}

func (app *App) commitWikiReview(r *http.Request, i *Identity, id string, number int, decision, note, risk string) error {
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
	tx, err := app.collaborationTransaction(r, i, true, true)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if !wikiReviewer(tx, i.ID) {
		return errForbidden
	}
	v, err := loadWikiVersion(tx, id, number)
	if err != nil {
		return err
	}
	if !submissionVisible(tx, v, i.ID) || v.Review != "pending" {
		return errForbidden
	}
	now := time.Now().Unix()
	op := operation{EventID: randomID(), ObjectID: id, Action: "wiki-review-rejected", OwnerID: i.ID, VersionNumber: number, CreatedAt: now}
	if decision == "accept" {
		if v.Base != v.Published || v.Number <= v.Published {
			return errConflict
		}
		if !activeManagementUser(tx, v.Editor) || validateWikiEntry(v) != nil {
			return errInput
		}
		_, err = tx.Exec("UPDATE wiki_versions SET review_status='accepted',reviewer_id=?,review_reason=?,reviewed_at=? WHERE page_id=? AND number=?", i.ID, note, now, id, number)
		if err == nil {
			_, err = tx.Exec("UPDATE wiki_pages SET published_version=?,updated_at=? WHERE id=?", number, now, id)
		}
		op.Action = "wiki-changed"
		op.Reason = ""
	} else if risk != "" {
		op.Action = "wiki-submission-withdraw"
		if err = app.preserveWiki(tx, id); err == nil {
			err = wipeWikiSubmissionSQL(tx, id, number, wikiReason(risk)+"，提交内容已限制展示")
		}
		op.Reason = risk
		if err == nil {
			_, err = tx.Exec("UPDATE wiki_versions SET reviewer_id=?,reviewed_at=? WHERE page_id=? AND number=?", i.ID, now, id, number)
		}
	} else {
		_, err = tx.Exec("UPDATE wiki_versions SET review_status='rejected',reviewer_id=?,review_reason=?,reviewed_at=? WHERE page_id=? AND number=?", i.ID, note, now, id, number)
	}
	if err != nil {
		return err
	}
	if err = managementEvent(tx, i.ID, "wiki-review", id, map[string]int{"version": number}, map[string]string{"decision": decision, "risk": risk}); err != nil {
		return err
	}
	if err = app.finishWikiOperation(tx, op, records); err != nil {
		return err
	}
	if risk != "" {
		return app.scrubBackupsLocked()
	}
	return nil
}

func wipeWikiSubmissionSQL(tx *sql.Tx, id string, number int, reason string) error {
	_, err := tx.Exec("UPDATE wiki_versions SET title='',summary='',body='',fields_json='{}',sources_json='[]',edit_summary='',review_status='withdrawn',review_reason=? WHERE page_id=? AND number=?", reason, id, number)
	if err == nil {
		_, err = tx.Exec("UPDATE wiki_pages SET state='hidden',published_version=0 WHERE id=? AND published_version=?", id, number)
	}
	return err
}

func withdrawOwnWikiSubmissions(tx *sql.Tx, user string) error {
	rows, err := tx.Query("SELECT page_id,number FROM wiki_versions WHERE editor_id=? AND review_status IN ('pending','rejected')", user)
	if err != nil {
		return err
	}
	type ref struct {
		id string
		n  int
	}
	refs := []ref{}
	for rows.Next() {
		var v ref
		if err = rows.Scan(&v.id, &v.n); err != nil {
			rows.Close()
			return err
		}
		refs = append(refs, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, v := range refs {
		if err = wipeWikiSubmissionSQL(tx, v.id, v.n, ""); err != nil {
			return err
		}
	}
	return nil
}

func (app *App) withdrawWikiSubmission(w http.ResponseWriter, r *http.Request) {
	i := app.wikiRightsIdentity(w, r)
	if i == nil {
		return
	}
	id := chi.URLParam(r, "wiki")
	number, err := strconv.Atoi(chi.URLParam(r, "number"))
	if err != nil || number < 1 {
		fail(w, errInput)
		return
	}
	app.opsMu.Lock()
	defer app.opsMu.Unlock()
	unlock, err := app.journalLock()
	if err != nil {
		fail(w, err)
		return
	}
	defer unlock()
	records, err := app.readJournal()
	if err != nil {
		fail(w, err)
		return
	}
	tx, err := app.collaborationTransaction(r, i, false, false)
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback()
	v, err := loadWikiVersion(tx, id, number)
	if err != nil {
		fail(w, err)
		return
	}
	if v.Editor != i.ID || v.State != "active" || (v.Review != "pending" && v.Review != "rejected") {
		fail(w, errForbidden)
		return
	}
	op := operation{EventID: randomID(), ObjectID: id, Action: "wiki-submission-withdraw", OwnerID: i.ID, VersionNumber: number, CreatedAt: time.Now().Unix()}
	if err = wipeWikiSubmissionSQL(tx, id, number, ""); err == nil {
		err = app.finishWikiOperation(tx, op, records)
	}
	if err == nil {
		err = app.scrubBackupsLocked()
	}
	if err != nil {
		fail(w, err)
		return
	}
	http.Redirect(w, r, "/my/wiki", 303)
}
