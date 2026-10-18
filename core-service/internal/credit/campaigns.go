package credit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/makeasinger/api/internal/identity"
)

// Campaign errors.
var (
	// ErrCampaignNotActive is returned when stopping a campaign that is not
	// active (stopping is final, spec 6v2 §2.4).
	ErrCampaignNotActive = errors.New("credit: campaign not active")
	// ErrCampaignNotFound is returned for an unknown campaign id.
	ErrCampaignNotFound = errors.New("credit: campaign not found")
	// ErrCampaignInvalid is returned for a campaign that fails validation.
	ErrCampaignInvalid = errors.New("credit: campaign invalid")
)

// CampaignStatus is the state of a campaign. The values are the DB values;
// JSON gives the wire name.
type CampaignStatus string

// Campaign statuses (kredi.kampanya.durum).
const (
	CampaignActive    CampaignStatus = "aktif"
	CampaignQuotaFull CampaignStatus = "kota_doldu"
	CampaignEnded     CampaignStatus = "bitti"
	CampaignStopped   CampaignStatus = "durduruldu"
)

// JSON is the API name: active, quota_full, ended, stopped.
func (s CampaignStatus) JSON() string {
	switch s {
	case CampaignActive:
		return "active"
	case CampaignQuotaFull:
		return "quota_full"
	case CampaignEnded:
		return "ended"
	case CampaignStopped:
		return "stopped"
	default:
		return string(s)
	}
}

// Campaign is one signup-gift campaign.
type Campaign struct {
	ID        int64
	Name      string
	Quota     int
	PerUser   int
	ValidDays int
	Joined    int
	// EndsAt is the last day of the campaign (a date, UTC); nil: it ends when
	// the quota is full.
	EndsAt    *time.Time
	Status    CampaignStatus
	StartedAt time.Time
	StoppedAt *time.Time
	CreatedBy string
}

// NewCampaign is the input of Create.
type NewCampaign struct {
	Name      string
	Quota     int
	PerUser   int
	ValidDays int
	// EndsAt is the last day (the time of day is ignored); nil: no date.
	EndsAt *time.Time
}

// CampaignOverview is the admin list: every campaign newest first plus the
// numbers of the current month.
type CampaignOverview struct {
	// Active is the one active campaign, nil when there is none.
	Active    *Campaign
	Campaigns []Campaign
	// MonthCredits and MonthPeople are what campaigns gave since the first
	// of this month (UTC).
	MonthCredits int
	MonthPeople  int
	// QuotaLeft is the free places of Active, 0 when there is none.
	QuotaLeft int
}

// Participant is one recent campaign member. UserID is empty once the
// account was deleted.
type Participant struct {
	UserID   string
	JoinedAt time.Time
}

// CampaignDetail is one campaign with its numbers.
type CampaignDetail struct {
	Campaign
	// Distributed is joined * perUser; Used is what was spent from the
	// campaign's lots (net of refunds, expiry not counted); UsedPercent is
	// Used / Distributed, 0 when nothing was given.
	Distributed int
	Used        int
	UsedPercent int
	Recent      []Participant
}

// SignupJoin is one person asking for the gift.
type SignupJoin struct {
	UserID    string
	EmailHash string
	CreatedAt time.Time
}

// CampaignStore is the persistence of campaigns. The join, the quota
// counter and the gift lot are one transaction.
type CampaignStore interface {
	CreateCampaign(ctx context.Context, in NewCampaign, by string, now time.Time) (Campaign, *Campaign, error)
	StopCampaign(ctx context.Context, id int64, now time.Time) (Campaign, error)
	CampaignOverview(ctx context.Context, now time.Time) (CampaignOverview, error)
	CampaignDetail(ctx context.Context, id int64) (CampaignDetail, error)
	// JoinCampaign gives the gift of the active campaign when j qualifies;
	// it reports whether a gift was written.
	JoinCampaign(ctx context.Context, j SignupJoin, now time.Time) (bool, error)
	// EndCampaigns marks active campaigns whose end date passed as ended.
	EndCampaigns(ctx context.Context, now time.Time) (int, error)
	// MissingGifts lists users created since `since` that the active
	// campaign covers and that have no participation row.
	MissingGifts(ctx context.Context, since, now time.Time, limit int) ([]SignupJoin, error)
}

// Campaigns holds the campaign rules.
type Campaigns struct {
	store CampaignStore
	now   func() time.Time
}

// NewCampaigns builds the campaign service. A nil clock is time.Now.
func NewCampaigns(store CampaignStore, now func() time.Time) *Campaigns {
	if now == nil {
		now = time.Now
	}
	return &Campaigns{store: store, now: func() time.Time { return now().UTC() }}
}

