package auth

import (
	"context"
	"net/url"
	"testing"
)

func TestDevVerifier(t *testing.T) {
	id, err := Dev{}.Verify(context.Background(), "dev:s1:Full: Name")
	if err != nil || id.Subject != "s1" || id.Name != "Full: Name" || id.Issuer != "dev" {
		t.Fatalf("%+v %v", id, err)
	}
	// a browser sends non-Latin names percent-encoded; both forms are understood
	for raw, want := range map[string]string{
		"dev:k1:" + url.PathEscape("ئاوات کوردی"): "ئاوات کوردی",
		"dev:k2:ئاوات":        "ئاوات",
		"dev:p1:100%":         "100%",
		"dev:p2:Ali%20Hassan": "Ali Hassan",
	} {
		id, err := Dev{}.Verify(context.Background(), raw)
		if err != nil || id.Name != want {
			t.Errorf("%q -> %q, %v (want %q)", raw, id.Name, err, want)
		}
	}
	for _, bad := range []string{"", "dev:", "dev:x", "dev::n", "dev:x: ", "foo:x:y"} {
		if _, err := (Dev{}).Verify(context.Background(), bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestPasswords(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	h2, _ := HashPassword("correct horse battery")
	if h == h2 {
		t.Fatal("hashes must be salted")
	}
	if ok, err := CheckPassword(h, "correct horse battery"); !ok || err != nil {
		t.Fatalf("right password refused: %v", err)
	}
	if ok, _ := CheckPassword(h, "correct horse batterY"); ok {
		t.Fatal("wrong password accepted")
	}
	for _, bad := range []string{"", "plain", "$argon2id$v=19$m=0,t=1,p=1$AA$AA", "$bcrypt$x$y$z$w"} {
		if ok, err := CheckPassword(bad, "x"); ok || err == nil {
			t.Errorf("malformed hash %q: ok=%v err=%v", bad, ok, err)
		}
	}
	CheckDummy("anything") // must not panic
}

func TestPasswordRules(t *testing.T) {
	for pw, ok := range map[string]bool{
		"short":                 false,
		"aaaaaaaaaaaa":          false,
		"password123":           false,
		"Password123":           false, // common, case-insensitively
		"ali.hassan-2024":       false, // contains the email's local part
		"blue-river-seven":      true,
		"ئاوات کوردی ١٢٣":       true, // any script
		string(make([]rune, 0)): false,
	} {
		if got := ValidatePassword(pw, "ali.hassan@example.org") == ""; got != ok {
			t.Errorf("%q: acceptable=%v, want %v", pw, got, ok)
		}
	}
}

func TestTokens(t *testing.T) {
	a, ha, err := NewToken("iah_s_")
	if err != nil || len(a) < 40 || a[:6] != "iah_s_" {
		t.Fatalf("%q %v", a, err)
	}
	b, _, _ := NewToken("iah_s_")
	if a == b || string(HashToken(a)) != string(ha) {
		t.Fatal("tokens must be random and hash consistently")
	}
}
