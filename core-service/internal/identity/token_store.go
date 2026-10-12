package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

var (
	ErrTokenNotFound = errors.New("jeton bulunamadi")
	ErrTokenReused   = errors.New("jeton yeniden kullanildi")
)

type AccessRecord struct {
	ID        string
	UserID    string
	ClientID  string
	Scopes    []string
	ExpiresAt time.Time
}

type RefreshRecord struct {
	ID        string
	FamilyID  string
	UserID    string
	ClientID  string
	TokenHash []byte
	Scopes    []string
	Audience  []string
	AMR       []string
	AuthTime  time.Time
	ExpiresAt time.Time
	UsedAt    *time.Time
	RevokedAt *time.Time
}

type TokenStore interface {
	SaveAccess(ctx context.Context, id, userID, clientID string, scopes []string, expiresAt time.Time) error
	GetAccess(ctx context.Context, id string) (*AccessRecord, error)
	// DeleteAccess deletes a single access token record. op.Storage.RevokeToken
	// calls it when it targets an access token: the token itself (being a
	// JWT) still verifies as long as its signature is valid, but
	// introspection and /userinfo can no longer find it.
	DeleteAccess(ctx context.Context, id string) error
	CreateRefresh(ctx context.Context, r *RefreshRecord) (string, error)
	RotateRefresh(ctx context.Context, presented string, fresh *RefreshRecord) (string, error)
	GetRefresh(ctx context.Context, presented string) (*RefreshRecord, error)
	// GetRefreshByID, unlike GetRefresh, looks up by the database ID, NOT
	// by the TOKEN ITSELF. op.Storage.RevokeToken requires this: see the
	// comment on GetRefreshByID itself.
	GetRefreshByID(ctx context.Context, id string) (*RefreshRecord, error)
	// RefreshFamilyID returns the family ID of the PRESENTED TOKEN and does
	// NOT filter for validity (used, revoked and expired records are found
	// too). Because GetRefresh does NOT return the record itself when it
	// detects reuse (returning partial data on the error path would leave
	// the door open for the caller to take it as valid), the family ID is
	// reached through this narrow method; GetRefreshByID is no use here,
	// because what we have is the raw token, not the database ID.
	RefreshFamilyID(ctx context.Context, presented string) (string, error)
	RevokeFamily(ctx context.Context, familyID string) error
	RevokeUser(ctx context.Context, userID, clientID string) error
}

// generateToken produces a 32-byte cryptographically random token and
// returns it in a URL-safe form.
func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("jeton uretilemedi: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// hashToken produces the value that gets stored. The token itself is
// NEVER stored: if the database leaks, the tokens must be unusable.
func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// accessKey is the Redis key of an access token record. Access records
// are short-lived and are deleted automatically by their TTL; that is why
// they are kept in Redis rather than in Postgres.
func accessKey(id string) string { return "kimlik:at:" + id }

// PostgresTokenStore: refresh records live in Postgres (durable, since
// they need the family relation and the revocation history), access
// records live in Redis (short-lived, deleted by TTL).
type PostgresTokenStore struct {
	pool *pgxpool.Pool
	rdb  *redis.Client
}

func NewPostgresTokenStore(pool *pgxpool.Pool, rdb *redis.Client) *PostgresTokenStore {
	return &PostgresTokenStore{pool: pool, rdb: rdb}
}

func (s *PostgresTokenStore) SaveAccess(ctx context.Context, id, userID, clientID string, scopes []string, expiresAt time.Time) error {
	record := AccessRecord{
		ID:        id,
		UserID:    userID,
		ClientID:  clientID,
		Scopes:    scopes,
		ExpiresAt: expiresAt,
	}
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("access kaydi serilenemedi: %w", err)
	}
	// TTL = access lifetime: the record deletes itself and no permanent data
	// is written to Redis. The read side still checks ExpiresAt (a defense
	// against clock skew and against the TTL not having been enforced
	// yet).
	ttl := time.Until(expiresAt)
	if ttl <= 0 {
		return nil
	}
	if err := s.rdb.Set(ctx, accessKey(id), data, ttl).Err(); err != nil {
		return fmt.Errorf("access kaydi yazilamadi: %w", err)
	}
	return nil
}

func (s *PostgresTokenStore) GetAccess(ctx context.Context, id string) (*AccessRecord, error) {
	data, err := s.rdb.Get(ctx, accessKey(id)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrTokenNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("access kaydi okunamadi: %w", err)
	}
	var record AccessRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, fmt.Errorf("access kaydi cozumlenemedi: %w", err)
	}
	if time.Now().UTC().After(record.ExpiresAt) {
		return nil, ErrTokenNotFound
	}
	return &record, nil
}

