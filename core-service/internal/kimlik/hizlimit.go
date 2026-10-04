package kimlik

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// NEDEN BU DOSYA VAR
//
// OP dinleyicisi ikinci bir net/http sunucusudur ve ana API'nin Fiber
// middleware zincirinden (dolayisiyla internal/middleware/ratelimit.go'dan)
// TAMAMEN AYRIDIR. Yani POST /giris ve POST /kayit, kimlik dogrulamasi
// GEREKTIRMEYEN ve hic hiz limiti OLMAYAN uclardi. Inceleme sirasinda
// canli olculen uc etki:
//
//  1. Sinirsiz sifre brute-force: 25 yanlis denemenin hicbiri 429 almadi.
//  2. CPU DoS: bcrypt cost 12 ile istek basi ~258 ms CPU. OP ile ana API
//     AYNI binary'de kostugu icin birkac paralel baglanti pod'un CPU'sunu
//     tuketip /api/* ucarini da dusurur.
//  3. Sinirsiz hesap uretimi: e-posta dogrulama ve CAPTCHA MVP'de yok.
//
// Bu dosya her uc etkiyi de Redis tabanli bir sabit pencere sayaciyla
// kapatir. Sayac kalibi internal/middleware/ratelimit.go'dakiyle AYNIDIR
// (SET key 0 EX pencere NX + INCR, TEK bir TxPipeline icinde): TTL
// olusturma aninda kenetlenir, yani "TTL'siz kalan anahtar yuzunden
// kalici kilit" tuzagina dusulmez.

// hizLimitOnEki, bu paketin Redis anahtar on ekidir. API tarafindaki
// "ratelimit:" on ekinden AYRI: oradaki sayaclar kullanici kimligine,
// buradakiler kimlik dogrulanmamis bir uca (IP / e-posta ozeti) baglidir
// ve ikisinin anahtar uzayi karismamali.
const hizLimitOnEki = "kimlik:hizlimit"

// hizLimitRedisZamanAsimi, sayac islemleri icin ust sinir. Redis yavassa
// giris ucu kilitlenip kalmamali; sure dolarsa FAIL-CLOSED calisir (503).
const hizLimitRedisZamanAsimi = 2 * time.Second

// hataHizLimit, 429 govdesinde donen metin. Hangi sayacin (IP mi e-posta
// mi) doldugu SOYLENMEZ: e-posta sayacinin dolmasi "bu e-posta deneniyor"
// bilgisini dogrulardi.
const hataHizLimit = "Cok fazla deneme yapildi, lutfen biraz sonra tekrar deneyin."

// hataHizLimitDenetlenemedi, Redis'e ulasilamadiginda donen metin.
//
// FAIL-CLOSED gerekcesi: Redis bu akis icin ZATEN zorunludur — auth
// istekleri (IstekDepo) ve cerez/oturum baglamasi (OturumDepo) Redis'te
// tutulur. Redis yokken giris akisi HICBIR SEKILDE tamamlanamaz, yani
// istegi burada reddetmek yeni bir bagimlilik getirmez; yalnizca zaten
// olan arizayi dogru durum koduyla bildirir. Fail-open ise Redis'i
// dusurebilen birine sinirsiz brute-force ve sinirsiz hesap uretimi
// verirdi.
const hataHizLimitDenetlenemedi = "Istek su an denetlenemiyor, lutfen biraz sonra tekrar deneyin."

// HizLimitAyar, OP dinleyicisindeki hiz limiti degerleri. Degerler
// config'ten (AuthConfig) gelir; burada yalnizca pencereler sabittir.
type HizLimitAyar struct {
	// GirisIPPerMin, POST /giris icin IP basina dakikadaki ust sinir.
	GirisIPPerMin int

	// GirisEpostaPerSaat, POST /giris icin e-posta basina saatteki ust
	// sinir. IP sayaci dagitik bir brute-force'u (her istek farkli IP)
	// durdurmaz; bu sayac tek bir hesaba yonelen denemeleri sinirlar.
	GirisEpostaPerSaat int

	// KayitIPPerSaat, POST /kayit icin IP basina saatteki ust sinir.
	KayitIPPerSaat int

	// GuvenilenProxy, istek bize ulasmadan once gecen GUVENILEN ters
	// proxy sayisidir (bu kurulumda Traefik => 1). Bkz. istemciIP.
	GuvenilenProxy int
}

