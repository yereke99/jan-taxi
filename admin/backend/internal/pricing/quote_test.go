package pricing

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

func TestActiveTariffSeed(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	cityID := testutil.CityID(t, pool, "EMB")
	for _, cat := range Categories {
		tr, err := ActiveTariff(ctx, pool, cityID, cat, "sedan")
		if err != nil {
			t.Fatalf("%s: %v", cat, err)
		}
		if tr.Category != cat || tr.Version != 1 || !tr.IsActive || tr.MinFare != 500 || tr.MaxFare != 1500 ||
			tr.FareStep != 50 || tr.BaseFare != 500 || tr.CommissionBps != 1000 || tr.CreatedBy != nil || len(tr.ExtraFees) != 0 {
			t.Fatalf("%s seed tariff = %+v", cat, tr)
		}
		got, err := GetTariff(ctx, pool, tr.ID)
		if err != nil || got.ID != tr.ID {
			t.Fatalf("GetTariff: %v %+v", err, got)
		}
	}
	if _, err := ActiveTariff(ctx, pool, uuid.New(), CategoryTaxi, "sedan"); !errors.Is(err, ErrNoActiveTariff) {
		t.Fatalf("unknown city: %v", err)
	}
	if _, err := GetTariff(ctx, pool, uuid.New()); !errors.Is(err, ErrTariffNotFound) {
		t.Fatalf("unknown tariff: %v", err)
	}
	all, err := ListTariffs(ctx, pool, TariffFilter{CityID: &cityID, VehicleClass: "sedan"})
	if err != nil || len(all) != 3 || all[0].Category != CategoryTaxi || all[1].Category != CategoryParcel || all[2].Category != CategoryOther {
		t.Fatalf("ListTariffs = %v %+v", err, all)
	}
}

func createTestQuote(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID) StoredQuote {
	t.Helper()
	ctx := context.Background()
	cityID := testutil.CityID(t, pool, "EMB")
	tr, err := ActiveTariff(ctx, pool, cityID, CategoryTaxi, "sedan")
	if err != nil {
		t.Fatal(err)
	}
	geom := json.RawMessage(`{"type":"LineString","coordinates":[[58.1442,48.8264],[58.1504,48.8291],[58.1418,48.8247]]}`)
	sq, err := CreateQuote(ctx, pool, NewQuote{
		UserID: userID, CityID: cityID, Tariff: tr,
		Stops: []QuoteStop{
			{Lat: 48.82640001, Lon: 58.1442, Address: "Жиенқали Тілепбергенов көшесі, 12", AddressDetails: "2-кіреберіс"},
			{Lat: 48.8291, Lon: 58.1504, Address: "Теміржол вокзалы"},
			{Lat: 48.8247, Lon: 58.1418, Address: "Орталық базар"},
		},
		DistanceM: 2500, DurationS: 400,
		Legs:     []Leg{{DistanceM: 1200, DurationS: 180}, {DistanceM: 1300, DurationS: 220}},
		Geometry: geom, BBox: [4]float64{58.1418, 48.8247, 58.1504, 48.8291},
		Fare: Compute(tr, 2500, 400, 3),
	})
	if err != nil {
		t.Fatalf("CreateQuote: %v", err)
	}
	return sq
}

func load(t *testing.T, pool *pgxpool.Pool, quoteID, userID uuid.UUID) (StoredQuote, error) {
	t.Helper()
	var sq StoredQuote
	err := inTx(t, pool, func(tx pgx.Tx) error {
		var err error
		sq, err = LoadQuoteForUpdate(context.Background(), tx, quoteID, userID)
		return err
	})
	return sq, err
}

