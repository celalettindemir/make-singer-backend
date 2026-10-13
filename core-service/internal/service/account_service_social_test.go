package service

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

type fakeSocialCleaner struct {
	ids     []string
	listErr error
	deleted []string

	tracesErr           error
	tracesDeleted       []string
	deleter             *selectiveDeleter // if set, the prefix count at the moment of trace deletion is recorded
	prefixCountAtTraces int
}

func (s *fakeSocialCleaner) DeleteUserTraces(_ context.Context, userID string) error {
	s.tracesDeleted = append(s.tracesDeleted, userID)
	if s.deleter != nil {
		s.prefixCountAtTraces = len(s.deleter.prefixes)
	}
	return s.tracesErr
}

func (s *fakeSocialCleaner) ListPublicationIDs(context.Context, string) ([]string, error) {
	return s.ids, s.listErr
}

func (s *fakeSocialCleaner) DeleteUserData(_ context.Context, userID string) error {
	s.deleted = append(s.deleted, userID)
	s.ids = nil // the publication rows go too
	return nil
}

// selectiveDeleter returns an error only for the given prefix.
type selectiveDeleter struct {
	prefixes   []string
	failPrefix string
}

func (s *selectiveDeleter) DeletePrefix(_ context.Context, prefix string) (int, error) {
	s.prefixes = append(s.prefixes, prefix)
	if s.failPrefix != "" && strings.HasPrefix(prefix, s.failPrefix) {
		return 0, errors.New("r2 is down")
	}
	return 1, nil
}

const (
	socialPub1 = "9a1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
	socialPub2 = "9b1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
)

func TestAccountService_DeleteRemovesSocialPrefixesAndRows(t *testing.T) {
	deleter := &selectiveDeleter{}
	social := &fakeSocialCleaner{ids: []string{socialPub1, socialPub2}, deleter: deleter}
	svc := NewAccountService(&fakeIdentity{}, deleter, unreachableRedis(), &fakeQueue{}, social)
	if err := svc.Delete(context.Background(), accountID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(deleter.prefixes) != 7 {
		t.Fatalf("expected 5 user + 2 social prefixes: %v", deleter.prefixes)
	}
	if !slices.Equal(deleter.prefixes[5:], []string{"sosyal/" + socialPub1 + "/", "sosyal/" + socialPub2 + "/"}) {
		t.Fatalf("social prefixes = %v", deleter.prefixes[5:])
	}
	if !slices.Equal(social.deleted, []string{accountID}) {
		t.Fatalf("social rows must be deleted: %v", social.deleted)
	}
	if !slices.Equal(social.tracesDeleted, []string{accountID}) || social.prefixCountAtTraces != 5 {
		t.Fatalf("traces must be deleted before the social prefix loop: %v, prefix %d", social.tracesDeleted, social.prefixCountAtTraces)
	}
}

// Even while R2 is down the profile (and traces) are deleted immediately so
// the user becomes invisible; publication rows remain and list the same ids again.
func TestAccountService_CleanupDeletesProfileDespiteR2Error(t *testing.T) {
	deleter := &selectiveDeleter{failPrefix: "sosyal/"}
	social := &fakeSocialCleaner{ids: []string{socialPub1, socialPub2}, deleter: deleter}
	svc := NewAccountService(&fakeIdentity{}, deleter, unreachableRedis(), &fakeQueue{}, social)
	if err := svc.Cleanup(context.Background(), accountID); err == nil {
		t.Fatal("an R2 error must be returned")
	}
	if !slices.Equal(social.tracesDeleted, []string{accountID}) || social.prefixCountAtTraces != 5 {
		t.Fatalf("the profile must be deleted before R2: %v, prefix %d", social.tracesDeleted, social.prefixCountAtTraces)
	}
	if len(social.deleted) != 0 {
		t.Fatal("publication rows must not be deleted while the R2 cleanup is incomplete")
	}

	// Delayed retry: R2 recovered, the same ids are listed again.
	deleter.failPrefix = ""
	if err := svc.Cleanup(context.Background(), accountID); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if !slices.Equal(deleter.prefixes[12:], []string{"sosyal/" + socialPub1 + "/", "sosyal/" + socialPub2 + "/"}) {
		t.Fatalf("the retry must delete the same social prefixes: %v", deleter.prefixes)
	}
	if !slices.Equal(social.deleted, []string{accountID}) {
		t.Fatalf("rows must be deleted at the end of the retry: %v", social.deleted)
	}
}

// A trace deletion error does not stop the R2 loop; the error is added to the joined error.
func TestAccountService_CleanupTraceErrorDoesNotStopR2(t *testing.T) {
	deleter := &selectiveDeleter{}
	traceErr := errors.New("pg is down")
	social := &fakeSocialCleaner{ids: []string{socialPub1, socialPub2}, tracesErr: traceErr}
	svc := NewAccountService(&fakeIdentity{}, deleter, unreachableRedis(), &fakeQueue{}, social)
	if err := svc.Cleanup(context.Background(), accountID); !errors.Is(err, traceErr) {
		t.Fatalf("a trace delete error must be returned: %v", err)
	}
	if len(deleter.prefixes) != 7 {
		t.Fatalf("all prefixes must be deleted despite the trace error: %v", deleter.prefixes)
	}
	if !slices.Equal(social.deleted, []string{accountID}) {
		t.Fatalf("rows must be deleted when R2 succeeds: %v", social.deleted)
	}
}

// On an R2 error the rows remain: the delayed task finds the ids again.
func TestAccountService_CleanupSocialR2ErrorKeepsRows(t *testing.T) {
	deleter := &selectiveDeleter{failPrefix: "sosyal/" + socialPub2}
	social := &fakeSocialCleaner{ids: []string{socialPub1, socialPub2}}
	svc := NewAccountService(&fakeIdentity{}, deleter, unreachableRedis(), &fakeQueue{}, social)
	if err := svc.Cleanup(context.Background(), accountID); err == nil {
		t.Fatal("an R2 error must be returned")
	}
	if len(social.deleted) != 0 {
		t.Fatal("rows must not be deleted while the R2 cleanup is incomplete")
	}
	if len(deleter.prefixes) != 7 {
		t.Fatalf("all prefixes must be tried even on error: %v", deleter.prefixes)
	}
}

func TestAccountService_CleanupSocialListError(t *testing.T) {
	social := &fakeSocialCleaner{listErr: errors.New("pg is down")}
	svc := NewAccountService(&fakeIdentity{}, &selectiveDeleter{}, unreachableRedis(), &fakeQueue{}, social)
	if err := svc.Cleanup(context.Background(), accountID); err == nil {
		t.Fatal("a list error must be returned")
	}
	if len(social.deleted) != 0 {
		t.Fatal("rows must not be deleted while the list is unreadable")
	}
}

// Without R2 there are no files; the rows are still deleted.
func TestAccountService_CleanupWithoutR2DeletesSocialRows(t *testing.T) {
	social := &fakeSocialCleaner{ids: []string{socialPub1}}
	svc := NewAccountService(&fakeIdentity{}, nil, unreachableRedis(), &fakeQueue{}, social)
	if err := svc.Cleanup(context.Background(), accountID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !slices.Equal(social.deleted, []string{accountID}) {
		t.Fatalf("rows must be deleted: %v", social.deleted)
	}
}