// HizLimit, OP dinleyicisinin giris/kayit uclari icin Redis tabanli
// sabit pencere hiz limitidir.
type HizLimit struct {
	rdb  *redis.Client
	ayar HizLimitAyar

	// epostaAnahtari, e-posta ozetini uretmek icin kullanilan HMAC
	// anahtaridir (uretimde AUTH_CRYPTO_KEY). E-posta Redis anahtarinda
	// DUZ METIN tutulmaz: e-posta PII'dir ve Redis'i okuyabilen biri
	// aksi halde "son bir saatte hangi hesaplara giris denendi"
	// listesini duz metin olarak elde ederdi. Duz SHA-256 de yeterli
	// degil (bilinen bir e-posta listesi dogrudan denenebilir); anahtarli
	// HMAC, anahtari bilmeyen icin ozetleri baglantisiz kilar.
	epostaAnahtari []byte
}

// NewHizLimit, hiz limitini kurar. rdb nil ise veya ayar degerleri
// gecersizse hata doner: sessizce "limitsiz" moda dusmek tam da
// kapatmaya calistigimiz kusurdur.
func NewHizLimit(rdb *redis.Client, ayar HizLimitAyar, epostaAnahtari []byte) (*HizLimit, error) {
	if rdb == nil {
		return nil, errors.New("hiz limiti: redis istemcisi yok")
	}
	if ayar.GirisIPPerMin <= 0 {
		return nil, fmt.Errorf("hiz limiti: giris_ip_per_min pozitif olmali, gelen: %d", ayar.GirisIPPerMin)
	}
	if ayar.GirisEpostaPerSaat <= 0 {
		return nil, fmt.Errorf("hiz limiti: giris_eposta_per_saat pozitif olmali, gelen: %d", ayar.GirisEpostaPerSaat)
	}
	if ayar.KayitIPPerSaat <= 0 {
		return nil, fmt.Errorf("hiz limiti: kayit_ip_per_saat pozitif olmali, gelen: %d", ayar.KayitIPPerSaat)
	}
	if ayar.GuvenilenProxy < 0 {
		return nil, fmt.Errorf("hiz limiti: guvenilen_proxy negatif olamaz, gelen: %d", ayar.GuvenilenProxy)
	}
	if len(epostaAnahtari) == 0 {
		return nil, errors.New("hiz limiti: e-posta ozet anahtari bos")
	}
	return &HizLimit{rdb: rdb, ayar: ayar, epostaAnahtari: epostaAnahtari}, nil
}

// sayac, tek bir hiz limiti sayacini tanimlar.
type sayac struct {
	anahtar string
	sinir   int
	pencere time.Duration
}

// GirisSar, POST /giris handler'ini hiz limitiyle sarar: once IP sayaci,
// sonra e-posta sayaci. Sira onemli: IP sayaci form govdesini okumadan
// once kosar, boylece govdeyi hic ayristirmadan en ucuz yolda reddeder.
func (h *HizLimit) GirisSar(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.izinVer(w, r, sayac{
			anahtar: h.ipAnahtar("giris", r),
			sinir:   h.ayar.GirisIPPerMin,
			pencere: time.Minute,
		}) {
			return
		}
		// Form burada ayristirilir; asagidaki handler ParseForm'u tekrar
		// cagirsa bile net/http ikinci cagriyi no-op yapar (r.PostForm
		// zaten dolu), yani govde iki kez okunmaz.
		if err := r.ParseForm(); err != nil {
			http.Error(w, "form okunamadi", http.StatusBadRequest)
			return
		}
		// epostaSayacAnahtari: KUCULTME ZORUNLU, bkz. fonksiyon notu.
		eposta := epostaSayacAnahtari(r.PostFormValue("eposta"))
		if eposta == "" {
			// E-postasiz bir POST zaten giris olamaz; IP sayaci onu
			// saydi, e-posta sayacini kirletmeye gerek yok.
			next.ServeHTTP(w, r)
			return
		}
		if !h.izinVer(w, r, sayac{
			anahtar: h.epostaAnahtar("giris", eposta),
			sinir:   h.ayar.GirisEpostaPerSaat,
			pencere: time.Hour,
		}) {
			return
		}
		next.ServeHTTP(w, r)
	})
}

