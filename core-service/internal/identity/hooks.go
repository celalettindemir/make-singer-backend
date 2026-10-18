package identity

import (
	"context"
	"log"
	"sync"
	"time"
)

// NewUser is what a signup hook learns about the account that was just
// created. The identity package knows nothing about the hook's consumers
// (credit imports identity, never the other way round).
type NewUser struct {
	ID        string
	Email     string
	CreatedAt time.Time
}

// SignupHook runs after a user row is committed (password signup and the
// first Apple/Google sign-in alike). It has no return value: a hook must
// never fail a signup, so it logs its own errors; a panic is recovered and
// logged here.
type SignupHook func(context.Context, NewUser)

// signupHookTimeout bounds one hook run. The request context may already be
// cancelled by the time the response is written; the user row is committed,
// so the hook gets its own deadline.
const signupHookTimeout = 10 * time.Second

// signupHooks is the registry the stores embed. The zero value is ready.
type signupHooks struct {
	mu    sync.RWMutex
	hooks []SignupHook
}

func (r *signupHooks) add(h SignupHook) {
	if h == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hooks = append(r.hooks, h)
}

// run calls every hook in registration order, after commit. A panicking
// hook is logged and the next one still runs.
func (r *signupHooks) run(ctx context.Context, u *User) {
	r.mu.RLock()
	hooks := append([]SignupHook(nil), r.hooks...)
	r.mu.RUnlock()
	if len(hooks) == 0 {
		return
	}
	hctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), signupHookTimeout)
	defer cancel()
	nu := NewUser{ID: u.ID, Email: u.Email, CreatedAt: u.CreatedAt}
	for _, h := range hooks {
		func() {
			defer func() {
				if p := recover(); p != nil {
					log.Printf("identity: signup hook panicked: %v", p)
				}
			}()
			h(hctx, nu)
		}()
	}
}
