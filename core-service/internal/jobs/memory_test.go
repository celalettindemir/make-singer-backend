package jobs

import (
	"context"
	"testing"
	"time"

	"github.com/makeasinger/api/internal/model"
)

// TestMemoryStore_MatchesPostgres runs the same scenarios against both stores.
func TestMemoryStore_MatchesPostgres(t *testing.T) {
	type env struct {
		repo     Repo
		sonSeq   func(id string) int64
		backdate func(id string, created, completed time.Time)
		hook     func(func(context.Context, model.Job))
	}
	mem := func() env {
		m := NewMemoryStore(nil)
		return env{
			repo: m,
			sonSeq: func(id string) int64 {
				j, _ := m.Get(ctx(), id)
				return j.LastSeq
			},
			backdate: func(id string, created, completed time.Time) {
				m.mu.Lock()
				defer m.mu.Unlock()
				m.jobs[id].CreatedAt = created
				if completed.IsZero() {
					m.jobs[id].CompletedAt = nil
				} else {
					m.jobs[id].CompletedAt = &completed
				}
			},
			hook: m.OnFinished,
		}
	}
	pg := func(t *testing.T) env {
		pool := testPool(t)
		s := NewPostgresStore(pool, nil)
		return env{repo: s,
			sonSeq: func(id string) int64 {
				var n int64
				_ = pool.QueryRow(ctx(), `SELECT son_seq FROM uretim.isler WHERE id=$1`, id).Scan(&n)
				return n
			},
			backdate: pgBackdate(pool), hook: s.OnFinished}
	}
	for name, mk := range map[string]func(*testing.T) env{
		"memory":   func(*testing.T) env { return mem() },
		"postgres": pg,
	} {
		t.Run(name, func(t *testing.T) {
			scenarios := map[string]func(*testing.T, env){
				"seq":      func(t *testing.T, e env) { scenarioConsecutiveSeq(t, e.repo, e.sonSeq) },
				"skip":     func(t *testing.T, e env) { scenarioSkipWrite(t, e.repo) },
				"owner":    func(t *testing.T, e env) { scenarioOwnerOnly(t, e.repo) },
				"lyrics":   func(t *testing.T, e env) { scenarioActiveLyrics(t, e.repo) },
				"list":     func(t *testing.T, e env) { scenarioList(t, e.repo, e.backdate) },
				"stale":    func(t *testing.T, e env) { scenarioStale(t, e.repo) },
				"sweep":    func(t *testing.T, e env) { scenarioSweep(t, e.repo, e.backdate) },
				"finished": func(t *testing.T, e env) { scenarioOnFinished(t, e.repo, e.hook) },
			}
			for sn, sc := range scenarios {
				t.Run(sn, func(t *testing.T) { sc(t, mk(t)) })
			}
		})
	}
}
