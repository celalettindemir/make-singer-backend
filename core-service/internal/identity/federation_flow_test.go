package identity

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Bu dosya federasyon akisini UCTAN UCA, gercek bir net/http
// dinleyicisi (muxKur) ve CEREZ KAVANOZU uzerinde olcer. Test
// altyapisi sahte_yukari_akis_test.go'dadir.
//
// NEDEN BU DOSYA VAR: Faz 1'de gorev basina incelemeler, kendi
// katmanlarimizla kutuphanenin cagri sirasi arasindaki uyusmazligi
// YAPISAL OLARAK yakalayamadi; canliya cikan iki Critical tam oradaydi.
// Burada hicbir iddia tek bir katmanin cagrisina degil, akisin
// GOZLENEBILIR sonucuna (uretilen kod, verilen jeton, yazilan eslesme)
// dayanir.
//
// Her saldiri testi AYNI KALIBI izler: once onkosulun GERCEKTEN
// kuruldugu assert edilir (pozitif kontrol), sonra red olculur. Bu
// kalip zorunlu: Faz 1'de iki test, duzeltmeyi hic olcmedigi halde
// yesil gecmisti.

// ---------------------------------------------------------------------
// Ortak yardimcilar
// ---------------------------------------------------------------------

// kullaniciBilgisi, access token ile /userinfo ucunu cagirir.
//
// NEDEN AYRI BIR YUZEY: id_token ve /userinfo, claim'leri BIRBIRINDEN
// BAGIMSIZ iki yoldan doldurur (id_token: Depo.SetUserinfoFromRequest,
// userinfo: Depo.SetUserinfoFromToken). Birinin dogru olmasi digerini
// kanitlamaz, bu yuzden mutlu yol testleri IKISINI de olcer.
func (o *akisOrtami) kullaniciBilgisi(t *testing.T, erisimJetonu string) map[string]any {
	t.Helper()
	b := o.YeniTarayici(t)
	yanit := b.istekBaslikli(t, http.MethodGet, o.issuer+"/userinfo", map[string]string{
		"Authorization": "Bearer " + erisimJetonu,
	})
	if yanit.Durum != http.StatusOK {
		t.Fatalf("/userinfo durumu = %d, govde: %s", yanit.Durum, yanit.Govde())
	}
	return jsonCoz(t, yanit.Govde())
}

// federasyonKodu, federasyon akisini bastan sona kosturur ve uretilen
// authorization code ile akisi kosturan tarayiciyi doner.
func (o *akisOrtami) federasyonKodu(t *testing.T, saglayici string) (string, *fedTarayici) {
	t.Helper()
	oturum := o.YeniTarayici(t)
	istekID := oturum.AuthorizeBaslat(t)
	son := oturum.FederasyonAkisiniKostur(t, saglayici, istekID)
	kod := son.KonumKod()
	if kod == "" {
		t.Fatalf("%s akisi kod uretmedi; son durum %d, konum %q, govde: %s",
			saglayici, son.Durum, son.Konum(), son.Govde())
	}
	return kod, oturum
}

// federasyonJetonu, federasyon akisini bastan sona kosturur ve token
// ucunun yanitini doner. Mutlu yol testlerinin ortak govdesi.
func (o *akisOrtami) federasyonJetonu(
	t *testing.T, saglayici string,
) (map[string]any, *fedTarayici) {
	t.Helper()
	kod, oturum := o.federasyonKodu(t, saglayici)
	durum, jeton := o.jetonAl(t, kod, oturum.verifier)
	if durum != http.StatusOK {
		t.Fatalf("token durumu = %d, govde: %v", durum, jeton)
	}
	return jeton, oturum
}

// erisimJetonuSub, access token'in sub claim'ini (bizim kullanici
// kimligimizi) doner.
func erisimJetonuSub(t *testing.T, jeton map[string]any) string {
	t.Helper()
	erisim, _ := jeton["access_token"].(string)
	if erisim == "" {
		t.Fatal("access_token yok")
	}
	_, iddialar := jwtIddialari(t, erisim)
	sub, _ := iddialar["sub"].(string)
	if sub == "" {
		t.Fatal("access token'da sub yok")
	}
	return sub
}

