package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// mockIdP is a minimal OIDC provider: discovery document, JWKS and a signer.
type mockIdP struct {
	srv    *httptest.Server
	key    *rsa.PrivateKey
	signer jose.Signer
}

func newMockIdP(t *testing.T) *mockIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	m := &mockIdP{key: key}
	mux := http.NewServeMux()
	m.srv = httptest.NewServer(mux)
	t.Cleanup(m.srv.Close)
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"issuer": m.srv.URL, "jwks_uri": m.srv.URL + "/jwks",
			"authorization_endpoint": m.srv.URL + "/auth", "token_endpoint": m.srv.URL + "/token",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{
			{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
	})
	m.signer, err = jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: key, KeyID: "k1"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func (m *mockIdP) token(t *testing.T, signer jose.Signer, mutate func(*jwt.Claims, map[string]any)) string {
	t.Helper()
	std := jwt.Claims{Issuer: m.srv.URL, Subject: "kc-user-1", Audience: jwt.Audience{"account"},
		IssuedAt: jwt.NewNumericDate(time.Now()), Expiry: jwt.NewNumericDate(time.Now().Add(5 * time.Minute))}
	extra := map[string]any{"azp": "iamhere-web", "name": "ئاوات Kurdî", "email": "a@example.org", "preferred_username": "awat"}
	if mutate != nil {
		mutate(&std, extra)
	}
	raw, err := jwt.Signed(signer).Claims(std).Claims(extra).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestOIDCVerify(t *testing.T) {
	m := newMockIdP(t)
	v, err := NewOIDC(context.Background(), m.srv.URL, "iamhere-web")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	id, err := v.Verify(ctx, m.token(t, m.signer, nil))
	if err != nil || id.Subject != "kc-user-1" || id.Issuer != m.srv.URL || id.Name != "ئاوات Kurdî" || id.Email != "a@example.org" {
		t.Fatalf("%+v %v", id, err)
	}

	// no `name` claim: fall back to the username so an account always has a name
	id, err = v.Verify(ctx, m.token(t, m.signer, func(_ *jwt.Claims, x map[string]any) { delete(x, "name") }))
	if err != nil || id.Name != "awat" {
		t.Fatalf("%+v %v", id, err)
	}

	// client may be identified by audience instead of azp
	id, err = v.Verify(ctx, m.token(t, m.signer, func(c *jwt.Claims, x map[string]any) {
		x["azp"] = "someone-else"
		c.Audience = jwt.Audience{"iamhere-web"}
	}))
	if err != nil {
		t.Fatalf("aud match: %v", err)
	}

	otherKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	forger, _ := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: otherKey, KeyID: "k1"}}, nil)

	bad := map[string]string{
		"token for another client": m.token(t, m.signer, func(_ *jwt.Claims, x map[string]any) { x["azp"] = "other-app" }),
		"expired": m.token(t, m.signer, func(c *jwt.Claims, _ map[string]any) {
			c.Expiry = jwt.NewNumericDate(time.Now().Add(-time.Hour))
		}),
		"wrong issuer":     m.token(t, m.signer, func(c *jwt.Claims, _ map[string]any) { c.Issuer = "https://evil.example" }),
		"forged signature": m.token(t, forger, nil),
		"garbage":          "not.a.jwt",
		"empty":            "",
	}
	for name, tok := range bad {
		if _, err := v.Verify(ctx, tok); err != ErrUnauthenticated {
			t.Errorf("%s: want ErrUnauthenticated, got %v", name, err)
		}
	}
}

func TestDevVerifier(t *testing.T) {
	id, err := Dev{}.Verify(context.Background(), "dev:s1:Full: Name")
	if err != nil || id.Subject != "s1" || id.Name != "Full: Name" || id.Issuer != "dev" {
		t.Fatalf("%+v %v", id, err)
	}
	for _, bad := range []string{"", "dev:", "dev:x", "dev::n", "dev:x: ", "foo:x:y"} {
		if _, err := (Dev{}).Verify(context.Background(), bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
