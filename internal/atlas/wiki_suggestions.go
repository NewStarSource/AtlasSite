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

type wikiSuggestion struct {
	ID, Page, Author, Alias, Target, Old, Proposed, Evidence, State, Resolution string
	Base, Version, Result                                                       int
	Created                                                                     int64
	Sources                                                                     []wikiSource
}

func suggestionTarget(target string) string {
	return map[string]string{"title": "名称", "summary": "一句话说明", "body": "正文或段落", "status": "社群状态", "first_verified_at": "首次确认日期", "work_type": "作品类型", "creator": "创作者公开署名", "public_url": "公开作品入口"}[target]
}
func suggestionState(state string) string {
	return map[string]string{"pending": "待处理", "accepted": "已采纳", "partial": "已部分采纳", "rejected": "已退回", "withdrawn": "已撤回"}[state]
}

func loadWikiSuggestion(q managementQuery, id string) (wikiSuggestion, error) {
	g := wikiSuggestion{}
	var sources string
	err := q.QueryRow("SELECT id,page_id,base_version,author_id,author_alias,target,old_text,proposed,evidence,sources_json,state,version,result_version,resolution,created_at FROM wiki_suggestions WHERE id=?", id).Scan(&g.ID, &g.Page, &g.Base, &g.Author, &g.Alias, &g.Target, &g.Old, &g.Proposed, &g.Evidence, &sources, &g.State, &g.Version, &g.Result, &g.Resolution, &g.Created)
	if err == nil {
		err = json.Unmarshal([]byte(sources), &g.Sources)
	}
	return g, err
}

func suggestionCandidate(current wikiVersion, g wikiSuggestion, text string) (wikiVersion, error) {
	if !formalWiki(current.Kind) || g.Base != current.Number || suggestionTarget(g.Target) == "" {
		return current, errConflict
	}
	current.EditSummary = "采纳建议 " + g.ID
	f := wikiFields(current)
	switch g.Target {
	case "title":
		current.Title = text
	case "summary":
		current.Summary = text
	case "body":
		if g.Old != "" {
			if strings.Count(current.Body, g.Old) != 1 {
				return current, errConflict
			}
			current.Body = strings.Replace(current.Body, g.Old, text, 1)
		} else {
			current.Body = text
		}
	case "status":
		if current.Kind != "community" {
			return current, errInput
		}
		f.Status = text
	case "first_verified_at":
		if current.Kind != "community" {
			return current, errInput
		}
		f.FirstVerified = text
	case "work_type":
		if current.Kind != "work" {
			return current, errInput
		}
		f.WorkType = text
	case "creator":
		if current.Kind != "work" {
			return current, errInput
		}
		f.Creator = text
	case "public_url":
		if current.Kind != "work" {
			return current, errInput
		}
		f.PublicURL = text
	}
	fields, _ := json.Marshal(f)
	current.Fields = string(fields)
	seen := map[string]bool{}
	for _, s := range current.Sources {
		seen[s.ID] = true
	}
	for _, s := range g.Sources {
		if seen[s.ID] {
			return current, errInput
		}
		seen[s.ID] = true
		current.Sources = append(current.Sources, s)
	}
	return current, validateWikiEntry(current)
}

