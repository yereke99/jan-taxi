package db_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
	"github.com/yereke99/jantaxi/admin/backend/migrations"
)

// A clean seed can be rolled back and upgraded on an existing installation;
// repeated startup does not duplicate it or replace an administrator's edits.
func TestZhalagashMigrationRoundTrip(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	goose.SetBaseFS(migrations.FS)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	if err := goose.DownToContext(ctx, sqlDB, ".", 16); err != nil {
		t.Fatalf("rollback unused defaults: %v", err)
	}
	// A pre-existing policy and village must survive the upgrade.
	if _, err := pool.Exec(ctx, `
		UPDATE app_settings SET value = '75' WHERE key = 'commission.fixed_amount';
		INSERT INTO commission_areas (name, commission_bps, shape, center_lat, center_lon, radius_m)
		VALUES ('Бұрынғы ауыл', 900, 'circle', 48.83, 58.15, 2000)`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := db.Migrate(ctx, pool); err != nil {
			t.Fatalf("upgrade/startup: %v", err)
		}
	}
	var count int
	var valid, sameFootprint, coversCentre, coversAksu bool
	if err := pool.QueryRow(ctx, `
		SELECT count(*), bool_and(ST_IsValid(a.boundary)), bool_and(ST_Equals(a.boundary, z.geom)),
		       bool_and(ST_Covers(a.boundary, c.center::geometry)),
		       bool_or(ST_Covers(a.boundary, ST_SetSRID(ST_MakePoint(64.699036, 45.051296), 4326)))
		FROM cities c JOIN service_zones z ON z.city_id = c.id
		JOIN commission_areas a ON a.name = 'Жалағаш'
		JOIN tariffs t ON t.city_id = c.id AND t.category = 'taxi' AND t.vehicle_class = 'sedan'
		WHERE c.code = 'ZHL' AND c.is_active AND z.is_active AND a.is_active AND t.is_active
		  AND t.min_fare = 400 AND t.base_fare = 400 AND t.max_fare = 1500 AND t.fare_step = 50
		  AND a.commission_bps = 500 AND a.shape = 'polygon' AND a.radius_m IS NULL
		  AND t.commission_bps = 500`).Scan(&count, &valid, &sameFootprint, &coversCentre, &coversAksu); err != nil {
		t.Fatal(err)
	}
	if count != 1 || !valid || !sameFootprint || !coversCentre || coversAksu {
		t.Fatalf("seed: count=%d valid=%v same=%v centre=%v Aksu=%v", count, valid, sameFootprint, coversCentre, coversAksu)
	}
	var preserved bool
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT value = '75'::jsonb FROM app_settings WHERE key = 'commission.fixed_amount')
		AND EXISTS (SELECT 1 FROM commission_areas WHERE name = 'Бұрынғы ауыл' AND commission_bps = 900)
		AND NOT EXISTS (SELECT 1 FROM cities WHERE name_ru = 'Аксу')`).Scan(&preserved); err != nil || !preserved {
		t.Fatalf("pre-existing settings/areas or Aksu changed: %v %v", preserved, err)
	}
	// Once edited, rollback must preserve all four rows atomically.
	if _, err := pool.Exec(ctx, `UPDATE commission_areas SET commission_bps = 600 WHERE name = 'Жалағаш'`); err != nil {
		t.Fatal(err)
	}
	err := goose.DownToContext(ctx, sqlDB, ".", 16)
	if err == nil || !strings.Contains(err.Error(), "Zhalagash defaults are configured or used") {
		t.Fatalf("rollback edited village: %v", err)
	}
	if v, err := db.MigrationStatus(ctx, pool); err != nil || v != 17 {
		t.Fatalf("version after refused rollback: %d %v", v, err)
	}
	if err := pool.QueryRow(ctx, `SELECT commission_bps FROM commission_areas WHERE name = 'Жалағаш'`).Scan(&count); err != nil || count != 600 {
		t.Fatalf("edited commission lost: %d %v", count, err)
	}
}

func TestZhalagashMigrationPreservesExistingVillage(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	goose.SetBaseFS(migrations.FS)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	if err := goose.DownToContext(ctx, sqlDB, ".", 16); err != nil {
		t.Fatal(err)
	}
	// The Russian spelling is the same village, with a manually chosen policy.
	if _, err := pool.Exec(ctx, `INSERT INTO commission_areas
		(name, commission_bps, shape, center_lat, center_lon, radius_m, is_active)
		VALUES ('Жалагаш', 700, 'circle', 45.0808963, 64.6809179, 2700, false)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := goose.DownToContext(ctx, sqlDB, ".", 16); err != nil {
		t.Fatalf("rollback seed alongside existing village: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM commission_areas
		WHERE name = 'Жалагаш' AND commission_bps = 700 AND radius_m = 2700 AND NOT is_active`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("existing village replaced/removed: %d %v", count, err)
	}
}
