package social

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/hibiken/asynq"
)

const (
	pubX = "9a1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
	pubY = "9b1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
)

func storeWithProfile() *fakeStore {
	store := newFakeStore()
	store.profiles[userA] = &ProfileRecord{UserID: userA, Username: "ali", DisplayName: "Ali"}
	return store
}

func fakePublication(id, owner, visibility, status string) *PublicationRecord {
	rec := &PublicationRecord{OwnerID: owner}
	rec.ID, rec.Slug, rec.Title = id, "Ab3dEf9h", "Orijinal"
	rec.Owner = ProfileSummary{Username: "sahip"}
	rec.Visibility, rec.Status, rec.BackingEnabled, rec.HasStems = visibility, status, true, true
	return rec
}

// publicationRequestA is a valid request using userA's own files.
func publicationRequestA() PublicationRequest {
	req := validRequest()
	req.MasterURL = "https://makeasinger-private.hesap123.r2.cloudflarestorage.com/masters/" + userA + "/p1/m1.wav?X-Amz-Signature=x"
	req.Stems[0].URL = "stems/" + userA + "/p1/drums.mp3"
	return req
}

func optionValue(opts []asynq.Option, optType asynq.OptionType) any {
	for _, opt := range opts {
		if opt.Type() == optType {
			return opt.Value()
		}
	}
	return nil
}

func TestService_CreatePublication_NoProfile(t *testing.T) {
	s, q := newTestService(newFakeStore())
	if _, err := s.CreatePublication(context.Background(), userA, publicationRequestA()); !errors.Is(err, ErrNoProfile) {
		t.Fatalf("ErrProfilYok bekleniyordu: %v", err)
	}
	if len(q.tasks) != 0 {
		t.Fatal("profil yokken is kurulmamali")
	}
}

func TestService_CreatePublication_SavesAndEnqueues(t *testing.T) {
	store := storeWithProfile()
	s, q := newTestService(store)
	resp, err := s.CreatePublication(context.Background(), userA, publicationRequestA())
	if err != nil {
		t.Fatalf("YayinOlustur: %v", err)
	}
	if resp.Status != StatusPreparing || !ValidSlug(resp.Slug) {
		t.Fatalf("yanit = %+v", resp)
	}
	if len(store.inserted) != 1 {
		t.Fatalf("tek yayin eklenmeli: %d", len(store.inserted))
	}
	pub := store.inserted[0]
	if pub.ID != resp.ID || pub.Slug != resp.Slug || pub.OwnerID != userA || !pub.BackingEnabled || pub.SourcePublicationID != "" {
		t.Fatalf("yeni yayin = %+v", pub)
	}
	if pub.Preparation.MasterKey != "masters/"+userA+"/p1/m1.wav" {
		t.Fatalf("master anahtari = %q", pub.Preparation.MasterKey)
	}
	if len(pub.Preparation.Stems) != 1 || pub.Preparation.Stems[0] != (PreparationStem{ID: "drums", Instrument: "drums", Duration: 184, Key: "stems/" + userA + "/p1/drums.mp3"}) {
		t.Fatalf("stemler = %+v", pub.Preparation.Stems)
	}
	if len(q.tasks) != 1 || q.tasks[0].Type() != TaskTypePrepare {
		t.Fatalf("hazirla isi kurulmali: %v", q.tasks)
	}
	var payload TaskPayload
	if err := json.Unmarshal(q.tasks[0].Payload(), &payload); err != nil || payload.PublicationID != resp.ID {
		t.Fatalf("yuk = %s", q.tasks[0].Payload())
	}
	opts := q.options[0]
	if optionValue(opts, asynq.QueueOpt) != QueueSocial || optionValue(opts, asynq.MaxRetryOpt) != 3 || optionValue(opts, asynq.RetentionOpt) != 24*time.Hour {
		t.Fatalf("secenekler = %v", opts)
	}
}

func TestService_CreatePublication_URLErrors(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*PublicationRequest)
		wantErr error
	}{
		{"yabanci master", func(i *PublicationRequest) { i.MasterURL = "masters/" + userB + "/p/m.wav" }, ErrOwnership},
		{"yabanci stem", func(i *PublicationRequest) { i.Stems[0].URL = "stems/" + userB + "/p/s.mp3" }, ErrOwnership},
		{"bozuk adres", func(i *PublicationRequest) { i.MasterURL = "https://kotu.example.com/masters/x/m.wav" }, ErrValidation},
		{"ciplak anlamsiz", func(i *PublicationRequest) { i.MasterURL = "merhaba" }, ErrValidation},
		{"public dosya", func(i *PublicationRequest) { i.MasterURL = "exports/" + userA + "/e1.mp3" }, ErrValidation},
	}
	for _, tc := range cases {
		store := storeWithProfile()
		s, q := newTestService(store)
		req := publicationRequestA()
		tc.mutate(&req)
		if _, err := s.CreatePublication(context.Background(), userA, req); !errors.Is(err, tc.wantErr) {
			t.Errorf("%s: %v bekleniyordu, gelen %v", tc.name, tc.wantErr, err)
		}
		if len(store.inserted) != 0 || len(q.tasks) != 0 {
			t.Errorf("%s: hatali istek kaydedilmemeli", tc.name)
		}
	}
}

