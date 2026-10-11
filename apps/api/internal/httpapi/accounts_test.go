package httpapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/iamhere/api/internal/app"
)

var linkRe = regexp.MustCompile(`https://attend\.example\.org/#/(verify|reset)/(iah_l_[A-Za-z0-9_-]+)`)

// waitLink waits for the n-th email (1-based) and returns the link of the given kind in it.
func waitLink(t *testing.T, dir string, n int, kind string) (to, token string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		files, _ := filepath.Glob(filepath.Join(dir, "*.eml"))
		sort.Strings(files)
		if len(files) >= n {
			b, _ := os.ReadFile(files[n-1])
			body := strings.ReplaceAll(string(b), "=\r\n", "") // quoted-printable soft breaks
			if m := linkRe.FindStringSubmatch(body); m != nil && m[1] == kind {
				hdrTo := regexp.MustCompile(`(?m)^To: (.+)\r$`).FindStringSubmatch(body)
				return hdrTo[1], m[2]
			}
			t.Fatalf("email %d has no %s link:\n%s", n, kind, body)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("email %d (%s) never arrived", n, kind)
	return "", ""
}

func TestAccountsOverHTTP(t *testing.T) {
	ts, mailDir := newServerMail(t)
	anon := client{t, ts.URL, ""}

	var pc PublicConfig
	anon.json("GET", "/v1/public-config", nil, 200, &pc)
	if pc.Auth != "local" || !pc.Email {
		t.Fatalf("%+v", pc)
	}

	// ---- sign up: name, email, password; mistakes are explained
	for _, bad := range []struct {
		body map[string]any
		code string
	}{
		{map[string]any{"name": "", "email": "sara@example.org", "password": "blue-river-seven"}, "invalid_name"},
		{map[string]any{"name": "Sara", "email": "sara@example", "password": "blue-river-seven"}, "invalid_email"},
		{map[string]any{"name": "Sara", "email": "not an email", "password": "blue-river-seven"}, "invalid_email"},
		{map[string]any{"name": "Sara", "email": "sara@example.org", "password": "short"}, "weak_password"},
		{map[string]any{"name": "Sara", "email": "sara@example.org", "password": "password123"}, "weak_password"},
	} {
		code, b := anon.do("POST", "/v1/auth/signup", bad.body)
		if code != 400 || !strings.Contains(string(b), bad.code) {
			t.Errorf("%v: %d %s", bad.body, code, b)
		}
	}
	var sara app.SignedIn
	anon.json("POST", "/v1/auth/signup", map[string]any{"name": "Sara Ahmed", "email": "  Sara@Example.org ", "password": "blue-river-seven"}, 201, &sara)
	if !strings.HasPrefix(sara.Token, "iah_s_") || sara.User.FullName != "Sara Ahmed" {
		t.Fatalf("%+v", sara)
	}
	code, b := anon.do("POST", "/v1/auth/signup", map[string]any{"name": "Other", "email": "SARA@example.org", "password": "another-pass-9"})
	if code != 409 || !strings.Contains(string(b), "email_taken") {
		t.Fatalf("same email, other case: %d %s", code, b)
	}
	me := client{t, ts.URL, sara.Token}
	var acct app.Account
	me.json("GET", "/v1/auth/account", nil, 200, &acct)
	if acct.Email != "sara@example.org" || acct.EmailVerified {
		t.Fatalf("%+v", acct)
	}

	// ---- confirm the email from the link that was sent
	to, tok := waitLink(t, mailDir, 1, "verify")
	if !strings.Contains(to, "sara@example.org") {
		t.Fatalf("sent to %q", to)
	}
	anon.json("POST", "/v1/auth/email/confirm", map[string]any{"token": tok}, 204, nil)
	anon.json("POST", "/v1/auth/email/confirm", map[string]any{"token": tok}, 410, nil) // single use
	me.json("GET", "/v1/auth/account", nil, 200, &acct)
	if !acct.EmailVerified {
		t.Fatal("email not confirmed")
	}

	// ---- one identity everywhere: two organizations, one person, one history
	var orgA, orgB app.Org
	for i, o := range []*app.Org{&orgA, &orgB} {
		owner := client{t, ts.URL, "dev:owner-" + string(rune('a'+i)) + ":Owner"}
		owner.json("POST", "/v1/orgs", map[string]any{"name": "Org " + string(rune('A'+i)), "slug": "acct-org-" + string(rune('a'+i))}, 201, o)
		var s app.Session
		owner.json("POST", "/v1/orgs/"+o.ID.String()+"/sessions", map[string]any{"title": "Workshop " + string(rune('A'+i))}, 201, &s)
		var q app.QRInfo
		owner.json("GET", "/v1/orgs/"+o.ID.String()+"/sessions/"+s.ID.String()+"/qr", nil, 200, &q)
		var res app.CheckInResult
		me.json("POST", "/v1/checkins", map[string]any{"token": q.Token}, 200, &res)
		if res.Status != "verified" {
			t.Fatalf("%+v", res)
		}
	}
	var hist struct{ Items []app.HistoryRow }
	me.json("GET", "/v1/me/attendance", nil, 200, &hist)
	if len(hist.Items) != 2 {
		t.Fatalf("one account, two organizations' workshops: %+v", hist)
	}

	// ---- sign in: a wrong email and a wrong password look the same
	var w1, w2 map[string]string
	anon.json("POST", "/v1/auth/signin", map[string]any{"email": "nobody@example.org", "password": "blue-river-seven"}, 401, &w1)
	anon.json("POST", "/v1/auth/signin", map[string]any{"email": "sara@example.org", "password": "wrong-password-1"}, 401, &w2)
	if w1["error"] != "wrong_credentials" || w1["message"] != w2["message"] {
		t.Fatalf("%v vs %v", w1, w2)
	}
	var phone app.SignedIn
	anon.json("POST", "/v1/auth/signin", map[string]any{"email": "SARA@example.org", "password": "blue-river-seven"}, 200, &phone)
	onPhone := client{t, ts.URL, phone.Token}
	onPhone.json("GET", "/v1/me", nil, 200, nil)

	// ---- change password: needs the current one; signs out the other devices
	me.json("POST", "/v1/auth/password", map[string]any{"current": "nope-nope-nope", "new": "green-hill-eight"}, 400, nil)
	me.json("POST", "/v1/auth/password", map[string]any{"current": "blue-river-seven", "new": "green-hill-eight"}, 204, nil)
	me.json("GET", "/v1/me", nil, 200, nil)
	onPhone.json("GET", "/v1/me", nil, 401, nil)

	// ---- forgot password -> emailed link -> new password, everywhere else signed out
	anon.json("POST", "/v1/auth/password/forgot", map[string]any{"email": "nobody@example.org"}, 202, nil) // same answer, no email
	anon.json("POST", "/v1/auth/password/forgot", map[string]any{"email": "Sara@example.org"}, 202, nil)
	_, reset := waitLink(t, mailDir, 2, "reset")
	anon.json("POST", "/v1/auth/password/reset", map[string]any{"token": reset, "password": "short"}, 400, nil) // link survives a weak choice
	var back app.SignedIn
	anon.json("POST", "/v1/auth/password/reset", map[string]any{"token": reset, "password": "red-sky-nine-two"}, 200, &back)
	anon.json("POST", "/v1/auth/password/reset", map[string]any{"token": reset, "password": "red-sky-nine-two"}, 410, nil)
	me.json("GET", "/v1/me", nil, 401, nil)
	(client{t, ts.URL, back.Token}).json("GET", "/v1/me", nil, 200, nil)
	anon.json("POST", "/v1/auth/signin", map[string]any{"email": "sara@example.org", "password": "green-hill-eight"}, 401, nil)
	anon.json("POST", "/v1/auth/signin", map[string]any{"email": "sara@example.org", "password": "red-sky-nine-two"}, 200, nil)

	// ---- sign out ends this device only
	var other app.SignedIn
	anon.json("POST", "/v1/auth/signin", map[string]any{"email": "sara@example.org", "password": "red-sky-nine-two"}, 200, &other)
	cur := client{t, ts.URL, back.Token}
	cur.json("POST", "/v1/auth/signout", nil, 204, nil)
	cur.json("GET", "/v1/me", nil, 401, nil)
	(client{t, ts.URL, other.Token}).json("GET", "/v1/me", nil, 200, nil)

	// ---- guessing is slowed down per account, whatever the IP
	for i := 0; i < 10; i++ {
		anon.do("POST", "/v1/auth/signin", map[string]any{"email": "sara@example.org", "password": "guess-guess-" + string(rune('a'+i))})
	}
	code, b = anon.do("POST", "/v1/auth/signin", map[string]any{"email": "sara@example.org", "password": "red-sky-nine-two"})
	if code != 429 || !strings.Contains(string(b), "too_many_attempts") {
		t.Fatalf("after 10 wrong passwords: %d %s", code, b)
	}

	// ---- deleting the account ends its sessions and frees the email
	oc := client{t, ts.URL, other.Token}
	oc.json("DELETE", "/v1/me", nil, 204, nil)
	oc.json("GET", "/v1/me", nil, 401, nil)
	anon.json("POST", "/v1/auth/signup", map[string]any{"name": "Sara Again", "email": "sara@example.org", "password": "blue-river-seven"}, 201, nil)

	// garbage tokens and links
	(client{t, ts.URL, "iah_s_" + strings.Repeat("A", 43)}).json("GET", "/v1/me", nil, 401, nil)
	anon.json("POST", "/v1/auth/email/confirm", map[string]any{"token": "nope"}, 410, nil)
	anon.json("POST", "/v1/auth/signup", map[string]any{"name": "X", "email": "x@example.org", "password": "blue-river-seven", "admin": true}, 400, nil)
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.5:4444"
	r.Header.Set("X-Forwarded-For", "6.6.6.6, 203.0.113.9")
	if ip := (&Server{}).clientIP(r); ip != "10.0.0.5" {
		t.Fatalf("not behind a proxy: forwarded header must be ignored, got %s", ip)
	}
	if ip := (&Server{trustProxy: true}).clientIP(r); ip != "203.0.113.9" {
		t.Fatalf("behind the proxy: the hop it added, got %s", ip)
	}
	r.Header.Del("X-Forwarded-For")
	if ip := (&Server{trustProxy: true}).clientIP(r); ip != "10.0.0.5" {
		t.Fatalf("no header: %s", ip)
	}
	_ = http.StatusOK
}
