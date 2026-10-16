// Package db wraps the PostgreSQL connection pool, transactions, migrations and
// error classification shared by every domain package.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/yereke99/jantaxi/admin/backend/migrations"
)

// Querier is satisfied by *pgxpool.Pool, *pgxpool.Conn and pgx.Tx, so read helpers
// can run either inside or outside a transaction.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Open creates a connection pool and verifies connectivity.
func Open(ctx context.Context, url string, maxConns int32) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	if maxConns > 0 {
		cfg.MaxConns = maxConns
	}
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.HealthCheckPeriod = 30 * time.Second
	cfg.ConnConfig.RuntimeParams["application_name"] = "jantaxi-api"
	cfg.ConnConfig.RuntimeParams["timezone"] = "UTC"
	cfg.AfterConnect = func(_ context.Context, conn *pgx.Conn) error {
		RegisterUTCTimestamps(conn)
		return nil
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

// RegisterUTCTimestamps makes conn return timestamptz values in UTC. pgx
// otherwise converts them to the process's local zone (e.g. +05:00 on a host in
// Kazakhstan) regardless of the session time zone, and API responses must be
// RFC 3339 UTC (ARCHITECTURE §10).
func RegisterUTCTimestamps(conn *pgx.Conn) {
	conn.TypeMap().RegisterType(&pgtype.Type{
		Name:  "timestamptz",
		OID:   pgtype.TimestamptzOID,
		Codec: &pgtype.TimestamptzCodec{ScanLocation: time.UTC},
	})
}

// WithTx runs fn inside a READ COMMITTED transaction. The transaction commits when
// fn returns nil and rolls back otherwise (including on panic).
func WithTx(ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx) error) (err error) {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

// Migrate applies all pending embedded migrations.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	return withGoose(func() error { return goose.UpContext(ctx, sqlDB, ".") })
}

// MigrationStatus returns the current schema version.
func MigrationStatus(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	var v int64
	err := withGoose(func() error {
		var err error
		v, err = goose.GetDBVersionContext(ctx, sqlDB)
		return err
	})
	return v, err
}

// LatestMigrationVersion is the highest version embedded in the binary.
func LatestMigrationVersion() (int64, error) {
	var v int64
	err := withGoose(func() error {
		ms, err := goose.CollectMigrations(".", 0, goose.MaxVersion)
		if err != nil {
			return err
		}
		last, err := ms.Last()
		if err != nil {
			return err
		}
		v = last.Version
		return nil
	})
	return v, err
}

func withGoose(fn func() error) error {
	goose.SetBaseFS(migrations.FS)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	return fn()
}

// --- error classification ----------------------------------------------------

// PgError returns the underlying *pgconn.PgError, if any.
func PgError(err error) *pgconn.PgError {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr
	}
	return nil
}

// IsUniqueViolation reports a 23505 error; when constraint is non-empty the
// violated constraint/index name must match.
func IsUniqueViolation(err error, constraint string) bool {
	return isCode(err, "23505", constraint)
}

// IsCheckViolation reports a 23514 error, optionally for a specific constraint.
func IsCheckViolation(err error, constraint string) bool {
	return isCode(err, "23514", constraint)
}

// IsForeignKeyViolation reports a 23503 error, optionally for a specific constraint.
func IsForeignKeyViolation(err error, constraint string) bool {
	return isCode(err, "23503", constraint)
}

// IsNoRows reports whether err is pgx.ErrNoRows or sql.ErrNoRows.
func IsNoRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows) || errors.Is(err, sql.ErrNoRows)
}

func isCode(err error, code, constraint string) bool {
	pgErr := PgError(err)
	if pgErr == nil || pgErr.Code != code {
		return false
	}
	return constraint == "" || pgErr.ConstraintName == constraint
}

// --- advisory-lock jobs -------------------------------------------------------

// LockKey derives a stable advisory-lock key from a job name.
func LockKey(name string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("jantaxi:" + name))
	return int64(h.Sum64())
}

// RunExclusive runs fn only if this process obtains the session advisory lock for
// name; it reports whether fn ran. Used so that a job runs on one API replica at a time.
func RunExclusive(ctx context.Context, pool *pgxpool.Pool, name string, fn func(ctx context.Context) error) (bool, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Release()

	key := LockKey(name)
	var ok bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&ok); err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	defer func() {
		_, _ = conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", key)
	}()
	return true, fn(ctx)
}
