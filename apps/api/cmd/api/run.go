package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/iamhere/api/internal/app"
	"github.com/iamhere/api/internal/config"
	"github.com/iamhere/api/internal/db"
	"github.com/iamhere/api/internal/httpapi"
	"github.com/iamhere/api/internal/mail"
	"github.com/iamhere/api/internal/vision"
)

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := openPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}

	svc, err := newService(pool, cfg, log)
	if err != nil {
		return err
	}
	if cfg.VisionURL == "" {
		log.Warn("IAH_VISION_URL not set: strict mode's photo check is off (strict check-ins go to review)")
	}
	if !svc.MailEnabled() {
		log.Warn("no email configured (IAH_SMTP_URL): password reset by email is off; use `api reset-link <email>`")
	}
	if cfg.DevAuth {
		log.Warn("DEV AUTH ENABLED: do not expose this instance")
	}
	go expireLoop(ctx, svc, log)

	return serve(ctx, cfg, httpapi.New(svc, log, pool.Ping, httpapi.Options{
		CORSOrigins: cfg.CORSOrigins, WebDir: cfg.WebDir, DevAuth: cfg.DevAuth, TrustProxy: cfg.TrustProxy,
	}), log)
}

func newService(pool *pgxpool.Pool, cfg config.Config, log *slog.Logger) (*app.Service, error) {
	var sender mail.Sender = mail.Off{}
	switch {
	case cfg.SMTPURL != "":
		m, err := mail.NewSMTP(cfg.SMTPURL, cfg.MailFrom)
		if err != nil {
			return nil, err
		}
		sender = m
	case cfg.MailDir != "":
		sender = mail.Dir{Path: cfg.MailDir, From: "I Am Here <no-reply@localhost>"}
	}
	public := cfg.PublicURL
	if public == "" && cfg.Env == "development" {
		public = "http://localhost" + cfg.ListenAddr
	}
	return app.New(pool, app.Options{
		QRGraceSlots: cfg.QRGraceSlots,
		CheckinGrace: cfg.CheckinGrace,
		MaxFixAge:    cfg.MaxFixAge,
		Vision:       vision.New(cfg.VisionURL),
		Mail:         sender,
		PublicURL:    public,
		Log:          log,
	}), nil
}

func openPool(ctx context.Context, url string) (*pgxpool.Pool, error) {
	pcfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	pcfg.MaxConns = 30 // bounded: protects Postgres from check-in bursts
	pcfg.MaxConnLifetime = 30 * time.Minute
	return pgxpool.NewWithConfig(ctx, pcfg)
}

// expireLoop persists the 'expired' state; reads already derive it from ends_at.
func expireLoop(ctx context.Context, svc *app.Service, log *slog.Logger) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := svc.ExpireSessions(ctx); err != nil && ctx.Err() == nil {
				log.Error("expire sessions", "err", err.Error())
			}
			if _, err := svc.PurgeSessionPhotos(ctx); err != nil && ctx.Err() == nil {
				log.Error("purge session photos", "err", err.Error())
			}
			if err := svc.PurgeAuth(ctx); err != nil && ctx.Err() == nil {
				log.Error("purge expired sign-ins", "err", err.Error())
			}
			if err := svc.PurgeLearned(ctx); err != nil && ctx.Err() == nil {
				log.Error("purge learned views", "err", err.Error())
			}
		}
	}
}

func serve(ctx context.Context, cfg config.Config, h http.Handler, log *slog.Logger) error {
	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    16 << 10,
		// no WriteTimeout: the SSE endpoint is long-lived and bounds itself
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("listening", "addr", cfg.ListenAddr, "env", cfg.Env)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// resetLink prints a one-time password reset link for an account: the way to
// help someone back in when the server has no email service.
func resetLink(log *slog.Logger, email string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx := context.Background()
	pool, err := openPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}
	svc, err := newService(pool, cfg, log)
	if err != nil {
		return err
	}
	link, err := svc.ResetLink(ctx, email)
	if err != nil {
		return err
	}
	fmt.Println(link)
	fmt.Fprintln(os.Stderr, "Send this link to its owner. It works once, for one hour.")
	return nil
}
