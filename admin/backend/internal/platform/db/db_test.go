package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

// TestTimestamptzScansAsUTC checks that pools from db.Open return timestamptz
// values in UTC even when the process runs in a Kazakhstan time zone, so API
// responses never carry +05:00 offsets.
func TestTimestamptzScansAsUTC(t *testing.T) {
	orig := time.Local
	time.Local = time.FixedZone("Asia/Aqtobe", 5*3600)
	t.Cleanup(func() { time.Local = orig })

	pool := testutil.NewDB(t)
	ctx := context.Background()

	var ts time.Time
	var nullable *time.Time
	if err := pool.QueryRow(ctx, `SELECT TIMESTAMPTZ '2026-10-16 09:31:05+05', now()`).Scan(&ts, &nullable); err != nil {
		t.Fatal(err)
	}
	if ts.Location() != time.UTC || nullable == nil || nullable.Location() != time.UTC {
		t.Fatalf("pool scan: %v (%v), %v", ts, ts.Location(), nullable)
	}
	if want := time.Date(2026, 10, 16, 4, 31, 5, 0, time.UTC); !ts.Equal(want) {
		t.Fatalf("instant changed: %v, want %v", ts, want)
	}

	// Transactions use connections from the same pool and must behave the same.
	err := db.WithTx(ctx, pool, func(tx pgx.Tx) error {
		var inTx time.Time
		if err := tx.QueryRow(ctx, `SELECT now()`).Scan(&inTx); err != nil {
			return err
		}
		if inTx.Location() != time.UTC {
			t.Errorf("tx scan location: %v", inTx.Location())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
