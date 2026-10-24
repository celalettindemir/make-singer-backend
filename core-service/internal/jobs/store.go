package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/makeasinger/api/internal/model"
)

// PostgresStore keeps jobs and their event log in uretim.isler and
// uretim.is_olaylari.
type PostgresStore struct {
	pool       *pgxpool.Pool
	pub        Publisher
	onFinished []func(ctx context.Context, job model.Job)
}

var _ Repo = (*PostgresStore)(nil)

// NewPostgresStore returns a store; pub may be nil.
func NewPostgresStore(pool *pgxpool.Pool, pub Publisher) *PostgresStore {
	return &PostgresStore{pool: pool, pub: pub}
}

// OnFinished registers a hook called after commit when a job's status became
// succeeded or failed. Register hooks before the store is used concurrently.
func (s *PostgresStore) OnFinished(fn func(ctx context.Context, job model.Job)) {
	s.onFinished = append(s.onFinished, fn)
}

const jobColumns = `id, sahip, tur, durum, ilerleme, adim, proje_id, proje_adi, girdi, sonuc,
	hata, hata_kodu, deneme, son_seq, olusturma, baslama, bitis`

func scanJob(row pgx.Row) (*model.Job, error) {
	var (
		j                   model.Job
		id                  uuid.UUID
		status              string
		step, errText, code *string
	)
	if err := row.Scan(&id, &j.OwnerID, &j.Type, &status, &j.Progress, &step, &j.ProjectID,
		&j.ProjectName, &j.Payload, &j.Result, &errText, &code, &j.RetryCount, &j.LastSeq,
		&j.CreatedAt, &j.StartedAt, &j.CompletedAt); err != nil {
		return nil, err
	}
	j.ID = id.String()
	j.Status = model.JobStatus(status)
	if step != nil {
		j.CurrentStep = *step
	}
	if code != nil {
		j.ErrorCode = *code
	}
	j.Error = errText
	return &j, nil
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func parseID(id string) (uuid.UUID, bool) {
	u, err := uuid.Parse(id)
	return u, err == nil
}

func (s *PostgresStore) Create(ctx context.Context, job *model.Job) error {
	if job.ID == "" {
		job.ID = uuid.NewString()
	}
	id, ok := parseID(job.ID)
	if !ok {
		return fmt.Errorf("job id %q is not a uuid", job.ID)
	}
	if job.Status == "" {
		job.Status = model.JobStatusQueued
	}
	if job.CreatedAt.IsZero() {
		job.CreatedAt = time.Now().UTC()
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `INSERT INTO uretim.isler
		(id, sahip, tur, durum, ilerleme, adim, proje_id, proje_adi, girdi, sonuc, hata, hata_kodu,
		 deneme, son_seq, olusturma, baslama, bitis, son_kullanma)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,0,$14,$15,$16,$17)`,
		id, job.OwnerID, job.Type, string(job.Status), job.Progress, nilIfEmpty(job.CurrentStep),
		job.ProjectID, job.ProjectName, job.Payload, job.Result, job.Error, nilIfEmpty(job.ErrorCode),
		job.RetryCount, job.CreatedAt, job.StartedAt, job.CompletedAt,
		job.CreatedAt.Add(ResultRetention))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "isler_sozler_aktif" {
			_ = tx.Rollback(ctx)
			var existing uuid.UUID
			if qerr := s.pool.QueryRow(ctx, `SELECT id FROM uretim.isler
				WHERE sahip=$1 AND proje_id=$2 AND tur='lyrics' AND durum IN ('queued','running')`,
				job.OwnerID, job.ProjectID).Scan(&existing); qerr == nil {
				return &activeJobError{jobID: existing.String()}
			}
			return ErrActiveLyricsJob
		}
		return err
	}
	events, err := insertEvents(ctx, tx, id, job.OwnerID, 0, []Derived{{Kind: KindQueued, Data: map[string]any{}}})
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	job.LastSeq = events[len(events)-1].Seq
	s.publish(ctx, events)
	return nil
}

