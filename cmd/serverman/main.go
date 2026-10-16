package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"serverman/internal/app"
	"serverman/internal/auth"
	"serverman/internal/config"
	"serverman/internal/core"
	"serverman/internal/httpapi"
	"serverman/internal/modules/monitor"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "", "path to a JSON configuration file")
	flag.Parse()
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	authentication, err := auth.Load(cfg.TokenFile, cfg.PublicOrigin)
	if err != nil {
		return err
	}
	jobs, err := core.NewJobs(ctx, cfg.StateDir)
	if err != nil {
		return err
	}
	defer jobs.Close()
	monitoring := monitor.New(ctx, time.Duration(cfg.SampleIntervalSeconds)*time.Second)
	defer monitoring.Close()
	registry, err := app.Registry(cfg.AllowWrites, jobs, monitoring)
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr: cfg.Listen, Handler: httpapi.Handler(authentication, registry),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 30 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 32 << 10,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			slog.Error("graceful shutdown failed", "error", err)
			_ = server.Close()
		}
	}()
	slog.Info("server listening", "address", cfg.Listen, "tokenFile", cfg.TokenFile, "allowWrites", cfg.AllowWrites)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