func TestService_CreatePublication_RetriesOnSlugConflict(t *testing.T) {
	store := storeWithProfile()
	store.slugConflicts = 2
	s, _ := newTestService(store)
	if _, err := s.CreatePublication(context.Background(), userA, publicationRequestA()); err != nil {
		t.Fatalf("iki cakismadan sonra basarmali: %v", err)
	}
	store = storeWithProfile()
	store.slugConflicts = 5
	s, _ = newTestService(store)
	if _, err := s.CreatePublication(context.Background(), userA, publicationRequestA()); err == nil {
		t.Fatal("bes cakismada vazgecmeli")
	}
}

func TestService_CreatePublication_RollsBackOnQueueError(t *testing.T) {
	store := storeWithProfile()
	s, q := newTestService(store)
	q.err = errors.New("redis kapali")
	if _, err := s.CreatePublication(context.Background(), userA, publicationRequestA()); err == nil {
		t.Fatal("kuyruk hatasi donmeli")
	}
	if len(store.removed) != 1 || store.removed[0] != store.inserted[0].ID {
		t.Fatalf("eklenen yayin geri alinmali: %v", store.removed)
	}
}

// Rollback happens even if the request context is canceled during the queue error.
func TestService_CreatePublication_RollsBackOnCanceledContext(t *testing.T) {
	store := storeWithProfile()
	s, q := newTestService(store)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	q.before = cancel
	q.err = errors.New("redis kapali")
	if _, err := s.CreatePublication(ctx, userA, publicationRequestA()); err == nil {
		t.Fatal("kuyruk hatasi donmeli")
	}
	if len(store.removed) != 1 || store.removed[0] != store.inserted[0].ID {
		t.Fatalf("iptal edilen baglamda da yayin geri alinmali: %v", store.removed)
	}
}

func TestService_CreatePublication_Cover(t *testing.T) {
	coverRequest := func(sourceID string) PublicationRequest {
		req := publicationRequestA()
		req.SourcePublicationID = &sourceID
		return req
	}
	cases := []struct {
		name     string
		source   *PublicationRecord
		sourceID string
		wantErr  error
	}{
		{"yok", nil, pubX, ErrSourceNotFound},
		{"gecersiz kimlik", nil, "x", ErrSourceNotFound},
		{"gizli", fakePublication(pubX, userB, VisibilityFollowers, StatusPublished), pubX, ErrSourceNotFound},
		{"hazirlaniyor", fakePublication(pubX, userB, VisibilityEveryone, StatusPreparing), pubX, ErrSourceNotFound},
		{"cover'in cover'i", func() *PublicationRecord {
			rec := fakePublication(pubX, userB, VisibilityEveryone, StatusPublished)
			rec.SourcePublicationID = pubY
			return rec
		}(), pubX, ErrSourceNotFound},
		{"altyapi kapali", func() *PublicationRecord {
			rec := fakePublication(pubX, userB, VisibilityEveryone, StatusPublished)
			rec.BackingEnabled = false
			return rec
		}(), pubX, ErrBackingDisabled},
	}
	for _, tc := range cases {
		store := storeWithProfile()
		if tc.source != nil {
			store.publications[tc.source.ID] = tc.source
		}
		s, _ := newTestService(store)
		if _, err := s.CreatePublication(context.Background(), userA, coverRequest(tc.sourceID)); !errors.Is(err, tc.wantErr) {
			t.Errorf("%s: %v bekleniyordu, gelen %v", tc.name, tc.wantErr, err)
		}
	}

	store := storeWithProfile()
	store.publications[pubX] = fakePublication(pubX, userB, VisibilityLink, StatusPublished)
	s, _ := newTestService(store)
	if _, err := s.CreatePublication(context.Background(), userA, coverRequest(pubX)); err != nil {
		t.Fatalf("link yayini cover'lanabilir: %v", err)
	}
	pub := store.inserted[0]
	if pub.BackingEnabled || len(pub.Preparation.Stems) != 0 || pub.SourcePublicationID != pubX || pub.SourceTitle != "Orijinal" || pub.SourceOwnerUsername != "sahip" {
		t.Fatalf("cover = %+v", pub)
	}
}

