package social

import (
	"context"
	"time"

	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/config"
)

// sahteKuyruk, kuyruga konan gorevleri ve seceneklerini kaydeder.
type sahteKuyruk struct {
	gorevler   []*asynq.Task
	secenekler [][]asynq.Option
	hata       error
	once       func() // dolu ise Enqueue basinda cagrilir (or. istek baglamini iptal eder)
}

func (q *sahteKuyruk) Enqueue(t *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	if q.once != nil {
		q.once()
	}
	if q.hata != nil {
		return nil, q.hata
	}
	q.gorevler = append(q.gorevler, t)
	q.secenekler = append(q.secenekler, opts)
	return &asynq.TaskInfo{}, nil
}

// testR2, client testlerindeki yapilandirmanin aynisidir.
func testR2() *config.R2Config {
	return &config.R2Config{
		AccountID: "hesap123", PublicBucket: "makeasinger-public", PrivateBucket: "makeasinger-private",
		PublicURL: "https://makesinger-cdn.celalettindemir.dev", PresignTTL: time.Hour,
	}
}

// testServisi, sahte depo ve kuyrukla, R2 istemcisi olmadan servis kurar.
func testServisi(d *sahteDepo) (*Service, *sahteKuyruk) {
	q := &sahteKuyruk{}
	return NewService(d, q, nil, testR2()), q
}

// sahteDepo, servis testleri icin bellek ici Depo'dur. Yalnizca servisin
// kararlarini sinamaya yetecek kadar davranir; SQL davranisi canli
// Postgres testlerinde sinanir.
type sahteDepo struct {
	profiller map[string]*ProfileRecord // kullanici id -> kayit
	sayilar   ProfileCounts
	hata      error // dolu ise her cagri bu hatayla doner

	yayinlar      map[string]*PublicationRecord // yayin id -> kayit
	eklenen       []PublicationInsert
	slugCakismasi int // ilk n YayinEkle errSlugCakisti doner
	yamalar       []PublicationPatch
	kaldirilan    []string

	hazirliklar map[string]*PreparationRecord
	yayinlanan  map[string]PreparationResult
	hataYapilan []string

	akisOgeleri []PublicationSummary
	sonAkis     akisCagrisi

	takipler      map[[2]string]bool // {takipci, takip edilen}
	oneriKayitlar []ProfileRecord
	sonOneri      [2]any // {izleyici, limit}

	yorumKayitlari  map[string]*CommentRecord
	yorumOgeleri    []Comment
	eklenenYorumlar []CommentInsert
	silinenYorumlar []string
	sonYorumImleci  *CommentCursor
	sonYorumLimiti  int

	sikayetler []ReportInsert

	stemListeleri map[string][]StemRecord

	onizlemeler map[string]*PreviewRecord // slug -> kayit
}

func yeniSahteDepo() *sahteDepo {
	return &sahteDepo{
		profiller:   map[string]*ProfileRecord{},
		yayinlar:    map[string]*PublicationRecord{},
		hazirliklar: map[string]*PreparationRecord{},
		yayinlanan:  map[string]PreparationResult{},
		takipler:    map[[2]string]bool{},

		yorumKayitlari: map[string]*CommentRecord{},
	}
}

func (d *sahteDepo) GetProfile(_ context.Context, id string) (*ProfileRecord, error) {
	if d.hata != nil {
		return nil, d.hata
	}
	k, ok := d.profiller[id]
	if !ok {
		return nil, ErrNoProfile
	}
	return k, nil
}

func (d *sahteDepo) GetProfileByUsername(_ context.Context, ad string) (*ProfileRecord, error) {
	if d.hata != nil {
		return nil, d.hata
	}
	for _, k := range d.profiller {
		if k.Username == ad {
			return k, nil
		}
	}
	return nil, ErrNotFound
}

