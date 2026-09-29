package kimlik

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
)

// SahteTokenStore, testler icin bellek ici TokenStore. Rotasyon, yeniden
// kullanim tespiti ve aile iptali mantigi PostgresTokenStore ile BIREBIR
// ayni sirada ve ayni kosullarla isler; testler iki uygulamayi ayni
// sozlesmeye gore olcer.
type SahteTokenStore struct {
	mu      sync.Mutex
	access  map[string]*AccessKayit
	refresh map[string]*RefreshKayit // anahtar: token_hash'in string hali
}

func NewSahteTokenStore() *SahteTokenStore {
	return &SahteTokenStore{
		access:  map[string]*AccessKayit{},
		refresh: map[string]*RefreshKayit{},
	}
}

func (s *SahteTokenStore) AccessKaydet(ctx context.Context, id, userID, clientID string, scopes []string, expiresAt time.Time) error {
	// Redis tarafinda TTL <= 0 ise kayit hic yazilmaz; burada da ayni.
	if time.Until(expiresAt) <= 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.access[id] = &AccessKayit{
		ID:        id,
		UserID:    userID,
		ClientID:  clientID,
		Scopes:    scopes,
		ExpiresAt: expiresAt,
	}
	return nil
}

func (s *SahteTokenStore) AccessOku(ctx context.Context, id string) (*AccessKayit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kayit, ok := s.access[id]
	if !ok {
		return nil, ErrJetonYok
	}
	if time.Now().UTC().After(kayit.ExpiresAt) {
		return nil, ErrJetonYok
	}
	return kayit, nil
}

// RefreshOlustur yeni bir AILE baslatir: family_id yeni uretilir.
func (s *SahteTokenStore) RefreshOlustur(ctx context.Context, k *RefreshKayit) (string, error) {
	jeton, err := jetonUret()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ekle(k, uuid.NewString(), jeton)
	return jeton, nil
}

// ekle, s.mu kilitliyken cagrilir. Postgres'teki INSERT ile ayni alanlari
// doldurur; jetonun kendisi degil ozeti saklanir.
func (s *SahteTokenStore) ekle(k *RefreshKayit, familyID, jeton string) {
	ozet := jetonOzet(jeton)
	s.refresh[string(ozet)] = &RefreshKayit{
		ID:        uuid.NewString(),
		FamilyID:  familyID,
		UserID:    k.UserID,
		ClientID:  k.ClientID,
		TokenHash: ozet,
		Scopes:    k.Scopes,
		Audience:  k.Audience,
		AMR:       k.AMR,
		AuthTime:  k.AuthTime,
		ExpiresAt: k.ExpiresAt,
	}
}

// RefreshDondur, Postgres uygulamasiyla ayni sirayla karar verir:
// kayit yok -> revoked -> used (aile iptali) -> suresi gecmis -> rotasyon.
// Bellek ici oldugu icin tek kilit, Postgres'teki islem + FOR UPDATE
// satir kilidinin karsiligidir.
func (s *SahteTokenStore) RefreshDondur(ctx context.Context, sunulan string, yeni *RefreshKayit) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kayit, ok := s.refresh[string(jetonOzet(sunulan))]
	if !ok {
		return "", ErrJetonYok
	}
	// Sira onemli: iptal edilmis jeton zaten olu oldugu icin (aile bir
	// kez iptal edildikten sonra) yeniden kullanim alarmi tekrar
	// calmasin; yeniden kullanim kontrolu ondan sonra gelir.
	if kayit.RevokedAt != nil {
		return "", ErrJetonYok
	}
	if kayit.UsedAt != nil {
		// Yeniden kullanim: ailenin tamamini oldur ve ayri bir hata don.
		s.aileIptal(kayit.FamilyID)
		return "", ErrJetonTekrar
	}
	if time.Now().UTC().After(kayit.ExpiresAt) {
		return "", ErrJetonYok
	}
	jeton, err := jetonUret()
	if err != nil {
		return "", err
	}
	simdi := time.Now().UTC()
	kayit.UsedAt = &simdi
	// Yeni kayit AYNI family_id ile eklenir: zincirin izi korunur ki
	// sonradan bir yeniden kullanim gorulurse tum zincir iptal edilebilsin.
	s.ekle(yeni, kayit.FamilyID, jeton)
	return jeton, nil
}

// RefreshOku, Postgres uygulamasiyla ayni sekilde tuketilmis, iptal
// edilmis ve suresi gecmis jetonlarin hepsine ErrJetonYok doner.
func (s *SahteTokenStore) RefreshOku(ctx context.Context, sunulan string) (*RefreshKayit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kayit, ok := s.refresh[string(jetonOzet(sunulan))]
	if !ok {
		return nil, ErrJetonYok
	}
	if kayit.RevokedAt != nil || kayit.UsedAt != nil || time.Now().UTC().After(kayit.ExpiresAt) {
		return nil, ErrJetonYok
	}
	return kayit, nil
}

func (s *SahteTokenStore) AileIptal(ctx context.Context, familyID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.aileIptal(familyID)
	return nil
}

// aileIptal, s.mu kilitliyken cagrilir. Postgres karsiligi:
// UPDATE ... WHERE family_id = $1 AND revoked_at IS NULL
func (s *SahteTokenStore) aileIptal(familyID string) {
	simdi := time.Now().UTC()
	for _, kayit := range s.refresh {
		if kayit.FamilyID == familyID && kayit.RevokedAt == nil {
			iptal := simdi
			kayit.RevokedAt = &iptal
		}
	}
}

// KullaniciIptal, Postgres karsiligi:
// UPDATE ... WHERE user_id = $1 AND client_id = $2 AND revoked_at IS NULL
func (s *SahteTokenStore) KullaniciIptal(ctx context.Context, userID, clientID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	simdi := time.Now().UTC()
	for _, kayit := range s.refresh {
		if kayit.UserID == userID && kayit.ClientID == clientID && kayit.RevokedAt == nil {
			iptal := simdi
			kayit.RevokedAt = &iptal
		}
	}
	return nil
}

// TumKayitlar, testlerin depolanan bicimi denetlemesi icindir.
func (s *SahteTokenStore) TumKayitlar() []*RefreshKayit {
	s.mu.Lock()
	defer s.mu.Unlock()
	hepsi := make([]*RefreshKayit, 0, len(s.refresh))
	for _, kayit := range s.refresh {
		hepsi = append(hepsi, kayit)
	}
	return hepsi
}