func TestService_PublicationVisibility(t *testing.T) {
	store := newFakeStore()
	store.publications[pubX] = fakePublication(pubX, userA, VisibilityEveryone, StatusFailed)
	store.publications[pubY] = fakePublication(pubY, userA, VisibilityLink, StatusPublished)
	store.publications[pubY].Slug = "Zz9yYx8w"
	s, _ := newTestService(store)
	ctx := context.Background()

	if pub, err := s.Publication(ctx, userA, pubX); err != nil || !pub.IsMine || pub.Status != StatusFailed {
		t.Fatalf("sahip hata yayinini gormeli: %+v, %v", pub, err)
	}
	if _, err := s.Publication(ctx, userB, pubX); !errors.Is(err, ErrNotFound) {
		t.Fatalf("yabanci hata yayinini gormemeli: %v", err)
	}
	if pub, err := s.Publication(ctx, userB, pubY); err != nil || pub.IsMine {
		t.Fatalf("link detayda gorunur: %+v, %v", pub, err)
	}
	if _, err := s.Publication(ctx, userB, "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("gecersiz id: %v", err)
	}
	if pub, err := s.PublicationBySlug(ctx, userB, "Zz9yYx8w"); err != nil || pub.ID != pubY {
		t.Fatalf("slug: %+v, %v", pub, err)
	}
	if _, err := s.PublicationBySlug(ctx, userB, "kisa"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("gecersiz slug: %v", err)
	}
}

func TestService_UpdatePublication(t *testing.T) {
	store := newFakeStore()
	disabled := fakePublication(pubX, userA, VisibilityEveryone, StatusPublished)
	disabled.BackingEnabled = false
	store.publications[pubX] = disabled
	s, _ := newTestService(store)
	ctx := context.Background()
	backingOn, link := true, VisibilityLink

	if _, err := s.UpdatePublication(ctx, userB, pubX, PublicationPatch{Visibility: &link}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("baskasi guncelleyemez: %v", err)
	}
	// A stem copy exists: backing can be re-enabled.
	patch, err := s.UpdatePublication(ctx, userA, pubX, PublicationPatch{BackingEnabled: &backingOn, Visibility: &link})
	if err != nil || !patch.BackingEnabled || patch.Visibility != VisibilityLink {
		t.Fatalf("guncelle = %+v, %v", patch, err)
	}

	noStems := fakePublication(pubY, userA, VisibilityEveryone, StatusPublished)
	noStems.BackingEnabled, noStems.HasStems = false, false
	store.publications[pubY] = noStems
	if _, err := s.UpdatePublication(ctx, userA, pubY, PublicationPatch{BackingEnabled: &backingOn}); !errors.Is(err, ErrNoStems) {
		t.Fatalf("stemsiz yayin: %v", err)
	}
	noStems.HasStems, noStems.SourcePublicationID = true, pubX
	if _, err := s.UpdatePublication(ctx, userA, pubY, PublicationPatch{BackingEnabled: &backingOn}); !errors.Is(err, ErrNoStems) {
		t.Fatalf("cover'in altyapisi acilamaz: %v", err)
	}
	bad := "gizli"
	if _, err := s.UpdatePublication(ctx, userA, pubX, PublicationPatch{Visibility: &bad}); !errors.Is(err, ErrValidation) {
		t.Fatalf("gecersiz gorunurluk: %v", err)
	}
}

func TestService_DeletePublication(t *testing.T) {
	store := newFakeStore()
	store.publications[pubX] = fakePublication(pubX, userA, VisibilityEveryone, StatusPublished)
	s, q := newTestService(store)
	ctx := context.Background()
	if err := s.DeletePublication(ctx, userB, pubX); !errors.Is(err, ErrNotFound) {
		t.Fatalf("baskasi silemez: %v", err)
	}
	if err := s.DeletePublication(ctx, userA, pubX); err != nil {
		t.Fatalf("sil: %v", err)
	}
	if len(q.tasks) != 1 || q.tasks[0].Type() != TaskTypeDeleteFiles {
		t.Fatalf("dosya silme isi kurulmali: %v", q.tasks)
	}
	opts := q.options[0]
	if optionValue(opts, asynq.QueueOpt) != "bakim" || optionValue(opts, asynq.ProcessInOpt) != 24*time.Hour {
		t.Fatalf("secenekler = %v", opts)
	}
	if err := s.DeletePublication(ctx, userA, pubX); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ikinci silme 404: %v", err)
	}
	if _, err := s.Publication(ctx, userA, pubX); !errors.Is(err, ErrNotFound) {
		t.Fatalf("silinen yayin sahibine de gorunmez: %v", err)
	}

	store.publications[pubY] = fakePublication(pubY, userA, VisibilityEveryone, StatusPublished)
	q.err = errors.New("redis kapali")
	if err := s.DeletePublication(ctx, userA, pubY); err != nil {
		t.Fatalf("kuyruk hatasi silmeyi bozmamali: %v", err)
	}
}

