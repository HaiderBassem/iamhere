package config

import (
	"testing"
	"time"
)

func TestValidate(t *testing.T) {
	cases := []struct {
		name string
		c    Config
		ok   bool
	}{
		{"dev, nothing else needed", Config{Env: "development", DatabaseURL: "x"}, true},
		{"dev with dev auth", Config{Env: "development", DatabaseURL: "x", DevAuth: true}, true},
		{"missing database", Config{Env: "development"}, false},
		{"production forbids dev auth", Config{Env: "production", DatabaseURL: "x", DevAuth: true, PublicURL: "https://a.org"}, false},
		{"production needs its https address", Config{Env: "production", DatabaseURL: "x"}, false},
		{"production refuses an http address", Config{Env: "production", DatabaseURL: "x", PublicURL: "http://a.org"}, false},
		{"production refuses the mail directory", Config{Env: "production", DatabaseURL: "x", PublicURL: "https://a.org", MailDir: "/tmp/m"}, false},
		{"smtp needs a from address", Config{Env: "development", DatabaseURL: "x", SMTPURL: "smtp://h:587"}, false},
		{"public url must be a url", Config{Env: "development", DatabaseURL: "x", PublicURL: "attend.example.org"}, false},
		{"typo in env", Config{Env: "prod", DatabaseURL: "x"}, false},
		{"cors wildcard refused", Config{Env: "development", DatabaseURL: "x", CORSOrigins: []string{"*"}}, false},
		{"cors trailing slash refused", Config{Env: "development", DatabaseURL: "x", CORSOrigins: []string{"https://a.org/"}}, false},
		{"cors origin ok", Config{Env: "development", DatabaseURL: "x", CORSOrigins: []string{"https://a.org"}}, true},
		{"production ok", Config{Env: "production", DatabaseURL: "x", PublicURL: "https://a.org", SMTPURL: "smtp://u:p@h:587", MailFrom: "a@a.org"}, true},
		{"production ok without email", Config{Env: "production", DatabaseURL: "x", PublicURL: "https://a.org"}, true},
	}
	for _, c := range cases {
		if err := c.c.Validate(); (err == nil) != c.ok {
			t.Errorf("%s: err=%v want ok=%v", c.name, err, c.ok)
		}
	}
}

func TestQRAllowance(t *testing.T) {
	t.Setenv("IAH_DATABASE_URL", "postgres://x")
	for _, c := range []struct {
		env  string
		want time.Duration
		ok   bool
	}{{"", time.Second, true}, {"0", 0, true}, {"250", 250 * time.Millisecond, true}, {"5001", 0, false}, {"-1", 0, false}, {"1s", 0, false}} {
		t.Setenv("IAH_QR_ALLOWANCE_MS", c.env)
		got, err := Load()
		if (err == nil) != c.ok || (c.ok && got.QRAllowance != c.want) {
			t.Errorf("%q: %v %v", c.env, got.QRAllowance, err)
		}
	}
	t.Setenv("IAH_QR_ALLOWANCE_MS", "")
	t.Setenv("IAH_QR_GRACE_SLOTS", "2") // replaced: say so instead of silently ignoring it
	if _, err := Load(); err == nil {
		t.Error("old setting accepted")
	}
}
