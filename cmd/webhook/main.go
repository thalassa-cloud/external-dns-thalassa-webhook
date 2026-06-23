package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/thalassa-cloud/external-dns-thalassa-webhook/internal/server"
	"github.com/thalassa-cloud/external-dns-thalassa-webhook/internal/thalassa"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	var (
		logLevel  string
		logFormat string
		host      string
		port      int
	)

	thalassaFlags := thalassa.RegisterFlags(flag.CommandLine)

	flag.StringVar(&logLevel, "log-level", envStringDefault("LOG_LEVEL", "info"), "Log level (debug, info, warn, error)")
	flag.StringVar(&logFormat, "log-format", envStringDefault("LOG_FORMAT", "text"), "Log format (text, json)")
	flag.StringVar(&host, "host", envStringDefault("WEBHOOK_HOST", "0.0.0.0"), "Webhook server host")
	flag.IntVar(&port, "port", envIntDefaultPositive("WEBHOOK_PORT", 8888), "Webhook server port")
	flag.Parse()

	setupLogging(logLevel, logFormat)

	slog.Info("Starting external-dns-thalassa-webhook",
		"version", version,
		"commit", commit,
		"date", date,
	)

	cfg, err := thalassaFlags.Config()
	if err != nil {
		slog.Error("Invalid configuration", "error", err)
		os.Exit(1)
	}

	provider, err := thalassa.NewProvider(cfg)
	if err != nil {
		slog.Error("Failed to create Thalassa provider", "error", err)
		os.Exit(1)
	}

	srv := server.New(provider, &server.Config{
		Host:         host,
		Port:         port,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		slog.Info("Received signal, shutting down...", "signal", sig.String())
		cancel()
	}()

	if err := srv.Start(ctx); err != nil {
		slog.Error("Server error", "error", err)
		os.Exit(1)
	}

	slog.Info("Shutdown complete")
}

func setupLogging(level, format string) {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: lvl}
	var handler slog.Handler
	if format == "json" {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}
	slog.SetDefault(slog.New(handler))
}

func envStringDefault(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

func envIntDefaultPositive(key string, defaultValue int) int {
	v := os.Getenv(key)
	if v == "" {
		return defaultValue
	}
	n := 0
	for _, c := range v {
		if c < '0' || c > '9' {
			return defaultValue
		}
		n = n*10 + int(c-'0')
	}
	if n <= 0 {
		return defaultValue
	}
	return n
}
