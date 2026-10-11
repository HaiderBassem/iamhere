// Package config loads and validates runtime configuration from the environment.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
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
