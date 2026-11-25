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

	"github.com/BaoTrung37/FinCore/internal/modules/auth"
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

	pool, err := database.NewPostgresPool(ctx, cfg.DatabaseURL)
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
	authHandler := auth.NewHandler(authSvc)

	router := httpserver.NewRouter(log, pool)
	authHandler.RegisterRoutes(router, tm)

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