// SayfaGetSar, GET /giris ve GET /kayit'i IP basina dakikalik bir
// sayacla sarar. ad, sayac anahtarindaki uc adidir ("giris" / "kayit").
//
// NEDEN POST SAYACINDAN AYRI ANAHTAR: GET ile POST ayni anahtari
// paylassa formu acmak sifre deneme butcesini tuketirdi ve mesru
// kullanici kendi akisini kilitlerdi. Ayrica 429'un anlami karisirdi.
//
// NEDEN AYNI SINIR DEGERI (GirisIPPerMin): GET ucuz bir uctur (sablon
// uretimi; bcrypt yok, kullanici/jeton deposuna dokunulmaz), ama kimlik
// dogrulamasiz ve /authorize'dan daha ucuz bir yuzeydir. Dakikada
// login_ip_per_min kadar sayfa uretimi elle kullanimin cok uzerinde,
// otomatik taramanin ise belirgin altinda; ayri bir yapilandirma
// anahtari eklemeye deger bir ayrisma yok.
func (h *HizLimit) SayfaGetSar(ad string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.izinVer(w, r, sayac{
			anahtar: h.ipAnahtar(ad+"-get", r),
			sinir:   h.ayar.GirisIPPerMin,
			pencere: time.Minute,
		}) {
			return
		}
		next.ServeHTTP(w, r)
	})
}

// KayitSar, POST /kayit handler'ini IP basina saatlik bir sayacla sarar.
// Burada e-posta basina sayac YOKTUR: kayitta e-posta heniz bir hesaba
// ait degil, sayac yalnizca "ayni e-postayi tekrar denemek" durumunu
// olcer ve 409 zaten onu bildiriyor. Kotu kullanimi sinirlayan sey
// hesap uretim HIZIDIR, yani IP sayaci.
func (h *HizLimit) KayitSar(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.izinVer(w, r, sayac{
			anahtar: h.ipAnahtar("kayit", r),
			sinir:   h.ayar.KayitIPPerSaat,
			pencere: time.Hour,
		}) {
			return
		}
		next.ServeHTTP(w, r)
	})
}

// izinVer, sayaci artirir ve istegin devam edip etmeyecegini soyler.
// false donduyse yanit ZATEN yazilmistir (429 veya 503).
func (h *HizLimit) izinVer(w http.ResponseWriter, r *http.Request, s sayac) bool {
	ctx, iptal := context.WithTimeout(r.Context(), hizLimitRedisZamanAsimi)
	defer iptal()

	sayi, err := h.artir(ctx, s)
	if err != nil {
		// FAIL-CLOSED. Hata metni loglanir ama baglanti dizgesi/anahtar
		// icerigi yazilmaz: anahtar e-posta ozeti tasiyor ve hata metni
		// Redis adresini (sir tasiyabilir) icerebilir, bu yuzden
		// yalnizca sabit bir mesaj basilir.
		log.Print("hiz limiti denetlenemedi, istek reddedildi (fail-closed)")
		w.Header().Set("Retry-After", "5")
		http.Error(w, hataHizLimitDenetlenemedi, http.StatusServiceUnavailable)
		return false
	}
	if sayi > int64(s.sinir) {
		w.Header().Set("Retry-After", strconv.Itoa(h.bekleSaniye(s)))
		http.Error(w, hataHizLimit, http.StatusTooManyRequests)
		return false
	}
	return true
}

// artir, sabit pencere sayacini TEK bir TxPipeline icinde olusturup
// artirir. SET ... NX + INCR sirasi anahtari yalnizca yokken olusturur
// ve TTL'i olusturma aninda kenetler.
func (h *HizLimit) artir(ctx context.Context, s sayac) (int64, error) {
	boru := h.rdb.TxPipeline()
	boru.SetArgs(ctx, s.anahtar, 0, redis.SetArgs{Mode: "NX", TTL: s.pencere})
	artir := boru.Incr(ctx, s.anahtar)
	// redis.Nil = SET NX uygulanmadi (anahtar zaten vardi); normal akis.
	if _, err := boru.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return 0, err
	}
	return artir.Result()
}

// bekleSaniye, Retry-After degerini uretir. En az 1: TTL okunamazsa veya
// anahtar bir sekilde TTL'siz kalirsa negatif/sifir deger donmemeli.
func (h *HizLimit) bekleSaniye(s sayac) int {
	bekle := s.pencere
	ctx, iptal := context.WithTimeout(context.Background(), hizLimitRedisZamanAsimi)
	defer iptal()
	if ttl, err := h.rdb.TTL(ctx, s.anahtar).Result(); err == nil && ttl > 0 {
		bekle = ttl
	}
	saniye := int(bekle.Seconds())
	if saniye < 1 {
		saniye = 1
	}
	return saniye
}

func (h *HizLimit) ipAnahtar(ad string, r *http.Request) string {
	return fmt.Sprintf("%s:%s:ip:%s", hizLimitOnEki, ad, istemciIP(r, h.ayar.GuvenilenProxy))
}

