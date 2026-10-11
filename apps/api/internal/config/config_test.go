package config

import "testing"

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