// yukariAkisKoduAl, yukari akistan GECERLI bir (state, code) ciftini
// alir ama BIZIM geri bacagimizi KOSTURMAZ: donen deger, geri ucunun
// cagrilmaya hazir TAM adresidir. Saldiri testlerinin girdisi tam
// olarak budur -- saldirgan kendi yetkilendirmesinden gecerli bir cift
// elde eder.
func yukariAkisKoduAl(t *testing.T, b *fedTarayici, saglayici, istekID string) string {
	t.Helper()
	baslat := b.FederasyonBaslat(t, saglayici, istekID)
	if baslat.Durum != http.StatusFound {
		t.Fatalf("baslat durumu = %d, beklenen 302. govde: %s", baslat.Durum, baslat.Govde())
	}
	if baslat.fedCerezi() == nil {
		t.Fatal("baslat federasyon cerezi yazmadi; senaryo kurulamadi")
	}
	// Kullanici yukari akista onay verir (capraz-site ust-duzey GET).
	yukari := b.istek(t, http.MethodGet, baslat.Konum(), nil, baglamCaprazGet)
	if yukari.Durum != http.StatusFound {
		t.Fatalf("yukari akis /authorize durumu = %d, beklenen 302", yukari.Durum)
	}
	geri := yukari.Konum()
	ayristirilan, err := url.Parse(geri)
	if err != nil {
		t.Fatalf("geri adresi cozulemedi: %v", err)
	}
	if ayristirilan.Query().Get("code") == "" || ayristirilan.Query().Get("state") == "" {
		t.Fatalf("yukari akis code+state dondurmedi: %q", geri)
	}
	return geri
}

// ---------------------------------------------------------------------
// Mutlu yollar
// ---------------------------------------------------------------------

// Ucan uca: /authorize -> /giris -> Google butonu ->
// /federasyon/google/baslat -> sahte Google -> /federasyon/google/geri
// -> /authorize/callback -> kod -> /oauth/token -> access + id + refresh
//
// Gercek bir net/http dinleyicisi ve CEREZ KAVANOZU ile kosar; cerez
// davranisi (Path, SameSite) akisin parcasidir ve elle kurulan
// isteklerle olculemez.
func TestUctanUcaGoogleGirisi(t *testing.T) {
	const eposta = "google-kullanici@ornek.com"
	ortam := akisOrtamiKur(t, sahteOPAyar{
		Eposta:           eposta,
		EpostaDogrulandi: true,
		Ad:               "Google Kullanici",
		Sub:              "google-sub-1",
	})

	jeton, _ := ortam.federasyonJetonu(t, providerGoogle)

	// 0) Yukari akisa GITTIGIMIZ istek de akisin parcasi: kendi PKCE
	//    ciftimizi ve nonce'umuzu gonderdigimiz orada gorunur.
	sorgu := ortam.YukariAkis(t, providerGoogle).authorizeSorgusu()
	if sorgu.Get("code_challenge_method") != "S256" || sorgu.Get("code_challenge") == "" {
		t.Errorf("yukari akisa S256 PKCE gonderilmedi: %v", sorgu)
	}
	if sorgu.Get("nonce") == "" {
		t.Error("yukari akisa nonce gonderilmedi")
	}

	// 1) access_token: RS256 ile imzali ve BIZIM istemcimiz icin.
	erisim, _ := jeton["access_token"].(string)
	if erisim == "" {
		t.Fatal("access_token yok")
	}
	baslik, erisimIddia := jwtIddialari(t, erisim)
	if baslik["alg"] != "RS256" {
		t.Errorf("access token alg = %v, beklenen RS256", baslik["alg"])
	}
	if !audIcerir(erisimIddia["aud"], "makesinger-mobil") {
		t.Errorf("access token aud = %v, beklenen makesinger-mobil", erisimIddia["aud"])
	}

	// 2) amr = ["fed"]: kullanicinin federasyonla dogrulandigi
	//    jetonda GORUNMELI. amr'i tasiyan jeton id_token'dir
	//    (brief access token diyor; kutuphane onu id_token'a koyuyor,
	//    bkz. rapor).
	idJeton, _ := jeton["id_token"].(string)
	if idJeton == "" {
		t.Fatal("id_token yok")
	}
	_, idIddia := jwtIddialari(t, idJeton)
	amr := amrDegerleri(idIddia["amr"])
	if len(amr) != 1 || amr[0] != amrFederation {
		t.Errorf("amr = %v, beklenen [%s]", amr, amrFederation)
	}

	// 3) id_token OIDC Core 1.0 §2'nin REQUIRED claim'ini TASIMALI:
	//    sub DOLU ve access token'in sub'uyla AYNI kullanici. Ayrica
	//    email scope'u istendigi icin email de id_token'da olmali.
	//    Bu iddia ayri bir yuzeydir: kutuphane id_token userinfo'sunu
	//    Depo.SetUserinfoFromRequest'ten besler, /userinfo ucunu ise
	//    Depo.SetUserinfoFromToken'dan.
	beklenenSub := erisimJetonuSub(t, jeton)
	if idSub, _ := idIddia["sub"].(string); idSub != beklenenSub {
		t.Errorf("id_token sub = %v, beklenen %q (access token'in sub'u)", idIddia["sub"], beklenenSub)
	}
	if idIddia["email"] != eposta {
		t.Errorf("id_token email = %v, beklenen %q", idIddia["email"], eposta)
	}

	// 4) Federasyon DOGRU kullaniciyi cozdu: userinfo'da yukari
	//    akisin e-postasi ve bizim sub'umuz dolu.
	bilgi := ortam.kullaniciBilgisi(t, erisim)
	if bilgi["sub"] == "" || bilgi["sub"] == nil {
		t.Error("userinfo'da sub yok")
	}
	if bilgi["email"] != eposta {
		t.Errorf("userinfo email = %v, beklenen %q", bilgi["email"], eposta)
	}

	// 5) refresh_token verildi ve BIR KEZ donebilir; donen jetonla
	//    eski jeton gecersizlesir (aile rotasyonu).
	refresh, _ := jeton["refresh_token"].(string)
	if refresh == "" {
		t.Fatal("offline_access istendi ama refresh_token yok")
	}
	durum, yeni := ortam.yenile(t, refresh)
	if durum != http.StatusOK {
		t.Fatalf("refresh durumu = %d, govde: %v", durum, yeni)
	}
	yeniRefresh, _ := yeni["refresh_token"].(string)
	if yeniRefresh == "" || yeniRefresh == refresh {
		t.Errorf("refresh dondurulmedi: eski ile ayni veya bos (%q)", yeniRefresh)
	}
	if tekrarDurum, tekrarGovde := ortam.yenile(t, refresh); tekrarDurum == http.StatusOK {
		t.Errorf("tuketilmis refresh_token yeniden kabul edildi: %v", tekrarGovde)
	}
}