func TestQuoteLifecycle(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	userID, _ := testutil.CreateUser(t, pool)
	otherID, _ := testutil.CreateUser(t, pool)

	created := createTestQuote(t, pool, userID)
	if created.ID == uuid.Nil || created.StopsHash == "" || created.CommissionBps != 1000 || created.Category != CategoryTaxi {
		t.Fatalf("created = %+v", created)
	}
	if d := time.Until(created.ExpiresAt); d < 9*time.Minute || d > 11*time.Minute {
		t.Fatalf("expires in %v", d)
	}

	got, err := load(t, pool, created.ID, userID)
	if err != nil {
		t.Fatalf("LoadQuoteForUpdate: %v", err)
	}
	if got.Stops[0].Lat != 48.8264 || got.Stops[0].AddressDetails != "2-кіреберіс" || got.Stops[1].Address != "Теміржол вокзалы" ||
		got.Stops[1].AddressDetails != "" || got.Stops[2].Address != "Орталық базар" || len(got.Stops) != 3 {
		t.Fatalf("stops = %+v", got.Stops)
	}
	if got.StopsHash != created.StopsHash || got.StopsHash != StopsHash(got.Stops) {
		t.Fatalf("hash %s / %s", got.StopsHash, created.StopsHash)
	}
	if len(got.Legs) != 2 || got.Legs[1] != (Leg{1300, 220}) || got.DistanceM != 2500 || got.DurationS != 400 {
		t.Fatalf("legs = %+v", got)
	}
	if got.BBox != created.BBox || got.TariffID != created.TariffID || got.UserID != userID {
		t.Fatalf("bbox/tariff = %+v", got)
	}
	var geom map[string]any
	if err := json.Unmarshal(got.Geometry, &geom); err != nil || geom["type"] != "LineString" {
		t.Fatalf("geometry = %s", got.Geometry)
	}
	if got.RecommendedFare != 500 || got.MinFare != 500 || got.MaxFare != 1500 || got.FareStep != 50 || got.UsedAt != nil {
		t.Fatalf("fares = %+v", got)
	}

	if _, err := load(t, pool, created.ID, otherID); !errors.Is(err, ErrQuoteNotFound) {
		t.Fatalf("other user: %v", err)
	}
	if _, err := load(t, pool, uuid.New(), userID); !errors.Is(err, ErrQuoteNotFound) {
		t.Fatalf("unknown quote: %v", err)
	}

	// Used once, then refused.
	commit := func(fn func(tx pgx.Tx) error) error {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if err := fn(tx); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if err := commit(func(tx pgx.Tx) error {
		if _, err := LoadQuoteForUpdate(ctx, tx, created.ID, userID); err != nil {
			return err
		}
		return MarkQuoteUsed(ctx, tx, created.ID)
	}); err != nil {
		t.Fatalf("use quote: %v", err)
	}
	if _, err := load(t, pool, created.ID, userID); !errors.Is(err, ErrQuoteExpired) {
		t.Fatalf("used quote: %v", err)
	}
	if err := commit(func(tx pgx.Tx) error { return MarkQuoteUsed(ctx, tx, created.ID) }); !errors.Is(err, ErrQuoteExpired) {
		t.Fatalf("second MarkQuoteUsed: %v", err)
	}

	// Expiry is decided by the database clock.
	fresh := createTestQuote(t, pool, userID)
	if _, err := pool.Exec(ctx, `UPDATE fare_quotes SET expires_at = now() - interval '1 second' WHERE id = $1`, fresh.ID); err != nil {
		t.Fatal(err)
	}
	_, err = load(t, pool, fresh.ID, userID)
	if he := httpxError(t, err); he.Status != 422 || he.Code != "quote_expired" {
		t.Fatalf("expired: %v", err)
	}
}

func TestLoadQuoteLocksRow(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	userID, _ := testutil.CreateUser(t, pool)
	q := createTestQuote(t, pool, userID)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := LoadQuoteForUpdate(ctx, tx, q.ID, userID); err != nil {
		t.Fatal(err)
	}
	// A second transaction cannot take the same row while the first holds it.
	tx2, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx2.Rollback(ctx)
	var n int
	err = tx2.QueryRow(ctx, `SELECT count(*) FROM (SELECT id FROM fare_quotes WHERE id = $1 FOR UPDATE NOWAIT) s`, q.ID).Scan(&n)
	if err == nil {
		t.Fatal("row was not locked")
	}
}

func TestCleanupQuotes(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	userID, _ := testutil.CreateUser(t, pool)

	old := createTestQuote(t, pool, userID)
	recent := createTestQuote(t, pool, userID)
	used := createTestQuote(t, pool, userID)
	live := createTestQuote(t, pool, userID)
	for _, u := range []struct {
		id  uuid.UUID
		sql string
	}{
		{old.ID, `UPDATE fare_quotes SET expires_at = now() - interval '25 hours' WHERE id = $1`},
		{recent.ID, `UPDATE fare_quotes SET expires_at = now() - interval '23 hours' WHERE id = $1`},
		{used.ID, `UPDATE fare_quotes SET expires_at = now() - interval '48 hours', used_at = now() - interval '48 hours' WHERE id = $1`},
	} {
		if _, err := pool.Exec(ctx, u.sql, u.id); err != nil {
			t.Fatal(err)
		}
	}
	n, err := CleanupQuotes(ctx, pool, 24*time.Hour)
	if err != nil || n != 1 {
		t.Fatalf("CleanupQuotes = %d, %v", n, err)
	}
	var left []uuid.UUID
	rows, err := pool.Query(ctx, `SELECT id FROM fare_quotes ORDER BY created_at`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		left = append(left, id)
	}
	rows.Close()
	if len(left) != 3 || left[0] == old.ID {
		t.Fatalf("left = %v (old %s, recent %s, used %s, live %s)", left, old.ID, recent.ID, used.ID, live.ID)
	}
	if n, err := CleanupQuotes(ctx, pool, 24*time.Hour); err != nil || n != 0 {
		t.Fatalf("second run = %d, %v", n, err)
	}
}
