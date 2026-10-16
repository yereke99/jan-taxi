package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/yereke99/jantaxi/admin/backend/internal/app"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/config"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/logging"
)

func cmdServe(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	migrate := fs.Bool("migrate", true, "apply pending database migrations before serving")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	log := logging.New(cfg.LogLevel, cfg.LogFormat)
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		stop() // a second signal terminates immediately
	}()

	log.Info("starting jantaxi api", slog.String("version", version()), slog.Any("config", cfg))
	if *migrate {
		if err := migrateUp(ctx, cfg.DatabaseURL, log); err != nil {
			log.Error("migrations failed", slog.Any("err", err))
			return 1
		}
	}
	a, err := app.New(ctx, cfg, log)
	if err != nil {
		log.Error("startup failed", slog.Any("err", err))
		return 1
	}
	defer a.Close()
	if err := a.Run(ctx); err != nil {
		log.Error("server stopped with an error", slog.Any("err", err))
		return 1
	}
	return 0
}

// migrateUp applies pending migrations while holding a session advisory lock,
// so replicas starting together do not run goose concurrently.
func migrateUp(ctx context.Context, databaseURL string, log *slog.Logger) error {
	pool, err := db.Open(ctx, databaseURL, 4)
	if err != nil {
		return err
	}
	defer pool.Close()

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	key := db.LockKey("schema_migrate")
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, key); err != nil {
		return fmt.Errorf("migration lock: %w", err)
	}
	defer func() { _, _ = conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, key) }()

	before, err := db.MigrationStatus(ctx, pool)
	if err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if err := db.Migrate(ctx, pool); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	after, err := db.MigrationStatus(ctx, pool)
	if err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if after != before {
		log.Info("database migrated", slog.Int64("from", before), slog.Int64("to", after))
	} else {
		log.Info("database schema is up to date", slog.Int64("version", after))
	}
	return nil
}