// Ayni akis, ama donus CROSS-SITE POST.
// Bu test K2'nin canli kanitidir: SameSite=Lax bir cerezle GECMEZ,
// SameSite=None ile GECER. Kanit yalnizca yazili degil OLCULUYOR:
// cerez kavanozu SameSite kuralini uygular (standart cookiejar onu yok
// sayar, bkz. cerezKavanozu) ve cerezin Lax'e cevrilmesi bu testi
// DUSURUR (mutasyon kaniti raporda).
func TestUctanUcaAppleGirisiFormPost(t *testing.T) {
	const eposta = "apple-kullanici@ornek.com"
	ortam := akisOrtamiKur(t, sahteOPAyar{
		Eposta:           eposta,
		EpostaDogrulandi: true,
		Ad:               "Apple Kullanici",
		Sub:              "apple-sub-1",
	})

	// ONKOSUL: donus GERCEKTEN capraz-site POST olmali. Baslat
	// adresinde response_mode=form_post yoksa test, Google ile ayni
	// GET donusunu olcer ve K2 hakkinda HICBIR SEY soylemez.
	oturum := ortam.YeniTarayici(t)
	istekID := oturum.AuthorizeBaslat(t)
	baslat := oturum.FederasyonBaslat(t, providerApple, istekID)
	if baslat.Durum != http.StatusFound {
		t.Fatalf("baslat durumu = %d, beklenen 302", baslat.Durum)
	}
	yukariURL, err := url.Parse(baslat.Konum())
	if err != nil {
		t.Fatalf("yukari akis adresi cozulemedi: %v", err)
	}
	if yukariURL.Query().Get("response_mode") != "form_post" {
		t.Fatalf("Apple akisi form_post istemiyor (response_mode=%q); test K2'yi OLCMUYOR",
			yukariURL.Query().Get("response_mode"))
	}
	// Federasyon cerezi, capraz-site POST'ta tasinabilmek icin
	// SameSite=None olmali ve yuzeyi /federasyon ile sinirli olmali.
	cerez := baslat.fedCerezi()
	if cerez == nil {
		t.Fatal("baslat federasyon cerezi yazmadi")
	}
	if cerez.SameSite != http.SameSiteNoneMode {
		t.Errorf("federasyon cerezi SameSite = %v, beklenen None", cerez.SameSite)
	}
	if cerez.Path != "/federasyon" {
		t.Errorf("federasyon cerezi Path = %q, beklenen /federasyon", cerez.Path)
	}

	// Akisin kalani: sahte Apple form_post ile BIZE POST eder.
	son := oturum.akisiTakipEt(t, baslat)
	kod := son.KonumKod()
	if kod == "" {
		t.Fatalf("form_post akisi kod uretmedi; son durum %d, konum %q, govde: %s",
			son.Durum, son.Konum(), son.Govde())
	}
	durum, jeton := ortam.jetonAl(t, kod, oturum.verifier)
	if durum != http.StatusOK {
		t.Fatalf("token durumu = %d, govde: %v", durum, jeton)
	}
	erisim, _ := jeton["access_token"].(string)
	if erisim == "" {
		t.Fatal("access_token yok")
	}
	if bilgi := ortam.kullaniciBilgisi(t, erisim); bilgi["email"] != eposta {
		t.Errorf("userinfo email = %v, beklenen %q", bilgi["email"], eposta)
	}
}

