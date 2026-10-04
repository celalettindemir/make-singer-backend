package kimlik

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// SahteUserStore, testler icin bellek ici UserStore.
//
// SOZLESME: PostgresUserStore ile BIREBIR AYNI davranmak zorundadir
// (bkz. TestUserStoreSozlesmesi). Uyusmayan bir sahte depo, testler
// gecerken uretimin sasmasina yol acar; bu dalda tam bu ayrisma uc ayri
// kusur uretti:
//
//   - Create/ByEmail e-postayi normalize etmiyordu (uretim trim ediyor),
//     yani Email/Name alanlari uretimden FARKLI saklaniyordu.
//   - Buyuk/kucuk harf duyarsiz tekillik eksikti: " e@t " ve "e@t" icin
//     Postgres ErrEpostaKullanimda donerken sahte depo IKINCI hesabi
//     olusturuyordu.
//   - ByEmail/ByID DAHILI isaretci donduruyordu; cagiran donen *User'i
//     mutasyona ugratirsa depo bozuluyordu. PostgresUserStore her
//     okumada satiri yeniden tarayarak dogal olarak taze bir yapi doner,
//     sahte deponun ayni yalitimi ELLE saglamasi gerekir (jeton
//     depolarindaki refreshKopya/accessKopya ile ayni gerekce).
type SahteUserStore struct {
	mu          sync.Mutex
	epostayaGor map[string]*User
	idyeGore    map[string]*User
}

func NewSahteUserStore() *SahteUserStore {
	return &SahteUserStore{
		epostayaGor: map[string]*User{},
		idyeGore:    map[string]*User{},
	}
}

// sahteEpostaAnahtar, Postgres'teki "UNIQUE (lower(email))" kisitini
// taklit eder: once uretimin uyguladigi normalizasyon (trim), sonra
// buyuk/kucuk harf duyarsizlik.
func sahteEpostaAnahtar(eposta string) string {
	return strings.ToLower(epostaNormalize(eposta))
}

func (s *SahteUserStore) Create(ctx context.Context, email, name, password string) (*User, error) {
	hash, err := sifreHashle(password)
	if err != nil {
		return nil, err
	}
	anahtar := sahteEpostaAnahtar(email)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.epostayaGor[anahtar]; ok {
		return nil, ErrEpostaKullanimda
	}
	k := &User{
		ID: uuid.NewString(),
		// Uretimdeki PostgresUserStore.Create ile AYNI normalizasyon.
		Email:        epostaNormalize(email),
		Name:         strings.TrimSpace(name),
		PasswordHash: hash,
		CreatedAt:    time.Now().UTC(),
	}
	s.epostayaGor[anahtar] = k
	s.idyeGore[k.ID] = k
	return kullaniciKopya(k), nil
}

func (s *SahteUserStore) ByEmail(ctx context.Context, email string) (*User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if k, ok := s.epostayaGor[sahteEpostaAnahtar(email)]; ok {
		return kullaniciKopya(k), nil
	}
	return nil, ErrKullaniciYok
}

func (s *SahteUserStore) ByID(ctx context.Context, id string) (*User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if k, ok := s.idyeGore[id]; ok {
		return kullaniciKopya(k), nil
	}
	return nil, ErrKullaniciYok
}

// kullaniciKopya, cagirana DAHILI isaretci vermemek icin kaydin yuzeysel
// bir kopyasini doner. User yalnizca deger alanlardan (string, bool,
// time.Time) olustugu icin yuzeysel kopya yeterlidir; dilim/isaretci
// alan eklenirse burada da klonlanmasi gerekir.
func kullaniciKopya(k *User) *User {
	if k == nil {
		return nil
	}
	kopya := *k
	return &kopya
}
