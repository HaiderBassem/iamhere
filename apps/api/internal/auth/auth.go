// Package auth holds the authentication primitives: password hashing, opaque
// tokens, and the development-only identity token. It never decides
// authorization; that lives in the app package.
package auth

import (
	"context"
	"errors"
	"net/url"
	"strings"
)

var ErrUnauthenticated = errors.New("auth: invalid or missing credentials")

// Identity is an identity asserted by a development token.
type Identity struct {
	Issuer  string
	Subject string
	Name    string
	Email   string
}

// Dev accepts "dev:<subject>:<full name>". Local development and tests only;
// config.Validate refuses to enable it in production.
//
// Browsers can only send Latin-1 in header values, so a web client sends the
// name percent-encoded (encodeURIComponent); it is decoded here. A name that is
// not valid percent-encoding (for example a literal "100%") is taken as-is.
type Dev struct{}

func (Dev) Verify(_ context.Context, raw string) (Identity, error) {
	parts := strings.SplitN(raw, ":", 3)
	if len(parts) != 3 || parts[0] != "dev" || parts[1] == "" || strings.TrimSpace(parts[2]) == "" {
		return Identity{}, ErrUnauthenticated
	}
	name := parts[2]
	if dec, err := url.PathUnescape(name); err == nil {
		name = dec
	}
	return Identity{Issuer: "dev", Subject: parts[1], Name: name}, nil
}