// Ikinci giris AYNI hesaba dusmeli: (saglayici, sub) eslesmesi kalici.
func TestUctanUcaIkinciGirisAyniHesap(t *testing.T) {
	ortam := akisOrtamiKur(t, sahteOPAyar{
		Eposta:           "tekrar@ornek.com",
		EpostaDogrulandi: true,
		Ad:               "Tekrar Giren",
		Sub:              "google-sub-sabit",
	})

	ilk, _ := ortam.federasyonJetonu(t, providerGoogle)
	ilkSub := erisimJetonuSub(t, ilk)
	// ONKOSUL: ilk giris GERCEKTEN bir eslesme yazmis olmali; aksi
	// halde ikinci girisin "ayni hesap" olmasi hicbir sey kanitlamaz.
	if sayi := ortam.FedereEslesmeSayisi(t); sayi != 1 {
		t.Fatalf("ilk giristen sonra eslesme sayisi = %d, beklenen 1", sayi)
	}

	ikinci, _ := ortam.federasyonJetonu(t, providerGoogle)
	if ikinciSub := erisimJetonuSub(t, ikinci); ikinciSub != ilkSub {
		t.Errorf("ikinci giris BASKA hesaba dustu: %q != %q", ikinciSub, ilkSub)
	}
	if sayi := ortam.FedereEslesmeSayisi(t); sayi != 1 {
		t.Errorf("ikinci giris yeni bir eslesme yazdi: sayi = %d", sayi)
	}
}

// Apple ikinci giriste email claim'i GONDERMEZ. Bu, hesabi
// kaybetmeye yol acmamali -- eslesme sub uzerinden.
func TestUctanUcaAppleIkinciGiristeEpostaGelmez(t *testing.T) {
	ortam := akisOrtamiKur(t, sahteOPAyar{
		Eposta:           "apple-ilk@ornek.com",
		EpostaDogrulandi: true,
		Ad:               "Apple Ilk",
		Sub:              "apple-sub-sabit",
	})

	ilk, _ := ortam.federasyonJetonu(t, providerApple)
	ilkSub := erisimJetonuSub(t, ilk)
	if sayi := ortam.FedereEslesmeSayisi(t); sayi != 1 {
		t.Fatalf("ilk giristen sonra eslesme sayisi = %d, beklenen 1", sayi)
	}

	// ONKOSUL: Apple ikinci turda e-postayi HIC gondermez. TEK DEGISEN
	// budur: Ad gibi diger alanlar DOGRU kalir, yoksa testin hangi
	// eksikligi olctugu belirsizlesir.
	apple := ortam.YukariAkis(t, providerApple)
	apple.ayarla(func(a *sahteOPAyar) { a.Eposta = "" })

	ikinci, _ := ortam.federasyonJetonu(t, providerApple)
	if ikinciSub := erisimJetonuSub(t, ikinci); ikinciSub != ilkSub {
		t.Errorf("e-postasiz ikinci giris BASKA hesaba dustu: %q != %q", ikinciSub, ilkSub)
	}
	if sayi := ortam.FedereEslesmeSayisi(t); sayi != 1 {
		t.Errorf("e-postasiz ikinci giris yeni hesap acti: eslesme sayisi = %d", sayi)
	}
	// E-posta kaybolmamali: ilk turda ogrenilen adres hesapta kalir.
	erisim, _ := ikinci["access_token"].(string)
	if bilgi := ortam.kullaniciBilgisi(t, erisim); bilgi["email"] != "apple-ilk@ornek.com" {
		t.Errorf("userinfo email = %v, beklenen ilk turdaki adres", bilgi["email"])
	}
	// BIZIM id_token'imiz de e-postayi TASIR. Celiski degil: claim'in
	// kaynagi yukari akisin bu turdaki yaniti DEGIL, kendi
	// veritabanimizdaki kullanici kaydidir (Depo.userinfoDoldur,
	// kullanici.ByID uzerinden). Yukari akisin e-postayi bir daha hic
	// gondermemesi kendi jetonlarimizi fakirlestirmez.
	idJeton, _ := ikinci["id_token"].(string)
	if idJeton == "" {
		t.Fatal("id_token yok")
	}
	_, idIddia := jwtIddialari(t, idJeton)
	if idIddia["email"] != "apple-ilk@ornek.com" {
		t.Errorf("id_token email = %v, beklenen ilk turdaki adres", idIddia["email"])
	}
	if idSub, _ := idIddia["sub"].(string); idSub != ilkSub {
		t.Errorf("id_token sub = %v, beklenen %q", idIddia["sub"], ilkSub)
	}
}

