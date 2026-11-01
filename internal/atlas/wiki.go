package atlas

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

const wikiCommunityVisibleSQL = `(v.community_id IS NULL OR EXISTS(SELECT 1 FROM communities c WHERE c.id=v.community_id AND c.status IN ('active','uncategorized')))`

type wikiSource struct {
	ID, Title, URL, Accessed string
	Expired                  bool
}
type wikiVersion struct {
	PageID, Kind, Creator, State, Title, Summary, Body, Community, Editor, Alias, EditSummary, Fields, Review string
	Number, Latest, Published, Base                                                                           int
	Created, Updated                                                                                          int64
	Sources                                                                                                   []wikiSource
}

var wikiSourceID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)
var wikiFootnotes = regexp.MustCompile(`\[\^([^\]\r\n]+)\]`)
var wikiDefinitions = regexp.MustCompile(`(?m)^\s*\[\^[^\]\r\n]+\]:`)

func parseWikiSources(raw string) ([]wikiSource, error) {
	if !checkText(raw, 20000) {
		return nil, errInput
	}
	var sources []wikiSource
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "|")
		if len(fields) < 4 || len(fields) > 5 {
			return nil, errInput
		}
		for n := range fields {
			fields[n] = strings.TrimSpace(fields[n])
		}
		s := wikiSource{ID: fields[0], Title: fields[1], URL: fields[2], Accessed: fields[3]}
		if len(fields) == 5 {
			if fields[4] != "expired" {
				return nil, errInput
			}
			s.Expired = true
		}
		date, err := time.Parse("2006-01-02", s.Accessed)
		if !wikiSourceID.MatchString(s.ID) || seen[s.ID] || s.Title == "" || !checkText(s.Title, 200) || !checkText(s.URL, 1000) || !safeSource(s.URL) || err != nil || date.Format("2006-01-02") > time.Now().In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("2006-01-02") || len(sources) >= 20 {
			return nil, errInput
		}
		seen[s.ID] = true
		sources = append(sources, s)
	}
	if len(sources) == 0 {
		return nil, errInput
	}
	return sources, nil
}

