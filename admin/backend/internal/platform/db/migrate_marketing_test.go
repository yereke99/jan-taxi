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

// TestMarketingMigrationRollback: migration 00016 is additive and reversible
// while no marketing data exists — down restores schema 15 exactly as before
// and up applies it again with the seeded drafts — and refuses to drop
// issued coupons once there are any.
func TestMarketingMigrationRollback(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	goose.SetBaseFS(migrations.FS)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	exists := func(sql string) bool {
		t.Helper()
		var ok bool
		if err := pool.QueryRow(ctx, sql).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		return ok
	}
	const marketingTables = `SELECT to_regclass('marketing_campaigns') IS NOT NULL`
	const promoColumn = `SELECT EXISTS (SELECT 1 FROM information_schema.columns
		WHERE table_name = 'orders' AND column_name = 'promo_discount')`

	if !exists(marketingTables) || !exists(promoColumn) {
		t.Fatal("migration 00016 not applied")
	}
	if err := goose.DownToContext(ctx, sqlDB, ".", 15); err != nil {
		t.Fatalf("down to 15: %v", err)
	}
	if exists(marketingTables) || exists(promoColumn) {
		t.Fatal("down left marketing schema behind")
	}
	if v, err := db.MigrationStatus(ctx, pool); err != nil || v != 15 {
		t.Fatalf("version after down: %d %v", v, err)
	}
	if err := goose.UpContext(ctx, sqlDB, "."); err != nil {
		t.Fatalf("up again: %v", err)
	}
	var drafts int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM marketing_campaigns WHERE status = 'draft' AND is_system`).Scan(&drafts); err != nil || drafts != 3 {
		t.Fatalf("seeded drafts after up: %d %v", drafts, err)
	}

	// Once a coupon exists the rollback refuses to destroy it.
	uid, _ := testutil.CreateUser(t, pool)
	if _, err := pool.Exec(ctx, `
		INSERT INTO marketing_coupons (campaign_id, version_id, user_id, amount, min_order_fare, categories, expires_at)
		SELECT c.id, v.id, $1, 400, 400, ARRAY['taxi'], now() + interval '72 hours'
		FROM marketing_campaigns c JOIN marketing_campaign_versions v ON v.campaign_id = c.id
		WHERE c.key = 'first_ride_coupon_400'`, uid); err != nil {
		t.Fatal(err)
	}
	err := goose.DownToContext(ctx, sqlDB, ".", 15)
	if err == nil || !strings.Contains(err.Error(), "marketing data exists") {
		t.Fatalf("down with a coupon: %v", err)
	}
	if !exists(marketingTables) {
		t.Fatal("refused down still dropped tables")
	}
}
