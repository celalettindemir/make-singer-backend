package social

import "testing"

// Spec §4.9 table, for a viewer who is not the owner.
func TestVisibility_Table(t *testing.T) {
	cases := []struct {
		visibility string
		following  bool
		inList     bool
		detail     bool
	}{
		{VisibilityEveryone, false, true, true},
		{VisibilityEveryone, true, true, true},
		{VisibilityFollowers, false, false, false},
		{VisibilityFollowers, true, true, true},
		{VisibilityLink, false, false, true},
		{VisibilityLink, true, false, true},
	}
	for _, tc := range cases {
		in := VisibilityInput{OwnerID: "s", ViewerID: "i", Visibility: tc.visibility, Status: StatusPublished, ViewerFollows: tc.following}
		if got := CanSeeInList(in); got != tc.inList {
			t.Errorf("%s follow=%v list = %v, want %v", tc.visibility, tc.following, got, tc.inList)
		}
		if got := CanSeeDetail(in); got != tc.detail {
			t.Errorf("%s follow=%v detail = %v, want %v", tc.visibility, tc.following, got, tc.detail)
		}
	}
}

func TestVisibility_OwnerSeesEveryStatus(t *testing.T) {
	for _, vis := range []string{VisibilityEveryone, VisibilityFollowers, VisibilityLink} {
		for _, status := range []string{StatusPreparing, StatusPublished, StatusFailed} {
			in := VisibilityInput{OwnerID: "s", ViewerID: "s", Visibility: vis, Status: status}
			if !CanSeeDetail(in) || !CanSeeInList(in) {
				t.Errorf("owner %s/%s must see their publication", vis, status)
			}
		}
	}
}

func TestVisibility_StrangerCannotSeePreparingOrFailed(t *testing.T) {
	for _, status := range []string{StatusPreparing, StatusFailed} {
		in := VisibilityInput{OwnerID: "s", ViewerID: "i", Visibility: VisibilityEveryone, Status: status, ViewerFollows: true}
		if CanSeeDetail(in) || CanSeeInList(in) {
			t.Errorf("stranger %s must not see the publication", status)
		}
	}
}

func TestVisibility_NobodySeesDeleted(t *testing.T) {
	for _, viewer := range []string{"s", "i"} {
		in := VisibilityInput{OwnerID: "s", ViewerID: viewer, Visibility: VisibilityEveryone, Status: StatusPublished, Deleted: true}
		if CanSeeDetail(in) || CanSeeInList(in) {
			t.Errorf("%s must not see a deleted publication", viewer)
		}
	}
}

// An empty viewer id is not the owner even if the owner id is also empty.
func TestVisibility_EmptyViewerIsNotOwner(t *testing.T) {
	in := VisibilityInput{OwnerID: "", ViewerID: "", Visibility: VisibilityEveryone, Status: StatusPreparing}
	if CanSeeDetail(in) || CanSeeInList(in) {
		t.Fatal("an empty viewer must not see a publication in preparation")
	}
}
