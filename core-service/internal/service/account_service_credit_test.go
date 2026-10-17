package service

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeCreditCleaner struct {
	users []string
	err   error
}

func (f *fakeCreditCleaner) DeleteUserData(_ context.Context, userID string) error {
	f.users = append(f.users, userID)
	return f.err
}

func TestAccountService_CleanupCallsCreditCleaner(t *testing.T) {
	cc := &fakeCreditCleaner{}
	svc := NewAccountService(&fakeIdentity{}, &fakeDeleter{}, unreachableRedis(), &fakeQueue{}, nil).WithCreditCleaner(cc)
	if err := svc.Cleanup(context.Background(), accountID); err != nil {
		t.Fatal(err)
	}
	if err := svc.Cleanup(context.Background(), accountID); err != nil { // delayed re-run
		t.Fatal(err)
	}
	if len(cc.users) != 2 || cc.users[0] != accountID {
		t.Fatalf("credit cleaner calls = %v", cc.users)
	}
}

func TestAccountService_CreditCleanupErrorJoined(t *testing.T) {
	boom := errors.New("kredi db down")
	r2 := errors.New("r2 down")
	cc := &fakeCreditCleaner{err: boom}
	svc := NewAccountService(&fakeIdentity{}, &fakeDeleter{err: r2}, unreachableRedis(), &fakeQueue{}, nil).WithCreditCleaner(cc)
	err := svc.Cleanup(context.Background(), accountID)
	if !errors.Is(err, boom) || !errors.Is(err, r2) {
		t.Fatalf("err = %v, want both credit and R2 errors", err)
	}
	if !strings.Contains(err.Error(), "credit") {
		t.Errorf("err lacks credit context: %v", err)
	}
	if len(cc.users) != 1 {
		t.Errorf("credit cleaner must run even when R2 fails: %v", cc.users)
	}
}
