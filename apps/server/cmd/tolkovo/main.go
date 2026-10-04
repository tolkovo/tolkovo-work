// Command tolkovo is the single server binary.
//
//	tolkovo serve [--role=all|api|realtime|worker]
//	tolkovo migrate up|down|reset|version
//	tolkovo keygen
//	tolkovo version
package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/riverqueue/river"
	"golang.org/x/sync/errgroup"

	"github.com/tolkovo/tolkovo/apps/server/internal/api"
	"github.com/tolkovo/tolkovo/apps/server/internal/auth"
	"github.com/tolkovo/tolkovo/apps/server/internal/conversations"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/config"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/crypto"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/db"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/jobs"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/logging"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/mail"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/migrate"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/outbox"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/rdb"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/storage"
	"github.com/tolkovo/tolkovo/apps/server/internal/realtime"
	"github.com/tolkovo/tolkovo/apps/server/internal/tenancy"
)

// version is set at build time: -ldflags "-X main.version=v0.1.0".
var version = "dev"

const usage = `usage:
  tolkovo serve [--role=all|api|realtime|worker]
  tolkovo migrate up|down|reset|version
  tolkovo keygen
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
	case "keygen":
		// A new TOLKOVO_MASTER_KEY (docs/02 §13.3). Printed only to stdout,
		// never logged.
		key, err := crypto.GenerateKey()
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(stdout, key)
		return err
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
	var runREST, runRealtime, runWorker bool
	switch *role {
	case "all":
		runREST, runRealtime, runWorker = true, true, true
	case "api":
		runREST = true
	case "realtime":
		runRealtime = true
	case "worker":
		runWorker = true
	default:
		return fmt.Errorf("unknown role %q", *role)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if runWorker && cfg.SystemDatabaseURL == "" {
		return fmt.Errorf("%sSYSTEM_DATABASE_URL is required for role %q", config.Prefix, *role)
	}
	log := logging.New(stderr, cfg.LogLevel, cfg.LogFormat)

	store, closeStore, err := openStorage(cfg, log)
	if err != nil {
		return err
	}
	defer closeStore()

	pool, err := db.Open(ctx, cfg.DatabaseURL.Reveal())
	if err != nil {
		return err
	}
	defer pool.Close()

	// Every role needs Redis: sessions (api, realtime), fan-out (realtime,
	// and the worker's relay publishes to it).
	redisClient, err := rdb.Open(ctx, cfg.RedisURL.Reveal())
	if err != nil {
		return err
	}
	defer func() { _ = redisClient.Close() }()

	g, gctx := errgroup.WithContext(ctx)
	if runWorker {
		sys, err := db.Open(ctx, cfg.SystemDatabaseURL.Reveal())
		if err != nil {
			return fmt.Errorf("system database: %w", err)
		}
		defer sys.Close()
		if err := startWorker(gctx, g, cfg, log, pool, sys, redisClient, realtime.NewPublisher(redisClient, "")); err != nil {
			return err
		}
	}
	if runREST || runRealtime {
		deps, err := apiDeps(ctx, cfg, log, pool, redisClient, store)
		if err != nil {
			return err
		}
		var gw *realtime.Gateway
		if runRealtime {
			gw = realtime.New(pool, redisClient, log, realtime.Options{TrustedOrigins: deps.TrustedOrigins})
			g.Go(func() error { return gw.Run(gctx) })
			deps.Realtime = gw
			deps.RealtimeOnly = !runREST
		}
		if !runREST {
			deps.Files = nil
		}
		startHTTP(gctx, g, cfg, log, deps, gw, *role)
	}
	log.Info("started", "role", *role, "version", version, "env", cfg.Env)

	err = g.Wait()
	if err != nil {
		return err
	}
	log.Info("stopped")
	return nil
}

// startWorker runs River workers and the outbox relay until ctx is done.
// The relay publishes events to the realtime fan-out (ADR-0005).
func startWorker(
	ctx context.Context, g *errgroup.Group, cfg config.Config, log *slog.Logger, pool, sys *db.DB,
	redisClient *redis.Client, pub outbox.Publisher,
) error {
	relay := outbox.NewRelay(sys.Pool(), pub, log)
	if err := relay.CheckRole(ctx); err != nil {
		return err
	}

	workers := river.NewWorkers()
	river.AddWorker(workers, outbox.NewPruneWorker(sys.Pool(), cfg.OutboxRetention))
	periodic := []*river.PeriodicJob{outbox.PrunePeriodic()}
	if err := addMailWorkers(workers, cfg, pool, redisClient, log); err != nil {
		return err
	}
	client, err := jobs.New(pool.Pool(), jobs.Options{
		Log:         log,
		Workers:     workers,
		Periodic:    periodic,
		StopTimeout: cfg.ShutdownTimeout,
	})
	if err != nil {
		return fmt.Errorf("jobs: %w", err)
	}
	// River stops on its own when its start context is cancelled; Stop
	// below waits for running jobs, bounded by SoftStopTimeout.
	if err := client.Start(ctx); err != nil {
		return fmt.Errorf("start jobs: %w", err)
	}
	g.Go(func() error { return relay.Run(ctx) })
	g.Go(func() error {
		<-ctx.Done()
		stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.ShutdownTimeout+5*time.Second)
		defer cancel()
		if err := client.Stop(stopCtx); err != nil {
			return fmt.Errorf("stop jobs: %w", err)
		}
		return nil
	})
	return nil
}

// addMailWorkers registers the auth emails (invitations, password reset,
// email confirmation) when outgoing email is configured. Without it the api
// queues no email jobs.
func addMailWorkers(workers *river.Workers, cfg config.Config, pool *db.DB, redisClient *redis.Client, log *slog.Logger) error {
	if !cfg.MailEnabled() {
		log.Warn("TOLKOVO_SMTP_HOST is not set: no email is sent; invitation links are shown to admins, password reset is off")
		return nil
	}
	sender, err := mail.NewSMTP(mail.SMTPOptions{
		Host: cfg.SMTP.Host, Port: cfg.SMTP.Port, Username: cfg.SMTP.User, Password: cfg.SMTP.Password.Reveal(),
		From: cfg.SMTP.From, TLS: cfg.SMTP.TLS, Timeout: cfg.SMTP.Timeout,
	})
	if err != nil {
		return err
	}
	return auth.AddMailWorkers(workers, auth.MailOptions{
		Tenancy: tenancy.New(pool), Redis: redisClient, Sender: sender, PanelURL: cfg.PanelBaseURL(),
	})
}

// apiDeps wires the modules the HTTP layer needs.
func apiDeps(
	ctx context.Context, cfg config.Config, log *slog.Logger, pool *db.DB, redisClient *redis.Client, store storage.Storage,
) (api.Deps, error) {
	tenants := tenancy.New(pool)
	// Insert-only: the api queues email jobs, the worker role sends them.
	inserter, err := jobs.New(pool.Pool(), jobs.Options{Log: log})
	if err != nil {
		return api.Deps{}, fmt.Errorf("jobs: %w", err)
	}
	authSvc, err := auth.New(ctx, tenants, redisClient, auth.Options{
		SessionIdleTimeout: cfg.SessionIdleTimeout,
		SessionMaxAge:      cfg.SessionMaxAge,
		HashConcurrency:    runtime.GOMAXPROCS(0),
		Jobs:               inserter,
		MailEnabled:        cfg.MailEnabled(),
		PanelURL:           cfg.PanelBaseURL(),
	})
	if err != nil {
		return api.Deps{}, err
	}
	deps := api.Deps{
		Log: log, DB: pool, Version: version,
		Redis:         pingFunc(func(ctx context.Context) error { return redisClient.Ping(ctx).Err() }),
		Tenancy:       tenants,
		Auth:          authSvc,
		Conversations: conversations.New(pool, store),
		SecureCookies: cfg.SecureCookies(),
		SessionMaxAge: cfg.SessionMaxAge,
		// The public URL is where browsers reach the api, also behind a
		// proxy that rewrites Host, so its origin is always trusted.
		TrustedOrigins: append(cfg.PublicOrigins(), cfg.TrustedOrigins...),
		TrustedProxies: cfg.TrustedProxyPrefixes(),
	}
	// Only the fs driver needs the api to carry file bytes; s3 clients go
	// straight to the bucket.
	if fsStore, ok := store.(*storage.FS); ok {
		deps.Files = fsStore
	}
	return deps, nil
}

// pingFunc adapts a function to api.Pinger.
type pingFunc func(ctx context.Context) error

func (f pingFunc) Ping(ctx context.Context) error { return f(ctx) }

// startHTTP serves the API until ctx is done, then drains connections.
// http.Server.Shutdown does not track hijacked WebSocket connections, so
// the gateway (nil without role realtime) is shut down alongside.
func startHTTP(
	ctx context.Context, g *errgroup.Group, cfg config.Config, log *slog.Logger, deps api.Deps, gw *realtime.Gateway, role string,
) {
	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           api.NewRouter(deps),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	g.Go(func() error {
		log.Info("http server listening", "addr", cfg.HTTPAddr, "role", role)
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("http server: %w", err)
		}
		return nil
	})
	g.Go(func() error {
		<-ctx.Done()
		log.Info("shutting down", "timeout", cfg.ShutdownTimeout)
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.ShutdownTimeout)
		defer cancel()
		var sg errgroup.Group
		sg.Go(func() error {
			if err := srv.Shutdown(shutdownCtx); err != nil {
				return fmt.Errorf("graceful shutdown: %w", err)
			}
			return nil
		})
		if gw != nil {
			sg.Go(func() error {
				if err := gw.Shutdown(shutdownCtx); err != nil {
					return fmt.Errorf("realtime shutdown: %w", err)
				}
				return nil
			})
		}
		return sg.Wait()
	})
}

// openStorage builds the file storage driver from the configuration
// (docs/adr/0009-file-storage.md). It makes no network calls.
func openStorage(cfg config.Config, log *slog.Logger) (storage.Storage, func(), error) {
	sc := cfg.Storage
	if sc.Driver == config.StorageS3 {
		s, err := storage.NewS3(storage.S3Options{
			Endpoint: sc.S3Endpoint, Region: sc.S3Region, Bucket: sc.S3Bucket,
			AccessKeyID: sc.S3AccessKeyID, SecretAccessKey: sc.S3SecretAccessKey.Reveal(),
			UsePathStyle: sc.S3PathStyle, TTL: sc.URLTTL,
		})
		if err != nil {
			return nil, nil, err
		}
		return s, func() {}, nil
	}
	key := []byte(sc.SigningKey.Reveal())
	if len(key) == 0 {
		// Config allows this only in development (it fails in production).
		key = make([]byte, storage.MinSigningKeyLen)
		if _, err := rand.Read(key); err != nil {
			return nil, nil, fmt.Errorf("generate storage signing key: %w", err)
		}
		log.Warn("TOLKOVO_STORAGE_SIGNING_KEY is not set: using a random key, file links stop working after restart")
	}
	s, err := storage.NewFS(storage.FSOptions{
		Root: sc.FSRoot, LinkBaseURL: strings.TrimRight(cfg.PublicURL, "/") + api.FilesPath,
		SigningKey: key, TTL: sc.URLTTL,
	})
	if err != nil {
		return nil, nil, err
	}
	return s, func() { _ = s.Close() }, nil
}

func runMigrate(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) != 1 {
		return errors.New(usage)
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	m, err := migrate.Open(ctx, cfg.MigrateDatabaseURL.Reveal())
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
