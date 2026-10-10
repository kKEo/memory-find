// Package webpolicy decides where a stats window may go. The window shows
// one memors-mcp server's pages; documents on those pages can link anywhere
// (their text comes from the knowledge base), so anything off that server
// either opens in the default browser, when the user clicked it, or is
// refused.
package webpolicy

import (
	"net/url"
	"strings"
)

// Decision is what to do with a navigation.
type Decision int

// The decisions.
const (
	Allow    Decision = iota // load it in the window
	External                 // open it in the default browser instead
	Deny                     // do nothing
)

// Decide judges a navigation to target in a window showing origin (a base
// URL such as http://127.0.0.1:8765). clicked is true when the user
// activated a link, as opposed to a redirect or a page refreshing itself.
func Decide(origin, target string, clicked bool) Decision {
	if target == "about:blank" {
		return Allow
	}
	o, err1 := url.Parse(origin)
	t, err2 := url.Parse(target)
	if err1 != nil || err2 != nil {
		return Deny
	}
	scheme := strings.ToLower(t.Scheme)
	if scheme != "http" && scheme != "https" {
		return Deny
	}
	if scheme == strings.ToLower(o.Scheme) && strings.EqualFold(hostPort(t), hostPort(o)) && t.User == nil {
		return Allow
	}
	if clicked {
		return External
	}
	return Deny
}

// hostPort spells out the default port, so http://h and http://h:80 match.
func hostPort(u *url.URL) string {
	if u.Port() != "" {
		return u.Host
	}
	switch strings.ToLower(u.Scheme) {
	case "http":
		return u.Hostname() + ":80"
	case "https":
		return u.Hostname() + ":443"
	}
	return u.Host
}
