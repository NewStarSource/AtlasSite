package atlas

import (
	"database/sql"
	"encoding/json"
)

type caseExplanation struct {
	Detail   string        `json:"detail"`
	Snapshot *isolatedBody `json:"content_snapshot,omitempty"`
}

func (app *App) caseExplanation(detail, object, viewer string) caseExplanation {
	result := caseExplanation{Detail: detail}
	if object == "" {
		return result
	}
	if app.postVisible(object, viewer) {
		if p, err := app.getPost(object); err == nil {
			result.Snapshot = &isolatedBody{Kind: "post", Owner: p.AuthorID, Title: p.Title, Content: p.Content}
		}
	} else if app.replyVisible(object, viewer) {
		if reply, err := app.getReply(object); err == nil {
			result.Snapshot = &isolatedBody{Kind: "reply", Owner: reply.AuthorID, Content: reply.Content}
		}
	}
	return result
}

// Local operators inspect evidence in a protected file, never HTTP responses or logs.
func (app *App) CaseEvidence(id, destination string) error {
	var owner sql.NullString
	if err := app.db.QueryRow("SELECT user_id FROM cases WHERE id=?", id).Scan(&owner); err != nil {
		return err
	}
	var explanation caseExplanation
	if err := app.privateGet("case:"+id, owner.String, "case", &explanation); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(explanation, "", "  ")
	if err != nil {
		return err
	}
	return exclusiveFile(destination, raw)
}
