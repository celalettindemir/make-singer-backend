package credit

import (
	"context"
	"sync"
)

// SpendCall is one recorded Spend or SpendAction call. ID is the ref or the
// action id.
type SpendCall struct {
	User string
	Op   Operation
	ID   string
	// Project is the projectID of a Spend call.
	Project string
}

// RefundCall is one recorded Refund or RefundAction call. User is empty for
// Refund.
type RefundCall struct {
	User string
	Op   Operation
	ID   string
}

// FakeSpender is a Spender for tests in other packages. It records calls
// and answers from its settable fields; set them before use.
type FakeSpender struct {
	mu sync.Mutex

	// Insufficient makes Spend and SpendAction fail with an
	// *InsufficientError of Required against Bal.
	Insufficient bool
	// Expired makes SpendAction fail with ErrActionExpired.
	Expired bool
	// Required is the price reported in the InsufficientError.
	Required int
	// Debt is the debt reported in the InsufficientError.
	Debt int
	// Bal is the balance returned by every call.
	Bal int
	// ChargedFirst makes SpendAction report charged=true on the first call
	// per action id.
	ChargedFirst bool
	// Pro is the HasPro() answer; a Pro plan also covers Plus in HasPlan.
	Pro bool
	// Plus makes HasPlan(TierPlus) true.
	Plus bool
	// IsEnforced is the Enforced() answer.
	IsEnforced bool
	// Err, when set, is returned by every call after recording it.
	Err error

	Spends        []SpendCall
	Actions       []SpendCall
	Refunds       []RefundCall
	ActionRefunds []RefundCall
	seen          map[string]bool
}

var _ Spender = (*FakeSpender)(nil)

func (f *FakeSpender) fail() error {
	if f.Err != nil {
		return f.Err
	}
	if f.Insufficient {
		return &InsufficientError{Required: f.Required, Balance: f.Bal, Debt: f.Debt}
	}
	return nil
}

// Spend records the call.
func (f *FakeSpender) Spend(_ context.Context, userID string, op Operation, ref, projectID string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Spends = append(f.Spends, SpendCall{User: userID, Op: op, ID: ref, Project: projectID})
	if err := f.fail(); err != nil {
		return f.Bal, err
	}
	return f.Bal, nil
}

// SpendAction records the call.
func (f *FakeSpender) SpendAction(_ context.Context, userID string, op Operation, actionID string) (int, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Actions = append(f.Actions, SpendCall{User: userID, Op: op, ID: actionID})
	if f.Expired {
		return 0, false, ErrActionExpired
	}
	if err := f.fail(); err != nil {
		return f.Bal, false, err
	}
	if f.seen == nil {
		f.seen = map[string]bool{}
	}
	key := userID + ":" + actionID
	first := f.ChargedFirst && !f.seen[key]
	f.seen[key] = true
	return f.Bal, first, nil
}

// Refund records the call.
func (f *FakeSpender) Refund(_ context.Context, op Operation, ref string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Refunds = append(f.Refunds, RefundCall{Op: op, ID: ref})
	return f.Err
}

// RefundAction records the call.
func (f *FakeSpender) RefundAction(_ context.Context, userID string, op Operation, actionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ActionRefunds = append(f.ActionRefunds, RefundCall{userID, op, actionID})
	return f.Err
}

// Balance returns Bal.
func (f *FakeSpender) Balance(context.Context, string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Bal, f.Err
}

// Enforced returns IsEnforced.
func (f *FakeSpender) Enforced() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.IsEnforced
}

// HasPro returns Pro.
func (f *FakeSpender) HasPro(context.Context, string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Pro, f.Err
}

// HasPlan answers from Pro and Plus.
func (f *FakeSpender) HasPlan(_ context.Context, _ string, min Tier) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if min == TierPro {
		return f.Pro, f.Err
	}
	return f.Pro || f.Plus, f.Err
}
