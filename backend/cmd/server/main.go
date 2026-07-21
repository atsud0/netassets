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

	"netassets/internal/bootstrap"
	"netassets/internal/config"
	"netassets/internal/httpserver"
	"netassets/internal/platform/database"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("load configuration", "error", err)
		os.Exit(1)
	}

	logger := newLogger(cfg.Environment)
	slog.SetDefault(logger)

	ctx := context.Background()
	pool, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("connect database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := bootstrap.Ensure(ctx, pool, cfg, logger); err != nil {
		logger.Error("bootstrap initial data", "error", err)
		os.Exit(1)
	}

	handler := httpserver.New(cfg, pool, logger)
	server := &http.Server{
		Addr:              cfg.Address,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("server starting", "address", cfg.Address, "environment", cfg.Environment)
		serverErrors <- server.ListenAndServe()
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-signals:
		logger.Info("shutdown signal received", "signal", sig.String())
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server stopped unexpectedly", "error", err)
			os.Exit(1)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
		os.Exit(1)
	}
	logger.Info("server stopped")
}

func newLogger(environment string) *slog.Logger {
	options := &slog.HandlerOptions{Level: slog.LevelInfo}
	if environment == "development" {
		options.Level = slog.LevelDebug
		return slog.New(slog.NewTextHandler(os.Stdout, options))
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, options))
}
