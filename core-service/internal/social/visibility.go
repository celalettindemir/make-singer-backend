package social

import "fmt"

// Visibility and status values (contract §3).
const (
	VisibilityEveryone  = "herkes"
	VisibilityFollowers = "takipciler"
	VisibilityLink      = "link"

	StatusPreparing = "hazirlaniyor"
	StatusPublished = "yayinda"
	StatusFailed    = "hata"
)

// VisibilityInput is everything needed to decide whether a publication is
// visible to a viewer.
type VisibilityInput struct {
	OwnerID       string
	ViewerID      string
	Visibility    string
	Status        string
	Deleted       bool
	ViewerFollows bool // whether the viewer follows the owner
}

// canSee is the single source of truth for the spec §4.9 table:
//   - nobody sees a deleted publication (owner included; repeating DELETE is 404).
//   - the owner sees their own publication in every status (hazirlaniyor and hata included).
//   - others see only "yayinda": everyone always, followers only those who
//     follow, link only in the detail.
//
// visibilityCondition builds the same rule in SQL; their equality is checked
// with all combinations in the live Postgres test.
func canSee(input VisibilityInput, detail bool) bool {
	if input.Deleted {
		return false
	}
	if input.ViewerID != "" && input.OwnerID == input.ViewerID {
		return true
	}
	if input.Status != StatusPublished {
		return false
	}
	switch input.Visibility {
	case VisibilityEveryone:
		return true
	case VisibilityFollowers:
		return input.ViewerFollows
	case VisibilityLink:
		return detail
	}
	return false
}

// CanSeeDetail is the rule for a detail opened by id or slug.
func CanSeeDetail(input VisibilityInput) bool { return canSee(input, true) }

// CanSeeInList is the rule for profile lists and counters; link
// publications are listed only to their owner.
func CanSeeInList(input VisibilityInput) bool { return canSee(input, false) }

// visibilityCondition is the SQL counterpart of canSee. alias is the
// publication table's alias, viewer is the viewer ID parameter (e.g. "$1").
// Both are constant strings; user input never enters here.
func visibilityCondition(alias, viewer string, detail bool) string {
	link := ""
	if detail {
		link = fmt.Sprintf(" OR %s.gorunurluk = 'link'", alias)
	}
	return fmt.Sprintf(`(%[1]s.silinme IS NULL AND (%[1]s.sahip_id = %[2]s OR (%[1]s.durum = 'yayinda' AND (%[1]s.gorunurluk = 'herkes'%[3]s OR (%[1]s.gorunurluk = 'takipciler' AND EXISTS (SELECT 1 FROM sosyal.takip gt WHERE gt.takipci_id = %[2]s AND gt.takip_edilen_id = %[1]s.sahip_id))))))`,
		alias, viewer, link)
}
