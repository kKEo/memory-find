package live

import (
	"strings"
	"testing"
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
