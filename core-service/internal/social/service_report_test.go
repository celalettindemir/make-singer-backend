package social

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestService_ReportPublicationAndComment(t *testing.T) {
	store := commentStore()
	store.commentRecords[comment1] = &CommentRecord{ID: comment1, PublicationID: pubX, AuthorID: userB, PublicationOwnerID: userB}
	s, _ := newTestService(store)
	ctx := context.Background()

	if err := s.Report(ctx, userC, ReportInput{TargetType: "yayin", TargetID: strings.ToUpper(pubX), Reason: "spam"}); err != nil {
		t.Fatalf("yayin sikayeti: %v", err)
	}
	if err := s.Report(ctx, userC, ReportInput{TargetType: "yorum", TargetID: comment1, Reason: "taciz", Description: "  kaba  "}); err != nil {
		t.Fatalf("yorum sikayeti: %v", err)
	}
	if len(store.reports) != 2 {
		t.Fatalf("sikayetler = %+v", store.reports)
	}
	pub, c := store.reports[0], store.reports[1]
	if pub.ReporterID != userC || pub.TargetID != pubX || pub.PublicationID != pubX || pub.Reason != "spam" || pub.ID == "" {
		t.Fatalf("yayin sikayeti = %+v", pub)
	}
	if c.TargetType != "yorum" || c.TargetID != comment1 || c.PublicationID != pubX || c.Description != "kaba" {
		t.Fatalf("yorum sikayeti = %+v", c)
	}
}

func TestService_ReportErrors(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name  string
		setup func(store *fakeStore)
		input ReportInput
		want  error
	}{
		{"bilinmeyen hedef turu", nil, ReportInput{TargetType: "profil", TargetID: pubX, Reason: "spam"}, ErrValidation},
		{"bilinmeyen neden", nil, ReportInput{TargetType: "yayin", TargetID: pubX, Reason: "kotu"}, ErrValidation},
		{"uzun aciklama", nil, ReportInput{TargetType: "yayin", TargetID: pubX, Reason: "diger", Description: strings.Repeat("a", 301)}, ErrValidation},
		{"NUL iceren aciklama", nil, ReportInput{TargetType: "yayin", TargetID: pubX, Reason: "diger", Description: "kaba\x00"}, ErrValidation},
		{"bozuk kimlik", nil, ReportInput{TargetType: "yayin", TargetID: "x", Reason: "spam"}, ErrNotFound},
		{"olmayan yayin", nil, ReportInput{TargetType: "yayin", TargetID: pubY, Reason: "spam"}, ErrNotFound},
		{"gorunmeyen yayin", func(store *fakeStore) { store.publications[pubX].Visibility = VisibilityFollowers },
			ReportInput{TargetType: "yayin", TargetID: pubX, Reason: "spam"}, ErrNotFound},
		{"olmayan yorum", nil, ReportInput{TargetType: "yorum", TargetID: commentC, Reason: "spam"}, ErrNotFound},
		{"silinmis yorum", func(store *fakeStore) {
			store.commentRecords[comment1] = &CommentRecord{ID: comment1, PublicationID: pubX, Deleted: true}
		}, ReportInput{TargetType: "yorum", TargetID: comment1, Reason: "spam"}, ErrNotFound},
		{"gorunmeyen yayinin yorumu", func(store *fakeStore) {
			store.commentRecords[comment1] = &CommentRecord{ID: comment1, PublicationID: pubX}
			store.publications[pubX].Deleted = true
		}, ReportInput{TargetType: "yorum", TargetID: comment1, Reason: "spam"}, ErrNotFound},
		{"gizli (takipciler) yayinin yorumu", func(store *fakeStore) {
			store.commentRecords[comment1] = &CommentRecord{ID: comment1, PublicationID: pubX, AuthorID: userB, PublicationOwnerID: userB}
			store.publications[pubX].Visibility = VisibilityFollowers
		}, ReportInput{TargetType: "yorum", TargetID: comment1, Reason: "spam"}, ErrNotFound},
	}
	for _, c := range cases {
		store := commentStore()
		if c.setup != nil {
			c.setup(store)
		}
		s, _ := newTestService(store)
		if err := s.Report(ctx, userC, c.input); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, beklenen %v", c.name, err, c.want)
		}
		if len(store.reports) != 0 {
			t.Errorf("%s: sikayet yazilmamali", c.name)
		}
	}
}

// A malformed targetId is a 404 without reaching the store: the store returns a
// different error on every call, so a call result would be distinguishable from ErrNotFound.
func TestService_ReportMalformedIDSkipsStore(t *testing.T) {
	store := commentStore()
	store.err = errors.New("depo cagrilmamali")
	s, _ := newTestService(store)
	for _, kind := range []string{"yayin", "yorum"} {
		err := s.Report(context.Background(), userC, ReportInput{TargetType: kind, TargetID: "uuid-degil", Reason: "spam"})
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: %v, beklenen ErrBulunamadi (depo cagrilmadan)", kind, err)
		}
	}
	if len(store.reports) != 0 {
		t.Fatalf("sikayet yazilmamali: %+v", store.reports)
	}
}
