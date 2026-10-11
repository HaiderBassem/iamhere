package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/iamhere/api/internal/app"
	"github.com/iamhere/api/internal/auth"
	"github.com/iamhere/api/internal/config"
	"github.com/iamhere/api/internal/db"
	"github.com/iamhere/api/internal/httpapi"
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

	ver, err := newVerifier(ctx, cfg, log)
	if err != nil {
		return err
	}
	svc := app.New(pool, app.Options{
		QRGraceSlots: cfg.QRGraceSlots,
		CheckinGrace: cfg.CheckinGrace,
		MaxFixAge:    cfg.MaxFixAge,
	})
	go expireLoop(ctx, svc, log)

	return serve(ctx, cfg, httpapi.New(svc, ver, log, pool.Ping, httpapi.Options{CORSOrigins: cfg.CORSOrigins, Public: publicConfig(cfg), WebDir: cfg.WebDir}), log)
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

func newVerifier(ctx context.Context, cfg config.Config, log *slog.Logger) (auth.Verifier, error) {
	if cfg.OIDCIssuer == "" {
		log.Warn("DEV AUTH ENABLED: do not expose this instance")
		return auth.Dev{}, nil
	}
	return auth.NewOIDC(ctx, cfg.OIDCIssuer, cfg.OIDCClientID)
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

func publicConfig(cfg config.Config) httpapi.PublicConfig {
	if cfg.OIDCIssuer == "" {
		return httpapi.PublicConfig{Auth: "dev"}
	}
	return httpapi.PublicConfig{Auth: "oidc", Issuer: cfg.OIDCIssuer, ClientID: cfg.OIDCClientID}
}
