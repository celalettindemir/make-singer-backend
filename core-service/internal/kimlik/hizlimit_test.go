package kimlik

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// ---- istemciIP: GERCEK istemci adresinin cozumu (ag gerekmez) ----

// X-Forwarded-For'a korsuz guvenmek saldirganin basligi uydurup hiz
// limitini atlamasina izin verir. Secilen yol: guvenilen proxy sayisi
// kadar SAGDAN saymak (her guvenilen proxy gordugu adresi SONA ekler).
func TestIstemciIPGuvenilenProxySayisinaGoreCozulur(t *testing.T) {
	durumlar := []struct {
		ad  string
		xff string
		// xffSatirlari, AYNI basligin birden fazla SATIRI icin; xff ile
		// birlikte kullanilmaz.
		xffSatirlari   []string
		remoteAddr     string
		guvenilenProxy int
		beklenen       string
	}{
		{
			ad: "proxy yok: XFF HIC okunmaz",
			// Proxy'siz kurulumda baslik tumuyle istemci denetiminde.
			xff:            "1.2.3.4",
			remoteAddr:     "203.0.113.9:51234",
			guvenilenProxy: 0,
			beklenen:       "203.0.113.9",
		},
		{
			ad:             "tek proxy (Traefik): son eleman alinir",
			xff:            "198.51.100.7",
			remoteAddr:     "10.0.0.1:40000",
			guvenilenProxy: 1,
			beklenen:       "198.51.100.7",
		},
		{
			ad: "tek proxy + UYDURMA on ekler: yine son eleman alinir",
			// Saldirgan sola ne yazarsa yazsin Traefik kendi gordugu
			// adresi SONA ekler; limit atlatilamaz.
			xff:            "1.1.1.1, 2.2.2.2, 198.51.100.7",
			remoteAddr:     "10.0.0.1:40000",
			guvenilenProxy: 1,
			beklenen:       "198.51.100.7",
		},
		{
			ad:             "iki proxy: sondan ikinci eleman alinir",
			xff:            "1.1.1.1, 198.51.100.7, 10.0.0.5",
			remoteAddr:     "10.0.0.1:40000",
			guvenilenProxy: 2,
			beklenen:       "198.51.100.7",
		},
		{
			ad: "beklenenden kisa XFF: RemoteAddr'a dusulur",
			// Zincir beklenenden kisaysa baslik bizim proxy'lerimizden
			// gelmiyor demektir; uydurma degere guvenilmez.
			xff:            "198.51.100.7",
			remoteAddr:     "10.0.0.1:40000",
			guvenilenProxy: 3,
			beklenen:       "10.0.0.1",
		},
		{
			ad:             "secilen eleman gecerli IP degil: RemoteAddr'a dusulur",
			xff:            "1.1.1.1, bu-bir-ip-degil",
			remoteAddr:     "10.0.0.1:40000",
			guvenilenProxy: 1,
			beklenen:       "10.0.0.1",
		},
		{
			ad: "IKI AYRI XFF SATIRI: satirlar birlestirilir, en sagdaki alinir",
			// Header.Get yalnizca ILK satiri okur; saldirgan kendi
			// satirini ekleyip sagdan saymayi kendi degerinde yaptirabilir
			// ve inceleyici bu yolla "9.9.9.9" aldi. RFC 7230 3.2.2:
			// coklu satir virgulle birlestirilmis TEK listeye esdeger.
			xffSatirlari:   []string{"9.9.9.9", "1.1.1.1, 198.51.100.7"},
			remoteAddr:     "10.0.0.1:40000",
			guvenilenProxy: 1,
			beklenen:       "198.51.100.7",
		},
		{
			ad: "IKI AYRI XFF SATIRI + proxy yok: yine RemoteAddr",
			// guvenilenProxy == 0 iken baslik HIC okunmaz.
			xffSatirlari:   []string{"9.9.9.9", "1.1.1.1"},
			remoteAddr:     "203.0.113.9:51234",
			guvenilenProxy: 0,
			beklenen:       "203.0.113.9",
		},
		{
			ad:             "baslik yok: RemoteAddr",
			xff:            "",
			remoteAddr:     "192.0.2.33:1234",
			guvenilenProxy: 1,
			beklenen:       "192.0.2.33",
		},
	}
	for _, d := range durumlar {
		t.Run(d.ad, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, yolGiris, nil)
			r.RemoteAddr = d.remoteAddr
			if d.xff != "" {
				r.Header.Set("X-Forwarded-For", d.xff)
			}
			for _, satir := range d.xffSatirlari {
				r.Header.Add("X-Forwarded-For", satir)
			}
			if got := istemciIP(r, d.guvenilenProxy); got != d.beklenen {
				t.Errorf("istemciIP = %q, beklenen %q", got, d.beklenen)
			}
		})
	}
}

