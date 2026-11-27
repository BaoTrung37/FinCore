package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/BaoTrung37/FinCore/internal/modules/account"
	accountdb "github.com/BaoTrung37/FinCore/internal/modules/account/db"
	"github.com/BaoTrung37/FinCore/internal/modules/auth"
	"github.com/BaoTrung37/FinCore/internal/modules/ledger"
	"github.com/BaoTrung37/FinCore/internal/modules/payment"
	"github.com/BaoTrung37/FinCore/internal/modules/transfer"
	"github.com/BaoTrung37/FinCore/internal/modules/webhook"
	"github.com/BaoTrung37/FinCore/internal/platform/config"
	"github.com/BaoTrung37/FinCore/internal/platform/database"
	"github.com/BaoTrung37/FinCore/internal/platform/httpserver"
	"github.com/BaoTrung37/FinCore/internal/platform/logger"
	"github.com/BaoTrung37/FinCore/migrations"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server exited with error", slog.Any("error", err))
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := logger.New(cfg.LogLevel, cfg.AppEnv)
	log.Info("starting fincore api", slog.String("port", cfg.HTTPPort))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	initCtx, cancelInit := context.WithTimeout(ctx, 10*time.Second)
	defer cancelInit()

	pool, err := database.NewPostgresPool(initCtx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	if cfg.AutoMigrate {
		if err := database.MigrateUp(cfg.DatabaseURL, migrations.FS); err != nil {
			return err
		}
		log.Info("database migrations applied")
	}

	tm := auth.NewTokenManager(cfg.JWTSecret, cfg.JWTExpiration)
	authRepo := auth.NewPostgresRepository(pool)
	authSvc := auth.NewService(authRepo, tm)
	authHandler := auth.NewHandler(authSvc, log)

	router := httpserver.NewRouter(log, pool)
	authHandler.RegisterRoutes(router, tm)

	ledgerSvc := ledger.NewService(pool)

	accSvc := account.NewService(accountdb.New(pool))
	accHandler := account.NewHandler(accSvc, log, ledgerSvc)
	accHandler.RegisterRoutes(router, tm)

	webhookSvc := webhook.NewService(pool)
	webhookHandler := webhook.NewHandler(webhookSvc, log)
	webhookHandler.RegisterRoutes(router, tm)

	transferSvc := transfer.NewService(pool, accSvc, ledgerSvc)
	transferSvc.SetOutboxPublisher(webhookSvc)
	transferHandler := transfer.NewHandler(transferSvc, log)
	transferHandler.RegisterRoutes(router, tm)

	paymentSvc := payment.NewService(pool, accSvc, ledgerSvc, payment.NewMockBankGateway())
	paymentSvc.SetOutboxPublisher(webhookSvc)
	paymentHandler := payment.NewHandler(paymentSvc, log, cfg.MockBankWebhookSecret)
	paymentHandler.RegisterRoutes(router, tm)

	relayWorker := webhook.NewRelayWorker(pool, log, 2*time.Second)
	go relayWorker.Start(ctx)

	srv := &http.Server{
		Addr:              ":" + cfg.HTTPPort,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("shutting down fincore api")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}
