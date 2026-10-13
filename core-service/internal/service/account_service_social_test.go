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
		return 0, errors.New("r2 kapali")
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
		t.Fatalf("beklenmeyen hata: %v", err)
	}
	if len(deleter.prefixes) != 7 {
		t.Fatalf("5 kullanici + 2 sosyal onek bekleniyordu: %v", deleter.prefixes)
	}
	if !slices.Equal(deleter.prefixes[5:], []string{"sosyal/" + socialPub1 + "/", "sosyal/" + socialPub2 + "/"}) {
		t.Fatalf("sosyal onekler = %v", deleter.prefixes[5:])
	}
	if !slices.Equal(social.deleted, []string{accountID}) {
		t.Fatalf("sosyal satirlar silinmeli: %v", social.deleted)
	}
	if !slices.Equal(social.tracesDeleted, []string{accountID}) || social.prefixCountAtTraces != 5 {
		t.Fatalf("izler sosyal onek dongusunden once silinmeli: %v, onek %d", social.tracesDeleted, social.prefixCountAtTraces)
	}
}

// Even while R2 is down the profile (and traces) are deleted immediately so
// the user becomes invisible; publication rows remain and list the same ids again.
func TestAccountService_CleanupDeletesProfileDespiteR2Error(t *testing.T) {
	deleter := &selectiveDeleter{failPrefix: "sosyal/"}
	social := &fakeSocialCleaner{ids: []string{socialPub1, socialPub2}, deleter: deleter}
	svc := NewAccountService(&fakeIdentity{}, deleter, unreachableRedis(), &fakeQueue{}, social)
	if err := svc.Cleanup(context.Background(), accountID); err == nil {
		t.Fatal("R2 hatasi donmeli")
	}
	if !slices.Equal(social.tracesDeleted, []string{accountID}) || social.prefixCountAtTraces != 5 {
		t.Fatalf("profil R2'den once silinmeli: %v, onek %d", social.tracesDeleted, social.prefixCountAtTraces)
	}
	if len(social.deleted) != 0 {
		t.Fatal("R2 temizligi eksikken yayin satirlari silinmemeli")
	}

	// Delayed retry: R2 recovered, the same ids are listed again.
	deleter.failPrefix = ""
	if err := svc.Cleanup(context.Background(), accountID); err != nil {
		t.Fatalf("tekrar: %v", err)
	}
	if !slices.Equal(deleter.prefixes[12:], []string{"sosyal/" + socialPub1 + "/", "sosyal/" + socialPub2 + "/"}) {
		t.Fatalf("tekrar ayni sosyal onekleri silmeli: %v", deleter.prefixes)
	}
	if !slices.Equal(social.deleted, []string{accountID}) {
		t.Fatalf("tekrar sonunda satirlar silinmeli: %v", social.deleted)
	}
}

// A trace deletion error does not stop the R2 loop; the error is added to the joined error.
func TestAccountService_CleanupTraceErrorDoesNotStopR2(t *testing.T) {
	deleter := &selectiveDeleter{}
	traceErr := errors.New("pg kapali")
	social := &fakeSocialCleaner{ids: []string{socialPub1, socialPub2}, tracesErr: traceErr}
	svc := NewAccountService(&fakeIdentity{}, deleter, unreachableRedis(), &fakeQueue{}, social)
	if err := svc.Cleanup(context.Background(), accountID); !errors.Is(err, traceErr) {
		t.Fatalf("iz silme hatasi donmeli: %v", err)
	}
	if len(deleter.prefixes) != 7 {
		t.Fatalf("iz hatasina ragmen tum onekler silinmeli: %v", deleter.prefixes)
	}
	if !slices.Equal(social.deleted, []string{accountID}) {
		t.Fatalf("R2 basariliyken satirlar silinmeli: %v", social.deleted)
	}
}

// On an R2 error the rows remain: the delayed task finds the ids again.
func TestAccountService_CleanupSocialR2ErrorKeepsRows(t *testing.T) {
	deleter := &selectiveDeleter{failPrefix: "sosyal/" + socialPub2}
	social := &fakeSocialCleaner{ids: []string{socialPub1, socialPub2}}
	svc := NewAccountService(&fakeIdentity{}, deleter, unreachableRedis(), &fakeQueue{}, social)
	if err := svc.Cleanup(context.Background(), accountID); err == nil {
		t.Fatal("R2 hatasi donmeli")
	}
	if len(social.deleted) != 0 {
		t.Fatal("R2 temizligi eksikken satirlar silinmemeli")
	}
	if len(deleter.prefixes) != 7 {
		t.Fatalf("hata olsa da tum onekler denenmeli: %v", deleter.prefixes)
	}
}

func TestAccountService_CleanupSocialListError(t *testing.T) {
	social := &fakeSocialCleaner{listErr: errors.New("pg kapali")}
	svc := NewAccountService(&fakeIdentity{}, &selectiveDeleter{}, unreachableRedis(), &fakeQueue{}, social)
	if err := svc.Cleanup(context.Background(), accountID); err == nil {
		t.Fatal("liste hatasi donmeli")
	}
	if len(social.deleted) != 0 {
		t.Fatal("liste okunamadan satir silinmemeli")
	}
}

// Without R2 there are no files; the rows are still deleted.
func TestAccountService_CleanupWithoutR2DeletesSocialRows(t *testing.T) {
	social := &fakeSocialCleaner{ids: []string{socialPub1}}
	svc := NewAccountService(&fakeIdentity{}, nil, unreachableRedis(), &fakeQueue{}, social)
	if err := svc.Cleanup(context.Background(), accountID); err != nil {
		t.Fatalf("beklenmeyen hata: %v", err)
	}
	if !slices.Equal(social.deleted, []string{accountID}) {
		t.Fatalf("satirlar silinmeli: %v", social.deleted)
	}
}