// ---- Kurulum: sessizce "limitsiz" moda dusmek yasak ----

func TestHizLimitGecersizAyardaKurulmaz(t *testing.T) {
	gecerli := HizLimitAyar{GirisIPPerMin: 1, GirisEpostaPerSaat: 1, KayitIPPerSaat: 1}
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	t.Cleanup(func() { _ = rdb.Close() })

	bozuk := func(degistir func(*HizLimitAyar)) HizLimitAyar {
		a := gecerli
		degistir(&a)
		return a
	}
	durumlar := []struct {
		ad   string
		rdb  *redis.Client
		ayar HizLimitAyar
		key  []byte
	}{
		{"redis yok", nil, gecerli, []byte("anahtar")},
		{"giris ip siniri 0", rdb, bozuk(func(a *HizLimitAyar) { a.GirisIPPerMin = 0 }), []byte("anahtar")},
		{"giris eposta siniri 0", rdb, bozuk(func(a *HizLimitAyar) { a.GirisEpostaPerSaat = 0 }), []byte("anahtar")},
		{"kayit ip siniri 0", rdb, bozuk(func(a *HizLimitAyar) { a.KayitIPPerSaat = 0 }), []byte("anahtar")},
		{"guvenilen proxy negatif", rdb, bozuk(func(a *HizLimitAyar) { a.GuvenilenProxy = -1 }), []byte("anahtar")},
		{"eposta ozet anahtari bos", rdb, gecerli, nil},
	}
	for _, d := range durumlar {
		t.Run(d.ad, func(t *testing.T) {
			if _, err := NewHizLimit(d.rdb, d.ayar, d.key); err == nil {
				t.Error("gecersiz ayarla hiz limiti kuruldu")
			}
		})
	}
}

// ---- FAIL-CLOSED: Redis'e ulasilamazsa istek GECMEZ ----

// Gerekce: Redis bu akis icin zaten zorunlu (auth istekleri + cerez
// baglamasi). Fail-open, Redis'i dusurebilen birine sinirsiz
// brute-force verirdi. Bu test ag GEREKTIRMEZ: 127.0.0.1:1'e baglanti
// yerelde aninda reddedilir.
func TestHizLimitRedisYokkenFailClosed(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{
		Addr:        "127.0.0.1:1",
		DialTimeout: 200 * time.Millisecond,
		MaxRetries:  -1,
	})
	t.Cleanup(func() { _ = rdb.Close() })
	hiz, err := NewHizLimit(rdb, HizLimitAyar{
		GirisIPPerMin: 100, GirisEpostaPerSaat: 100, KayitIPPerSaat: 100,
	}, []byte(testCryptoAnahtari))
	if err != nil {
		t.Fatalf("NewHizLimit: %v", err)
	}

	cagrildi := false
	h := hiz.GirisSar(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { cagrildi = true }))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, girisPostIstegi("kurban@ornek.com", "198.51.100.7"))

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("durum = %d, beklenen 503 (fail-closed)", w.Code)
	}
	if cagrildi {
		t.Error("Redis yokken istek handler'a gecti (fail-open)")
	}
}

// ---- Canli Redis ile sayac davranisi ----

func testHizLimit(t *testing.T, ayar HizLimitAyar) (*HizLimit, *redis.Client) {
	t.Helper()
	rdb := testRedis(t) // Redis yoksa burada atlanir
	hiz, err := NewHizLimit(rdb, ayar, []byte(testCryptoAnahtari))
	if err != nil {
		t.Fatalf("NewHizLimit: %v", err)
	}
	return hiz, rdb
}