// DeleteAccess removes the access record from Redis. If the record does
// not exist already (expired or never written) it silently counts as
// success.
func (s *PostgresTokenStore) DeleteAccess(ctx context.Context, id string) error {
	if err := s.rdb.Del(ctx, accessKey(id)).Err(); err != nil {
		return fmt.Errorf("access kaydi silinemedi: %w", err)
	}
	return nil
}

// CreateRefresh starts a new FAMILY: family_id is freshly generated.
// Rotation continues this family, while reuse detection revokes the whole
// family.
func (s *PostgresTokenStore) CreateRefresh(ctx context.Context, r *RefreshRecord) (string, error) {
	token, err := generateToken()
	if err != nil {
		return "", err
	}
	if err := s.insertRefresh(ctx, s.pool, r, uuid.NewString(), token); err != nil {
		return "", err
	}
	return token, nil
}

// execer is the minimal interface satisfied by both the pool and the
// transaction: it lets insertRefresh use the same SQL both inside and
// outside a transaction.
type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func (s *PostgresTokenStore) insertRefresh(ctx context.Context, c execer, r *RefreshRecord, familyID, token string) error {
	_, err := c.Exec(ctx,
		`INSERT INTO refresh_tokens
		   (id, family_id, user_id, client_id, token_hash, scopes, audience, amr, auth_time, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		uuid.NewString(), familyID, r.UserID, r.ClientID, hashToken(token),
		r.Scopes, r.Audience, r.AMR, r.AuthTime, r.ExpiresAt)
	if err != nil {
		return fmt.Errorf("refresh jetonu yazilamadi: %w", err)
	}
	return nil
}

// RotateRefresh consumes the presented token and adds a new token to the
// same family. It runs in a single transaction and locks the row.
func (s *PostgresTokenStore) RotateRefresh(ctx context.Context, presented string, fresh *RefreshRecord) (string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("islem baslatilamadi: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		id, familyID     string
		userID, clientID string
		expiresAt        time.Time
		usedAt           *time.Time
		revokedAt        *time.Time
	)
	// FOR UPDATE: if two requests arrive with the same token and race, the
	// second must see the first one's used_at write, otherwise reuse
	// detection silently slips through.
	err = tx.QueryRow(ctx,
		`SELECT id, family_id, user_id, client_id, expires_at, used_at, revoked_at
		   FROM refresh_tokens WHERE token_hash = $1 FOR UPDATE`,
		hashToken(presented)).Scan(&id, &familyID, &userID, &clientID, &expiresAt, &usedAt, &revokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrTokenNotFound
	}
	if err != nil {
		return "", fmt.Errorf("jeton okunamadi: %w", err)
	}
	// Order matters: a revoked token is already dead (once the family has
	// been revoked), so the reuse alarm must not fire again; the reuse
	// check comes after it.
	if revokedAt != nil {
		return "", ErrTokenNotFound
	}
	if usedAt != nil {
		// Reuse: kill the whole family and return a distinct error.
		if _, err := tx.Exec(ctx,
			`UPDATE refresh_tokens SET revoked_at = now()
			  WHERE family_id = $1 AND revoked_at IS NULL`, familyID); err != nil {
			return "", fmt.Errorf("aile iptal edilemedi: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			// A commit error must NOT swallow the reuse information: the
			// caller must see through errors.Is that this is a theft alarm.
			return "", fmt.Errorf("%w (iptal commit edilemedi: %v)", ErrTokenReused, err)
		}
		return "", ErrTokenReused
	}
	// Ownership: if the calling layer has a bug, another user's (or
	// another client's) record must not be added to the same family. A
	// cheap defense: on a mismatch the token is treated as if it did not
	// exist.
	if userID != fresh.UserID || clientID != fresh.ClientID {
		return "", ErrTokenNotFound
	}
	if time.Now().UTC().After(expiresAt) {
		return "", ErrTokenNotFound
	}

	token, err := generateToken()
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE refresh_tokens SET used_at = now() WHERE id = $1`, id); err != nil {
		return "", fmt.Errorf("jeton tuketilemedi: %w", err)
	}
	// The new record is added with the SAME family_id: the trace of the chain
	// is preserved so that if a reuse is seen later the whole chain can be
	// revoked.
	if err := s.insertRefresh(ctx, tx, fresh, familyID, token); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("rotasyon commit edilemedi: %w", err)
	}
	return token, nil
}