// ---------------------------------------------------------------------
// Saldirilar
// ---------------------------------------------------------------------

// Saldirgan kendi Google yetkilendirmesinden gecerli bir state+code
// cifti alir ve kurbanin tarayicisinda callback'i acar. Kurbanin
// akisi saldirganin hesabina TAMAMLANMAMALI.
//
// NEDEN KURBAN DA KENDI FEDERASYON BACAGINI KOSTURUYOR: kurban yalnizca
// /authorize yapsa kurbanda kimlik_fed cerezi HIC olmaz ve red
// "cerez YOK" dalindan gelir -- yani bu test
// TestSaldiriCerezsizCallbackReddedilir ile AYNI mekanizmayi olcer,
// cerez DEGERI karsilastirmasi (subtle.ConstantTimeCompare, bkz.
// FedereSayfalar.Geri) ise hic kosmaz. Kurbana kendi cerezini aldirinca
// cerez VARDIR ama DEGERI saldirganinkiyle eslesmez: olculen sey
// baglamanin kendisi olur.
func TestSaldiriBaskaTarayicidaBaslatilanAkisTamamlanamaz(t *testing.T) {
	ortam := akisOrtamiKur(t, sahteOPAyar{
		Eposta:           "saldirgan@ornek.com",
		EpostaDogrulandi: true,
		Sub:              "google-sub-saldirgan",
	})

	saldirgan := ortam.YeniTarayici(t)
	kurban := ortam.YeniTarayici(t)
	saldirganIstek := saldirgan.AuthorizeBaslat(t)
	kurbanIstek := kurban.AuthorizeBaslat(t)

	// ONKOSUL 1: saldirgan GECERLI bir state+code cifti elde etti.
	geriURL := yukariAkisKoduAl(t, saldirgan, providerGoogle, saldirganIstek)

	// ONKOSUL 2: kurban KENDI federasyon bacagini baslatti, yani
	// kurbanin tarayicisinda da bir kimlik_fed cerezi VAR.
	kurbanBaslat := kurban.FederasyonBaslat(t, providerGoogle, kurbanIstek)
	if kurbanBaslat.Durum != http.StatusFound {
		t.Fatalf("kurbanin baslat durumu = %d, beklenen 302", kurbanBaslat.Durum)
	}
	if kurbanBaslat.fedCerezi() == nil {
		t.Fatal("kurbanin baslat'i federasyon cerezi yazmadi; senaryo kurulamadi")
	}

	// ONKOSUL 3: iki cerezin DEGERLERI farkli. Esit olsalardi asagidaki
	// red cerez baglamasindan degil baska bir seyden gelirdi.
	kurbanCerez := kurban.kavanoz.cerezDegeri(cookieNameFederation)
	saldirganCerez := saldirgan.kavanoz.cerezDegeri(cookieNameFederation)
	if kurbanCerez == "" || saldirganCerez == "" {
		t.Fatalf("cerez degerleri okunamadi (kurban %q, saldirgan %q)", kurbanCerez, saldirganCerez)
	}
	if kurbanCerez == saldirganCerez {
		t.Fatal("iki tarayicinin federasyon cerezi AYNI; baglama testi bos olurdu")
	}

	// Saldirgan o adresi kurbanin tarayicisinda actirir: kurbanda cerez
	// VAR ama DEGERI saldirganin state kaydindakiyle eslesmedigi icin
	// reddedilmeli.
	red := kurban.istek(t, http.MethodGet, geriURL, nil, baglamCaprazGet)
	if red.Durum != http.StatusForbidden {
		t.Errorf("baska tarayicida callback durumu = %d, beklenen 403. govde: %s",
			red.Durum, red.Govde())
	}
	if ortam.IstekTamamlandiMi(t, saldirganIstek) {
		t.Fatal("HESAP DEVRALMA: saldirganin istegi kurbanin tarayicisinda tamamlandi")
	}
	if ortam.IstekTamamlandiMi(t, kurbanIstek) {
		t.Fatal("kurbanin istegi saldirganin akisiyla tamamlandi")
	}
	if _, err := kurban.KodDegis(t, saldirganIstek); err == nil {
		t.Fatal("kurbanin tarayicisinda kod degisimi basarili oldu")
	}
	if sayi := ortam.FedereEslesmeSayisi(t); sayi != 0 {
		t.Errorf("reddedilen akis eslesme yazdi: sayi = %d", sayi)
	}

	// POZITIF KONTROL: AYNI yolla alinmis ikinci bir cift, saldirganin
	// KENDI tarayicisinda calisir. Yani olculen sey cerez baglamasidir,
	// bozuk bir senaryo kurulumu degil.
	kendiIstek := saldirgan.AuthorizeBaslat(t)
	kendiGeri := yukariAkisKoduAl(t, saldirgan, providerGoogle, kendiIstek)
	kendi := saldirgan.akisiTakipEt(t,
		saldirgan.istek(t, http.MethodGet, kendiGeri, nil, baglamCaprazGet))
	if kendi.KonumKod() == "" {
		t.Fatalf("saldirganin kendi tarayicisinda akis tamamlanmadi (durum %d); senaryo gecersiz",
			kendi.Durum)
	}
}