// girisPostIstegi, POST /giris'e benzeyen bir form istegi uretir.
func girisPostIstegi(eposta, ip string) *http.Request {
	form := url.Values{"eposta": {eposta}, "sifre": {"herhangiBirSifre"}, "authRequestID": {"istek-1"}}
	r := httptest.NewRequest(http.MethodPost, yolGiris, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.RemoteAddr = ip + ":40000"
	return r
}

// gecenSayisi, sarilmis handler'i n kez cagirir ve kac tanesinin
// handler'a ULASTIGINI ve son durum kodunu doner.
func gecenSayisi(t *testing.T, h http.Handler, n int, istek func(i int) *http.Request) (int, int) {
	t.Helper()
	gecen := 0
	sonDurum := 0
	for i := 0; i < n; i++ {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, istek(i))
		sonDurum = w.Code
		if w.Code == http.StatusOK {
			gecen++
		}
	}
	return gecen, sonDurum
}

func izinVerenHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
}

// POST /giris: IP basina dakikalik sinir. Sinirin UZERINDEKI istek 429
// almali; eskiden 25 yanlis sifre denemesinin HICBIRI 429 almiyordu.
func TestGirisHizLimitiIPBasinaUygulanir(t *testing.T) {
	hiz, _ := testHizLimit(t, HizLimitAyar{
		GirisIPPerMin: 3, GirisEpostaPerSaat: 1000, KayitIPPerSaat: 1000,
	})
	h := hiz.GirisSar(izinVerenHandler())

	// Her istekte FARKLI e-posta: olculen sey yalnizca IP sayaci olsun.
	gecen, sonDurum := gecenSayisi(t, h, 5, func(i int) *http.Request {
		return girisPostIstegi(string(rune('a'+i))+"@ornek.com", "198.51.100.7")
	})
	if gecen != 3 {
		t.Errorf("gecen istek = %d, beklenen 3", gecen)
	}
	if sonDurum != http.StatusTooManyRequests {
		t.Errorf("son durum = %d, beklenen 429", sonDurum)
	}
}

// Dagitik brute-force (her istek ayri IP) IP sayacini atlar; e-posta
// basina sayac tek bir hesaba yonelen denemeleri sinirlar.
func TestGirisHizLimitiEpostaBasinaUygulanir(t *testing.T) {
	hiz, _ := testHizLimit(t, HizLimitAyar{
		GirisIPPerMin: 1000, GirisEpostaPerSaat: 3, KayitIPPerSaat: 1000,
	})
	h := hiz.GirisSar(izinVerenHandler())

	// Her istekte FARKLI IP: olculen sey yalnizca e-posta sayaci olsun.
	gecen, sonDurum := gecenSayisi(t, h, 5, func(i int) *http.Request {
		return girisPostIstegi("kurban@ornek.com", "198.51.100."+string(rune('1'+i)))
	})
	if gecen != 3 {
		t.Errorf("gecen istek = %d, beklenen 3", gecen)
	}
	if sonDurum != http.StatusTooManyRequests {
		t.Errorf("son durum = %d, beklenen 429", sonDurum)
	}
}

// Farkli e-postalar AYNI sayaci paylasmamali (aksi halde bir kullanici
// digerini kilitler).
func TestGirisEpostaSayaclariAyrisir(t *testing.T) {
	hiz, _ := testHizLimit(t, HizLimitAyar{
		GirisIPPerMin: 1000, GirisEpostaPerSaat: 2, KayitIPPerSaat: 1000,
	})
	h := hiz.GirisSar(izinVerenHandler())

	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, girisPostIstegi("birinci@ornek.com", "198.51.100.7"))
		if w.Code != http.StatusOK {
			t.Fatalf("birinci hesap %d. istekte durum = %d", i+1, w.Code)
		}
	}
	// Birinci hesabin sayaci doldu; ikinci hesap ETKILENMEMELI.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, girisPostIstegi("ikinci@ornek.com", "198.51.100.7"))
	if w.Code != http.StatusOK {
		t.Errorf("ikinci hesap durumu = %d, beklenen 200 (sayaclar ayrismali)", w.Code)
	}
}

