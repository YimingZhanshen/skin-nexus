package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"skin-nexus/server/internal/verifyproxy"
)

// runVerifyProxy is deliberately independent of config.Load, store.Open and the
// main server assembly. The optional proxy knows only its three upstream facts.
func runVerifyProxy(parent context.Context, args []string) error {
	cfg, err := verifyproxy.LoadConfig(args, processEnv())
	if err != nil {
		return err
	}
	server, err := verifyproxy.NewServer(cfg, nil)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.ListenAndServe() }()
	slog.Info("verify_proxy listening", "addr", cfg.Listen)
	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return errors.New("verify-proxy: listener failed")
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			slog.Error("verify_proxy shutdown_failed")
			return errors.New("verify-proxy: graceful shutdown failed")
		}
		return nil
	}
}

func processEnv() map[string]string {
	out := make(map[string]string)
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if ok && strings.HasPrefix(key, "SKINNEXUS_VERIFY_PROXY_") {
			out[key] = value
		}
	}
	return out
}
