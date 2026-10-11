// Package config loads and validates runtime configuration from the environment.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Env          string // "development" | "production"
	ListenAddr   string
	DatabaseURL  string
	OIDCIssuer   string // Keycloak realm URL
	OIDCClientID string // expected audience / authorized party
	DevAuth      bool   // accept "dev:<sub>:<name>" bearer tokens; never in production
	CheckinGrace time.Duration
	QRGraceSlots uint64
	MaxFixAge    time.Duration
	CORSOrigins  []string // exact origins allowed to call the API from a browser
}

func Load() (Config, error) {
	c := Config{
		Env:          get("IAH_ENV", "development"),
		ListenAddr:   get("IAH_LISTEN", ":8080"),
		DatabaseURL:  os.Getenv("IAH_DATABASE_URL"),
		OIDCIssuer:   os.Getenv("IAH_OIDC_ISSUER"),
		OIDCClientID: os.Getenv("IAH_OIDC_CLIENT_ID"),
		DevAuth:      os.Getenv("IAH_DEV_AUTH") == "1",
		CheckinGrace: 5 * time.Second,
		QRGraceSlots: 2,
		MaxFixAge:    60 * time.Second,
		CORSOrigins:  splitList(os.Getenv("IAH_CORS_ORIGINS")),
	}
	if v := os.Getenv("IAH_QR_GRACE_SLOTS"); v != "" {
		n, err := strconv.ParseUint(v, 10, 8)
		if err != nil {
			return c, fmt.Errorf("IAH_QR_GRACE_SLOTS: %w", err)
		}
		c.QRGraceSlots = n
	}
	return c, c.Validate()
}

func (c Config) Validate() error {
	// A typo such as "prod" must not silently disable the production checks.
	if c.Env != "development" && c.Env != "production" {
		return fmt.Errorf("IAH_ENV must be \"development\" or \"production\", got %q", c.Env)
	}
	for _, o := range c.CORSOrigins {
		if o == "*" || !(strings.HasPrefix(o, "https://") || strings.HasPrefix(o, "http://")) || strings.HasSuffix(o, "/") {
			return fmt.Errorf("IAH_CORS_ORIGINS: %q must be an exact origin such as https://app.example.org", o)
		}
	}
	if c.DatabaseURL == "" {
		return errors.New("IAH_DATABASE_URL is required")
	}
	if c.Env == "production" {
		if c.DevAuth {
			return errors.New("IAH_DEV_AUTH must not be enabled in production")
		}
		if c.OIDCIssuer == "" || c.OIDCClientID == "" {
			return errors.New("IAH_OIDC_ISSUER and IAH_OIDC_CLIENT_ID are required in production")
		}
	}
	if !c.DevAuth && c.OIDCIssuer == "" {
		return errors.New("configure IAH_OIDC_ISSUER, or IAH_DEV_AUTH=1 for local development")
	}
	return nil
}

func get(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func splitList(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