// POST /kayit: IP basina saatlik sinir. Inceleme sirasinda 15 hesap
// kimlik dogrulamasiz olusturulabiliyordu.
func TestKayitHizLimitiIPBasinaUygulanir(t *testing.T) {
	hiz, _ := testHizLimit(t, HizLimitAyar{
		GirisIPPerMin: 1000, GirisEpostaPerSaat: 1000, KayitIPPerSaat: 2,
	})
	h := hiz.KayitSar(izinVerenHandler())

	gecen, sonDurum := gecenSayisi(t, h, 4, func(i int) *http.Request {
		r := girisPostIstegi(string(rune('a'+i))+"@ornek.com", "198.51.100.7")
		r.URL.Path = yolKayit
		return r
	})
	if gecen != 2 {
		t.Errorf("gecen istek = %d, beklenen 2", gecen)
	}
	if sonDurum != http.StatusTooManyRequests {
		t.Errorf("son durum = %d, beklenen 429", sonDurum)
	}
}

// 429 yaniti Retry-After tasimali: istemci ne zaman tekrar deneyecegini
// bilmeli ve deger asla <= 0 olmamali.
func TestHizLimit429RetryAfterTasir(t *testing.T) {
	hiz, _ := testHizLimit(t, HizLimitAyar{
		GirisIPPerMin: 1, GirisEpostaPerSaat: 1000, KayitIPPerSaat: 1000,
	})
	h := hiz.GirisSar(izinVerenHandler())
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, girisPostIstegi("kurban@ornek.com", "198.51.100.7"))
		if i == 1 {
			if w.Code != http.StatusTooManyRequests {
				t.Fatalf("durum = %d, beklenen 429", w.Code)
			}
			if w.Header().Get("Retry-After") == "" {
				t.Error("429 yanitinda Retry-After yok")
			}
		}
	}
}

// E-posta PII'dir: Redis anahtarinda DUZ METIN tutulmaz. Bu dalda tam
// bu kusur daha once cikti.
func TestHizLimitEpostayiRedisAnahtarindaDuzMetinTutmaz(t *testing.T) {
	hiz, rdb := testHizLimit(t, HizLimitAyar{
		GirisIPPerMin: 1000, GirisEpostaPerSaat: 1000, KayitIPPerSaat: 1000,
	})
	const eposta = "gizli.kullanici@ornek.com"
	h := hiz.GirisSar(izinVerenHandler())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, girisPostIstegi(eposta, "198.51.100.7"))
	if w.Code != http.StatusOK {
		t.Fatalf("durum = %d, beklenen 200", w.Code)
	}

	anahtarlar, err := rdb.Keys(context.Background(), hizLimitOnEki+"*").Result()
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}
	if len(anahtarlar) == 0 {
		t.Fatal("hic hiz limiti anahtari yazilmamis")
	}
	for _, a := range anahtarlar {
		if strings.Contains(strings.ToLower(a), "gizli") ||
			strings.Contains(strings.ToLower(a), "ornek.com") ||
			strings.Contains(strings.ToLower(a), eposta) {
			t.Errorf("Redis anahtari e-postayi duz metin tasiyor: %q", a)
		}
	}
}