// Saldirgan kurbanin state'ini ele gecirmis olsa bile, cerez
// olmadan tamamlayamamali (K2).
func TestSaldiriCerezsizCallbackReddedilir(t *testing.T) {
	ortam := akisOrtamiKur(t, sahteOPAyar{
		Eposta:           "cerezsiz@ornek.com",
		EpostaDogrulandi: true,
		Sub:              "google-sub-cerezsiz",
	})

	oturum := ortam.YeniTarayici(t)
	istekID := oturum.AuthorizeBaslat(t)
	// ONKOSUL: gecerli bir state+code cifti var ve cerez YAZILDI
	// (yukariAkisKoduAl ikisini de dogrular).
	geriURL := yukariAkisKoduAl(t, oturum, providerGoogle, istekID)

	// Cerez SILINIR: tek degisen sey budur.
	oturum.kavanoz.cerezSil(cookieNameFederation)
	red := oturum.istek(t, http.MethodGet, geriURL, nil, baglamCaprazGet)
	if red.Durum != http.StatusForbidden {
		t.Errorf("cerezsiz callback durumu = %d, beklenen 403. govde: %s", red.Durum, red.Govde())
	}
	if ortam.IstekTamamlandiMi(t, istekID) {
		t.Fatal("cerezsiz callback istegi tamamladi")
	}
	if sayi := ortam.FedereEslesmeSayisi(t); sayi != 0 {
		t.Errorf("cerezsiz callback eslesme yazdi: sayi = %d", sayi)
	}

	// POZITIF KONTROL: cerez YERINDE olan yeni bir tur tamamlanir.
	yeniIstek := oturum.AuthorizeBaslat(t)
	yeniGeri := yukariAkisKoduAl(t, oturum, providerGoogle, yeniIstek)
	son := oturum.akisiTakipEt(t, oturum.istek(t, http.MethodGet, yeniGeri, nil, baglamCaprazGet))
	if son.KonumKod() == "" {
		t.Fatalf("cerezli akis tamamlanmadi (durum %d); test cerezi DEGIL baska bir seyi olcuyor",
			son.Durum)
	}
}

// Ayni callback iki kez oynatilamaz (state GETDEL).
func TestSaldiriCallbackTekrariReddedilir(t *testing.T) {
	ortam := akisOrtamiKur(t, sahteOPAyar{
		Eposta:           "tekrar-oynatma@ornek.com",
		EpostaDogrulandi: true,
		Sub:              "google-sub-tekrar",
	})

	oturum := ortam.YeniTarayici(t)
	istekID := oturum.AuthorizeBaslat(t)
	geriURL := yukariAkisKoduAl(t, oturum, providerGoogle, istekID)

	// ONKOSUL: callback BIR KEZ gercekten calisir.
	ilk := oturum.akisiTakipEt(t, oturum.istek(t, http.MethodGet, geriURL, nil, baglamCaprazGet))
	if ilk.KonumKod() == "" {
		t.Fatalf("ilk callback kod uretmedi (durum %d); tekrar testi bos olurdu", ilk.Durum)
	}
	if !ortam.IstekTamamlandiMi(t, istekID) {
		t.Fatal("ilk callback istegi tamamlamadi; tekrar testi bos olurdu")
	}
	google := ortam.YukariAkis(t, providerGoogle)
	if sayi := google.tokenSayaci(); sayi != 1 {
		t.Fatalf("ilk turda yukari akis token cagrisi = %d, beklenen 1", sayi)
	}

	// AYNI adres ikinci kez: state kaydi GETDEL ile tuketildi.
	tekrar := oturum.istek(t, http.MethodGet, geriURL, nil, baglamCaprazGet)
	if tekrar.Durum != http.StatusForbidden {
		t.Errorf("tekrar oynatilan callback durumu = %d, beklenen 403. govde: %s",
			tekrar.Durum, tekrar.Govde())
	}
	// Red, yukari akisa GITMEDEN once olmali: state kontrolu kod
	// degisiminden ONCE kosuyor.
	if sayi := google.tokenSayaci(); sayi != 1 {
		t.Errorf("tekrar oynatma yukari akisa yeni bir token cagrisi yapti: sayi = %d", sayi)
	}
	if sayi := ortam.FedereEslesmeSayisi(t); sayi != 1 {
		t.Errorf("tekrar oynatma eslesme sayisini degistirdi: %d, beklenen 1", sayi)
	}
}

