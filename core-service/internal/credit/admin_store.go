package credit

import (
	"context"
	"fmt"
	"strings"
	"time"
)

var _ AdminStore = (*PostgresStore)(nil)

// UserSummaries reads the balance, the plan and the gift campaign name of
// each user in ids, three queries for the whole page. Users without any
// credit data read as balance 0, no plan, no campaign.
func (s *PostgresStore) UserSummaries(ctx context.Context, ids []string, now time.Time) (map[string]UserSummary, error) {
	out := make(map[string]UserSummary, len(ids))
	for _, id := range ids {
		out[id] = UserSummary{}
	}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT i.id::text,
		       COALESCE((SELECT SUM(l.kalan) FROM kredi.dilim l
		                 WHERE l.kullanici_id = i.id AND NOT l.sure_islendi AND (l.bitis IS NULL OR l.bitis > $2)), 0)::int
		     - COALESCE((SELECT h.borc FROM kredi.hesap h WHERE h.kullanici_id = i.id), 0)
		FROM unnest($1::text[]::uuid[]) AS i(id)`, ids, now)
	if err != nil {
		return nil, fmt.Errorf("could not read the balances: %w", err)
	}
	for rows.Next() {
		var id string
		var bal int
		if err := rows.Scan(&id, &bal); err != nil {
			rows.Close()
			return nil, fmt.Errorf("could not read a balance: %w", err)
		}
		u := out[id]
		u.Balance = bal
		out[id] = u
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("could not read the balances: %w", err)
	}

	rows, err = s.pool.Query(ctx, `
		SELECT DISTINCT ON (kullanici_id) kullanici_id::text, urun, durum, donem_sonu
		FROM kredi.abonelik WHERE kullanici_id = ANY($1::text[]::uuid[])
		ORDER BY kullanici_id, donem_sonu DESC NULLS LAST, orijinal_islem_id`, ids)
	if err != nil {
		return nil, fmt.Errorf("could not read the plans: %w", err)
	}
	for rows.Next() {
		var id string
		var sub Subscription
		var periodEnd *time.Time
		if err := rows.Scan(&id, &sub.Product, &sub.Status, &periodEnd); err != nil {
			rows.Close()
			return nil, fmt.Errorf("could not read a plan: %w", err)
		}
		if periodEnd != nil {
			sub.PeriodEnd = *periodEnd
		}
		u := out[id]
		u.Plan = planOf(&sub)
		out[id] = u
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("could not read the plans: %w", err)
	}

	rows, err = s.pool.Query(ctx, `
		SELECT DISTINCT ON (p.kullanici_id) p.kullanici_id::text, k.ad
		FROM kredi.kampanya_katilim p JOIN kredi.kampanya k ON k.id = p.kampanya_id
		WHERE p.kullanici_id = ANY($1::text[]::uuid[])
		ORDER BY p.kullanici_id, p.olusturuldu DESC`, ids)
	if err != nil {
		return nil, fmt.Errorf("could not read the campaign names: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, fmt.Errorf("could not read a campaign name: %w", err)
		}
		u := out[id]
		u.CampaignName = &name
		out[id] = u
	}
	return out, rows.Err()
}

// AdminMovements lists movements of all users, newest first. It returns up
// to f.Limit rows and the cursor of the next page ("" on the last page).
// A damaged cursor gives ErrBadCursor.
func (s *PostgresStore) AdminMovements(ctx context.Context, f AdminMovementFilter) ([]AdminMovementRow, string, error) {
	args := []any{}
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	// The campaign of a movement: the one it was granted by (kampanya_id) or
	// the one whose lot it touched (spends, refunds, expiry).
	const campaign = `coalesce(m.kampanya_id, lc.cid)`
	where := []string{"TRUE"}
	if f.Tur != "" {
		where = append(where, "m.tur = "+arg(f.Tur))
	}
	if f.Ref != "" {
		where = append(where, "m.ref = "+arg(f.Ref))
	}
	if f.UserID != "" {
		where = append(where, "m.kullanici_id = "+arg(f.UserID)+"::text::uuid")
	}
	if f.CampaignID != nil {
		where = append(where, campaign+" = "+arg(*f.CampaignID)+"::bigint")
	}
	if f.From != nil {
		where = append(where, "m.olusturuldu >= "+arg(*f.From)+"::timestamptz")
	}
	if f.To != nil {
		where = append(where, "m.olusturuldu < "+arg(*f.To)+"::timestamptz")
	}
	if f.Cursor != "" {
		c, err := decodeCursor(f.Cursor)
		if err != nil {
			return nil, "", err
		}
		where = append(where, "(m.olusturuldu, m.id) < ("+arg(c.T)+"::timestamptz, "+arg(c.ID)+"::bigint)")
	}
	limit := f.Limit
	if limit <= 0 {
		limit = AdminPageSize
	}
	rows, err := s.pool.Query(ctx, `
		SELECT m.id, m.kullanici_id::text, m.tur, m.ref, m.sebep, m.tutar, m.yapan, m.notu,
		       m.proje_id::text, m.kademe, m.olusturuldu, `+campaign+`
		FROM kredi.hareket m
		LEFT JOIN LATERAL (
			SELECT split_part(l.ref, ':', 1)::bigint AS cid
			FROM kredi.dilim_hareket dh JOIN kredi.dilim l ON l.id = dh.dilim_id
			WHERE dh.hareket_id = m.id AND m.kampanya_id IS NULL
			  AND l.kaynak = 'kampanya' AND l.ref ~ '^[0-9]+:'
			ORDER BY l.id LIMIT 1
		) lc ON true
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY m.olusturuldu DESC, m.id DESC LIMIT `+arg(limit+1)+`::int`, args...)
	if err != nil {
		return nil, "", fmt.Errorf("could not read the movements: %w", err)
	}
	defer rows.Close()
	var out []AdminMovementRow
	for rows.Next() {
		var r AdminMovementRow
		if err := rows.Scan(&r.ID, &r.UserID, &r.Tur, &r.Ref, &r.Reason, &r.Amount, &r.By, &r.Note,
			&r.ProjectID, &r.Tier, &r.CreatedAt, &r.CampaignID); err != nil {
			return nil, "", fmt.Errorf("could not read a movement: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("could not read the movements: %w", err)
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		next = encodeCursor(out[len(out)-1].Movement)
	}
	return out, next, nil
}