// Hiz limiti URETIM KABLOLAMASINDA gercekten devrede mi: istek gercek
// mux uzerinden (muxKur) gecer. Bu testin yakaladigi sey, limitin
// yalnizca birim duzeyinde degil OP dinleyicisinde de bagli oldugudur.
func TestGirisHizLimitiUretimMuxundaDevrede(t *testing.T) {
	o := testSunucuAyarli(t, HizLimitAyar{
		GirisIPPerMin: 2, GirisEpostaPerSaat: 1000, KayitIPPerSaat: 1000,
	})
	o.testKullanici(t, "kurban@ornek.com", "dogruSifre12")

	verifier, challenge := pkceS256(t)
	_ = verifier
	c := tarayici(t)
	id := authRequestIDCikar(t, al(t, c, o.authorizeURL(challenge, "S256")))

	// Form BIR KEZ acilir ve ayni formdan uc kez POST edilir: gercek
	// tarayici da basarisiz denemede formu yeniden yuklemez. Ayrica GET
	// sayaci (ayri anahtar, bkz. SayfaGetSar) bu olcumu kirletmesin.
	csrf := o.girisFormuAc(t, c, id)

	// Ilk iki YANLIS sifre denemesi 401, ucuncusu 429 olmali.
	for i := 1; i <= 3; i++ {
		yanit := o.girisGonder(t, c, id, csrf, "kurban@ornek.com", "yanlisSifre12")
		switch {
		case i < 3 && yanit.StatusCode != http.StatusUnauthorized:
			t.Fatalf("%d. deneme durumu = %d, beklenen 401", i, yanit.StatusCode)
		case i == 3 && yanit.StatusCode != http.StatusTooManyRequests:
			t.Fatalf("%d. deneme durumu = %d, beklenen 429 (hiz limiti)", i, yanit.StatusCode)
		}
	}
}

// I1a: e-posta sayaci HARF DUYARSIZ olmali. Hesap kimligi Postgres'te
// harf duyarsizdir (UNIQUE (lower(email)), ByEmail lower(email) =
// lower($1)), yani "Kurban@x.com" AYNI hesaptir. Sayac anahtari
// kucultmezse dagitik bir brute-force harf varyantlariyla sinirsiz
// deneme yapar ve sayacin tek varlik sebebi tam bu senaryodur. Canli
// olcum (sinir 2/saat): ayni yazim 401 401 429, uc harf varyanti hepsi
// 401.
func TestGirisEpostaSayaciHarfDuyarsiz(t *testing.T) {
	hiz, _ := testHizLimit(t, HizLimitAyar{
		GirisIPPerMin: 1000, GirisEpostaPerSaat: 2, KayitIPPerSaat: 1000,
	})
	h := hiz.GirisSar(izinVerenHandler())

	// Her istekte FARKLI IP: olculen sey yalnizca e-posta sayaci olsun.
	// Her istekte FARKLI HARF YAZIMI: hepsi AYNI sayaca dusmeli.
	yazimlar := []string{
		"kurban@ornek.com", "Kurban@ornek.com", "KURBAN@ORNEK.COM",
		"kUrBaN@OrNeK.cOm", "  Kurban@Ornek.Com  ",
	}
	gecen, sonDurum := gecenSayisi(t, h, len(yazimlar), func(i int) *http.Request {
		return girisPostIstegi(yazimlar[i], "198.51.100."+strconv.Itoa(i+1))
	})
	if gecen != 2 {
		t.Errorf("gecen istek = %d, beklenen 2 (harf varyantlari AYNI sayaca dusmeli)", gecen)
	}
	if sonDurum != http.StatusTooManyRequests {
		t.Errorf("son durum = %d, beklenen 429", sonDurum)
	}
}

// I1b: AYNI basligin IKI SATIRI ile hiz limiti atlatilamaz. Yukaridaki
// istemciIP tablosu birim duzeyinde olcer; bu test sayacin GERCEKTEN
// ayni anahtara dustugunu olcer.
func TestGirisHizLimitiCokluXFFSatiriIleAtlatilamaz(t *testing.T) {
	hiz, _ := testHizLimit(t, HizLimitAyar{
		GirisIPPerMin: 2, GirisEpostaPerSaat: 1000, KayitIPPerSaat: 1000,
		GuvenilenProxy: 1,
	})
	h := hiz.GirisSar(izinVerenHandler())

	// Saldirgan HER istekte farkli bir ILK satir yazar; guvenilen
	// proxy'nin ekledigi deger hep ayni ve EN SAGDA.
	gecen, sonDurum := gecenSayisi(t, h, 4, func(i int) *http.Request {
		r := girisPostIstegi(string(rune('a'+i))+"@ornek.com", "10.0.0.1")
		r.Header.Add("X-Forwarded-For", "9.9.9."+strconv.Itoa(i+1))
		r.Header.Add("X-Forwarded-For", "198.51.100.7")
		return r
	})
	if gecen != 2 {
		t.Errorf("gecen istek = %d, beklenen 2 (coklu XFF satiri sayaci atlatmamali)", gecen)
	}
	if sonDurum != http.StatusTooManyRequests {
		t.Errorf("son durum = %d, beklenen 429", sonDurum)
	}
}

