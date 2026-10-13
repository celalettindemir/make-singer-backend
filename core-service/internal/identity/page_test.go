package identity

import (
	"context"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// countingUserStore counts UserStore calls. It is there to prove the claim
// "a rejected POST NEVER REACHED PASSWORD VERIFICATION": if the counter does
// not stay at zero, the rejection happened after the user store.
type countingUserStore struct {
	inner        UserStore
	mu           sync.Mutex
	byEmailCalls int
	createCalls  int
}

func (s *countingUserStore) Create(ctx context.Context, email, name, password string) (*User, error) {
	s.mu.Lock()
	s.createCalls++
	s.mu.Unlock()
	return s.inner.Create(ctx, email, name, password)
}

func (s *countingUserStore) ByEmail(ctx context.Context, email string) (*User, error) {
	s.mu.Lock()
	s.byEmailCalls++
	s.mu.Unlock()
	return s.inner.ByEmail(ctx, email)
}

// ByID is not counted: it is not used in the page flow, it is delegated directly.
func (s *countingUserStore) ByID(ctx context.Context, id string) (*User, error) {
	return s.inner.ByID(ctx, id)
}

func (s *countingUserStore) Delete(ctx context.Context, id string) error {
	return s.inner.Delete(ctx, id)
}

func (s *countingUserStore) counts() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.byEmailCalls, s.createCalls
}

// fakeCompleter is the in-memory equivalent of RequestCompleter. It records
// which request was bound to which user, and implements MarkDone's "a second
// call does NOT overwrite Subject" contract exactly (the real one lives in
// request.go and is measured in the Redis test).
type fakeCompleter struct {
	mu    sync.Mutex
	bound map[string]string
	amrs  map[string][]string
	Err   error
}

func newFakeCompleter() *fakeCompleter {
	return &fakeCompleter{bound: map[string]string{}, amrs: map[string][]string{}}
}

func (t *fakeCompleter) MarkDone(_ context.Context, id string, userID string, amr []string) error {
	if t.Err != nil {
		return t.Err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if existing, exists := t.bound[id]; exists {
		if existing == userID {
			return nil
		}
		return ErrRequestAlreadyCompleted
	}
	t.bound[id] = userID
	t.amrs[id] = append([]string(nil), amr...)
	return nil
}

func (t *fakeCompleter) subject(id string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.bound[id]
}

func (t *fakeCompleter) amrFor(id string) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.amrs[id]
}

type pagesEnv struct {
	pages     *Pages
	users     *countingUserStore
	sessions  *FakeSessionStore
	completer *fakeCompleter
}

func newTestPages(t *testing.T) *pagesEnv {
	t.Helper()
	users := &countingUserStore{inner: NewFakeUserStore()}
	sessionList := NewFakeSessionStore()
	doneBy := newFakeCompleter()
	// The real callback URL comes from op.AuthCallbackURL; a fixed address is
	// enough in the test because what is measured is the login logic.
	callback := func(_ context.Context, id string) string { return "/bitti?id=" + id }
	// secure=false: there is no TLS in the test; a Secure cookie could be sent in
	// httptest too, but we imitate the local development behavior.
	return &pagesEnv{
		pages:     NewPages(users, doneBy, sessionList, callback, false),
		users:     users,
		sessions:  sessionList,
		completer: doneBy,
	}
}

var csrfPattern = regexp.MustCompile(`name="csrf" value="([^"]*)"`)

// bindTestSession imitates what the /authorize leg does: it binds the given
// authRequestID to the CALLING browser and returns the cookie.
//
// In production ONLY /authorize does this (Pages.WrapAuthorize);
// GET /giris and GET /kayit DO NOT MINT a binding. The tests take that path
// too: first the binding, then the form.
func bindTestSession(t *testing.T, o *pagesEnv, id string, existing *http.Cookie) *http.Cookie {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/authorize", nil)
	if existing != nil {
		r.AddCookie(existing)
	}
	if err := o.pages.BindSession(w, r, id); err != nil {
		t.Fatalf("BaglamaKur: %v", err)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == cookieNameSession && c.Value != "" {
			return c
		}
	}
	t.Fatal("BaglamaKur oturum cerezi vermedi")
	return nil
}

// openForm imitates a browser starting the flow with /authorize and then
// opening the form with GET; it returns (session cookie, CSRF token).
func openForm(t *testing.T, o *pagesEnv, path, id string) (*http.Cookie, string) {
	t.Helper()
	cookie := bindTestSession(t, o, id, nil)
	return cookie, showForm(t, o, path, id, cookie)
}

// showForm opens the GET form with an existing cookie and returns the CSRF
// token in the form.
func showForm(t *testing.T, o *pagesEnv, path, id string, cookie *http.Cookie) string {
	t.Helper()
	w := formRequest(o, path, id, cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("form acilamadi: durum = %d", w.Code)
	}
	matches := csrfPattern.FindStringSubmatch(w.Body.String())
	if matches == nil || matches[1] == "" {
		t.Fatal("formda CSRF jetonu yok")
	}
	return matches[1]
}

