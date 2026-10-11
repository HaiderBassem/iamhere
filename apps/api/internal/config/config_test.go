package config

import "testing"

func TestValidate(t *testing.T) {
	cases := []struct {
		name string
		c    Config
		ok   bool
	}{
		{"dev with dev auth", Config{Env: "development", DatabaseURL: "x", DevAuth: true}, true},
		{"missing database", Config{Env: "development", DevAuth: true}, false},
		{"no auth configured", Config{Env: "development", DatabaseURL: "x"}, false},
		{"production forbids dev auth", Config{Env: "production", DatabaseURL: "x", DevAuth: true, OIDCIssuer: "https://k", OIDCClientID: "c"}, false},
		{"production needs oidc client", Config{Env: "production", DatabaseURL: "x", OIDCIssuer: "https://k"}, false},
		{"production ok", Config{Env: "production", DatabaseURL: "x", OIDCIssuer: "https://k", OIDCClientID: "c"}, true},
	}
	for _, c := range cases {
		if err := c.c.Validate(); (err == nil) != c.ok {
			t.Errorf("%s: err=%v want ok=%v", c.name, err, c.ok)
		}
	}
}