// maxCampaignName and maxCampaignCredits bound the admin input.
const (
	maxCampaignName    = 120
	maxCampaignCredits = 100000
	maxCampaignDays    = 3650
)

func (in NewCampaign) validate() error {
	name := strings.TrimSpace(in.Name)
	switch {
	case name == "" || len([]rune(name)) > maxCampaignName:
		return fmt.Errorf("%w: name", ErrCampaignInvalid)
	case in.Quota <= 0 || in.Quota > maxCampaignCredits:
		return fmt.Errorf("%w: quota", ErrCampaignInvalid)
	case in.PerUser <= 0 || in.PerUser > maxCampaignCredits:
		return fmt.Errorf("%w: per user", ErrCampaignInvalid)
	case in.ValidDays <= 0 || in.ValidDays > maxCampaignDays:
		return fmt.Errorf("%w: valid days", ErrCampaignInvalid)
	}
	return nil
}

// Create starts a campaign and, in the same transaction, stops the active
// one (returned as replaced).
func (c *Campaigns) Create(ctx context.Context, in NewCampaign, by string) (created Campaign, replaced *Campaign, err error) {
	if err := in.validate(); err != nil {
		return Campaign{}, nil, err
	}
	in.Name = strings.TrimSpace(in.Name)
	now := c.now()
	if in.EndsAt != nil {
		day := dateOf(*in.EndsAt)
		if day.Before(dateOf(now)) {
			return Campaign{}, nil, fmt.Errorf("%w: end date is in the past", ErrCampaignInvalid)
		}
		in.EndsAt = &day
	}
	return c.store.CreateCampaign(ctx, in, by, now)
}

// Stop ends a campaign for good. ErrCampaignNotActive when it is not active.
func (c *Campaigns) Stop(ctx context.Context, id int64) (Campaign, error) {
	return c.store.StopCampaign(ctx, id, c.now())
}

// List returns the overview.
func (c *Campaigns) List(ctx context.Context) (CampaignOverview, error) {
	return c.store.CampaignOverview(ctx, c.now())
}

// Get returns one campaign with its numbers.
func (c *Campaigns) Get(ctx context.Context, id int64) (CampaignDetail, error) {
	return c.store.CampaignDetail(ctx, id)
}

// GrantSignupGift is the identity signup hook (P6). It gives the active
// campaign's gift when the user signed up after the campaign started and a
// place is left. It is idempotent per user and per e-mail hash, and it never
// fails the signup: errors are logged and the expiry sweep retries.
func (c *Campaigns) GrantSignupGift(ctx context.Context, u identity.NewUser) {
	if _, err := c.join(ctx, u.ID, u.Email, u.CreatedAt); err != nil {
		log.Printf("credits: signup gift for user %s failed: %v", u.ID, err)
	}
}

func (c *Campaigns) join(ctx context.Context, userID, email string, createdAt time.Time) (bool, error) {
	return c.store.JoinCampaign(ctx, SignupJoin{
		UserID: userID, EmailHash: emailHash(email), CreatedAt: createdAt.UTC(),
	}, c.now())
}

// retryWindow is how far back the sweep looks for signups that missed their
// gift (a crash between commit and hook).
const retryWindow = 24 * time.Hour

const missingGiftBatch = 500

// Sweep ends campaigns whose end date passed and retries the gifts that
// signups of the last 24 hours missed. It reports how many campaigns ended
// and gifts were written.
func (c *Campaigns) Sweep(ctx context.Context, now time.Time) (ended, gifts int, err error) {
	now = now.UTC()
	ended, err = c.store.EndCampaigns(ctx, now)
	if err != nil {
		return 0, 0, fmt.Errorf("could not end the campaigns: %w", err)
	}
	missing, err := c.store.MissingGifts(ctx, now.Add(-retryWindow), now, missingGiftBatch)
	if err != nil {
		return ended, 0, fmt.Errorf("could not list the missing gifts: %w", err)
	}
	for _, j := range missing {
		ok, err := c.store.JoinCampaign(ctx, j, now)
		if err != nil {
			log.Printf("credits: retrying the signup gift of user %s failed: %v", j.UserID, err)
			continue
		}
		if ok {
			gifts++
		}
	}
	return ended, gifts, nil
}

// emailHash is the SHA-256 hex of the normalized e-mail (trimmed, lower
// case): one gift per person for life even after the account is deleted.
func emailHash(email string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email))))
	return hex.EncodeToString(sum[:])
}

// dateOf drops the time of day (UTC).
func dateOf(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
