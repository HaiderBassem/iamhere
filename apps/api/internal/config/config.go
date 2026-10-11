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
	DevAuth      bool   // also accept "dev:<sub>:<name>" bearer tokens (tests, scripts); never in production
	PublicURL    string // the web app's address, for links in emails (e.g. https://attend.example.org)
	SMTPURL      string // smtp://user:pass@host:587 or smtps://user:pass@host:465; empty = no email
	MailFrom     string // From address of account emails
	MailDir      string // development: write emails to this directory instead of sending them
	TrustProxy   bool   // the API is only reachable through the reverse proxy (client IP from X-Forwarded-For)
	CheckinGrace time.Duration
	QRGraceSlots uint64
	MaxFixAge    time.Duration
	CORSOrigins  []string // exact origins allowed to call the API from a browser
	WebDir       string   // optional: serve the web app from this directory
	VisionURL    string   // vision service for strict mode's photo check (empty = off)
}

func Load() (Config, error) {
	c := Config{
		Env:          get("IAH_ENV", "development"),
		ListenAddr:   get("IAH_LISTEN", ":8080"),
		DatabaseURL:  os.Getenv("IAH_DATABASE_URL"),
		DevAuth:      os.Getenv("IAH_DEV_AUTH") == "1",
		PublicURL:    strings.TrimRight(os.Getenv("IAH_PUBLIC_URL"), "/"),
		SMTPURL:      os.Getenv("IAH_SMTP_URL"),
		MailFrom:     os.Getenv("IAH_MAIL_FROM"),
		MailDir:      os.Getenv("IAH_MAIL_DIR"),
		TrustProxy:   os.Getenv("IAH_TRUST_PROXY") == "1",
		CheckinGrace: 5 * time.Second,
		QRGraceSlots: 2,
		MaxFixAge:    30 * time.Second, // strict mode only: GPS fixes older than this go to review
		CORSOrigins:  splitList(os.Getenv("IAH_CORS_ORIGINS")),
		WebDir:       os.Getenv("IAH_WEB_DIR"),
		VisionURL:    os.Getenv("IAH_VISION_URL"),
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
	if c.PublicURL != "" && !(strings.HasPrefix(c.PublicURL, "https://") || strings.HasPrefix(c.PublicURL, "http://")) {
		return fmt.Errorf("IAH_PUBLIC_URL: %q must start with https://", c.PublicURL)
	}
	if c.SMTPURL != "" && c.MailFrom == "" {
		return errors.New("IAH_MAIL_FROM is required with IAH_SMTP_URL (e.g. \"I Am Here <no-reply@example.org>\")")
	}
	if c.Env == "production" {
		if c.DevAuth {
			return errors.New("IAH_DEV_AUTH must not be enabled in production")
		}
		if c.MailDir != "" {
			return errors.New("IAH_MAIL_DIR is for development; use IAH_SMTP_URL in production")
		}
		if !strings.HasPrefix(c.PublicURL, "https://") {
			return errors.New("IAH_PUBLIC_URL (https://…, the address of the web app) is required in production")
		}
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