// Yukari akis BASKA bir uygulamaya verilmis bir id_token dondururse
// (aud yanlis) giris OLMAMALI.
func TestSaldiriBaskaUygulamaninTokeniyleGirilemez(t *testing.T) {
	ortam := akisOrtamiKur(t, sahteOPAyar{
		Eposta:           "aud@ornek.com",
		EpostaDogrulandi: true,
		Sub:              "google-sub-aud",
	})
	google := ortam.YukariAkis(t, providerGoogle)

	// ONKOSUL: dogru aud ile akis GERCEKTEN tamamlaniyor. Aksi halde
	// asagidaki red, aud'dan degil baska bir bozukluktan gelirdi.
	ortam.federasyonJetonu(t, providerGoogle)
	if sayi := ortam.FedereEslesmeSayisi(t); sayi != 1 {
		t.Fatalf("dogru aud ile eslesme sayisi = %d, beklenen 1", sayi)
	}

	// TEK DEGISEN: aud artik BASKA bir uygulamaya ait.
	google.ayarla(func(a *sahteOPAyar) { a.YanlisAudience = "baska-uygulama" })
	oturum := ortam.YeniTarayici(t)
	istekID := oturum.AuthorizeBaslat(t)
	red := oturum.FederasyonAkisiniKostur(t, providerGoogle, istekID)
	if red.Durum != http.StatusForbidden {
		t.Errorf("yanlis aud ile durum = %d, beklenen 403. govde: %s", red.Durum, red.Govde())
	}
	if red.KonumKod() != "" {
		t.Fatal("yanlis aud'lu id_token ile kod uretildi")
	}
	if ortam.IstekTamamlandiMi(t, istekID) {
		t.Fatal("yanlis aud'lu id_token ile istek tamamlandi")
	}
	if sayi := ortam.FedereEslesmeSayisi(t); sayi != 1 {
		t.Errorf("yanlis aud'lu akis yeni eslesme yazdi: sayi = %d", sayi)
	}
}

// nonce'u baska olan bir id_token enjekte edilemez.
func TestSaldiriNonceEnjeksiyonuReddedilir(t *testing.T) {
	ortam := akisOrtamiKur(t, sahteOPAyar{
		Eposta:           "nonce@ornek.com",
		EpostaDogrulandi: true,
		Sub:              "google-sub-nonce",
	})
	google := ortam.YukariAkis(t, providerGoogle)

	// ONKOSUL: dogru nonce ile akis tamamlaniyor.
	ortam.federasyonJetonu(t, providerGoogle)
	if sayi := ortam.FedereEslesmeSayisi(t); sayi != 1 {
		t.Fatalf("dogru nonce ile eslesme sayisi = %d, beklenen 1", sayi)
	}

	// TEK DEGISEN: id_token'in nonce'u BU akisa ait degil. Gercek
	// senaryo, saldirganin kendi akisindan aldigi gecerli bir
	// id_token'i kurbanin akisina enjekte etmesidir.
	google.ayarla(func(a *sahteOPAyar) { a.YanlisNonce = "saldirganin-nonce-degeri" })
	oturum := ortam.YeniTarayici(t)
	istekID := oturum.AuthorizeBaslat(t)
	red := oturum.FederasyonAkisiniKostur(t, providerGoogle, istekID)
	if red.Durum != http.StatusForbidden {
		t.Errorf("yanlis nonce ile durum = %d, beklenen 403. govde: %s", red.Durum, red.Govde())
	}
	if ortam.IstekTamamlandiMi(t, istekID) {
		t.Fatal("yanlis nonce'lu id_token ile istek tamamlandi")
	}
	if _, err := oturum.KodDegis(t, istekID); err == nil {
		t.Fatal("yanlis nonce'lu akisin kodu jetona cevrildi")
	}
}