// epostaSayacAnahtari, hiz limiti sayacinin HMAC girdisini uretir.
//
// NEDEN KUCULTME: hesap kimligi Postgres'te HARF DUYARSIZDIR
// (migrations/001_kullanici.sql: UNIQUE (lower(email)) ve ByEmail
// "WHERE lower(email) = lower($1)"). Sayac anahtari ise
// epostaNormalize'den (yalnizca TrimSpace, bkz. kullanici_depo.go)
// turetiliyordu, yani "Kurban@x.com" AYNI hesap ama FARKLI anahtar
// demekti: dagitik bir brute-force harf varyantlariyla sinirsiz deneme
// yapabiliyordu ve sayacin tek varlik sebebi tam bu senaryoydu. Canli
// olcum (sinir 2/saat): ayni yazim 401 401 429, uc harf varyanti ise
// hepsi 401.
//
// SAKLANAN e-posta DEGISMEZ: kucultme yalnizca sayac anahtarina
// uygulanir, kullanicinin yazdigi bicim goruntuleme icin korunur (bkz.
// epostaNormalize notu). Sahte depo ayni kalibi zaten uyguluyor
// (kullanici_depo_sahte.go sahteEpostaAnahtar).
func epostaSayacAnahtari(eposta string) string {
	return strings.ToLower(epostaNormalize(eposta))
}

// epostaAnahtar, e-postanin HMAC-SHA256 ozetinden anahtar uretir.
// E-posta Redis'e DUZ METIN yazilmaz (bkz. HizLimit.epostaAnahtari).
// Ozet 128 bite kisaltilir: cakisma olasiligi ihmal edilebilir, anahtar
// kisa kalir.
func (h *HizLimit) epostaAnahtar(ad, eposta string) string {
	mac := hmac.New(sha256.New, h.epostaAnahtari)
	mac.Write([]byte(eposta))
	return fmt.Sprintf("%s:%s:eposta:%s", hizLimitOnEki, ad, hex.EncodeToString(mac.Sum(nil)[:16]))
}

// istemciIP, hiz limiti sayacinin baglanacagi "gercek" istemci adresini
// cozer.
//
// NEDEN X-Forwarded-For'a OLDUGU GIBI GUVENILMEZ: basligi istemci de
// yazabilir. Soldaki (ilk) degeri almak, saldirganin her istekte farkli
// bir uydurma IP yazarak sayaci tamamen atlamasina izin verir.
//
// SECILEN YOL: guvenilenProxy, istek bize ulasmadan once gecen GUVENILEN
// ters proxy sayisidir (bu kurulumda Traefik => 1). Her guvenilen proxy
// X-Forwarded-For'a GORDUGU adresi EKLER, yani listenin SONDAN
// guvenilenProxy. elemani, en ictteki guvenilen proxy'nin gordugu
// adrestir ve istemci tarafindan uydurulamaz: saldirgan sola ne yazarsa
// yazsin, Traefik kendi gordugu adresi sona ekler ve biz onu okuruz.
//
// guvenilenProxy == 0 ise (proxy'siz kurulum) X-Forwarded-For HIC
// okunmaz, dogrudan RemoteAddr kullanilir.
//
// Secilen eleman gecerli bir IP degilse yine RemoteAddr'a dusulur:
// uydurma/bozuk bir deger Redis anahtari uzayini kirletmesin.
//
// SINIR: pod'a Traefik'i ATLAYARAK dogrudan ulasabilen biri icin
// X-Forwarded-For tumuyle kendi denetimindedir. Bu kurulumda OP
// dinleyicisi (8001) kume ici bir servistir ve disariya yalnizca Traefik
// uzerinden acilir; dogrudan erisebilen bir saldirgan icin zaten
// RemoteAddr de kendi adresidir, yani bu bir gerileme degil.
func istemciIP(r *http.Request, guvenilenProxy int) string {
	if guvenilenProxy > 0 {
		// TUM X-Forwarded-For SATIRLARI birlestirilir. Header.Get
		// yalnizca ILK satiri okur; istemci iki ayri XFF satiri
		// gonderdiginde (HTTP buna izin verir ve net/http ikisini de
		// saklar) sagdan sayma SALDIRGANIN satirinda yapilirdi ve
		// inceleyici bu yolla istemciIP'den "9.9.9.9" aldi. RFC 7230
		// 3.2.2'ye gore ayni basligin coklu satiri virgulle
		// birlestirilmis TEK bir listeye esdegerdir; guvenilen
		// proxy'nin ekledigi deger boylece yine EN SAGDA kalir.
		if xff := strings.Join(r.Header.Values("X-Forwarded-For"), ","); xff != "" {
			parcalar := strings.Split(xff, ",")
			if i := len(parcalar) - guvenilenProxy; i >= 0 {
				aday := strings.TrimSpace(parcalar[i])
				if net.ParseIP(aday) != nil {
					return aday
				}
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