// formRequest calls GET /giris or GET /kayit with the given cookie (may be
// nil).
func formRequest(o *pagesEnv, path, id string, cookie *http.Cookie) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, path+"?authRequestID="+url.QueryEscape(id), nil)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	if path == pathLogin {
		o.pages.Login(w, r)
	} else {
		o.pages.Signup(w, r)
	}
	return w
}

// postForm does a form POST with the given cookie (may be nil).
func postForm(o *pagesEnv, path string, form url.Values, cookie *http.Cookie) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	if path == pathLogin {
		o.pages.Login(w, r)
	} else {
		o.pages.Signup(w, r)
	}
	return w
}

func TestLogin_ShowsForm(t *testing.T) {
	o := newTestPages(t)
	cookie := bindTestSession(t, o, "abc", nil)
	w := formRequest(o, pathLogin, "abc", cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("durum = %d, beklenen 200", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{`name="eposta"`, `name="sifre"`, `name="authRequestID"`, `name="csrf"`, "abc"} {
		if !strings.Contains(body, want) {
			t.Errorf("sayfa %q icermiyor", want)
		}
	}
}

// While setting up the binding, /authorize must give the browser an HttpOnly +
// SameSite=Lax session cookie; this cookie is the flow's only binding point.
func TestWrapAuthorize_SetsSessionCookie(t *testing.T) {
	o := newTestPages(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/authorize", nil)
	if err := o.pages.BindSession(w, r, "abc"); err != nil {
		t.Fatalf("BaglamaKur: %v", err)
	}
	var cookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == cookieNameSession {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("oturum cerezi yok")
	}
	if !cookie.HttpOnly {
		t.Error("cerez HttpOnly degil")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, beklenen Lax", cookie.SameSite)
	}
	if cookie.Path != "/" {
		t.Errorf("Path = %q, beklenen /", cookie.Path)
	}
	// 32 bytes -> 43 characters in base64 raw url.
	if len(cookie.Value) < 43 {
		t.Errorf("cerez degeri %d karakter, 32 bayt entropi bekleniyordu", len(cookie.Value))
	}
	// The binding must have been written to the store.
	binding, err := o.sessions.ReadSession(context.Background(), cookie.Value)
	if err != nil {
		t.Fatalf("OturumOku: %v", err)
	}
	csrf, isBound := binding.CSRFFor("abc")
	if !isBound {
		t.Fatalf("authRequestID baglanmamis, kayit sayisi = %d", len(binding.Entries))
	}
	if csrf == "" {
		t.Error("baglamada CSRF jetonu yok")
	}
}

// C1 (re-review finding): GET /giris and GET /kayit DO NOT MINT a binding.
// On a GET that comes without a binding (or with a cookie bound to another
// id) the form is NOT EVEN shown. This closes the root of the exploit
// "the attacker gets a binding in their own browser and sends the link to
// the victim".
func TestFormGet_WithoutBindingRejected(t *testing.T) {
	for _, path := range []string{pathLogin, pathSignup} {
		t.Run(path, func(t *testing.T) {
			o := newTestPages(t)

			// (a) No cookie at all.
			w := formRequest(o, path, "istek-A", nil)
			if w.Code != http.StatusForbidden {
				t.Errorf("cerezsiz GET durumu = %d, beklenen 403", w.Code)
			}
			if strings.Contains(w.Body.String(), `name="csrf"`) {
				t.Error("reddedilen GET formu gosterdi")
			}
			if len(w.Result().Cookies()) != 0 {
				t.Error("reddedilen GET cerez yazdi (baglama mintlendi)")
			}

			// (b) A cookie bound to ANOTHER request.
			cookieB := bindTestSession(t, o, "istek-B", nil)
			w = formRequest(o, path, "istek-A", cookieB)
			if w.Code != http.StatusForbidden {
				t.Errorf("baska id'ye bagli cerezle GET durumu = %d, beklenen 403", w.Code)
			}
			if strings.Contains(w.Body.String(), `name="csrf"`) {
				t.Error("eslesmeyen cerezle form gosterildi")
			}

			// (c) A forged cookie value.
			w = formRequest(o, path, "istek-A", &http.Cookie{Name: cookieNameSession, Value: "uydurma"})
			if w.Code != http.StatusForbidden {
				t.Errorf("uydurma cerezle GET durumu = %d, beklenen 403", w.Code)
			}

			// (d) NEGATIVE CONTROL: with the right binding the form REALLY opens, so
			// the 403s above come from the binding.
			cookieA := bindTestSession(t, o, "istek-A", nil)
			w = formRequest(o, path, "istek-A", cookieA)
			if w.Code != http.StatusOK {
				t.Fatalf("bagli cerezle GET durumu = %d, beklenen 200", w.Code)
			}
			if !strings.Contains(w.Body.String(), `name="csrf"`) {
				t.Error("bagli cerezle form gosterilmedi")
			}
		})
	}
}

// PARALLEL FLOW (unit level): if two separate auth requests are started with
// the same cookie BOTH must stay bound. When the binding was moved to
// /authorize, if the "single id" model had been kept the second
// /authorize would overwrite the first and the first tab would get 403. Its
// end-to-end counterpart is in flow_test.go.
func TestBindSession_TwoParallelRequestsSameCookie(t *testing.T) {
	o := newTestPages(t)
	cookie := bindTestSession(t, o, "istek-1", nil)
	cookie2 := bindTestSession(t, o, "istek-2", cookie)
	if cookie2.Value != cookie.Value {
		t.Fatalf("ikinci /authorize cerez degerini degistirdi: %q -> %q", cookie.Value, cookie2.Value)
	}
	binding, err := o.sessions.ReadSession(context.Background(), cookie2.Value)
	if err != nil {
		t.Fatalf("OturumOku: %v", err)
	}
	for _, id := range []string{"istek-1", "istek-2"} {
		if _, isBound := binding.CSRFFor(id); !isBound {
			t.Errorf("%s baglamada yok", id)
		}
	}
	// The forms of both tabs must be openable.
	for _, id := range []string{"istek-1", "istek-2"} {
		if w := formRequest(o, pathLogin, id, cookie2); w.Code != http.StatusOK {
			t.Errorf("%s formu durumu = %d, beklenen 200", id, w.Code)
		}
	}
}

// The set must not grow WITHOUT BOUND (bloat surface): when the upper bound is
// exceeded the OLDEST entry drops and the newest remain.
func TestBindSession_EnforcesMaxBindings(t *testing.T) {
	o := newTestPages(t)
	var cookie *http.Cookie
	total := maxSessionBindings + 3
	for i := 0; i < total; i++ {
		cookie = bindTestSession(t, o, "istek-"+strconv.Itoa(i), cookie)
	}
	binding, err := o.sessions.ReadSession(context.Background(), cookie.Value)
	if err != nil {
		t.Fatalf("OturumOku: %v", err)
	}
	if len(binding.Entries) != maxSessionBindings {
		t.Fatalf("kayit sayisi = %d, beklenen %d", len(binding.Entries), maxSessionBindings)
	}
	// The oldest entries must have dropped and the newest must remain.
	if _, isBound := binding.CSRFFor("istek-0"); isBound {
		t.Error("ust sinir asildi ama en eski kayit dusmedi")
	}
	if _, isBound := binding.CSRFFor("istek-" + strconv.Itoa(total-1)); !isBound {
		t.Error("en yeni kayit dusmus")
	}
}

// If the issuer is https the cookie must be Secure; it must not be with
// http:// local development (otherwise the browser never stores the cookie
// and the flow breaks).
func TestSessionCookie_SecureFollowsIssuerScheme(t *testing.T) {
	for _, d := range []struct {
		name   string
		secure bool
	}{{"https", true}, {"http-yerel", false}} {
		t.Run(d.name, func(t *testing.T) {
			s := NewPages(NewFakeUserStore(), newFakeCompleter(), NewFakeSessionStore(),
				func(_ context.Context, id string) string { return "/bitti?id=" + id }, d.secure)
			w := httptest.NewRecorder()
			if err := s.BindSession(w, httptest.NewRequest(http.MethodGet, "/authorize", nil), "abc"); err != nil {
				t.Fatalf("BaglamaKur: %v", err)
			}
			cookieList := w.Result().Cookies()
			if len(cookieList) == 0 {
				t.Fatal("cerez yok")
			}
			if cookieList[0].Secure != d.secure {
				t.Errorf("Secure = %v, beklenen %v", cookieList[0].Secure, d.secure)
			}
		})
	}
}

// The login page is meaningless without an authRequestID; the form must not be shown.
func TestLogin_MissingAuthRequestIDRejected(t *testing.T) {
	o := newTestPages(t)
	w := httptest.NewRecorder()
	o.pages.Login(w, httptest.NewRequest(http.MethodGet, pathLogin, nil))
	if w.Code != http.StatusBadRequest {
		t.Errorf("durum = %d, beklenen 400 (authRequestID eksik)", w.Code)
	}
}

// C1: if there is NO cookie POST /giris is rejected and the user store is
// NEVER TOUCHED (i.e. password verification is not reached).
func TestLogin_NoCookieRejectedBeforePasswordCheck(t *testing.T) {
	o := newTestPages(t)
	if _, err := o.users.Create(t.Context(), "kurban@ornek.com", "K", "dogruSifre12"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	beforeByEmail, beforeCreate := o.users.counts()

	form := url.Values{
		"eposta": {"kurban@ornek.com"}, "sifre": {"dogruSifre12"},
		"authRequestID": {"abc"}, fieldCSRF: {"ne-olursa"},
	}
	w := postForm(o, pathLogin, form, nil)

	if w.Code != http.StatusForbidden {
		t.Fatalf("durum = %d, beklenen 403 (cerezsiz POST reddedilmeli)", w.Code)
	}
	afterByEmail, afterCreate := o.users.counts()
	if afterByEmail != beforeByEmail || afterCreate != beforeCreate {
		t.Errorf("kullanici deposuna dokunuldu (ByEmail %d->%d, Create %d->%d); red sifre dogrulamasindan SONRA olmus",
			beforeByEmail, afterByEmail, beforeCreate, afterCreate)
	}
	if w.Header().Get("Location") != "" {
		t.Error("reddedilen POST yonlendirme dondu")
	}
}

// C1: a POST with ANOTHER browser's cookie + ANOTHER authRequestID is
// rejected. An attacker must not be able to carry the cookie of their own auth
// request and make the victim's request complete (or the reverse).
func TestLogin_OtherAuthRequestIDRejected(t *testing.T) {
	o := newTestPages(t)
	if _, err := o.users.Create(t.Context(), "kurban@ornek.com", "K", "dogruSifre12"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Browser B opens a session for "istek-B".
	cookieB, csrfB := openForm(t, o, pathLogin, "istek-B")
	beforeByEmail, _ := o.users.counts()

	// With the same cookie, try to complete ANOTHER auth request (istek-A).
	form := url.Values{
		"eposta": {"kurban@ornek.com"}, "sifre": {"dogruSifre12"},
		"authRequestID": {"istek-A"}, fieldCSRF: {csrfB},
	}
	w := postForm(o, pathLogin, form, cookieB)
	if w.Code != http.StatusForbidden {
		t.Fatalf("durum = %d, beklenen 403 (cereze bagli id ile form id'si eslesmiyor)", w.Code)
	}
	if afterByEmail, _ := o.users.counts(); afterByEmail != beforeByEmail {
		t.Error("eslesmeyen id'de sifre dogrulamasina gidildi")
	}
	if o.completer.subject("istek-A") != "" {
		t.Error("istek-A bir kullaniciya baglandi")
	}
}

// C1: an undefined/forged cookie value is rejected too (there is no binding in
// the store).
func TestLogin_ForgedCookieRejected(t *testing.T) {
	o := newTestPages(t)
	form := url.Values{
		"eposta": {"a@ornek.com"}, "sifre": {"x"},
		"authRequestID": {"abc"}, fieldCSRF: {"y"},
	}
	w := postForm(o, pathLogin, form, &http.Cookie{Name: cookieNameSession, Value: "uydurma-deger"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("durum = %d, beklenen 403", w.Code)
	}
}

// C1: it is rejected if the CSRF token is missing or wrong.
func TestLogin_MissingOrWrongCSRFRejected(t *testing.T) {
	o := newTestPages(t)
	if _, err := o.users.Create(t.Context(), "kurban@ornek.com", "K", "dogruSifre12"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	cookie, csrf := openForm(t, o, pathLogin, "istek-1")

	for _, d := range []struct {
		name  string
		token string
	}{
		{"eksik", ""},
		{"yanlis", csrf + "x"},
		{"baska-rasgele", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"},
	} {
		t.Run(d.name, func(t *testing.T) {
			beforeByEmail, _ := o.users.counts()
			form := url.Values{
				"eposta": {"kurban@ornek.com"}, "sifre": {"dogruSifre12"},
				"authRequestID": {"istek-1"}, fieldCSRF: {d.token},
			}
			w := postForm(o, pathLogin, form, cookie)
			if w.Code != http.StatusForbidden {
				t.Fatalf("durum = %d, beklenen 403", w.Code)
			}
			if afterByEmail, _ := o.users.counts(); afterByEmail != beforeByEmail {
				t.Error("CSRF reddi sifre dogrulamasindan sonra olmus")
			}
		})
	}
}

// C1 happy path: with a valid cookie + matching authRequestID + correct CSRF
// the login WORKS and the flow returns to the callback.
func TestLogin_ValidCookieWorks(t *testing.T) {
	o := newTestPages(t)
	users, err := o.users.Create(t.Context(), "kurban@ornek.com", "K", "dogruSifre12")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	cookie, csrf := openForm(t, o, pathLogin, "istek-1")
	form := url.Values{
		"eposta": {"kurban@ornek.com"}, "sifre": {"dogruSifre12"},
		"authRequestID": {"istek-1"}, fieldCSRF: {csrf},
	}
	w := postForm(o, pathLogin, form, cookie)
	if w.Code != http.StatusFound {
		t.Fatalf("durum = %d, beklenen 302. govde: %s", w.Code, w.Body.String())
	}
	if location := w.Header().Get("Location"); location != "/bitti?id=istek-1" {
		t.Errorf("Location = %q", location)
	}
	if got := o.completer.subject("istek-1"); got != users.ID {
		t.Errorf("istek baglanan subject = %q, beklenen kullanici ID", got)
	}
	if got := o.completer.amrFor("istek-1"); !slices.Equal(got, []string{"pwd"}) {
		t.Errorf("amr = %v, beklenen [pwd]", got)
	}
}

// Signup is subject to the same binding.
func TestSignup_NoCookieRejectedNoUserCreated(t *testing.T) {
	o := newTestPages(t)
	form := url.Values{
		"eposta": {"yeni@ornek.com"}, "ad": {"Yeni"},
		"sifre": {"uzunSifre123"}, "authRequestID": {"abc"}, fieldCSRF: {"x"},
	}
	w := postForm(o, pathSignup, form, nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("durum = %d, beklenen 403", w.Code)
	}
	if _, create := o.users.counts(); create != 0 {
		t.Error("reddedilen kayit POST'unda kullanici olusturuldu")
	}
}

// C1 extra finding: a second signup with the SAME authRequestID must NOT
// overwrite Subject. Even if every POST carries its own valid cookie/CSRF, a
// pending request must not be rebindable to a different identity until a code
// is produced.
func TestSignup_SecondSignupKeepsSubject(t *testing.T) {
	o := newTestPages(t)
	const id = "istek-1"

	signUp := func(email string) *httptest.ResponseRecorder {
		cookie, csrf := openForm(t, o, pathSignup, id)
		form := url.Values{
			"eposta": {email}, "ad": {"K"}, "sifre": {"uzunSifre123"},
			"authRequestID": {id}, fieldCSRF: {csrf},
		}
		return postForm(o, pathSignup, form, cookie)
	}

	if w := signUp("birinci@ornek.com"); w.Code != http.StatusFound {
		t.Fatalf("ilk kayit durumu = %d, beklenen 302. govde: %s", w.Code, w.Body.String())
	}
	firstSubject := o.completer.subject(id)
	if firstSubject == "" {
		t.Fatal("ilk kayit istegi baglamadi")
	}
	// The signup POST is the FOURTH amr call site (page.go:586). The login
	// POST's assertion does NOT cover this line: it is a separate call and a
	// separate constant.
	if got := o.completer.amrFor(id); !slices.Equal(got, []string{"pwd"}) {
		t.Errorf("kayit sonrasi amr = %v, beklenen [pwd]", got)
	}

	w := signUp("ikinci@ornek.com")
	if w.Code == http.StatusFound {
		t.Error("ikinci kayit 302 dondu; istek yeniden baglanabiliyor")
	}
	if last := o.completer.subject(id); last != firstSubject {
		t.Errorf("Subject EZILDI: %q -> %q", firstSubject, last)
	}
}

// Enumeration leakage comes from DIFFERENT output for the SAME input
// depending on the account state. That is why here we do not compare TWO
// DIFFERENT emails but two account STATES with the SAME email (not
// registered at all / registered but wrong password). The output (status
// code, body, headers) must be exactly the same; otherwise an attacker can
// infer whether the account exists. Both attempts carry the SAME valid
// cookie/CSRF: otherwise both would stop at 403 and the test would be
// vacuous.
func TestLogin_FailureDoesNotRevealUserExistence(t *testing.T) {
	o := newTestPages(t)
	const email = "ayni@ornek.com"
	cookie, csrf := openForm(t, o, pathLogin, "istek-1")

	tryLogin := func() *httptest.ResponseRecorder {
		form := url.Values{
			"eposta": {email}, "sifre": {"yanlis"},
			"authRequestID": {"istek-1"}, fieldCSRF: {csrf},
		}
		return postForm(o, pathLogin, form, cookie)
	}

	// State A: there is no account with this email.
	a := tryLogin()

	// State B: the account exists, but the "wrong" password differs from the real
	// password; i.e. this branch also stays on the LOGIN-FAILED path.
	if _, err := o.users.Create(t.Context(), email, "V", "dogruSifre12"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	b := tryLogin()

	if a.Code != http.StatusUnauthorized {
		t.Fatalf("durum A = %d, beklenen %d (giris basarisiz olmali, 403 ise cerez baglamasinda durmus)",
			a.Code, http.StatusUnauthorized)
	}
	if a.Code != b.Code {
		t.Fatalf("durum kodlari farkli: A=%d B=%d", a.Code, b.Code)
	}
	if a.Body.String() != b.Body.String() {
		t.Error("iki govde farkli; hesabin varligi sizdiriliyor")
	}
	if !reflect.DeepEqual(a.Header(), b.Header()) {
		t.Error("iki yanitin header'lari farkli; hesabin varligi sizdiriliyor")
	}
}

// The page must escape user input; this is automatic because the template is
// produced with html/template, but it is tested against regression.
func TestPages_EscapeInput(t *testing.T) {
	o := newTestPages(t)
	bad := `"><script>alert(1)</script>`
	// The binding must be SET UP: otherwise GET stops at 403, the body is never
	// produced and the test becomes vacuous.
	cookie := bindTestSession(t, o, bad, nil)
	w := formRequest(o, pathLogin, bad, cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("durum = %d, beklenen 200 (sayfa gercekten uretildi mi?)", w.Code)
	}
	if strings.Contains(w.Body.String(), "<script>") {
		t.Error("girdi kacirilmamis, XSS mumkun")
	}
	if !strings.Contains(w.Body.String(), "&lt;script&gt;") {
		t.Error("kacislanmis <script> govdede yok; girdi hic yansitilmiyor olabilir")
	}
}

// On a failed login the entered email is echoed back into the form (this
// does NOT leak enumeration, because the attacker already sees the value they
// typed). But this echo opens an XSS surface; here we directly measure that
// html/template escapes it.
func TestLogin_EchoedEmailEscaped(t *testing.T) {
	o := newTestPages(t)
	cookie, csrf := openForm(t, o, pathLogin, "istek-1")
	bad := `"><script>alert(1)</script>`
	form := url.Values{
		"eposta": {bad}, "sifre": {"yanlis"},
		"authRequestID": {"istek-1"}, fieldCSRF: {csrf},
	}
	w := postForm(o, pathLogin, form, cookie)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("durum = %d, beklenen 401 (form gercekten islendi mi?)", w.Code)
	}

	body := w.Body.String()
	if strings.Contains(body, "<script>") {
		t.Error("ham <script> govdede bulundu, XSS mumkun")
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Error("kacislanmis <script> govdede bulunamadi; echo hic gorunmuyor olabilir")
	}
	if !strings.Contains(body, "&#34;") {
		t.Error("kacislanmis cift tirnak govdede bulunamadi")
	}
}

func TestSignup_ShowsForm(t *testing.T) {
	o := newTestPages(t)
	cookie := bindTestSession(t, o, "abc", nil)
	w := formRequest(o, pathSignup, "abc", cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("durum = %d", w.Code)
	}
	for _, want := range []string{`name="eposta"`, `name="sifre"`, `name="ad"`, `name="csrf"`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("sayfa %q icermiyor", want)
		}
	}
}

// Signing up with an email in use must give the user an understandable error
// but must not be a 500.
func TestSignup_EmailInUse(t *testing.T) {
	o := newTestPages(t)
	if _, err := o.users.Create(t.Context(), "var@ornek.com", "V", "dogruSifre12"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	cookie, csrf := openForm(t, o, pathSignup, "istek-1")
	form := url.Values{
		"eposta": {"var@ornek.com"}, "ad": {"Yeni"},
		"sifre": {"baskaSifre12"}, "authRequestID": {"istek-1"}, fieldCSRF: {csrf},
	}
	w := postForm(o, pathSignup, form, cookie)
	if w.Code != http.StatusConflict {
		t.Errorf("durum = %d, beklenen 409 (e-posta kullanimda)", w.Code)
	}
}

// ---- /authorize/callback leg ----

// callbackRequest calls /authorize/callback with the given cookie.
func callbackRequest(o *pagesEnv, id string, cookie *http.Cookie) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/authorize/callback?id="+url.QueryEscape(id), nil)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	o.pages.Callback(w, r)
	return w
}

// delegateSpy stands in for the library handler: was it called?
type delegateSpy struct{ called bool }

func (d *delegateSpy) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	d.called = true
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("delege"))
}

// C1: /authorize/callback is REJECTED without a cookie and the library
// handler is NEVER reached (i.e. no code is produced).
func TestAuthorizeCallback_NoCookieRejected(t *testing.T) {
	o := newTestPages(t)
	spy := &delegateSpy{}
	o.pages.SetCallbackDelegate(spy)

	w := callbackRequest(o, "istek-1", nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("durum = %d, beklenen 403", w.Code)
	}
	if spy.called {
		t.Error("kutuphane callback handler'ina delege edildi")
	}
}

// C1: callback is REJECTED with a cookie bound to another auth request.
func TestAuthorizeCallback_WrongCookieRejected(t *testing.T) {
	o := newTestPages(t)
	spy := &delegateSpy{}
	o.pages.SetCallbackDelegate(spy)
	cookie, _ := openForm(t, o, pathLogin, "istek-B")

	w := callbackRequest(o, "istek-A", cookie)
	if w.Code != http.StatusForbidden {
		t.Fatalf("durum = %d, beklenen 403", w.Code)
	}
	if spy.called {
		t.Error("eslesmeyen cerezle delege edildi")
	}
}

// C1 happy path: with the right cookie the callback delegates to the library
// handler.
func TestAuthorizeCallback_ValidCookieDelegates(t *testing.T) {
	o := newTestPages(t)
	spy := &delegateSpy{}
	o.pages.SetCallbackDelegate(spy)
	cookie, _ := openForm(t, o, pathLogin, "istek-1")

	w := callbackRequest(o, "istek-1", cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("durum = %d, beklenen 200", w.Code)
	}
	if !spy.called {
		t.Error("dogru cerezle delege edilmedi")
	}
}

// The callback is meaningless without the id parameter.
func TestAuthorizeCallback_MissingIDRejected(t *testing.T) {
	o := newTestPages(t)
	spy := &delegateSpy{}
	o.pages.SetCallbackDelegate(spy)
	w := httptest.NewRecorder()
	o.pages.Callback(w, httptest.NewRequest(http.MethodGet, "/authorize/callback", nil))
	if w.Code != http.StatusBadRequest {
		t.Errorf("durum = %d, beklenen 400", w.Code)
	}
	if spy.called {
		t.Error("id olmadan delege edildi")
	}
}

// A REAL PRODUCTION BUG (I4a): signup applies "strings.TrimSpace(email)" while
// login passed the form value RAW. A user who typed their email with leading
// or trailing whitespace could sign up (" x@y.com " -> "x@y.com" is stored),
// then when they tried to log in with the SAME input it could not be found
// and they got "E-posta veya sifre hatali": they had an account but could
// never get in with that input.
//
// This test measures that path end to end: SIGNUP with a padded email, then
// LOGIN with both the PADDED and the UNPADDED input.
func TestLogin_WorksAfterSignupWithPaddedEmail(t *testing.T) {
	const password = "gecerliSifre12"
	const padded = "  bosluk@ornek.com  "
	const unpadded = "bosluk@ornek.com"

	o := newTestPages(t)

	// Signup: padded input.
	cookie, csrf := openForm(t, o, pathSignup, "istek-kayit")
	w := postForm(o, pathSignup, url.Values{
		"authRequestID": {"istek-kayit"},
		fieldCSRF:       {csrf},
		"eposta":        {padded},
		"ad":            {"Bosluk Kullanici"},
		"sifre":         {password},
	}, cookie)
	if w.Code != http.StatusFound {
		t.Fatalf("kayit durumu = %d, beklenen 302 (govde: %s)", w.Code, w.Body.String())
	}

	// The stored value must be normalized.
	registered, err := o.users.ByEmail(context.Background(), unpadded)
	if err != nil {
		t.Fatalf("kayitli kullanici bulunamadi: %v", err)
	}
	if registered.Email != unpadded {
		t.Errorf("saklanan e-posta = %q, beklenen %q", registered.Email, unpadded)
	}

	// Login: both inputs must WORK.
	for _, input := range []string{padded, unpadded, "  BOSLUK@ORNEK.COM "} {
		t.Run("giris girdisi="+strconv.Quote(input), func(t *testing.T) {
			id := "istek-giris-" + input
			cookie, csrf := openForm(t, o, pathLogin, id)
			w := postForm(o, pathLogin, url.Values{
				"authRequestID": {id},
				fieldCSRF:       {csrf},
				"eposta":        {input},
				"sifre":         {password},
			}, cookie)
			if w.Code != http.StatusFound {
				t.Errorf("giris durumu = %d, beklenen 302 (govde: %s)", w.Code, w.Body.String())
			}
		})
	}

	// NEGATIVE CONTROL: the fix must not turn into "every password passes".
	// A wrong password must still get 401, i.e. the 302s above are thanks to
	// normalization, not thanks to a loosened verification.
	cookie, csrf = openForm(t, o, pathLogin, "istek-yanlis")
	w = postForm(o, pathLogin, url.Values{
		"authRequestID": {"istek-yanlis"},
		fieldCSRF:       {csrf},
		"eposta":        {padded},
		"sifre":         {"yanlisSifre12"},
	}, cookie)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("yanlis sifre durumu = %d, beklenen 401", w.Code)
	}
}

// ---- Login-CSRF: cross-site navigation at /authorize ----

// wrapAuthorize imitates the production /authorize leg: instead of the
// library handler it puts a handler that writes a 302 to the login page and
// wraps it with Pages.WrapAuthorize. If fetchSite is passed empty, the
// Sec-Fetch-Site header is NOT ADDED AT ALL (an old/non-browser client).
func wrapAuthorize(o *pagesEnv, fetchSite, id string, cookie *http.Cookie) *httptest.ResponseRecorder {
	libHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", pathLogin+"?authRequestID="+url.QueryEscape(id))
		w.WriteHeader(http.StatusFound)
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/authorize?client_id=makesinger-mobil", nil)
	if fetchSite != "" {
		r.Header.Set(headerFetchSite, fetchSite)
		r.Header.Set(headerFetchMode, "navigate")
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	o.pages.WrapAuthorize(libHandler).ServeHTTP(w, r)
	return w
}

// isBound reads from the store whether the given cookie value is bound to
// the given authRequestID.
func isBound(t *testing.T, o *pagesEnv, session, id string) bool {
	t.Helper()
	binding, err := o.sessions.ReadSession(context.Background(), session)
	if err != nil {
		return false
	}
	_, exists := binding.CSRFFor(id)
	return exists
}

// LOGIN-CSRF: an /authorize that comes by cross-site navigation DOES NOT ADD
// A NEW BINDING to the victim's EXISTING cookie. The request the attacker
// prepared (carrying their own PKCE challenge) cannot enter the victim's set;
// the victim cannot do GET /giris with that id (403) and the request CANNOT
// BE COMPLETED.
func TestWrapAuthorize_CrossSiteAddsNoBinding(t *testing.T) {
	o := newTestPages(t)
	// The victim's own legitimate flow: the cookie and binding are born here.
	victimCookie := bindTestSession(t, o, "mesru-1", nil)

	// The address the attacker prepared is opened in the victim's browser by a
	// cross-site top-level navigation.
	w := wrapAuthorize(o, fetchSiteCrossSite, "saldirgan-1", victimCookie)

	// The request itself goes to the library (a 302 is written): instead of
	// rejecting, only the binding is not set up.
	if w.Code != http.StatusFound {
		t.Fatalf("durum = %d, beklenen 302 (istek kutuphaneye gitmeli)", w.Code)
	}
	// The cookie must be UNTOUCHED: no new Set-Cookie.
	if cookieList := w.Result().Cookies(); len(cookieList) != 0 {
		t.Errorf("capraz-site istek %d cerez yazdi, beklenen 0", len(cookieList))
	}
	// The attacker's id must NOT have entered the victim's set.
	if isBound(t, o, victimCookie.Value, "saldirgan-1") {
		t.Fatal("capraz-site istek kurbanin kumesine baglama EKLEDI (login-CSRF acik)")
	}
	// The victim cannot open the form with that id: 403.
	if g := formRequest(o, pathLogin, "saldirgan-1", victimCookie); g.Code != http.StatusForbidden {
		t.Fatalf("enjekte id icin GET /giris durumu = %d, beklenen 403", g.Code)
	}
	// The victim's EXISTING binding must NOT BE BROKEN (we did not turn it into a DoS).
	if !isBound(t, o, victimCookie.Value, "mesru-1") {
		t.Error("kurbanin mevcut baglamasi kayboldu")
	}
	if g := formRequest(o, pathLogin, "mesru-1", victimCookie); g.Code != http.StatusOK {
		t.Errorf("kurbanin mesru formu durumu = %d, beklenen 200", g.Code)
	}
}

// For every NON-cross-site fetch-metadata value the binding IS SET UP.
//
// "none": navigation the user started directly -- including a native app
// opening the system browser; OUR LEGITIMATE FLOW.
// "same-origin"/"same-site": navigation returning from our own pages.
// NO header: fail-open (rationale in the isCrossSite documentation).
func TestWrapAuthorize_NonCrossSiteBinds(t *testing.T) {
	for _, d := range []struct{ name, fetchSite string }{
		{"none-native-uygulama", fetchSiteNone},
		{"same-origin", fetchSiteSameOrigin},
		{"same-site", fetchSiteSameSite},
		{"baslik-yok-fail-open", ""},
	} {
		t.Run(d.name, func(t *testing.T) {
			o := newTestPages(t)
			w := wrapAuthorize(o, d.fetchSite, "istek-1", nil)
			if w.Code != http.StatusFound {
				t.Fatalf("durum = %d, beklenen 302", w.Code)
			}
			var session string
			for _, c := range w.Result().Cookies() {
				if c.Name == cookieNameSession {
					session = c.Value
				}
			}
			if session == "" {
				t.Fatal("oturum cerezi verilmedi (baglama kurulmamis)")
			}
			if !isBound(t, o, session, "istek-1") {
				t.Fatal("baglama depoya yazilmamis")
			}
			// Happy path: the form must be openable.
			if g := formRequest(o, pathLogin, "istek-1", &http.Cookie{Name: cookieNameSession, Value: session}); g.Code != http.StatusOK {
				t.Fatalf("form durumu = %d, beklenen 200", g.Code)
			}
		})
	}
}

// The buttons appear ONLY for configured providers: a user who clicked the
// button of a provider that does not exist would see a 404, because that
// endpoint's handler is never wired (see buildMux).
func TestLogin_ShowsOnlyConfiguredProviders(t *testing.T) {
	o := newTestPages(t)
	o.pages.SetFederationProviders([]string{providerGoogle})
	cookie := bindTestSession(t, o, "abc", nil)
	w := formRequest(o, pathLogin, "abc", cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("durum = %d, beklenen 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "/federasyon/google/baslat") {
		t.Errorf("yapilandirilmis google butonu yok:\n%s", body)
	}
	if !strings.Contains(body, "Google ile giris yap") {
		t.Errorf("google butonunun etiketi yok:\n%s", body)
	}
	// "apple" must not appear AT ALL: neither the path nor the label.
	if strings.Contains(strings.ToLower(body), providerApple) {
		t.Errorf("yapilandirilmamis apple sayfada gecti:\n%s", body)
	}
}

// The button link MUST CARRY the authRequestID: if it does not, the request to
// complete cannot be found on return. The value contains characters that
// REQUIRE URL escaping: if it were joined by hand in the template, the "&"
// inside would inject a foreign parameter into the query string.
func TestLogin_ButtonCarriesAuthRequestID(t *testing.T) {
	o := newTestPages(t)
	o.pages.SetFederationProviders([]string{providerGoogle})
	const id = "istek 1&yabanci=1"
	cookie := bindTestSession(t, o, id, nil)
	w := formRequest(o, pathLogin, id, cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("durum = %d, beklenen 200", w.Code)
	}
	body := w.Body.String()
	pattern := regexp.MustCompile(`href="(/federasyon/google/baslat[^"]*)"`)
	matches := pattern.FindStringSubmatch(body)
	if matches == nil {
		t.Fatalf("google buton baglantisi bulunamadi:\n%s", body)
	}
	// The link is decoded the way the browser would see it: html/template's
	// attribute escaping is undone, then the URL is parsed.
	raw := html.UnescapeString(matches[1])
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("buton baglantisi cozulemedi (%q): %v", raw, err)
	}
	if matched := u.Query().Get("authRequestID"); matched != id {
		t.Errorf("authRequestID = %q, beklenen %q (ham: %q)", matched, id, raw)
	}
	if u.Query().Get("yabanci") != "" {
		t.Errorf("kacissiz birlestirme yabanci parametre sizdirdi: %q", raw)
	}
}
