// Command tolkovo is the single server binary.
//
//	tolkovo serve [--role=all|api|realtime|worker]
//	tolkovo migrate up|down|reset|version
//	tolkovo version
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tolkovo/tolkovo/apps/server/internal/api"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/config"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/db"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/logging"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/migrate"
)

// version is set at build time: -ldflags "-X main.version=v0.1.0".
var version = "dev"

const usage = `usage:
  tolkovo serve [--role=all|api|realtime|worker]
  tolkovo migrate up|down|reset|version
  tolkovo version`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "tolkovo:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	switch args[0] {
	case "version":
		_, err := fmt.Fprintln(stdout, version)
		return err
	case "serve":
		return serve(ctx, args[1:], stderr)
	case "migrate":
		return runMigrate(ctx, args[1:], stdout)
	default:
		return fmt.Errorf("unknown command %q\n%s", args[0], usage)
	}
}

func serve(ctx context.Context, args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	role := fs.String("role", "all", "process role: all, api, realtime or worker")
	if err := fs.Parse(args); err != nil {
		return err
	}
	switch *role {
	case "all", "api":
	case "realtime", "worker":
		return fmt.Errorf("role %q is not available yet: it arrives with the realtime and jobs tasks (docs/06 R-01, F-05)", *role)
	default:
		return fmt.Errorf("unknown role %q", *role)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := logging.New(stderr, cfg.LogLevel, cfg.LogFormat)

	pool, err := db.Open(ctx, cfg.DatabaseURL.Reveal())
	if err != nil {
		return err
	}
	defer pool.Close()

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           api.NewRouter(api.Deps{Log: log, DB: pool, Version: version}),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	errCh := make(chan error, 1)
	go func() {
		log.Info("http server started", "addr", cfg.HTTPAddr, "role", *role, "version", version, "env", cfg.Env)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
	}
	log.Info("shutting down", "timeout", cfg.ShutdownTimeout)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	if err := <-errCh; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http server: %w", err)
	}
	log.Info("stopped")
	return nil
}

func runMigrate(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) != 1 {
		return errors.New(usage)
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	m, err := migrate.Open(cfg.MigrateDatabaseURL.Reveal())
	if err != nil {
		return err
	}
	defer func() { _ = m.Close() }()

	switch args[0] {
	case "up":
		n, err := m.Up(ctx)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "applied %d migration(s)\n", n)
		return err
	case "down":
		return m.Down(ctx)
	case "reset":
		return m.Reset(ctx)
	case "version":
		v, err := m.Version(ctx)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(stdout, v)
		return err
	default:
		return fmt.Errorf("unknown migrate command %q\n%s", args[0], usage)
	}
}
