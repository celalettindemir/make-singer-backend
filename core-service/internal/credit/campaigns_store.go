package credit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

var _ CampaignStore = (*PostgresStore)(nil)

const campaignColumns = `id, ad, kota, kisi_basi, gecerlilik_gun, katilan, bitis_tarihi, durum, baslangic, durdurma, olusturan`

func scanCampaign(row pgx.Row) (Campaign, error) {
	var c Campaign
	var status string
	var ends *time.Time
	if err := row.Scan(&c.ID, &c.Name, &c.Quota, &c.PerUser, &c.ValidDays, &c.Joined,
		&ends, &status, &c.StartedAt, &c.StoppedAt, &c.CreatedBy); err != nil {
		return Campaign{}, err
	}
	if ends != nil {
		d := dateOf(*ends)
		c.EndsAt = &d
	}
	c.Status = CampaignStatus(status)
	return c, nil
}

// CreateCampaign stops the active campaign and inserts the new one in one
// transaction. An advisory lock serialises two creates (the partial unique
// index allows one active row).
func (s *PostgresStore) CreateCampaign(ctx context.Context, in NewCampaign, by string, now time.Time) (Campaign, *Campaign, error) {
	var created Campaign
	var replaced *Campaign
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('kredi.kampanya'))`); err != nil {
			return fmt.Errorf("could not lock the campaigns: %w", err)
		}
		old, err := scanCampaign(tx.QueryRow(ctx,
			`UPDATE kredi.kampanya SET durum = 'durduruldu', durdurma = $1
			 WHERE durum = 'aktif' RETURNING `+campaignColumns, now))
		switch {
		case err == nil:
			replaced = &old
		case errors.Is(err, pgx.ErrNoRows):
		default:
			return fmt.Errorf("could not stop the active campaign: %w", err)
		}
		created, err = scanCampaign(tx.QueryRow(ctx,
			`INSERT INTO kredi.kampanya (ad, kota, kisi_basi, gecerlilik_gun, bitis_tarihi, durum, baslangic, olusturan)
			 VALUES ($1, $2, $3, $4, $5::date, 'aktif', $6, $7) RETURNING `+campaignColumns,
			in.Name, in.Quota, in.PerUser, in.ValidDays, in.EndsAt, now, by))
		if err != nil {
			return fmt.Errorf("could not create the campaign: %w", err)
		}
		return nil
	})
	if err != nil {
		return Campaign{}, nil, err
	}
	return created, replaced, nil
}

// StopCampaign stops an active campaign; any other state is final.
func (s *PostgresStore) StopCampaign(ctx context.Context, id int64, now time.Time) (Campaign, error) {
	c, err := scanCampaign(s.pool.QueryRow(ctx,
		`UPDATE kredi.kampanya SET durum = 'durduruldu', durdurma = $2
		 WHERE id = $1 AND durum = 'aktif' RETURNING `+campaignColumns, id, now))
	if err == nil {
		return c, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Campaign{}, fmt.Errorf("could not stop the campaign: %w", err)
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM kredi.kampanya WHERE id = $1)`, id).Scan(&exists); err != nil {
		return Campaign{}, fmt.Errorf("could not read the campaign: %w", err)
	}
	if !exists {
		return Campaign{}, ErrCampaignNotFound
	}
	return Campaign{}, ErrCampaignNotActive
}

// CampaignOverview lists campaigns newest first and the month numbers.
func (s *PostgresStore) CampaignOverview(ctx context.Context, now time.Time) (CampaignOverview, error) {
	var o CampaignOverview
	rows, err := s.pool.Query(ctx,
		`SELECT `+campaignColumns+` FROM kredi.kampanya ORDER BY id DESC`)
	if err != nil {
		return o, fmt.Errorf("could not list the campaigns: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		c, err := scanCampaign(rows)
		if err != nil {
			return o, fmt.Errorf("could not read a campaign: %w", err)
		}
		o.Campaigns = append(o.Campaigns, c)
	}
	if err := rows.Err(); err != nil {
		return o, fmt.Errorf("could not list the campaigns: %w", err)
	}
	rows.Close()
	for i := range o.Campaigns {
		if o.Campaigns[i].Status == CampaignActive {
			a := o.Campaigns[i]
			o.Active = &a
			o.QuotaLeft = max(a.Quota-a.Joined, 0)
			break
		}
	}
	y, m, _ := now.In(istanbul).Date()
	monthStart := time.Date(y, m, 1, 0, 0, 0, 0, istanbul)
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*), coalesce(sum(k.kisi_basi), 0)
		 FROM kredi.kampanya_katilim p JOIN kredi.kampanya k ON k.id = p.kampanya_id
		 WHERE p.olusturuldu >= $1`, monthStart).Scan(&o.MonthPeople, &o.MonthCredits); err != nil {
		return o, fmt.Errorf("could not sum the month: %w", err)
	}
	return o, nil
}

const recentParticipants = 20

// CampaignDetail returns one campaign with distributed/used credits and the
// latest participants.
func (s *PostgresStore) CampaignDetail(ctx context.Context, id int64) (CampaignDetail, error) {
	c, err := scanCampaign(s.pool.QueryRow(ctx, `SELECT `+campaignColumns+` FROM kredi.kampanya WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return CampaignDetail{}, ErrCampaignNotFound
	}
	if err != nil {
		return CampaignDetail{}, fmt.Errorf("could not read the campaign: %w", err)
	}
	d := CampaignDetail{Campaign: c, Distributed: c.Joined * c.PerUser}
	// Used: what spends took from this campaign's lots, net of refunds.
	// Gift and expiry movements are not use.
	if err := s.pool.QueryRow(ctx, `
		SELECT coalesce(-sum(dh.delta), 0)::int
		FROM kredi.dilim_hareket dh
		JOIN kredi.dilim l ON l.id = dh.dilim_id
		JOIN kredi.hareket m ON m.id = dh.hareket_id
		WHERE l.kaynak = 'kampanya' AND l.ref LIKE $1 AND m.tur NOT IN ('kampanya', 'kampanya_sure')`,
		fmt.Sprintf("%d:%%", id)).Scan(&d.Used); err != nil {
		return CampaignDetail{}, fmt.Errorf("could not sum the used credits: %w", err)
	}
	if d.Distributed > 0 {
		d.UsedPercent = min((d.Used*100+d.Distributed/2)/d.Distributed, 100)
	}
	rows, err := s.pool.Query(ctx,
		`SELECT kullanici_id::text, olusturuldu FROM kredi.kampanya_katilim
		 WHERE kampanya_id = $1 AND kullanici_id IS NOT NULL ORDER BY olusturuldu DESC LIMIT $2`, id, recentParticipants)
	if err != nil {
		return CampaignDetail{}, fmt.Errorf("could not list the participants: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var p Participant
		if err := rows.Scan(&p.UserID, &p.JoinedAt); err != nil {
			return CampaignDetail{}, fmt.Errorf("could not read a participant: %w", err)
		}
		d.Recent = append(d.Recent, p)
	}
	return d, rows.Err()
}