// Minor: GET /giris ve GET /kayit da hiz limitine tabi. Uc kimlik
// dogrulamasiz ve /authorize'dan (gecerli client_id + redirect_uri +
// S256 ister) daha ucuz bir yuzey.
func TestSayfaGetHizLimitiUygulanir(t *testing.T) {
	hiz, _ := testHizLimit(t, HizLimitAyar{
		GirisIPPerMin: 2, GirisEpostaPerSaat: 1000, KayitIPPerSaat: 1000,
	})
	for _, ad := range []string{"giris", "kayit"} {
		t.Run(ad, func(t *testing.T) {
			h := hiz.SayfaGetSar(ad, izinVerenHandler())
			gecen, sonDurum := gecenSayisi(t, h, 4, func(int) *http.Request {
				r := httptest.NewRequest(http.MethodGet, yolGiris+"?authRequestID=istek-1", nil)
				r.RemoteAddr = "198.51.100.7:40000"
				return r
			})
			if gecen != 2 {
				t.Errorf("gecen istek = %d, beklenen 2", gecen)
			}
			if sonDurum != http.StatusTooManyRequests {
				t.Errorf("son durum = %d, beklenen 429", sonDurum)
			}
		})
	}
}

// GET sayaci POST sayacindan AYRI anahtarda olmali: aksi halde formu
// acmak sifre deneme butcesini tuketir ve mesru kullanici kendi akisini
// kilitler.
func TestSayfaGetSayaciPostSayacindanAyri(t *testing.T) {
	hiz, _ := testHizLimit(t, HizLimitAyar{
		GirisIPPerMin: 1, GirisEpostaPerSaat: 1000, KayitIPPerSaat: 1000,
	})
	const ip = "198.51.100.7"

	// GET butcesini TUKET.
	getH := hiz.SayfaGetSar("giris", izinVerenHandler())
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, yolGiris+"?authRequestID=istek-1", nil)
		r.RemoteAddr = ip + ":40000"
		getH.ServeHTTP(w, r)
		if i == 1 && w.Code != http.StatusTooManyRequests {
			t.Fatalf("GET butcesi dolmadi, durum = %d", w.Code)
		}
	}

	// POST hala GECMELI.
	w := httptest.NewRecorder()
	hiz.GirisSar(izinVerenHandler()).ServeHTTP(w, girisPostIstegi("kurban@ornek.com", ip))
	if w.Code != http.StatusOK {
		t.Errorf("POST durumu = %d, beklenen 200 (GET sayaci POST butcesini tuketmemeli)", w.Code)
	}
}

// Hiz limiti URETIM KABLOLAMASINDA GET uclarinda da devrede mi.
func TestGetHizLimitiUretimMuxundaDevrede(t *testing.T) {
	o := testSunucuAyarli(t, HizLimitAyar{
		GirisIPPerMin: 2, GirisEpostaPerSaat: 1000, KayitIPPerSaat: 1000,
	})
	o.testKullanici(t, "kurban@ornek.com", "dogruSifre12")
	_, challenge := pkceS256(t)
	c := tarayici(t)
	id := authRequestIDCikar(t, al(t, c, o.authorizeURL(challenge, "S256")))

	// Ilk iki GET gecer (biri 200), ucuncusu 429.
	adres := o.issuer + yolGiris + "?authRequestID=" + url.QueryEscape(id)
	for i := 1; i <= 3; i++ {
		yanit := al(t, c, adres)
		switch {
		case i < 3 && yanit.StatusCode != http.StatusOK:
			t.Fatalf("%d. GET durumu = %d, beklenen 200", i, yanit.StatusCode)
		case i == 3 && yanit.StatusCode != http.StatusTooManyRequests:
			t.Fatalf("%d. GET durumu = %d, beklenen 429 (hiz limiti)", i, yanit.StatusCode)
		}
	}
}