// insertEvents appends events after seq `last` and stores the new son_seq.
func insertEvents(ctx context.Context, tx pgx.Tx, id uuid.UUID, owner string, last int64, ds []Derived) ([]Event, error) {
	events := make([]Event, 0, len(ds))
	now := time.Now().UTC()
	for _, d := range ds {
		last++
		data, err := json.Marshal(d.Data)
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO uretim.is_olaylari (is_id, seq, tur, veri, zaman) VALUES ($1,$2,$3,$4,$5)`,
			id, last, d.Kind, data, now); err != nil {
			return nil, err
		}
		events = append(events, Event{JobID: id.String(), OwnerID: owner, Seq: last, Kind: d.Kind, Data: data, At: now})
	}
	if len(ds) == 0 {
		return events, nil
	}
	if _, err := tx.Exec(ctx, `UPDATE uretim.isler SET son_seq=$2 WHERE id=$1`, id, last); err != nil {
		return nil, err
	}
	return events, nil
}

func (s *PostgresStore) publish(ctx context.Context, events []Event) {
	if s.pub != nil && len(events) > 0 {
		s.pub.Publish(ctx, events)
	}
}

func (s *PostgresStore) Get(ctx context.Context, id string) (*model.Job, error) {
	u, ok := parseID(id)
	if !ok {
		return nil, ErrJobNotFound
	}
	j, err := scanJob(s.pool.QueryRow(ctx, `SELECT `+jobColumns+` FROM uretim.isler WHERE id=$1`, u))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrJobNotFound
	}
	return j, err
}

func (s *PostgresStore) GetOwned(ctx context.Context, userID, id string) (*model.Job, error) {
	j, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !j.IsOwnedBy(userID) {
		return nil, ErrJobNotFound
	}
	return j, nil
}

func (s *PostgresStore) Update(ctx context.Context, id string, fn func(*model.Job) error) error {
	return s.UpdateWith(ctx, id, func(j *model.Job, _ Emitter) error { return fn(j) })
}

type emitter struct{ out []Derived }

func (e *emitter) Emit(kind string, data any) { e.out = append(e.out, Derived{Kind: kind, Data: data}) }

func (s *PostgresStore) UpdateWith(ctx context.Context, id string, fn func(*model.Job, Emitter) error) error {
	u, ok := parseID(id)
	if !ok {
		return ErrJobNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	job, err := scanJob(tx.QueryRow(ctx, `SELECT `+jobColumns+` FROM uretim.isler WHERE id=$1 FOR UPDATE`, u))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrJobNotFound
	}
	if err != nil {
		return err
	}
	before := *job
	em := &emitter{}
	if err := fn(job, em); err != nil {
		return err
	}
	finishDefaults(&before, job)
	_, err = tx.Exec(ctx, `UPDATE uretim.isler SET durum=$2, ilerleme=$3, adim=$4, proje_id=$5,
		proje_adi=$6, girdi=$7, sonuc=$8, hata=$9, hata_kodu=$10, deneme=$11, baslama=$12, bitis=$13
		WHERE id=$1`,
		u, string(job.Status), job.Progress, nilIfEmpty(job.CurrentStep), job.ProjectID, job.ProjectName,
		job.Payload, job.Result, job.Error, nilIfEmpty(job.ErrorCode), job.RetryCount,
		job.StartedAt, job.CompletedAt)
	if err != nil {
		return err
	}
	events, err := insertEvents(ctx, tx, u, job.OwnerID, before.LastSeq,
		append(DeriveEvents(before, *job), em.out...))
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if len(events) > 0 {
		job.LastSeq = events[len(events)-1].Seq
	}
	s.publish(ctx, events)
	if finishedNow(before, *job) {
		for _, h := range s.onFinished {
			h(ctx, *job)
		}
	}
	return nil
}

// finishDefaults sets bitis when a job reaches a terminal state.
func finishDefaults(before, after *model.Job) {
	if isTerminal(after.Status) && !isTerminal(before.Status) && after.CompletedAt == nil {
		now := time.Now().UTC()
		after.CompletedAt = &now
	}
}

func finishedNow(before, after model.Job) bool {
	return before.Status != after.Status &&
		(after.Status == model.JobStatusSucceeded || after.Status == model.JobStatusFailed)
}

func (s *PostgresStore) List(ctx context.Context, userID string, since time.Time, limit int) ([]model.Job, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+jobColumns+` FROM uretim.isler
		WHERE sahip=$1 AND (durum IN ('queued','running') OR bitis >= $2)
		ORDER BY olusturma DESC LIMIT $3`, userID, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *j)
	}
	return out, rows.Err()
}

func (s *PostgresStore) StaleLyricsJobs(ctx context.Context, before time.Time) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT j.id FROM uretim.isler j
		WHERE j.tur='lyrics' AND j.durum IN ('queued','running')
		AND COALESCE((SELECT max(e.zaman) FROM uretim.is_olaylari e WHERE e.is_id=j.id), j.olusturma) < $1`, before)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id.String())
	}
	return out, rows.Err()
}

