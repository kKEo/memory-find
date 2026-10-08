package webpolicy

import "testing"

func TestDecide(t *testing.T) {
	const origin = "http://127.0.0.1:8765"
	for _, c := range []struct {
		target  string
		clicked bool
		want    Decision
	}{
		{"http://127.0.0.1:8765/login?code=x&next=%2Flive", false, Allow}, // the login link
		{"http://127.0.0.1:8765/live", false, Allow},                      // its redirect, and the page refreshing
		{"http://127.0.0.1:8765/doc/3", true, Allow},
		{"about:blank", false, Allow},
		{"https://example.com/source", true, External}, // a document's source link
		{"https://example.com/source", false, Deny},    // a redirect off the server
		{"http://127.0.0.1:8766/live", true, External}, // another server
		{"http://localhost:8765/live", true, External}, // another origin, even if the same machine
		{"https://127.0.0.1:8765/live", true, External},
		{"http://user@127.0.0.1:8765/live", false, Deny},
		{"javascript:alert(1)", true, Deny},
		{"file:///etc/passwd", true, Deny},
		{"x-apple.systempreferences:com.apple.preference.security", true, Deny},
		{"mailto:a@b.c", true, Deny},
		{"about:config", false, Deny},
		{"::not a url", true, Deny},
	} {
		if got := Decide(origin, c.target, c.clicked); got != c.want {
			t.Errorf("Decide(%q, clicked=%v) = %v, want %v", c.target, c.clicked, got, c.want)
		}
	}
	if Decide("https://box.example", "https://box.example:443/live", false) != Allow {
		t.Error("default port not matched")
	}
}
