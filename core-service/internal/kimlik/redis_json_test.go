package kimlik

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"
)

// jsonKeys returns the sorted top-level JSON keys of v.
func jsonKeys(t *testing.T, v any) []string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %T: %v", v, err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal %T: %v", v, err)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// TestRedisJSON_KeysUnchanged pins the JSON keys of every struct that is
// stored in Redis. Renaming a Go field must not change them: in-flight
// logins, federation callbacks and pending links would break during a
// rolling deploy.
func TestRedisJSON_KeysUnchanged(t *testing.T) {
	tests := []struct {
		v    any
		want []string
	}{
		{AuthIstek{}, []string{"AMR", "AuthTime", "ClientID", "CodeChallenge", "CodeChallengeMethod", "ID", "IpucuSubject", "Nonce", "RedirectURI", "ResponseMode", "ResponseType", "Scopes", "State", "Subject"}},
		{OturumBaglama{}, []string{"Kayitlar"}},
		{OturumKayit{}, []string{"AuthRequestID", "CSRF"}},
		{FedereDurum{}, []string{"AuthRequestID", "Cerez", "KodDogrulayici", "Nonce", "Saglayici"}},
		{BaglamaBekleme{}, []string{"AuthRequestID", "CSRF", "Eposta", "Saglayici", "Sub", "UserID"}},
		{AccessKayit{}, []string{"ClientID", "ExpiresAt", "ID", "Scopes", "UserID"}},
	}
	for _, tt := range tests {
		if got := jsonKeys(t, tt.v); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%T JSON keys = %v, want %v", tt.v, got, tt.want)
		}
	}
}
