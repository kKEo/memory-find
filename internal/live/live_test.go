package live

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSessionKey(t *testing.T) {
	raw := "PWQH4ZMJZJ6OOGM5HU3QB6FSDN"
	k := SessionKey(raw)
	if len(k) != 12 || strings.Trim(k, "0123456789abcdef") != "" {
		t.Errorf("SessionKey = %q, want 12 hex digits", k)
	}
	if k != SessionKey(raw) {
		t.Error("SessionKey is not stable")
	}
	if k == SessionKey(raw+"x") {
		t.Errorf("SessionKey %q is the same for different ids", k)
	}
}

// Every exported field is part of the /live.json contract, so each one
// names its JSON key explicitly.
func TestEveryFieldHasAJSONTag(t *testing.T) {
	for _, v := range []any{Snapshot{}, Session{}, Call{}} {
		typ := reflect.TypeOf(v)
		for i := range typ.NumField() {
			f := typ.Field(i)
			if f.IsExported() && f.Tag.Get("json") == "" {
				t.Errorf("%s.%s has no json tag", typ.Name(), f.Name)
			}
		}
	}
}

func TestRawIDsNeverEncoded(t *testing.T) {
	snap := Snapshot{
		Sessions: []Session{{ID: "raw-session-id", Key: "k", Since: time.Now()}},
		InFlight: []Call{{Session: "raw-session-id", SessionKey: "k"}},
	}
	b, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "raw-session-id") {
		t.Errorf("raw session id encoded: %s", b)
	}
	if strings.Contains(string(b), "last_call") {
		t.Errorf("zero last_call encoded: %s", b)
	}
}