// GetRefresh validates the presented token. The decision order is EXACTLY
// the same as RotateRefresh:
//
//	no record      -> ErrTokenNotFound
//	revoked_at     -> ErrTokenNotFound (family already dead, do not fire the alarm again)
//	used_at        -> ErrTokenReused (REUSE)
//	expired        -> ErrTokenNotFound
//
// Separating ErrTokenReused is mandatory: in the refresh flow the library
// calls TokenRequestByRefreshToken FIRST and ABORTS the flow if it returns
// an error (see op.RefreshTokenExchange), so it NEVER reaches
// RotateRefresh. Without this distinction reuse can never be detected in
// production and family revocation never runs. Leaking to the CLIENT which
// of the two it was is not allowed: the token store distinguishes them, and
// the Storage layer returns the same error to the client in both cases.
func (s *PostgresTokenStore) GetRefresh(ctx context.Context, presented string) (*RefreshRecord, error) {
	var r RefreshRecord
	err := s.pool.QueryRow(ctx,
		`SELECT id, family_id, user_id, client_id, token_hash, scopes, audience, amr,
		        auth_time, expires_at, used_at, revoked_at
		   FROM refresh_tokens WHERE token_hash = $1`,
		hashToken(presented)).Scan(
		&r.ID, &r.FamilyID, &r.UserID, &r.ClientID, &r.TokenHash, &r.Scopes, &r.Audience,
		&r.AMR, &r.AuthTime, &r.ExpiresAt, &r.UsedAt, &r.RevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrTokenNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("jeton okunamadi: %w", err)
	}
	if r.RevokedAt != nil {
		return nil, ErrTokenNotFound
	}
	if r.UsedAt != nil {
		return nil, ErrTokenReused
	}
	if time.Now().UTC().After(r.ExpiresAt) {
		return nil, ErrTokenNotFound
	}
	return &r, nil
}

// RefreshFamilyID finds the family ID from the raw token; it does not
// filter for validity (see the comment on the TokenStore interface).
func (s *PostgresTokenStore) RefreshFamilyID(ctx context.Context, presented string) (string, error) {
	var familyID string
	err := s.pool.QueryRow(ctx,
		`SELECT family_id FROM refresh_tokens WHERE token_hash = $1`,
		hashToken(presented)).Scan(&familyID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrTokenNotFound
	}
	if err != nil {
		return "", fmt.Errorf("aile kimligi okunamadi: %w", err)
	}
	return familyID, nil
}

// GetRefreshByID looks up BY ID (NOT by the token itself). Before calling
// RevokeToken the op library calls GetRefreshTokenInfo and passes the
// returned tokenID (not the raw token) to RevokeToken (see pkg/op/
// token_revocation.go). So in the /revoke flow refresh tokens MUST be
// found by ID; GetRefresh (lookup by hash) is NO USE here. NO validity
// filtering is done (used/revoked/expired are returned too): to reach the
// FamilyID of the record we want to revoke it is enough for the record to
// exist, and revoking an already revoked record again is harmless
// (RevokeFamily is idempotent).
func (s *PostgresTokenStore) GetRefreshByID(ctx context.Context, id string) (*RefreshRecord, error) {
	var r RefreshRecord
	err := s.pool.QueryRow(ctx,
		`SELECT id, family_id, user_id, client_id, token_hash, scopes, audience, amr,
		        auth_time, expires_at, used_at, revoked_at
		   FROM refresh_tokens WHERE id = $1`,
		id).Scan(
		&r.ID, &r.FamilyID, &r.UserID, &r.ClientID, &r.TokenHash, &r.Scopes, &r.Audience,
		&r.AMR, &r.AuthTime, &r.ExpiresAt, &r.UsedAt, &r.RevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrTokenNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("jeton okunamadi: %w", err)
	}
	return &r, nil
}

// RevokeFamily revokes all live tokens of a family.
func (s *PostgresTokenStore) RevokeFamily(ctx context.Context, familyID string) error {
	if _, err := s.pool.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = now()
		  WHERE family_id = $1 AND revoked_at IS NULL`, familyID); err != nil {
		return fmt.Errorf("aile iptal edilemedi: %w", err)
	}
	return nil
}

// RevokeUser revokes all refresh tokens of a user at that client. Logout
// uses it; it does not touch another user's tokens.
func (s *PostgresTokenStore) RevokeUser(ctx context.Context, userID, clientID string) error {
	if _, err := s.pool.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = now()
		  WHERE user_id = $1 AND client_id = $2 AND revoked_at IS NULL`,
		userID, clientID); err != nil {
		return fmt.Errorf("kullanici jetonlari iptal edilemedi: %w", err)
	}
	return nil
}

// Neither implementation may drift from the interface: if one changes,
// the build breaks.
var (
	_ TokenStore = (*PostgresTokenStore)(nil)
	_ TokenStore = (*FakeTokenStore)(nil)
)
