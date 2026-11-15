package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"server-man/internal/app"
	"server-man/internal/auth"
	"server-man/internal/buildinfo"
	"server-man/internal/config"
	"server-man/internal/core"
	"server-man/internal/httpapi"
	"server-man/internal/mcp"
	"server-man/internal/modules/monitor"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	version := flag.Bool("version", false, "print version and exit")
	configPath := flag.String("config", "", "path to the unified server-man YAML configuration")
	transport := flag.String("transport", "http", "transport: http (UI, APIs and MCP) or stdio (MCP only)")
	flag.Parse()
	if *version {
		fmt.Println(buildinfo.Version)
		return nil
	}
	if *transport != "http" && *transport != "stdio" {
		return fmt.Errorf("unknown transport %q", *transport)
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	jobs, err := core.NewJobs(ctx, cfg.StateDir)
	if err != nil {
		return err
	}
	defer jobs.Close()
	monitoring := monitor.New(ctx, time.Duration(cfg.SampleIntervalSeconds)*time.Second)
	defer monitoring.Close()
	registry, err := app.Registry(cfg, jobs, monitoring, core.Runner{})
	if err != nil {
		return err
	}
	if *transport == "stdio" {
		return mcp.New(registry).Stdio(ctx, os.Stdin, os.Stdout)
	}
	authentication, err := auth.Load(cfg.TokenFile, cfg.PublicOrigin)
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