func wikiSourcesText(sources []wikiSource) string {
	lines := []string{}
	for _, s := range sources {
		line := s.ID + " | " + s.Title + " | " + s.URL + " | " + s.Accessed
		if s.Expired {
			line += " | expired"
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func validWikiFootnotes(body string, sources []wikiSource) bool {
	if wikiDefinitions.MatchString(body) {
		return false
	}
	ids := map[string]bool{}
	for _, s := range sources {
		ids[s.ID] = true
	}
	for _, match := range wikiFootnotes.FindAllStringSubmatch(body, -1) {
		if !ids[match[1]] {
			return false
		}
	}
	return true
}

func wikiBody(v wikiVersion) string {
	body := v.Body
	for _, s := range v.Sources {
		body += "\n\n[^" + s.ID + "]: [来源 " + s.ID + "](#wiki-source-" + s.ID + ")，访问日期 " + s.Accessed + "。"
		if s.Expired {
			body += "来源已失效，待重新核验。"
		}
	}
	return renderProfileMarkdown(body)
}

func loadWikiVersion(q managementQuery, id string, number int) (wikiVersion, error) {
	v := wikiVersion{}
	var sources string
	err := q.QueryRow(`SELECT p.id,p.kind,p.creator_id,p.state,p.latest_version,p.published_version,p.updated_at,v.number,v.base_version,v.title,v.summary,v.body,v.fields_json,COALESCE(v.community_id,''),v.editor_id,v.editor_alias,v.edit_summary,v.review_status,v.sources_json,v.created_at FROM wiki_pages p JOIN wiki_versions v ON v.page_id=p.id AND v.number=CASE WHEN ?=0 THEN p.published_version ELSE ? END WHERE p.id=?`, number, number, id).Scan(&v.PageID, &v.Kind, &v.Creator, &v.State, &v.Latest, &v.Published, &v.Updated, &v.Number, &v.Base, &v.Title, &v.Summary, &v.Body, &v.Fields, &v.Community, &v.Editor, &v.Alias, &v.EditSummary, &v.Review, &sources, &v.Created)
	if err == nil {
		err = json.Unmarshal([]byte(sources), &v.Sources)
	}
	return v, err
}

func wikiReadable(q managementQuery, id string) bool {
	var n int
	return q.QueryRow(`SELECT count(*) FROM wiki_pages p JOIN wiki_versions v ON v.page_id=p.id AND v.number=p.published_version WHERE p.id=? AND p.state='active' AND v.review_status='accepted' AND `+wikiCommunityVisibleSQL, id).Scan(&n) == nil && n == 1
}

func wikiVersionReadable(q managementQuery, id string, number int) bool {
	if !wikiReadable(q, id) {
		return false
	}
	var n int
	return q.QueryRow(`SELECT count(*) FROM wiki_versions v WHERE v.page_id=? AND v.number=? AND v.review_status='accepted' AND `+wikiCommunityVisibleSQL, id, number).Scan(&n) == nil && n == 1
}

func (app *App) wikiGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !app.config.WikiEnabled {
			fail(w, sql.ErrNoRows)
			return
		}
		var mode string
		if app.db.QueryRow("SELECT mode FROM runtime_state WHERE id=1").Scan(&mode) != nil || mode == "isolated" {
			respond(w, 503, map[string]string{"code": "WIKI_UNAVAILABLE"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (app *App) wikiRoutes(r *chi.Mux) {
	r.Group(func(r chi.Router) {
		r.Use(app.wikiGate)
		r.Get("/wiki", app.wikiIndex)
		r.Get("/wiki/new", app.wikiEditor)
		r.Post("/wiki", app.saveWikiDiscovery)
		r.Get("/wiki/{wiki}", app.wikiPage)
		r.Get("/wiki/{wiki}/edit", app.wikiEditor)
		r.Post("/wiki/{wiki}/edit", app.saveWikiDiscovery)
		r.Get("/wiki/{wiki}/history", app.wikiHistory)
		r.Get("/wiki/{wiki}/versions/{number}", app.wikiHistoricalPage)
		r.Get("/wiki/{wiki}/diff", app.wikiDiff)
		r.Post("/wiki/{wiki}/withdraw", app.withdrawWiki)
		r.Post("/wiki/{wiki}/replies", app.createWikiReply)
		r.Post("/wiki/{wiki}/replies/{reply}/delete", app.deleteWikiReply)
		r.Get("/c/{slug}/wiki", app.communityWikiPage)
	})
}

func (app *App) saveWikiDiscovery(w http.ResponseWriter, r *http.Request) {
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
	v := wikiVersion{PageID: id, Kind: "discovery", Title: strings.TrimSpace(r.FormValue("title")), Summary: strings.TrimSpace(r.FormValue("summary")), Body: strings.TrimSpace(r.FormValue("body")), Community: r.FormValue("community"), Editor: i.ID, Alias: i.Alias, EditSummary: strings.TrimSpace(r.FormValue("edit_summary")), Fields: "{}", Review: "accepted"}
	if creating {
		v.PageID = r.FormValue("page_id")
	}
	var err error
	v.Latest, err = strconv.Atoi(r.FormValue("version"))
	if err == nil {
		v.Sources, err = parseWikiSources(r.FormValue("sources"))
	}
	if err != nil || v.Latest < 0 || creating != (v.Latest == 0) || v.PageID == "" || !checkText(v.PageID, 100) || strings.ContainsAny(v.PageID, "/?#") || v.Title == "" || v.Body == "" || v.EditSummary == "" || !checkText(v.Title, 200) || !checkText(v.Summary, 500) || !checkText(v.Body, 30000) || !checkText(v.EditSummary, 300) || !validWikiFootnotes(v.Body, v.Sources) || r.FormValue("license") != "CC BY-SA 4.0" {
		app.wikiEditorError(w, r, i, v, creating, errInput)
		return
	}
	if err = app.commitWikiDiscovery(r, i, &v, creating); err != nil {
		app.wikiEditorError(w, r, i, v, creating, err)
		return
	}
	http.Redirect(w, r, "/wiki/"+pathID(v.PageID), 303)
}

func (app *App) commitWikiDiscovery(r *http.Request, i *Identity, v *wikiVersion, creating bool) error {
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
	} else {
		if !wikiReadable(tx, v.PageID) {
			return sql.ErrNoRows
		}
		old, err := loadWikiVersion(tx, v.PageID, 0)
		if err != nil {
			return err
		}
		if old.Kind != "discovery" {
			return errForbidden
		}
		if old.Latest != v.Latest {
			return errConflict
		}
		if old.Title != v.Title && old.Creator != i.ID {
			return errForbidden
		}
		v.Base = old.Number
	}
	next := v.Latest + 1
	now := time.Now().Unix()
	sources, _ := json.Marshal(v.Sources)
	if creating {
		_, err = tx.Exec("INSERT INTO wiki_pages(id,kind,creator_id,published_version,latest_version,created_at,updated_at) VALUES(?,'discovery',?,?,?,?,?)", v.PageID, i.ID, next, next, now, now)
	} else {
		_, err = tx.Exec("UPDATE wiki_pages SET published_version=?,latest_version=?,updated_at=? WHERE id=?", next, next, now, v.PageID)
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO wiki_versions(page_id,number,base_version,title,summary,body,fields_json,sources_json,community_id,editor_id,editor_alias,edit_summary,review_status,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,'accepted',?)`, v.PageID, next, v.Base, v.Title, v.Summary, v.Body, v.Fields, string(sources), nullable(v.Community), i.ID, i.Alias, v.EditSummary, now)
	if err != nil {
		return err
	}
	op := operation{EventID: randomID(), ObjectID: v.PageID, Action: "wiki-changed", OwnerID: i.ID, VersionNumber: next, CreatedAt: now}
	if err = managementEvent(tx, i.ID, "wiki-discovery-save", v.PageID, map[string]int{"version": v.Latest}, map[string]int{"version": next}); err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO operation_events(event_id,object_id,action,created_at) VALUES(?,?,?,?)", op.EventID, op.ObjectID, op.Action, now); err != nil {
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

func wikiKind(kind string) string {
	return map[string]string{"discovery": "发现页", "community": "社群条目", "work": "作品条目"}[kind]
}
func wikiTime(unix int64) string {
	return time.Unix(unix, 0).In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("2006-01-02 15:04 +08:00")
}
func wikiReason(code string) string {
	return map[string]string{"nonexistent": "对象不存在", "duplicate": "重复页面", "privacy": "隐私请求", "copyright": "版权请求", "safety": "安全限制"}[code]
}
func wikiSourcesHTML(sources []wikiSource) string {
	body := `<section class="wiki-sources"><h2>来源</h2><ol>`
	for _, s := range sources {
		state := ""
		if s.Expired {
			state = " · 来源已失效，待重新核验"
		}
		body += `<li id="wiki-source-` + s.ID + `"><a href="` + esc(s.URL) + `" rel="noopener noreferrer">` + esc(s.Title) + `</a><small class="muted"> · 访问于 ` + esc(s.Accessed) + state + `</small></li>`
	}
	return body + `</ol></section>`
}

func wikiVersionText(v wikiVersion) string {
	return fmt.Sprintf("%s\n\n%s\n\n%s\n\n%s", v.Title, v.Summary, v.Body, wikiSourcesText(v.Sources))
}