// Spec'in ana saldirisi, UCTAN UCA: saldirgan kurbanin adresiyle
// sifreli hesap acar, kurban Google ile girer. Kurban SALDIRGANIN
// hesabina DUSMEMELI; baglama sayfasi cikmali ve sifre istenmeli.
//
// Bu test, yorumla belirtilen digerlerinin KALIBIDIR: onkosulun
// gercekten kuruldugu once assert edilir, sonra red olculur.
func TestSaldiriEpostayaDayaliHesapDevralmaEngellenir(t *testing.T) {
	ortam := akisOrtamiKur(t, sahteOPAyar{
		Eposta:           "kurban@ornek.com",
		EpostaDogrulandi: true, // email_verified=true OLSA BILE baglanmamali
		Sub:              "google-sub-kurban",
	})

	// 1) Saldirgan kurbanin adresiyle SIFRELI bir hesap acar.
	const saldirganSifresi = "saldirganin-sifresi-123"
	ortam.KayitOl(t, "kurban@ornek.com", "Saldirgan", saldirganSifresi)

	// POZITIF KONTROL: hesap GERCEKTEN acildi mi? Acilmadiysa 4.
	// adimdaki red hicbir sey olcmez ve test BOS olur.
	if jeton := ortam.SifreyleGiris(t, "kurban@ornek.com", saldirganSifresi); jeton == "" {
		t.Fatal("onkosul kurulamadi: saldirganin hesabi acilmamis")
	}

	// 2) Kurban giris sayfasindan Google'a gider.
	oturum := ortam.YeniTarayici(t)
	istekID := oturum.AuthorizeBaslat(t)
	yanit := oturum.FederasyonAkisiniKostur(t, providerGoogle, istekID)

	// 3) Baglama sayfasi cikmali: sifre SORULMALI.
	govde := yanit.Govde()
	if !strings.Contains(govde, `name="beklemeID"`) {
		t.Fatalf("baglama formu cikmadi; durum %d, govde: %s", yanit.Durum, govde)
	}
	if !strings.Contains(govde, `type="password"`) {
		t.Error("baglama sayfasi sifre sormuyor")
	}

	// 4) GIRIS OLMAMALI: istek tamamlanmadigi icin kod uretilmez ve
	//    access_token alinamaz.
	if oturum.IstekTamamlandiMi(t, istekID) {
		t.Fatal("federasyon, sifre sorulmadan istegi tamamladi (hesap devralma)")
	}
	if _, err := oturum.KodDegis(t, istekID); err == nil {
		t.Fatal("kod degisimi basarili oldu; giris yapilmis demektir")
	}

	// 5) Kurban saldirganin sifresini bilmedigi icin akis BURADA durur:
	//    yanlis sifreyle baglama REDDEDILMELI ve eslesme YAZILMAMALI.
	red := oturum.BaglamaGonder(t, govde, "kurbanin-kendi-sifresi-123")
	if red.Durum != http.StatusUnauthorized {
		t.Errorf("yanlis sifreyle baglama durumu = %d, beklenen 401", red.Durum)
	}
	if ortam.FedereEslesmeSayisi(t) != 0 {
		t.Error("yanlis sifreye ragmen federe eslesme yazilmis")
	}
}

// PKCE: federasyon yolu Faz 1'in S256 zorunlulugunu ATLAMAMALI.
// /authorize'a PKCE'siz gelinip federasyon butonuna basilamaz.
func TestSaldiriFederasyonPKCEyiAtlatmaz(t *testing.T) {
	ortam := akisOrtamiKur(t, sahteOPAyar{
		Eposta:           "pkce@ornek.com",
		EpostaDogrulandi: true,
		Sub:              "google-sub-pkce",
	})

	// 1) PKCE'siz /authorize REDDEDILIR, yani federasyon butonunun
	//    uzerinde duracagi bir authRequestID HIC DOGMAZ. Federasyon
	//    bacagi baglamasiz bir id ile cagrilamaz (bkz. Baslat).
	c := tarayici(t)
	pkceRed(t, al(t, c, ortam.authorizeURLKapsamli("", "", fedKapsamlar)))

	// 2) POZITIF KONTROL: federasyon yolu DOGRU verifier ile calisir.
	//    (Kod TEK KULLANIMLIK oldugu icin iki ayri tur gerekiyor:
	//    ayni kodu iki kez denemek, ikinci denemede "invalid code"
	//    olcerdi ve testin neyi olctugu belirsizlesirdi.)
	kod, oturum := ortam.federasyonKodu(t, providerGoogle)
	durum, jeton := ortam.jetonAl(t, kod, oturum.verifier)
	if durum != http.StatusOK {
		t.Fatalf("dogru verifier ile token durumu = %d, govde: %v", durum, jeton)
	}

	// 3) Federasyon yolundan uretilen kod da S256'ya BAGLI kalir:
	//    dogru verifier olmadan jetona cevrilemez. Bu, federasyonun
	//    PKCE'yi "atlatan bir yan kapi" olmadiginin olculebilir hali.
	yanlisKod, _ := ortam.federasyonKodu(t, providerGoogle)
	yanlisVerifier, _ := pkceS256(t)
	if durum, govde := ortam.jetonAl(t, yanlisKod, yanlisVerifier); durum == http.StatusOK {
		t.Fatalf("federasyon kodu YANLIS verifier ile jetona cevrildi: %v", govde)
	}
}