func (d *sahteDepo) UpsertProfile(_ context.Context, k ProfileRecord) (*ProfileRecord, error) {
	if d.hata != nil {
		return nil, d.hata
	}
	for id, v := range d.profiller {
		if v.Username == k.Username && id != k.UserID {
			return nil, ErrUsernameTaken
		}
	}
	k.CreatedAt = time.Now()
	d.profiller[k.UserID] = &k
	return &k, nil
}

func (d *sahteDepo) UsernameOwners(_ context.Context, adlar []string) (map[string]string, error) {
	if d.hata != nil {
		return nil, d.hata
	}
	m := map[string]string{}
	for _, ad := range adlar {
		for id, k := range d.profiller {
			if k.Username == ad {
				m[ad] = id
			}
		}
	}
	return m, nil
}

func (d *sahteDepo) ProfileCounts(context.Context, string, string) (*ProfileCounts, error) {
	if d.hata != nil {
		return nil, d.hata
	}
	s := d.sayilar
	return &s, nil
}

func (d *sahteDepo) InsertPublication(_ context.Context, y PublicationInsert) error {
	if d.hata != nil {
		return d.hata
	}
	if d.slugCakismasi > 0 {
		d.slugCakismasi--
		return errSlugConflict
	}
	d.eklenen = append(d.eklenen, y)
	return nil
}

// kopya, kaydin izleyiciye gore bir kopyasini doner (depo BenimMi'yi
// doldurur).
func (d *sahteDepo) kopya(k *PublicationRecord, izleyiciID string) *PublicationRecord {
	c := *k
	c.IsMine = c.OwnerID == izleyiciID
	return &c
}

func (d *sahteDepo) GetPublication(_ context.Context, id, izleyiciID string) (*PublicationRecord, error) {
	if d.hata != nil {
		return nil, d.hata
	}
	k, ok := d.yayinlar[id]
	if !ok {
		return nil, ErrNotFound
	}
	return d.kopya(k, izleyiciID), nil
}

func (d *sahteDepo) GetPublicationBySlug(_ context.Context, slug, izleyiciID string) (*PublicationRecord, error) {
	if d.hata != nil {
		return nil, d.hata
	}
	for _, k := range d.yayinlar {
		if k.Slug == slug {
			return d.kopya(k, izleyiciID), nil
		}
	}
	return nil, ErrNotFound
}

// sahibinCanliYayini, sahibin silinmemis yayinini bulur.
func (d *sahteDepo) sahibinCanliYayini(id, sahipID string) (*PublicationRecord, bool) {
	k, ok := d.yayinlar[id]
	if !ok || k.OwnerID != sahipID || k.Deleted {
		return nil, false
	}
	return k, true
}

func (d *sahteDepo) UpdatePublication(_ context.Context, id, sahipID string, yama PublicationPatch) error {
	if d.hata != nil {
		return d.hata
	}
	k, ok := d.sahibinCanliYayini(id, sahipID)
	if !ok {
		return ErrNotFound
	}
	d.yamalar = append(d.yamalar, yama)
	if yama.Description != nil {
		k.Description = *yama.Description
	}
	if yama.BackingEnabled != nil {
		k.BackingEnabled = *yama.BackingEnabled
	}
	if yama.Visibility != nil {
		k.Visibility = *yama.Visibility
	}
	if yama.CommentsEnabled != nil {
		k.CommentsEnabled = *yama.CommentsEnabled
	}
	return nil
}

func (d *sahteDepo) DeletePublication(_ context.Context, id, sahipID string) error {
	if d.hata != nil {
		return d.hata
	}
	k, ok := d.sahibinCanliYayini(id, sahipID)
	if !ok {
		return ErrNotFound
	}
	k.Deleted = true
	return nil
}

func (d *sahteDepo) RemovePublication(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err // gercek havuz da olu baglamda sorgu calistirmaz
	}
	d.kaldirilan = append(d.kaldirilan, id)
	return d.hata
}