func TestService_Retry(t *testing.T) {
	store := newFakeStore()
	store.publications[pubX] = fakePublication(pubX, userA, VisibilityEveryone, StatusFailed)
	store.publications[pubY] = fakePublication(pubY, userA, VisibilityEveryone, StatusPublished)
	s, q := newTestService(store)
	ctx := context.Background()
	if _, err := s.Retry(ctx, userB, pubX); !errors.Is(err, ErrNotFound) {
		t.Fatalf("baskasi: %v", err)
	}
	if _, err := s.Retry(ctx, userA, pubY); !errors.Is(err, ErrInvalidStatus) {
		t.Fatalf("yayindaki yayin: %v", err)
	}
	pub, err := s.Retry(ctx, userA, pubX)
	if err != nil || pub.Status != StatusPreparing || pub.ID != pubX {
		t.Fatalf("tekrar dene = %+v, %v", pub, err)
	}
	if len(q.tasks) != 1 || q.tasks[0].Type() != TaskTypePrepare {
		t.Fatalf("hazirla isi yeniden kurulmali: %v", q.tasks)
	}

	store.publications[pubX].Status = StatusFailed
	q.err = errors.New("redis kapali")
	if _, err := s.Retry(ctx, userA, pubX); err == nil {
		t.Fatal("kuyruk hatasi donmeli")
	}
	if store.publications[pubX].Status != StatusFailed {
		t.Fatalf("kuyruga girmeyen yayin hataya donmeli: %s", store.publications[pubX].Status)
	}
}

// The publication fails even if the request context is canceled during the queue error.
func TestService_Retry_MarksFailedOnCanceledContext(t *testing.T) {
	store := newFakeStore()
	store.publications[pubX] = fakePublication(pubX, userA, VisibilityEveryone, StatusFailed)
	s, q := newTestService(store)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	q.before = cancel
	q.err = errors.New("redis kapali")
	if _, err := s.Retry(ctx, userA, pubX); err == nil {
		t.Fatal("kuyruk hatasi donmeli")
	}
	if store.publications[pubX].Status != StatusFailed {
		t.Fatalf("iptal edilen baglamda da yayin hataya donmeli: %s", store.publications[pubX].Status)
	}
}

type signingStorage struct{}

func (signingStorage) Upload(context.Context, string, io.Reader, string) (string, time.Time, error) {
	return "", time.Time{}, nil
}
func (signingStorage) Delete(context.Context, string) error { return nil }
func (signingStorage) GetSignedURL(context.Context, string, time.Duration) (string, error) {
	return "", nil
}
func (signingStorage) URLFor(_ context.Context, key string) (string, time.Time, error) {
	return "https://imzali.example/" + key, time.Date(2026, 10, 12, 13, 0, 0, 0, time.UTC), nil
}

func TestService_Files(t *testing.T) {
	store := newFakeStore()
	ready := fakePublication(pubX, userA, VisibilityEveryone, StatusPublished)
	ready.MasterKey = "sosyal/" + pubX + "/master.wav"
	store.publications[pubX] = ready
	store.publications[pubY] = fakePublication(pubY, userA, VisibilityEveryone, StatusPreparing)
	ctx := context.Background()

	s, _ := newTestService(store)
	files, err := s.Files(ctx, userB, pubX)
	if err != nil || !strings.HasSuffix(files.Master.URL, "/sosyal/"+pubX+"/master.wav") || files.Master.ExpiresAt != nil {
		t.Fatalf("R2'siz adres = %+v, %v", files, err)
	}
	signed := NewService(store, &fakeQueue{}, signingStorage{}, testR2())
	files, err = signed.Files(ctx, userB, pubX)
	if err != nil || files.Master.URL != "https://imzali.example/sosyal/"+pubX+"/master.wav" || files.Master.ExpiresAt == nil {
		t.Fatalf("imzali adres = %+v, %v", files, err)
	}
	if _, err := s.Files(ctx, userA, pubY); !errors.Is(err, ErrInvalidStatus) {
		t.Fatalf("hazirlanan yayinin dosyasi yok: %v", err)
	}
	if _, err := s.Files(ctx, userB, pubY); !errors.Is(err, ErrNotFound) {
		t.Fatalf("yabanci hazirlanan yayini gormez: %v", err)
	}
}