func (s *PostgresStore) EventsAfter(ctx context.Context, userID, id string, afterSeq int64, limit int) ([]Event, error) {
	if _, err := s.GetOwned(ctx, userID, id); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT seq, tur, veri, zaman FROM uretim.is_olaylari
		WHERE is_id=$1 AND seq > $2 ORDER BY seq LIMIT $3`, id, afterSeq, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		e := Event{JobID: id, OwnerID: userID}
		if err := rows.Scan(&e.Seq, &e.Kind, &e.Data, &e.At); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *PostgresStore) Sweep(ctx context.Context, now time.Time) (int64, int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM uretim.is_olaylari WHERE is_id IN
		(SELECT id FROM uretim.isler WHERE bitis IS NOT NULL AND bitis < $1)`, now.Add(-EventRetention))
	if err != nil {
		return 0, 0, err
	}
	events := tag.RowsAffected()
	tag, err = s.pool.Exec(ctx, `DELETE FROM uretim.isler WHERE son_kullanma < $1`, now)
	if err != nil {
		return 0, events, err
	}
	return tag.RowsAffected(), events, nil
}

func (s *PostgresStore) DeleteOwner(ctx context.Context, userID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, q := range []string{
		`DELETE FROM uretim.isler WHERE sahip=$1`, // events cascade
		`DELETE FROM uretim.cihazlar WHERE sahip=$1`,
	} {
		if _, err := tx.Exec(ctx, q, userID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// UpsertDevice stores a push token for userID. A token already stored for
// another user moves to userID: the same install signed in as someone else.
func (s *PostgresStore) UpsertDevice(ctx context.Context, userID string, d model.Device) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO uretim.cihazlar (token, sahip, platform, dil, surum, guncelleme)
		VALUES ($1,$2,$3,$4,$5,now())
		ON CONFLICT (token) DO UPDATE SET sahip=EXCLUDED.sahip, platform=EXCLUDED.platform,
			dil=EXCLUDED.dil, surum=EXCLUDED.surum, guncelleme=EXCLUDED.guncelleme`,
		d.Token, userID, d.Platform, d.Language, d.AppVersion)
	return err
}

// DeleteDevice removes userID's token; another user's or an unknown token is a no-op.
func (s *PostgresStore) DeleteDevice(ctx context.Context, userID, token string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM uretim.cihazlar WHERE token=$1 AND sahip=$2`, token, userID)
	return err
}

// Devices returns the user's push tokens, newest first.
func (s *PostgresStore) Devices(ctx context.Context, userID string) ([]model.Device, error) {
	rows, err := s.pool.Query(ctx, `SELECT token, platform, dil, surum, guncelleme
		FROM uretim.cihazlar WHERE sahip=$1 ORDER BY guncelleme DESC, token`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Device{}
	for rows.Next() {
		var d model.Device
		if err := rows.Scan(&d.Token, &d.Platform, &d.Language, &d.AppVersion, &d.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DeleteToken removes a token whoever owns it (FCM said it is unregistered).
func (s *PostgresStore) DeleteToken(ctx context.Context, token string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM uretim.cihazlar WHERE token=$1`, token)
	return err
}
