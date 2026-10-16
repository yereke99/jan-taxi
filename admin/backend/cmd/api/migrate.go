package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/logging"
)

// databaseURL reads DATABASE_URL directly: the maintenance commands must work
// without the full server configuration (bot tokens, public URLs).
func databaseURL() (string, error) {
	u := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if u == "" {
		return "", errors.New("DATABASE_URL is required")
	}
	return u, nil
}

func cmdMigrate(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || (args[0] != "up" && args[0] != "status") {
		fmt.Fprintln(stderr, "usage: api migrate up|status")
		return 2
	}
	url, err := databaseURL()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if args[0] == "up" {
		log := logging.New(os.Getenv("LOG_LEVEL"), os.Getenv("LOG_FORMAT"))
		if err := migrateUp(ctx, url, log); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}

	pool, err := db.Open(ctx, url, 2)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer pool.Close()
	current, err := db.MigrationStatus(ctx, pool)
	if err != nil {
		fmt.Fprintln(stderr, "read schema version:", err)
		return 1
	}
	latest, err := db.LatestMigrationVersion()
	if err != nil {
		fmt.Fprintln(stderr, "read embedded migrations:", err)
		return 1
	}
	state := "up to date"
	if current < latest {
		state = "pending migrations"
	} else if current > latest {
		state = "database is newer than this binary"
	}
	fmt.Fprintf(stdout, "schema version %d, latest embedded %d: %s\n", current, latest, state)
	return 0
}