// JoinCampaign is the quota-safe gift. The active campaign row is locked
// FOR UPDATE, so two signups can never take the last place; the
// participation row (unique user and unique e-mail hash), the counter and
// the lot commit together.
func (s *PostgresStore) JoinCampaign(ctx context.Context, j SignupJoin, now time.Time) (bool, error) {
	joined := false
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var (
			id               int64
			perUser          int
			quota, validDays int
			joinedCount      int
			startedAt        time.Time
			endsOn           *time.Time
		)
		err := tx.QueryRow(ctx,
			`SELECT id, kota, kisi_basi, gecerlilik_gun, katilan, baslangic, bitis_tarihi
			 FROM kredi.kampanya WHERE durum = 'aktif' FOR UPDATE`).
			Scan(&id, &quota, &perUser, &validDays, &joinedCount, &startedAt, &endsOn)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("could not lock the active campaign: %w", err)
		}
		if j.CreatedAt.Before(startedAt) || joinedCount >= quota {
			return nil
		}
		if endsOn != nil && istanbulDay(now).After(dateOf(*endsOn)) {
			return nil // past its end day (Istanbul), the sweep has not run yet
		}
		tag, err := tx.Exec(ctx,
			`INSERT INTO kredi.kampanya_katilim (kampanya_id, kullanici_id, eposta_karma, olusturuldu)
			 VALUES ($1, $2::text::uuid, $3, $4) ON CONFLICT DO NOTHING`,
			id, j.UserID, j.EmailHash, now)
		if err != nil {
			return fmt.Errorf("could not record the participation: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return nil // this user or e-mail already had its gift
		}
		if _, err := tx.Exec(ctx,
			`UPDATE kredi.kampanya SET katilan = katilan + 1,
			   durum = CASE WHEN katilan + 1 >= kota THEN 'kota_doldu' ELSE durum END
			 WHERE id = $1`, id); err != nil {
			return fmt.Errorf("could not count the participation: %w", err)
		}
		ref := fmt.Sprintf("%d:%s", id, j.UserID)
		expires := now.AddDate(0, 0, validDays)
		if _, err := grantTx(ctx, tx, j.UserID, turKampanya, ref, Grant{
			Source: LotCampaign, Amount: perUser, ExpiresAt: &expires, CampaignID: &id,
		}, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE kredi.kampanya_katilim SET dilim_id = (SELECT id FROM kredi.dilim WHERE kaynak = 'kampanya' AND ref = $2)
			 WHERE kampanya_id = $1 AND kullanici_id = $3::text::uuid`, id, ref, j.UserID); err != nil {
			return fmt.Errorf("could not link the lot: %w", err)
		}
		joined = true
		return nil
	})
	return joined, err
}

// EndCampaigns marks active campaigns past their end date as ended.
func (s *PostgresStore) EndCampaigns(ctx context.Context, now time.Time) (int, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE kredi.kampanya SET durum = 'bitti'
		 WHERE durum = 'aktif' AND bitis_tarihi IS NOT NULL AND bitis_tarihi < $1::date`, istanbulDay(now))
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// MissingGifts reads the identity users table (same database) for signups
// the active campaign should have covered but has no participation row for.
func (s *PostgresStore) MissingGifts(ctx context.Context, since, now time.Time, limit int) ([]SignupJoin, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT u.id::text, u.email, u.created_at
		FROM kredi.kampanya k
		JOIN users u ON u.created_at >= k.baslangic AND u.created_at >= $1
		WHERE k.durum = 'aktif' AND k.katilan < k.kota
		  AND (k.bitis_tarihi IS NULL OR k.bitis_tarihi >= $2::date)
		  AND NOT EXISTS (SELECT 1 FROM kredi.kampanya_katilim p WHERE p.kullanici_id = u.id)
		ORDER BY u.created_at LIMIT $3`, since, istanbulDay(now), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SignupJoin
	for rows.Next() {
		var j SignupJoin
		var email string
		if err := rows.Scan(&j.UserID, &email, &j.CreatedAt); err != nil {
			return nil, err
		}
		j.EmailHash = emailHash(email)
		out = append(out, j)
	}
	return out, rows.Err()
}
