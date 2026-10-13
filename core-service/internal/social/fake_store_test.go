package social

import (
	"context"
	"time"

	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/config"
)

// fakeQueue records the tasks put on the queue and their options.
type fakeQueue struct {
	tasks   []*asynq.Task
	options [][]asynq.Option
	err     error
	before  func() // when set, called at the start of Enqueue (e.g. cancels the request context)
}

func (q *fakeQueue) Enqueue(t *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	if q.before != nil {
		q.before()
	}
	if q.err != nil {
		return nil, q.err
	}
	q.tasks = append(q.tasks, t)
	q.options = append(q.options, opts)
	return &asynq.TaskInfo{}, nil
}

// testR2 is the same configuration as the client tests use.
func testR2() *config.R2Config {
	return &config.R2Config{
		AccountID: "hesap123", PublicBucket: "makeasinger-public", PrivateBucket: "makeasinger-private",
		PublicURL: "https://makesinger-cdn.celalettindemir.dev", PresignTTL: time.Hour,
	}
}

// newTestService builds a service with the fake store and queue, without an R2 client.
func newTestService(store *fakeStore) (*Service, *fakeQueue) {
	q := &fakeQueue{}
	return NewService(store, q, nil, testR2()), q
}

// fakeStore is an in-memory Store for the service tests. It behaves only enough
// to exercise the service's decisions; SQL behavior is tested in the live
// Postgres tests.
type fakeStore struct {
	profiles map[string]*ProfileRecord // user id -> record
	counts   ProfileCounts
	err      error // when set, every call returns this error

	publications  map[string]*PublicationRecord // publication id -> record
	inserted      []PublicationInsert
	slugConflicts int // the first n InsertPublication calls return errSlugConflict
	patches       []PublicationPatch
	removed       []string

	preparations map[string]*PreparationRecord
	published    map[string]PreparationResult
	failed       []string

	feedItems []PublicationSummary
	lastFeed  feedCall

	follows           map[[2]string]bool // {follower, followee}
	suggestionRecords []ProfileRecord
	lastSuggestion    [2]any // {viewer, limit}

	commentRecords    map[string]*CommentRecord
	commentItems      []Comment
	insertedComments  []CommentInsert
	deletedComments   []string
	lastCommentCursor *CommentCursor
	lastCommentLimit  int

	reports []ReportInsert

	stemLists map[string][]StemRecord

	previews map[string]*PreviewRecord // slug -> record
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		profiles:     map[string]*ProfileRecord{},
		publications: map[string]*PublicationRecord{},
		preparations: map[string]*PreparationRecord{},
		published:    map[string]PreparationResult{},
		follows:      map[[2]string]bool{},

		commentRecords: map[string]*CommentRecord{},
	}
}

func (f *fakeStore) GetProfile(_ context.Context, id string) (*ProfileRecord, error) {
	if f.err != nil {
		return nil, f.err
	}
	rec, ok := f.profiles[id]
	if !ok {
		return nil, ErrNoProfile
	}
	return rec, nil
}

func (f *fakeStore) GetProfileByUsername(_ context.Context, username string) (*ProfileRecord, error) {
	if f.err != nil {
		return nil, f.err
	}
	for _, rec := range f.profiles {
		if rec.Username == username {
			return rec, nil
		}
	}
	return nil, ErrNotFound
}

func (f *fakeStore) UpsertProfile(_ context.Context, rec ProfileRecord) (*ProfileRecord, error) {
	if f.err != nil {
		return nil, f.err
	}
	for id, v := range f.profiles {
		if v.Username == rec.Username && id != rec.UserID {
			return nil, ErrUsernameTaken
		}
	}
	rec.CreatedAt = time.Now()
	f.profiles[rec.UserID] = &rec
	return &rec, nil
}

func (f *fakeStore) UsernameOwners(_ context.Context, names []string) (map[string]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	m := map[string]string{}
	for _, name := range names {
		for id, rec := range f.profiles {
			if rec.Username == name {
				m[name] = id
			}
		}
	}
	return m, nil
}

func (f *fakeStore) ProfileCounts(context.Context, string, string) (*ProfileCounts, error) {
	if f.err != nil {
		return nil, f.err
	}
	s := f.counts
	return &s, nil
}

func (f *fakeStore) InsertPublication(_ context.Context, ins PublicationInsert) error {
	if f.err != nil {
		return f.err
	}
	if f.slugConflicts > 0 {
		f.slugConflicts--
		return errSlugConflict
	}
	f.inserted = append(f.inserted, ins)
	return nil
}

// viewerCopy returns a copy of the record for the viewer (the store fills
// IsMine in).
func (f *fakeStore) viewerCopy(rec *PublicationRecord, viewerID string) *PublicationRecord {
	c := *rec
	c.IsMine = c.OwnerID == viewerID
	return &c
}