func (app *App) submitWikiSuggestion(w http.ResponseWriter, r *http.Request) {
	i := app.contentIdentity(w, r)
	if i == nil || !app.rateLimit(w, r, "wiki-suggestion:"+i.ID, 100) {
		return
	}
	if r.ParseForm() != nil {
		fail(w, errInput)
		return
	}
	g := wikiSuggestion{ID: randomID(), Page: chi.URLParam(r, "wiki"), Author: i.ID, Alias: i.Alias, Target: r.FormValue("target"), Old: strings.TrimSpace(r.FormValue("old_text")), Proposed: strings.TrimSpace(r.FormValue("proposed")), Evidence: strings.TrimSpace(r.FormValue("evidence"))}
	var err error
	g.Base, err = strconv.Atoi(r.FormValue("base"))
	if r.FormValue("sources") != "" && err == nil {
		g.Sources, err = parseWikiSources(r.FormValue("sources"))
	}
	if err != nil || g.Base < 1 || suggestionTarget(g.Target) == "" || g.Proposed == "" || g.Evidence == "" || !checkText(g.Old, 30000) || !checkText(g.Proposed, 30000) || !checkText(g.Evidence, 2000) || r.FormValue("license") != "CC BY-SA 4.0" {
		fail(w, errInput)
		return
	}
	app.opsMu.Lock()
	defer app.opsMu.Unlock()
	tx, err := app.collaborationTransaction(r, i, false, true)
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback()
	if !wikiReadable(tx, g.Page) {
		fail(w, sql.ErrNoRows)
		return
	}
	current, err := loadWikiVersion(tx, g.Page, 0)
	if err != nil {
		fail(w, err)
		return
	}
	if _, err = suggestionCandidate(current, g, g.Proposed); err != nil {
		fail(w, err)
		return
	}
	sources, _ := json.Marshal(g.Sources)
	basis := digest(g.Target + "\x00" + g.Old + "\x00" + g.Evidence + "\x00" + string(sources))
	var rejected int
	if err = tx.QueryRow(`SELECT count(*) FROM wiki_suggestions s WHERE s.page_id=? AND s.author_id=? AND s.basis_hash=? AND s.was_rejected=1`, g.Page, i.ID, basis).Scan(&rejected); err != nil {
		fail(w, err)
		return
	}
	if rejected > 0 {
		respond(w, 400, map[string]string{"code": "NEW_EVIDENCE_REQUIRED", "message": "相同依据已被退回，请提供新依据后再提交。"})
		return
	}
	now := time.Now().Unix()
	_, err = tx.Exec(`INSERT INTO wiki_suggestions(id,page_id,base_version,author_id,author_alias,target,old_text,proposed,evidence,sources_json,basis_hash,state,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,'pending',?,?)`, g.ID, g.Page, g.Base, i.ID, i.Alias, g.Target, g.Old, g.Proposed, g.Evidence, string(sources), basis, now, now)
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		fail(w, err)
		return
	}
	http.Redirect(w, r, "/wiki/"+pathID(g.Page)+"?suggestion="+pathID(g.ID)+"#suggestion-"+pathID(g.ID), 303)
}

func (app *App) resolveWikiSuggestion(w http.ResponseWriter, r *http.Request) {
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
	version, err := strconv.Atoi(r.FormValue("version"))
	decision, note, risk := r.FormValue("decision"), strings.TrimSpace(r.FormValue("note")), r.FormValue("risk")
	if err != nil || version < 1 || (decision != "accepted" && decision != "partial" && decision != "rejected") || note == "" || !checkText(note, 1000) || r.FormValue("confirm") != "reviewed" || (risk != "" && (decision != "rejected" || !wikiRisk(risk))) {
		fail(w, errInput)
		return
	}
	err = app.commitWikiSuggestion(r, i, chi.URLParam(r, "wiki"), chi.URLParam(r, "suggestion"), version, decision, note, risk, strings.TrimSpace(r.FormValue("replacement")))
	if err != nil {
		fail(w, err)
		return
	}
	http.Redirect(w, r, "/wiki/"+pathID(chi.URLParam(r, "wiki"))+"?suggestion="+pathID(chi.URLParam(r, "suggestion"))+"#suggestion-"+pathID(chi.URLParam(r, "suggestion")), 303)
}

func (app *App) commitWikiSuggestion(r *http.Request, i *Identity, page, id string, version int, decision, note, risk, replacement string) error {
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
	if !wikiReadable(tx, page) {
		return sql.ErrNoRows
	}
	g, err := loadWikiSuggestion(tx, id)
	if err != nil {
		return err
	}
	if g.Page != page || g.State != "pending" {
		return errForbidden
	}
	if g.Version != version {
		return errConflict
	}
	now := time.Now().Unix()
	result := 0
	if decision == "accepted" || decision == "partial" {
		current, err := loadWikiVersion(tx, page, 0)
		if err != nil {
			return err
		}
		text := g.Proposed
		if decision == "partial" {
			if replacement == "" || replacement == g.Proposed || !checkText(replacement, 30000) {
				return errInput
			}
			text = replacement
		}
		candidate, err := suggestionCandidate(current, g, text)
		if err != nil {
			return err
		}
		candidate.Number = current.Latest + 1
		candidate.Base = current.Number
		candidate.Review = "accepted"
		candidate.Created = now
		candidate.Editor = g.Author
		candidate.Alias = g.Alias
		if decision == "partial" {
			candidate.Editor = i.ID
			candidate.Alias = i.Alias
			candidate.EditSummary = "部分采纳建议 " + id
		}
		if err = insertWikiVersion(tx, candidate, i.ID, note, now); err != nil {
			return err
		}
		if _, err = tx.Exec("UPDATE wiki_pages SET latest_version=?,published_version=?,updated_at=? WHERE id=?", candidate.Number, candidate.Number, now, page); err != nil {
			return err
		}
		result = candidate.Number
	} else if risk != "" {
		if err = app.privatePut("wiki-suggestion-isolated:"+id, g.Author, "wiki-isolated", g, time.Now().Add(30*24*time.Hour).Unix()); err != nil {
			return err
		}
		note = wikiReason(risk) + "，建议原文已限制展示"
		if err = wipeWikiSuggestionSQL(tx, id, "rejected", note); err != nil {
			return err
		}
	}
	_, err = tx.Exec("UPDATE wiki_suggestions SET state=?,version=?,result_version=?,resolution=?,reviewer_id=?,updated_at=?,was_rejected=CASE WHEN ?='rejected' THEN 1 ELSE was_rejected END WHERE id=?", decision, g.Version+1, result, note, i.ID, now, decision, id)
	if err != nil {
		return err
	}
	_, err = tx.Exec("INSERT INTO wiki_suggestion_actions VALUES(?,?,?,?,?,?,?)", randomID(), id, i.ID, decision, note, result, now)
	if err != nil {
		return err
	}
	op := operation{EventID: randomID(), ObjectID: id, ParentID: page, Action: "wiki-suggestion-resolved", OwnerID: i.ID, TargetKind: decision, VersionNumber: result, Reason: risk, CreatedAt: now}
	if err = app.finishWikiOperation(tx, op, records); err != nil {
		return err
	}
	if risk != "" {
		return app.scrubBackupsLocked()
	}
	return nil
}

