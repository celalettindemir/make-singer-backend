package kimlik

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrYukariAkisYanit, yukari akis saglayicisinin token ucundan
// beklenmeyen bir yanit geldigini belirtir: HTTP hatasi, cozulemeyen
// govde, veya 200 ile birlikte gelen {"error":...}/id_token eksikligi.
// Hata govdesinin kendisi asla bu hataya eklenmez (bkz. JetonDegis):
// icinde kod veya client_secret parcalari olabilir.
var ErrYukariAkisYanit = errors.New("yukari akis saglayicisi beklenmeyen yanit verdi")

// yukariAkisZamanAsimi, yukari akis token ucuna uygulanan ust sinir.
// Zaman asimi olmadan cevap vermeyen bir saglayici, giris ucunu tutan
// goroutine'leri biriktirir.
const yukariAkisZamanAsimi = 10 * time.Second

// yukariAkisYanitSiniri, token ucunun yanit govdesinden okunacak en
// fazla bayt sayisi. Kotu niyetli ya da bozuk bir yanit bellegi
// doldurmasin diye io.ReadAll yerine io.LimitReader kullanilir.
const yukariAkisYanitSiniri = 1 << 20

// JetonDegis, yukari akis saglayicisindan (Apple veya Google) alinan
// yetkilendirme kodunu token ucunda id_token ile degistirir. Donen
// dize DOGRULANMAMIS bir id_token'dir; cagiran taraf bunu Gorev 2'nin
// auth.DogrulaNonce'una vermekle sorumludur -- bu fonksiyon imza ya
// da nonce kontrolu yapmaz.
func (y *YukariAkis) JetonDegis(
	ctx context.Context, istemci *http.Client, kod, kodDogrulayici, geriURL string,
) (string, error) {
	ctx, iptal := context.WithTimeout(ctx, yukariAkisZamanAsimi)
	defer iptal()

	sir := y.IstemciSir
	if y.Ad == saglayiciApple {
		var err error
		// K5: her degisimde yeniden uretilir, onbellege alinmaz.
		sir, err = appleIstemciSir(y.AppleEkipID, y.AppleAnahtarID, y.IstemciID, y.AppleAnahtarP8, time.Now())
		if err != nil {
			return "", fmt.Errorf("apple client_secret uretilemedi: %w", err)
		}
	}

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", kod)
	form.Set("redirect_uri", geriURL)
	form.Set("client_id", y.IstemciID)
	form.Set("client_secret", sir)
	if y.PKCEDestekli {
		form.Set("code_verifier", kodDogrulayici)
	}

	istek, err := http.NewRequestWithContext(ctx, http.MethodPost, y.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("istek kurulamadi: %w", err)
	}
	istek.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	yanit, err := istemci.Do(istek)
	if err != nil {
		return "", fmt.Errorf("yukari akisa ulasilamadi: %w", err)
	}
	defer func() { _ = yanit.Body.Close() }()

	// Govde SINIRLI okunur: kotu niyetli ya da bozuk bir yanit
	// bellegi doldurmasin.
	govde, err := io.ReadAll(io.LimitReader(yanit.Body, yukariAkisYanitSiniri))
	if err != nil {
		return "", fmt.Errorf("yanit okunamadi: %w", err)
	}
	if yanit.StatusCode != http.StatusOK {
		// HATA GOVDESI LOGLANMAZ ve KULLANICIYA GOSTERILMEZ: icinde
		// kod veya sir parcalari olabilir. Yalnizca durum kodu.
		return "", fmt.Errorf("%w: durum %d", ErrYukariAkisYanit, yanit.StatusCode)
	}
	var cozulen struct {
		IDToken string `json:"id_token"`
		Hata    string `json:"error"`
	}
	if err := json.Unmarshal(govde, &cozulen); err != nil {
		return "", fmt.Errorf("%w: govde cozumlenemedi", ErrYukariAkisYanit)
	}
	// 200 + {"error":...} GERCEK bir durumdur; govdeye bakmadan 200'u
	// basari saymak bos bir id_token'i dogrulamaya gondermek olur.
	if cozulen.Hata != "" || cozulen.IDToken == "" {
		return "", fmt.Errorf("%w: id_token yok", ErrYukariAkisYanit)
	}
	return cozulen.IDToken, nil
}
