package lyricsprovider

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/makeasinger/api/internal/client"
)

type fakeClient struct{ configured bool }

func (f *fakeClient) ChatCompletion(context.Context, string, string, float64) (string, error) {
	return "", nil
}
func (f *fakeClient) Temperatures() (float64, float64) { return 0, 0 }
func (f *fakeClient) IsConfigured() bool               { return f.configured }

type fakeStore struct {
	name  Name
	ok    bool
	err   error
	reads int
	setBy string
}

func (s *fakeStore) Get(context.Context) (Name, bool, error) {
	s.reads++
	return s.name, s.ok, s.err
}

func (s *fakeStore) Set(_ context.Context, name Name, by string) error {
	if s.err != nil {
		return s.err
	}
	s.name, s.ok, s.setBy = name, true, by
	return nil
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func setup(store Store) (*Selector, map[Name]client.ChatClient, *clock) {
	clients := map[Name]client.ChatClient{
		Groq: &fakeClient{configured: true}, OpenAI: &fakeClient{configured: true},
		Anthropic: &fakeClient{configured: true},
	}
	c := &clock{t: time.Unix(1000, 0)}
	return NewSelector(clients, store, Groq, 30*time.Second, c.now), clients, c
}

func TestSelector_NoRowUsesFallback(t *testing.T) {
	s, clients, _ := setup(&fakeStore{})
	name, c := s.Active(context.Background())
	if name != Groq || c != clients[Groq] {
		t.Errorf("Active = %v, want groq", name)
	}
}

func TestSelector_StoredProviderWins(t *testing.T) {
	s, clients, _ := setup(&fakeStore{name: Anthropic, ok: true})
	name, c := s.Active(context.Background())
	if name != Anthropic || c != clients[Anthropic] {
		t.Errorf("Active = %v, want anthropic", name)
	}
}

func TestSelector_CachesForTTL(t *testing.T) {
	store := &fakeStore{name: OpenAI, ok: true}
	s, _, clk := setup(store)
	s.Active(context.Background())
	store.name = Anthropic
	clk.t = clk.t.Add(29 * time.Second)
	if name, _ := s.Active(context.Background()); name != OpenAI {
		t.Errorf("within TTL = %v, want cached openai", name)
	}
	clk.t = clk.t.Add(2 * time.Second)
	if name, _ := s.Active(context.Background()); name != Anthropic {
		t.Errorf("after TTL = %v, want anthropic", name)
	}
	if store.reads != 2 {
		t.Errorf("store reads = %d, want 2", store.reads)
	}
}

func TestSelector_StoreErrorKeepsLastKnown(t *testing.T) {
	store := &fakeStore{name: OpenAI, ok: true}
	s, _, clk := setup(store)
	s.Active(context.Background())
	store.err = errors.New("db down")
	clk.t = clk.t.Add(time.Minute)
	if name, c := s.Active(context.Background()); name != OpenAI || c == nil {
		t.Errorf("store error = %v, want last known openai", name)
	}
}

func TestSelector_StoreErrorWithoutLastKnownUsesFallback(t *testing.T) {
	s, _, _ := setup(&fakeStore{err: errors.New("db down")})
	if name, _ := s.Active(context.Background()); name != Groq {
		t.Errorf("Active = %v, want fallback groq", name)
	}
}

func TestSelector_UnconfiguredStoredFallsBack(t *testing.T) {
	store := &fakeStore{name: OpenAI, ok: true}
	s, clients, _ := setup(store)
	clients[OpenAI].(*fakeClient).configured = false
	if name, _ := s.Active(context.Background()); name != Groq {
		t.Errorf("Active = %v, want fallback groq", name)
	}
}

func TestSelector_SetRejectsUnavailable(t *testing.T) {
	store := &fakeStore{}
	s, clients, _ := setup(store)
	clients[OpenAI].(*fakeClient).configured = false
	for _, name := range []Name{OpenAI, "gemini"} {
		if err := s.Set(context.Background(), name, "a@b.c"); !errors.Is(err, ErrUnavailable) {
			t.Errorf("Set(%s) err = %v, want ErrUnavailable", name, err)
		}
	}
	if store.ok {
		t.Error("store must be untouched")
	}
}

func TestSelector_SetRefreshesImmediately(t *testing.T) {
	store := &fakeStore{name: Groq, ok: true}
	s, _, _ := setup(store)
	s.Active(context.Background())
	if err := s.Set(context.Background(), Anthropic, "a@b.c"); err != nil {
		t.Fatal(err)
	}
	if store.setBy != "a@b.c" {
		t.Errorf("setBy = %q", store.setBy)
	}
	if name, _ := s.Active(context.Background()); name != Anthropic {
		t.Errorf("after Set = %v, want anthropic at once", name)
	}
}

func TestSelector_AvailableIsSortedConfigured(t *testing.T) {
	s, clients, _ := setup(&fakeStore{})
	clients[OpenAI].(*fakeClient).configured = false
	if got := s.Available(); !reflect.DeepEqual(got, []Name{Anthropic, Groq}) {
		t.Errorf("Available = %v", got)
	}
}

func TestFixed_AlwaysReturnsTheClient(t *testing.T) {
	c := &fakeClient{configured: true}
	name, got := Fixed(c).Active(context.Background())
	if got != c || name != "" {
		t.Errorf("Fixed.Active = %v, %v", name, got)
	}
}

func TestParseName(t *testing.T) {
	if n, err := ParseName("anthropic"); err != nil || n != Anthropic {
		t.Errorf("ParseName = %v, %v", n, err)
	}
	if _, err := ParseName("gemini"); err == nil {
		t.Error("unknown name must fail")
	}
}