func wikiSuggestionTable(tx *sql.Tx) (bool, error) {
	var n int
	err := tx.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='wiki_suggestions'").Scan(&n)
	return n == 1, err
}
func wipeWikiSuggestionSQL(tx *sql.Tx, id, state, note string) error {
	exists, err := wikiSuggestionTable(tx)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	_, err = tx.Exec("UPDATE wiki_suggestions SET old_text='',proposed='',evidence='',sources_json='[]',state=?,resolution=?,version=version+1,was_rejected=CASE WHEN ?='rejected' THEN 1 ELSE was_rejected END WHERE id=?", state, note, state, id)
	if err == nil {
		_, err = tx.Exec("UPDATE wiki_suggestion_actions SET note='' WHERE suggestion_id=?", id)
	}
	return err
}

func (app *App) withdrawWikiSuggestion(w http.ResponseWriter, r *http.Request) {
	i := app.wikiRightsIdentity(w, r)
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
	g, err := loadWikiSuggestion(tx, chi.URLParam(r, "suggestion"))
	if err != nil {
		fail(w, err)
		return
	}
	if g.Page != chi.URLParam(r, "wiki") || g.Author != i.ID || g.State == "withdrawn" || !wikiReadable(tx, g.Page) {
		fail(w, errForbidden)
		return
	}
	if g.Version != version {
		fail(w, errConflict)
		return
	}
	op := operation{EventID: randomID(), ObjectID: g.ID, ParentID: g.Page, Action: "wiki-suggestion-withdraw", OwnerID: i.ID, CreatedAt: time.Now().Unix()}
	if err = wipeWikiSuggestionSQL(tx, g.ID, "withdrawn", ""); err == nil {
		_, err = tx.Exec("INSERT INTO wiki_suggestion_actions VALUES(?,?,?,'withdrawn','',?,?)", randomID(), g.ID, i.ID, g.Result, op.CreatedAt)
	}
	if err == nil {
		err = app.finishWikiOperation(tx, op, records)
	}
	if err == nil {
		err = app.scrubBackupsLocked()
	}
	if err != nil {
		fail(w, err)
		return
	}
	http.Redirect(w, r, "/wiki/"+pathID(g.Page), 303)
}

func replayWikiSuggestion(tx *sql.Tx, op operation) error {
	exists, err := wikiSuggestionTable(tx)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if op.Action == "wiki-suggestion-withdraw" {
		return wipeWikiSuggestionSQL(tx, op.ObjectID, "withdrawn", "")
	}
	if op.Reason != "" {
		if err := wipeWikiSuggestionSQL(tx, op.ObjectID, "rejected", wikiReason(op.Reason)+"，建议原文已限制展示"); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec("UPDATE wiki_suggestions SET state=?,resolution='备份之后已有处理决定，详细说明未包含在此快照中。',result_version=?,version=version+1,was_rejected=CASE WHEN ?='rejected' THEN 1 ELSE was_rejected END WHERE id=?", op.TargetKind, op.VersionNumber, op.TargetKind, op.ObjectID); err != nil {
			return err
		}
	}
	if op.TargetKind == "accepted" || op.TargetKind == "partial" {
		_, err := tx.Exec("UPDATE wiki_pages SET state='hidden',latest_version=max(latest_version,?)+1 WHERE id=?", op.VersionNumber, op.ParentID)
		return err
	}
	return nil
}