func (f *fakeStore) GetPublication(_ context.Context, id, viewerID string) (*PublicationRecord, error) {
	if f.err != nil {
		return nil, f.err
	}
	rec, ok := f.publications[id]
	if !ok {
		return nil, ErrNotFound
	}
	return f.viewerCopy(rec, viewerID), nil
}

func (f *fakeStore) GetPublicationBySlug(_ context.Context, slug, viewerID string) (*PublicationRecord, error) {
	if f.err != nil {
		return nil, f.err
	}
	for _, rec := range f.publications {
		if rec.Slug == slug {
			return f.viewerCopy(rec, viewerID), nil
		}
	}
	return nil, ErrNotFound
}

// ownersLivePublication finds the owner's non-deleted publication.
func (f *fakeStore) ownersLivePublication(id, ownerID string) (*PublicationRecord, bool) {
	rec, ok := f.publications[id]
	if !ok || rec.OwnerID != ownerID || rec.Deleted {
		return nil, false
	}
	return rec, true
}

func (f *fakeStore) UpdatePublication(_ context.Context, id, ownerID string, patch PublicationPatch) error {
	if f.err != nil {
		return f.err
	}
	rec, ok := f.ownersLivePublication(id, ownerID)
	if !ok {
		return ErrNotFound
	}
	f.patches = append(f.patches, patch)
	if patch.Description != nil {
		rec.Description = *patch.Description
	}
	if patch.BackingEnabled != nil {
		rec.BackingEnabled = *patch.BackingEnabled
	}
	if patch.Visibility != nil {
		rec.Visibility = *patch.Visibility
	}
	if patch.CommentsEnabled != nil {
		rec.CommentsEnabled = *patch.CommentsEnabled
	}
	return nil
}

func (f *fakeStore) DeletePublication(_ context.Context, id, ownerID string) error {
	if f.err != nil {
		return f.err
	}
	rec, ok := f.ownersLivePublication(id, ownerID)
	if !ok {
		return ErrNotFound
	}
	rec.Deleted = true
	return nil
}

func (f *fakeStore) RemovePublication(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err // the real pool does not run a query on a dead context either
	}
	f.removed = append(f.removed, id)
	return f.err
}

func (f *fakeStore) ResetForRetry(_ context.Context, id, ownerID string) error {
	if f.err != nil {
		return f.err
	}
	rec, ok := f.ownersLivePublication(id, ownerID)
	if !ok {
		return ErrNotFound
	}
	if rec.Status != StatusFailed {
		return ErrInvalidStatus
	}
	rec.Status = StatusPreparing
	return nil
}

func (f *fakeStore) GetPreparation(_ context.Context, id string) (*PreparationRecord, error) {
	if f.err != nil {
		return nil, f.err
	}
	rec, ok := f.preparations[id]
	if !ok {
		return nil, ErrNotFound
	}
	return rec, nil
}

func (f *fakeStore) Publish(_ context.Context, id string, s PreparationResult) error {
	if f.err != nil {
		return f.err
	}
	f.published[id] = s
	return nil
}

func (f *fakeStore) MarkFailed(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err // the real pool does not run a query on a dead context either
	}
	f.failed = append(f.failed, id)
	if rec, ok := f.publications[id]; ok && rec.Status == StatusPreparing {
		rec.Status = StatusFailed
	}
	return f.err
}

// feedCall holds the latest arguments the fake feed methods received.
type feedCall struct {
	kind        string
	viewer      string
	owner       string
	covers      bool
	timeCursor  *TimeCursor
	scoreCursor *ScoreCursor
	reference   time.Time
	limit       int
}

func (f *fakeStore) timeList(c feedCall) ([]PublicationSummary, error) {
	f.lastFeed = c
	if f.err != nil {
		return nil, f.err
	}
	n := len(f.feedItems)
	if n > c.limit+1 {
		n = c.limit + 1
	}
	return f.feedItems[:n], nil
}

func (f *fakeStore) ListProfilePublications(_ context.Context, ownerID, viewerID string, coversOnly bool, cursor *TimeCursor, limit int) ([]PublicationSummary, error) {
	return f.timeList(feedCall{kind: "profil", viewer: viewerID, owner: ownerID, covers: coversOnly, timeCursor: cursor, limit: limit})
}

func (f *fakeStore) FeedNew(_ context.Context, viewerID string, cursor *TimeCursor, limit int) ([]PublicationSummary, error) {
	return f.timeList(feedCall{kind: "yeni", viewer: viewerID, timeCursor: cursor, limit: limit})
}

