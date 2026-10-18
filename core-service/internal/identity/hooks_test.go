package identity

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// hookRecorder collects NewUser values from a SignupHook.
type hookRecorder struct {
	mu    sync.Mutex
	users []NewUser
}

func (r *hookRecorder) hook(_ context.Context, u NewUser) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.users = append(r.users, u)
}

func (r *hookRecorder) all() []NewUser {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]NewUser(nil), r.users...)
}

type hookedUserStore interface {
	UserStore
	OnSignup(SignupHook)
}

func TestUserStore_SignupHookRunsAfterCommit(t *testing.T) {
	stores := map[string]func(t *testing.T) hookedUserStore{
		"fake":     func(*testing.T) hookedUserStore { return NewFakeUserStore() },
		"postgres": func(t *testing.T) hookedUserStore { return testPostgresUserStore(t) },
	}
	for name, build := range stores {
		t.Run(name, func(t *testing.T) {
			store := build(t)
			ctx := context.Background()
			rec := &hookRecorder{}
			var visible bool
			store.OnSignup(func(ctx context.Context, u NewUser) {
				// After commit: the row is readable by id.
				_, err := store.ByID(ctx, u.ID)
				visible = err == nil
				rec.hook(ctx, u)
			})
			store.OnSignup(func(context.Context, NewUser) { panic("hook boom") })
			after := &hookRecorder{}
			store.OnSignup(after.hook) // runs although the previous hook panicked

			email := "kanca-" + uuid.NewString() + "@ornek.com"
			u, err := store.Create(ctx, "  "+email+"  ", "Ad", "gecerliSifre12")
			if err != nil {
				t.Fatalf("a panicking hook must not fail the signup: %v", err)
			}
			got := rec.all()
			if len(got) != 1 || got[0].ID != u.ID || got[0].Email != email || got[0].CreatedAt.IsZero() {
				t.Fatalf("hook got %+v, want one call for %s / %s", got, u.ID, email)
			}
			if !visible {
				t.Error("the hook ran before the user row was readable")
			}
			if len(after.all()) != 1 {
				t.Error("a hook after a panicking one did not run")
			}

			// A failed signup (email in use) runs no hook.
			if _, err := store.Create(ctx, email, "Ad", "gecerliSifre12"); !errors.Is(err, ErrEmailInUse) {
				t.Fatalf("second create err = %v, want ErrEmailInUse", err)
			}
			if len(rec.all()) != 1 {
				t.Error("a rejected signup ran the hook")
			}
		})
	}
}

func TestFederatedStore_SignupHookRunsAfterCommit(t *testing.T) {
	for _, setup := range federatedStoreSetups() {
		t.Run(setup.name, func(t *testing.T) {
			k := setup.build(t)
			hooked, ok := k.store.(interface{ OnSignup(SignupHook) })
			if !ok {
				t.Fatalf("%T has no OnSignup", k.store)
			}
			ctx := context.Background()
			rec := &hookRecorder{}
			var visible bool
			hooked.OnSignup(func(ctx context.Context, u NewUser) {
				_, err := k.store.UserByIdentity(ctx, "google", "sub-hook")
				visible = err == nil
				rec.hook(ctx, u)
			})
			hooked.OnSignup(func(context.Context, NewUser) { panic("hook boom") })

			user, err := k.store.CreateFederatedUser(ctx, "google", "sub-hook", "h@ornek.com", "Hale", true)
			if err != nil {
				t.Fatalf("a panicking hook must not fail the sign-in: %v", err)
			}
			got := rec.all()
			if len(got) != 1 || got[0].ID != user.ID || got[0].Email != "h@ornek.com" {
				t.Fatalf("hook got %+v, want one call for %s", got, user.ID)
			}
			if !visible {
				t.Error("the hook ran before the identity was readable")
			}

			// A conflicting create runs no hook; linking runs none either.
			if _, err := k.store.CreateFederatedUser(ctx, "google", "sub-hook", "x@ornek.com", "X", true); err == nil {
				t.Fatal("duplicate identity was created")
			}
			if len(rec.all()) != 1 {
				t.Error("a rejected create ran the hook")
			}
		})
	}
}
