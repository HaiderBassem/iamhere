// Package auth verifies bearer tokens and yields an external Identity.
// It never decides authorization; that lives in the app package.
package auth

import (
	"context"
	"errors"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
)

var ErrUnauthenticated = errors.New("auth: invalid or missing credentials")

// Identity is the stable external identity. Email is optional and unverified
// here: accounts must work without email or phone.
type Identity struct {
	Issuer  string
	Subject string
	Name    string
	Email   string
}

type Verifier interface {
	Verify(ctx context.Context, rawToken string) (Identity, error)
}

// OIDC validates Keycloak (or any OIDC) access/ID tokens: signature via JWKS,
// issuer, expiry, and that the token was issued to our client.
type OIDC struct {
	v        *oidc.IDTokenVerifier
	issuer   string
	clientID string
}

func NewOIDC(ctx context.Context, issuer, clientID string) (*OIDC, error) {
	p, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, err
	}
	// Keycloak access tokens carry the client in `azp`, not always in `aud`,
	// so audience is checked manually below.
	v := p.Verifier(&oidc.Config{SkipClientIDCheck: true})
	return &OIDC{v: v, issuer: issuer, clientID: clientID}, nil
}

func (o *OIDC) Verify(ctx context.Context, raw string) (Identity, error) {
	tok, err := o.v.Verify(ctx, raw)
	if err != nil {
		return Identity{}, ErrUnauthenticated
	}
	var c struct {
		Name     string `json:"name"`
		Username string `json:"preferred_username"`
		Email    string `json:"email"`
		AZP      string `json:"azp"`
	}
	if err := tok.Claims(&c); err != nil {
		return Identity{}, ErrUnauthenticated
	}
	if c.AZP != o.clientID && !contains(tok.Audience, o.clientID) {
		return Identity{}, ErrUnauthenticated
	}
	name := strings.TrimSpace(c.Name)
	if name == "" {
		name = strings.TrimSpace(c.Username)
	}
	return Identity{Issuer: tok.Issuer, Subject: tok.Subject, Name: name, Email: c.Email}, nil
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// Dev accepts "dev:<subject>:<full name>". Local development and tests only;
// config.Validate refuses to enable it in production.
type Dev struct{}

func (Dev) Verify(_ context.Context, raw string) (Identity, error) {
	parts := strings.SplitN(raw, ":", 3)
	if len(parts) != 3 || parts[0] != "dev" || parts[1] == "" || strings.TrimSpace(parts[2]) == "" {
		return Identity{}, ErrUnauthenticated
	}
	return Identity{Issuer: "dev", Subject: parts[1], Name: parts[2]}, nil
}