func (f *fakeStore) FeedFollowing(_ context.Context, viewerID string, cursor *TimeCursor, limit int) ([]PublicationSummary, error) {
	return f.timeList(feedCall{kind: "takip", viewer: viewerID, timeCursor: cursor, limit: limit})
}

func (f *fakeStore) FeedForYou(_ context.Context, viewerID string, refTime time.Time, cursor *ScoreCursor, limit int) ([]ScoredSummary, error) {
	f.lastFeed = feedCall{kind: "senin-icin", viewer: viewerID, scoreCursor: cursor, reference: refTime, limit: limit}
	if f.err != nil {
		return nil, f.err
	}
	var items []ScoredSummary
	for i, item := range f.feedItems {
		if i > limit {
			break
		}
		items = append(items, ScoredSummary{PublicationSummary: item, Score: float64(100 - i)})
	}
	return items, nil
}

func (f *fakeStore) Like(_ context.Context, userID, pubID string) error {
	if f.err != nil {
		return f.err
	}
	if rec, ok := f.publications[pubID]; ok && !rec.LikedByMe {
		rec.LikedByMe = true
		rec.LikeCount++
	}
	return nil
}

func (f *fakeStore) Unlike(_ context.Context, userID, pubID string) error {
	if f.err != nil {
		return f.err
	}
	if rec, ok := f.publications[pubID]; ok && rec.LikedByMe {
		rec.LikedByMe = false
		rec.LikeCount--
	}
	return nil
}

func (f *fakeStore) Follow(_ context.Context, follower, followee string) error {
	if f.err != nil {
		return f.err
	}
	f.follows[[2]string{follower, followee}] = true
	return nil
}

func (f *fakeStore) Unfollow(_ context.Context, follower, followee string) error {
	if f.err != nil {
		return f.err
	}
	delete(f.follows, [2]string{follower, followee})
	return nil
}

func (f *fakeStore) FollowStatus(_ context.Context, follower, followee string) (*FollowStatus, error) {
	if f.err != nil {
		return nil, f.err
	}
	t := &FollowStatus{FollowedByMe: f.follows[[2]string{follower, followee}]}
	for idx := range f.follows {
		if idx[1] == followee {
			t.Followers++
		}
	}
	return t, nil
}

func (f *fakeStore) Suggestions(_ context.Context, viewerID string, limit int) ([]ProfileRecord, error) {
	f.lastSuggestion = [2]any{viewerID, limit}
	if f.err != nil {
		return nil, f.err
	}
	return f.suggestionRecords, nil
}

func (f *fakeStore) InsertComment(_ context.Context, ins CommentInsert) (*Comment, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.insertedComments = append(f.insertedComments, ins)
	p := f.profiles[ins.AuthorID]
	return &Comment{ID: ins.ID, Author: p.summary(), Text: ins.Text, TimeMs: ins.TimeMs, CreatedAt: time.Now()}, nil
}

func (f *fakeStore) ListComments(_ context.Context, _, _ string, cursor *CommentCursor, limit int) ([]Comment, error) {
	f.lastCommentCursor, f.lastCommentLimit = cursor, limit
	if f.err != nil {
		return nil, f.err
	}
	n := len(f.commentItems)
	if n > limit+1 {
		n = limit + 1
	}
	return f.commentItems[:n], nil
}

func (f *fakeStore) GetComment(_ context.Context, id string) (*CommentRecord, error) {
	if f.err != nil {
		return nil, f.err
	}
	rec, ok := f.commentRecords[id]
	if !ok {
		return nil, ErrNotFound
	}
	c := *rec
	return &c, nil
}

func (f *fakeStore) DeleteComment(_ context.Context, id string) error {
	if f.err != nil {
		return f.err
	}
	rec, ok := f.commentRecords[id]
	if !ok || rec.Deleted {
		return ErrNotFound
	}
	rec.Deleted = true
	f.deletedComments = append(f.deletedComments, id)
	return nil
}

func (f *fakeStore) InsertReport(_ context.Context, s ReportInsert) error {
	if f.err != nil {
		return f.err
	}
	f.reports = append(f.reports, s)
	return nil
}

func (f *fakeStore) ListCovers(_ context.Context, sourceID, viewerID string, cursor *TimeCursor, limit int) ([]PublicationSummary, error) {
	return f.timeList(feedCall{kind: "cover", viewer: viewerID, owner: sourceID, timeCursor: cursor, limit: limit})
}

func (f *fakeStore) ListStems(_ context.Context, pubID string) ([]StemRecord, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.stemLists[pubID], nil
}

func (f *fakeStore) GetPreview(_ context.Context, slug string) (*PreviewRecord, error) {
	if f.err != nil {
		return nil, f.err
	}
	rec, ok := f.previews[slug]
	if !ok {
		return nil, ErrNotFound
	}
	c := *rec
	return &c, nil
}
