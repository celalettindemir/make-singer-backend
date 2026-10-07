package kimlik

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// federeAnahtar, (saglayici, sub) ciftini map anahtarina cevirir. "\x00"
// ayirici secildi: saglayici veya sub degerlerinde gecmesi beklenmeyen,
// yani ayirici ile veri karismaz.
func federeAnahtar(saglayici, sub string) string {
	return saglayici + "\x00" + sub
}

// SahteFedereDepo, testler icin bellek ici FedereDepo.
//
// SOZLESME: PostgresFedereDepo ile BIREBIR AYNI davranmak zorundadir
// (bkz. TestFedereDepoSozlesmesi). Ayrica SahteUserStore'u PAYLASIR:
// FedereKullaniciOlustur orada olusturdugu kullaniciyi, KimlikleKullanici
// ise ayni depodan okur -- aynen Postgres uygulamasinin tek havuzu
// paylasmasi gibi.
type SahteFedereDepo struct {
	mu         sync.Mutex
	kullanici  *SahteUserStore
	eslesmeler map[string]string // federeAnahtar(saglayici, sub) -> userID
}

func NewSahteFedereDepo(kullanici *SahteUserStore) *SahteFedereDepo {
	return &SahteFedereDepo{
		kullanici:  kullanici,
		eslesmeler: map[string]string{},
	}
}

func (d *SahteFedereDepo) KimlikleKullanici(ctx context.Context, saglayici, sub string) (*User, error) {
	d.mu.Lock()
	userID, ok := d.eslesmeler[federeAnahtar(saglayici, sub)]
	d.mu.Unlock()
	if !ok {
		return nil, ErrKimlikYok
	}
	// SahteUserStore.ByID zaten derin kopya doner.
	return d.kullanici.ByID(ctx, userID)
}

func (d *SahteFedereDepo) Bagla(ctx context.Context, saglayici, sub, userID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.baglanabilirMi(saglayici, sub, userID); err != nil {
		return err
	}
	d.eslesmeler[federeAnahtar(saglayici, sub)] = userID
	return nil
}

// baglanabilirMi, Postgres'teki IKI unique kisiti taklit eder: (saglayici,
// sub) birincil anahtari ve (user_id, saglayici) tekilligi. Caller kilidi
// tutuyor olmalidir.
func (d *SahteFedereDepo) baglanabilirMi(saglayici, sub, userID string) error {
	if _, ok := d.eslesmeler[federeAnahtar(saglayici, sub)]; ok {
		return ErrKimlikKullanimda
	}
	onek := saglayici + "\x00"
	for anahtar, uid := range d.eslesmeler {
		if uid == userID && strings.HasPrefix(anahtar, onek) {
			return ErrKimlikKullanimda
		}
	}
	return nil
}

func (d *SahteFedereDepo) FedereKullaniciOlustur(
	ctx context.Context, saglayici, sub, email, ad string, epostaDogrulandi bool,
) (*User, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.baglanabilirMi(saglayici, sub, ""); err != nil {
		return nil, err
	}
	k, err := d.kullanici.federeEkle(email, ad, epostaDogrulandi)
	if err != nil {
		return nil, err
	}
	d.eslesmeler[federeAnahtar(saglayici, sub)] = k.ID
	return k, nil
}

func (d *SahteFedereDepo) SaglayicilarByUserID(ctx context.Context, userID string) ([]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var sag []string
	for anahtar, uid := range d.eslesmeler {
		if uid != userID {
			continue
		}
		idx := strings.IndexByte(anahtar, 0)
		sag = append(sag, anahtar[:idx])
	}
	sort.Strings(sag)
	return sag, nil
}

// federeEkle, SahteUserStore'a sifresiz (yalnizca federe) bir kullanici
// ekler. PostgresUserStore'un federe yolu gibi password_hash'i BOS
// birakir: SifreDogru boylece her sifre icin false doner. Create ile AYNI
// e-posta tekilligi kuralini uygular (buyuk/kucuk harf duyarsiz).
func (s *SahteUserStore) federeEkle(email, ad string, epostaDogrulandi bool) (*User, error) {
	anahtar := sahteEpostaAnahtar(email)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.epostayaGor[anahtar]; ok {
		return nil, ErrEpostaKullanimda
	}
	k := &User{
		ID:            uuid.NewString(),
		Email:         epostaNormalize(email),
		EmailVerified: epostaDogrulandi,
		Name:          strings.TrimSpace(ad),
		CreatedAt:     time.Now().UTC(),
	}
	s.epostayaGor[anahtar] = k
	s.idyeGore[k.ID] = k
	return kullaniciKopya(k), nil
}

var _ FedereDepo = (*SahteFedereDepo)(nil)
