package social

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Report targets and reasons (spec §3 sikayet).
const (
	TargetPublication = "yayin"
	TargetComment     = "yorum"

	maxReportDescriptionLen = 300
)

var reportReasons = map[string]bool{
	"spam": true, "taciz": true, "telif": true, "uygunsuz": true, "diger": true,
}

// ReportInput is the POST /sikayetler body. aciklama is optional.
type ReportInput struct {
	TargetType  string `json:"hedefTuru"`
	TargetID    string `json:"hedefId"`
	Reason      string `json:"neden"`
	Description string `json:"aciklama"`
}

// ReportInsert is a validated report to be written to the store.
// PublicationID is the publication the target belongs to (for a comment
// target, the comment's publication).
type ReportInsert struct {
	ID            string
	ReporterID    string
	TargetType    string
	TargetID      string
	PublicationID string
	Reason        string
	Description   string
}

// Report is POST /sikayetler (201). The target must be visible to the
// reporter: a publication by the detail rule, a comment that is not deleted
// and whose publication is visible. Otherwise, or with a malformed ID, it is
// ErrNotFound. Reporting again is not an error. No profile is needed;
// reporting one's own content is not rejected.
func (s *Service) Report(ctx context.Context, userID string, input ReportInput) error {
	id, err := parseUserID(userID)
	if err != nil {
		return err
	}
	if input.TargetType != TargetPublication && input.TargetType != TargetComment {
		return validationError("hedefTuru yayin ya da yorum olmali")
	}
	if !reportReasons[input.Reason] {
		return validationError("neden spam, taciz, telif, uygunsuz ya da diger olmali")
	}
	description := strings.TrimSpace(input.Description)
	if utf8.RuneCountInString(description) > maxReportDescriptionLen {
		return validationError("aciklama en cok 300 karakter olabilir")
	}
	if containsNUL(description) {
		return validationError("aciklama gecersiz karakter iceriyor")
	}
	targetID, ok := parseID(input.TargetID)
	if !ok {
		return ErrNotFound
	}

	pubID := targetID
	if input.TargetType == TargetComment {
		comment, err := s.store.GetComment(ctx, targetID)
		if err != nil {
			return err
		}
		if comment.Deleted {
			return ErrNotFound
		}
		pubID = comment.PublicationID
	}
	rec, err := s.visiblePublication(ctx, id, pubID)
	if err != nil {
		return err
	}
	return s.store.InsertReport(ctx, ReportInsert{
		ID: uuid.NewString(), ReporterID: id, TargetType: input.TargetType, TargetID: targetID,
		PublicationID: rec.ID, Reason: input.Reason, Description: description,
	})
}
