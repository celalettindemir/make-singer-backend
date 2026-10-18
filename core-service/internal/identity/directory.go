package identity

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Directory is a read-only view of the users table for the admin panel. The
// credit schema has no foreign key to users, so the admin API asks the
// identity side for e-mails and the user list instead of joining across
// schemas. A deleted user has no row and therefore never appears.
type Directory struct {
	pool *pgxpool.Pool
}

// NewDirectory returns a directory over pool.
func NewDirectory(pool *pgxpool.Pool) *Directory {
	return &Directory{pool: pool}
}

// DirectoryUser is the part of a user the admin panel shows.
type DirectoryUser struct {
	ID        string
	Email     string
	CreatedAt time.Time
}

// UserSearch selects users. The three match modes combine with OR; with none
// set every user matches. Results are newest account first.
type UserSearch struct {
	// ID is a full user id (exact match).
	ID string
	// ShortPrefix is a lower-case hex prefix of the id without dashes.
	ShortPrefix string
	// Email is a lower-case substring of the e-mail.
	Email string
	// After continues behind the user (AfterTime, AfterID) of the previous
	// page; the zero AfterTime means the first page.
	AfterTime time.Time
	AfterID   string
	Limit     int
}

// Search returns up to Limit users.
func (d *Directory) Search(ctx context.Context, q UserSearch) ([]DirectoryUser, error) {
	args := []any{}
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	var modes []string
	if q.ID != "" {
		modes = append(modes, "id = "+arg(q.ID)+"::text::uuid")
	}
	if q.ShortPrefix != "" {
		modes = append(modes, "replace(id::text, '-', '') LIKE "+arg(q.ShortPrefix+"%"))
	}
	if q.Email != "" {
		modes = append(modes, "strpos(lower(email), "+arg(q.Email)+") > 0")
	}
	where := "TRUE"
	if len(modes) > 0 {
		where = "(" + strings.Join(modes, " OR ") + ")"
	}
	if !q.AfterTime.IsZero() {
		where += " AND (created_at, id) < (" + arg(q.AfterTime) + "::timestamptz, " + arg(q.AfterID) + "::text::uuid)"
	}
	limit := arg(q.Limit)
	rows, err := d.pool.Query(ctx,
		`SELECT id::text, email, created_at FROM users WHERE `+where+
			` ORDER BY created_at DESC, id DESC LIMIT `+limit+`::int`, args...)
	if err != nil {
		return nil, fmt.Errorf("could not search the users: %w", err)
	}
	defer rows.Close()
	var out []DirectoryUser
	for rows.Next() {
		var u DirectoryUser
		if err := rows.Scan(&u.ID, &u.Email, &u.CreatedAt); err != nil {
			return nil, fmt.Errorf("could not read a user: %w", err)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// ByID returns one user; the bool is false when there is no such user.
func (d *Directory) ByID(ctx context.Context, id string) (DirectoryUser, bool, error) {
	rows, err := d.pool.Query(ctx,
		`SELECT id::text, email, created_at FROM users WHERE id = $1::text::uuid`, id)
	if err != nil {
		return DirectoryUser{}, false, fmt.Errorf("could not read the user: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return DirectoryUser{}, false, rows.Err()
	}
	var u DirectoryUser
	if err := rows.Scan(&u.ID, &u.Email, &u.CreatedAt); err != nil {
		return DirectoryUser{}, false, fmt.Errorf("could not read the user: %w", err)
	}
	return u, true, nil
}

// Emails maps user ids to their e-mails; unknown (deleted) ids are absent.
func (d *Directory) Emails(ctx context.Context, ids []string) (map[string]string, error) {
	out := make(map[string]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := d.pool.Query(ctx,
		`SELECT id::text, email FROM users WHERE id = ANY($1::text[]::uuid[])`, ids)
	if err != nil {
		return nil, fmt.Errorf("could not read the e-mails: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, email string
		if err := rows.Scan(&id, &email); err != nil {
			return nil, fmt.Errorf("could not read an e-mail: %w", err)
		}
		out[id] = email
	}
	return out, rows.Err()
}