func (d *sahteDepo) ResetForRetry(_ context.Context, id, sahipID string) error {
	if d.hata != nil {
		return d.hata
	}
	k, ok := d.sahibinCanliYayini(id, sahipID)
	if !ok {
		return ErrNotFound
	}
	if k.Status != StatusFailed {
		return ErrInvalidStatus
	}
	k.Status = StatusPreparing
	return nil
}

func (d *sahteDepo) GetPreparation(_ context.Context, id string) (*PreparationRecord, error) {
	if d.hata != nil {
		return nil, d.hata
	}
	k, ok := d.hazirliklar[id]
	if !ok {
		return nil, ErrNotFound
	}
	return k, nil
}

func (d *sahteDepo) Publish(_ context.Context, id string, s PreparationResult) error {
	if d.hata != nil {
		return d.hata
	}
	d.yayinlanan[id] = s
	return nil
}

func (d *sahteDepo) MarkFailed(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err // gercek havuz da olu baglamda sorgu calistirmaz
	}
	d.hataYapilan = append(d.hataYapilan, id)
	if k, ok := d.yayinlar[id]; ok && k.Status == StatusPreparing {
		k.Status = StatusFailed
	}
	return d.hata
}

// akisCagrisi, sahte akis yontemlerine son gelen girdilerdir.
type akisCagrisi struct {
	tur      string
	izleyici string
	sahip    string
	coverlar bool
	zaman    *TimeCursor
	puan     *ScoreCursor
	referans time.Time
	limit    int
}

func (d *sahteDepo) zamanListesi(c akisCagrisi) ([]PublicationSummary, error) {
	d.sonAkis = c
	if d.hata != nil {
		return nil, d.hata
	}
	n := len(d.akisOgeleri)
	if n > c.limit+1 {
		n = c.limit + 1
	}
	return d.akisOgeleri[:n], nil
}

func (d *sahteDepo) ListProfilePublications(_ context.Context, sahipID, izleyiciID string, coverlar bool, imlec *TimeCursor, limit int) ([]PublicationSummary, error) {
	return d.zamanListesi(akisCagrisi{tur: "profil", izleyici: izleyiciID, sahip: sahipID, coverlar: coverlar, zaman: imlec, limit: limit})
}

func (d *sahteDepo) FeedNew(_ context.Context, izleyiciID string, imlec *TimeCursor, limit int) ([]PublicationSummary, error) {
	return d.zamanListesi(akisCagrisi{tur: "yeni", izleyici: izleyiciID, zaman: imlec, limit: limit})
}

func (d *sahteDepo) FeedFollowing(_ context.Context, izleyiciID string, imlec *TimeCursor, limit int) ([]PublicationSummary, error) {
	return d.zamanListesi(akisCagrisi{tur: "takip", izleyici: izleyiciID, zaman: imlec, limit: limit})
}

func (d *sahteDepo) FeedForYou(_ context.Context, izleyiciID string, referans time.Time, imlec *ScoreCursor, limit int) ([]ScoredSummary, error) {
	d.sonAkis = akisCagrisi{tur: "senin-icin", izleyici: izleyiciID, puan: imlec, referans: referans, limit: limit}
	if d.hata != nil {
		return nil, d.hata
	}
	var ogeler []ScoredSummary
	for i, o := range d.akisOgeleri {
		if i > limit {
			break
		}
		ogeler = append(ogeler, ScoredSummary{PublicationSummary: o, Score: float64(100 - i)})
	}
	return ogeler, nil
}

func (d *sahteDepo) Like(_ context.Context, kullaniciID, yayinID string) error {
	if d.hata != nil {
		return d.hata
	}
	if k, ok := d.yayinlar[yayinID]; ok && !k.LikedByMe {
		k.LikedByMe = true
		k.LikeCount++
	}
	return nil
}

func (d *sahteDepo) Unlike(_ context.Context, kullaniciID, yayinID string) error {
	if d.hata != nil {
		return d.hata
	}
	if k, ok := d.yayinlar[yayinID]; ok && k.LikedByMe {
		k.LikedByMe = false
		k.LikeCount--
	}
	return nil
}

