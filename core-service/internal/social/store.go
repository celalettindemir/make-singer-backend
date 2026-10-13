package social

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ProfileStore covers the operations on sosyal.profil.
type ProfileStore interface {
	// GetProfile returns the user's profile, or ErrNoProfile if there is none.
	GetProfile(ctx context.Context, userID string) (*ProfileRecord, error)
	// GetProfileByUsername returns the profile with that username, or ErrNotFound.
	GetProfileByUsername(ctx context.Context, username string) (*ProfileRecord, error)
	// UpsertProfile creates or updates the profile. If another user owns the
	// username, it returns ErrUsernameTaken.
	UpsertProfile(ctx context.Context, profile ProfileRecord) (*ProfileRecord, error)
	// UsernameOwners returns the taken usernames among the given ones as a
	// username -> user ID map.
	UsernameOwners(ctx context.Context, usernames []string) (map[string]string, error)
	// ProfileCounts returns the owner's counters as seen by the viewer.
	ProfileCounts(ctx context.Context, ownerID, viewerID string) (*ProfileCounts, error)
}

// Store is the full set of persistent operations of the social module.
type Store interface {
	ProfileStore
	PublicationStore
	PreparationStore
	FeedStore
	LikeStore
	FollowStore
	CommentStore
	ReportStore
	CoverStore
	PreviewStore
}

// PostgresStore is the Postgres implementation of Store. IDs arrive already
// validated as UUIDs at the call site (the service uses parseID).
type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

var _ Store = (*PostgresStore)(nil)

// uniqueViolation returns the constraint name if err is a UNIQUE violation.
func uniqueViolation(err error) (string, bool) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return pgErr.ConstraintName, true
	}
	return "", false
}

const profileColumns = `kullanici_id::text, kullanici_adi, gorunen_ad, bio, olusturma`

// profileColumnsP is profileColumns with the "p" table alias.
const profileColumnsP = `p.kullanici_id::text, p.kullanici_adi, p.gorunen_ad, p.bio, p.olusturma`

func scanProfile(row pgx.Row) (*ProfileRecord, error) {
	var profile ProfileRecord
	if err := row.Scan(&profile.UserID, &profile.Username, &profile.DisplayName, &profile.Bio, &profile.CreatedAt); err != nil {
		return nil, err
	}
	return &profile, nil
}

func (s *PostgresStore) GetProfile(ctx context.Context, userID string) (*ProfileRecord, error) {
	profile, err := scanProfile(s.pool.QueryRow(ctx,
		`SELECT `+profileColumns+` FROM sosyal.profil WHERE kullanici_id = $1`, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoProfile
	}
	if err != nil {
		return nil, fmt.Errorf("could not read the profile: %w", err)
	}
	return profile, nil
}

func (s *PostgresStore) GetProfileByUsername(ctx context.Context, username string) (*ProfileRecord, error) {
	profile, err := scanProfile(s.pool.QueryRow(ctx,
		`SELECT `+profileColumns+` FROM sosyal.profil WHERE kullanici_adi = $1`, username))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("could not read the profile: %w", err)
	}
	return profile, nil
}

func (s *PostgresStore) UpsertProfile(ctx context.Context, profile ProfileRecord) (*ProfileRecord, error) {
	saved, err := scanProfile(s.pool.QueryRow(ctx, `
		INSERT INTO sosyal.profil (kullanici_id, kullanici_adi, gorunen_ad, bio)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (kullanici_id) DO UPDATE
		   SET kullanici_adi = EXCLUDED.kullanici_adi,
		       gorunen_ad    = EXCLUDED.gorunen_ad,
		       bio           = EXCLUDED.bio
		RETURNING `+profileColumns,
		profile.UserID, profile.Username, profile.DisplayName, profile.Bio))
	if _, ok := uniqueViolation(err); ok {
		return nil, ErrUsernameTaken
	}
	if err != nil {
		return nil, fmt.Errorf("could not save the profile: %w", err)
	}
	return saved, nil
}

func (s *PostgresStore) UsernameOwners(ctx context.Context, usernames []string) (map[string]string, error) {
	owners := make(map[string]string, len(usernames))
	if len(usernames) == 0 {
		return owners, nil
	}
	rows, err := s.pool.Query(ctx,
		`SELECT kullanici_adi, kullanici_id::text FROM sosyal.profil WHERE kullanici_adi = ANY($1)`, usernames)
	if err != nil {
		return nil, fmt.Errorf("could not read usernames: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var username, owner string
		if err := rows.Scan(&username, &owner); err != nil {
			return nil, fmt.Errorf("could not read a username: %w", err)
		}
		owners[username] = owner
	}
	return owners, rows.Err()
}

func (s *PostgresStore) ProfileCounts(ctx context.Context, ownerID, viewerID string) (*ProfileCounts, error) {
	visible := visibilityCondition("y", "$2", false)
	var counts ProfileCounts
	err := s.pool.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM sosyal.yayin y
		    WHERE y.sahip_id = $1 AND y.kaynak_yayin_id IS NULL AND y.durum = 'yayinda' AND `+visible+`),
		  (SELECT count(*) FROM sosyal.yayin y
		    WHERE y.sahip_id = $1 AND y.kaynak_yayin_id IS NOT NULL AND y.durum = 'yayinda' AND `+visible+`),
		  (SELECT count(*) FROM sosyal.takip WHERE takip_edilen_id = $1),
		  (SELECT count(*) FROM sosyal.takip WHERE takipci_id = $1),
		  EXISTS (SELECT 1 FROM sosyal.takip WHERE takipci_id = $2 AND takip_edilen_id = $1)`,
		ownerID, viewerID,
	).Scan(&counts.Stats.Songs, &counts.Stats.Covers, &counts.Stats.Followers, &counts.Stats.Following, &counts.FollowedByMe)
	if err != nil {
		return nil, fmt.Errorf("could not read profile counters: %w", err)
	}
	return &counts, nil
}
