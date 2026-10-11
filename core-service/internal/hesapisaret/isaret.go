// Package hesapisaret, silinmis hesap isaretini tutar. HTTP katmani
// (middleware), hesap servisi ve kuyruk isleyicileri ayni isareti okur;
// isaret bu yuzden hicbirine ait olmayan ayri bir pakettedir.
package hesapisaret

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// onek altindaki anahtar, hesabi silinmis bir kullaniciyi isaretler.
const onek = "kimlik:silindi:"

// Omur, isaretin yasam suresidir: is kaydi (job:{id}) omruyle aynidir.
// Silme aninda kuyrukta bekleyen ya da kosan her is, bu sure boyunca
// sahibinin silindigini gorur. Kullanici id'leri UUID'dir ve yeniden
// kullanilmaz; uzun omur baska bir kullaniciyi etkilemez.
const Omur = 24 * time.Hour

// Anahtar, kullanicinin silinmis isaretinin Redis anahtaridir.
func Anahtar(userID string) string { return onek + userID }

// Denetci, bir kullanicinin hesabinin silinip silinmedigini soyler.
type Denetci interface {
	Silinmis(ctx context.Context, userID string) (bool, error)
}

// RedisDenetci, isareti Redis'te EXISTS ile okur.
type RedisDenetci struct{ rdb *redis.Client }

func NewRedisDenetci(rdb *redis.Client) *RedisDenetci { return &RedisDenetci{rdb: rdb} }

func (d *RedisDenetci) Silinmis(ctx context.Context, userID string) (bool, error) {
	n, err := d.rdb.Exists(ctx, Anahtar(userID)).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