func (d *sahteDepo) Follow(_ context.Context, takipci, edilen string) error {
	if d.hata != nil {
		return d.hata
	}
	d.takipler[[2]string{takipci, edilen}] = true
	return nil
}

func (d *sahteDepo) Unfollow(_ context.Context, takipci, edilen string) error {
	if d.hata != nil {
		return d.hata
	}
	delete(d.takipler, [2]string{takipci, edilen})
	return nil
}

func (d *sahteDepo) FollowStatus(_ context.Context, takipci, edilen string) (*FollowStatus, error) {
	if d.hata != nil {
		return nil, d.hata
	}
	t := &FollowStatus{FollowedByMe: d.takipler[[2]string{takipci, edilen}]}
	for k := range d.takipler {
		if k[1] == edilen {
			t.Followers++
		}
	}
	return t, nil
}

func (d *sahteDepo) Suggestions(_ context.Context, izleyiciID string, limit int) ([]ProfileRecord, error) {
	d.sonOneri = [2]any{izleyiciID, limit}
	if d.hata != nil {
		return nil, d.hata
	}
	return d.oneriKayitlar, nil
}

func (d *sahteDepo) InsertComment(_ context.Context, y CommentInsert) (*Comment, error) {
	if d.hata != nil {
		return nil, d.hata
	}
	d.eklenenYorumlar = append(d.eklenenYorumlar, y)
	p := d.profiller[y.AuthorID]
	return &Comment{ID: y.ID, Author: p.summary(), Text: y.Text, TimeMs: y.TimeMs, CreatedAt: time.Now()}, nil
}

func (d *sahteDepo) ListComments(_ context.Context, _, _ string, imlec *CommentCursor, limit int) ([]Comment, error) {
	d.sonYorumImleci, d.sonYorumLimiti = imlec, limit
	if d.hata != nil {
		return nil, d.hata
	}
	n := len(d.yorumOgeleri)
	if n > limit+1 {
		n = limit + 1
	}
	return d.yorumOgeleri[:n], nil
}

func (d *sahteDepo) GetComment(_ context.Context, id string) (*CommentRecord, error) {
	if d.hata != nil {
		return nil, d.hata
	}
	k, ok := d.yorumKayitlari[id]
	if !ok {
		return nil, ErrNotFound
	}
	c := *k
	return &c, nil
}

func (d *sahteDepo) DeleteComment(_ context.Context, id string) error {
	if d.hata != nil {
		return d.hata
	}
	k, ok := d.yorumKayitlari[id]
	if !ok || k.Deleted {
		return ErrNotFound
	}
	k.Deleted = true
	d.silinenYorumlar = append(d.silinenYorumlar, id)
	return nil
}

func (d *sahteDepo) InsertReport(_ context.Context, s ReportInsert) error {
	if d.hata != nil {
		return d.hata
	}
	d.sikayetler = append(d.sikayetler, s)
	return nil
}

func (d *sahteDepo) ListCovers(_ context.Context, kaynakID, izleyiciID string, imlec *TimeCursor, limit int) ([]PublicationSummary, error) {
	return d.zamanListesi(akisCagrisi{tur: "cover", izleyici: izleyiciID, sahip: kaynakID, zaman: imlec, limit: limit})
}

func (d *sahteDepo) ListStems(_ context.Context, yayinID string) ([]StemRecord, error) {
	if d.hata != nil {
		return nil, d.hata
	}
	return d.stemListeleri[yayinID], nil
}

func (d *sahteDepo) GetPreview(_ context.Context, slug string) (*PreviewRecord, error) {
	if d.hata != nil {
		return nil, d.hata
	}
	k, ok := d.onizlemeler[slug]
	if !ok {
		return nil, ErrNotFound
	}
	c := *k
	return &c, nil
}
