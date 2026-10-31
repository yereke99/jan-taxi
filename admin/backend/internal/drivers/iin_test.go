package drivers_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/yereke99/jantaxi/admin/backend/internal/drivers"
	"github.com/yereke99/jantaxi/admin/backend/internal/drivers/driverstest"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
	"github.com/yereke99/jantaxi/admin/backend/migrations"
)

func constraintOf(err error) string {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return pg.Code + " " + pg.ConstraintName
	}
	return ""
}

// TestIINColumnRules covers migration 00013: the IIN is twelve digits kept as
// text, unique among live drivers only, and the Down step refuses to drop it.
func TestIINColumnRules(t *testing.T) {
	env := driverstest.New(t)
	ctx := context.Background()
	a := testutil.CreateDriver(t, env.Pool, testutil.DriverOptions{})
	b := testutil.CreateDriver(t, env.Pool, testutil.DriverOptions{Status: "draft"})
	c := testutil.CreateDriver(t, env.Pool, testutil.DriverOptions{Status: "draft"})
	setIIN := func(driver testutil.DriverFixture, v string) error {
		_, err := env.Pool.Exec(ctx, `UPDATE driver_profiles SET iin = $2 WHERE id = $1`, driver.DriverID, v)
		return err
	}

	for _, v := range []string{"90010130001", "9001013000170", "90010130001a", "", " 900101300017"} {
		if err := setIIN(a, v); constraintOf(err) != "23514 driver_profiles_iin_format" {
			t.Errorf("IIN %q: %v", v, err)
		}
	}
	if _, err := env.Pool.Exec(ctx, `UPDATE driver_profiles SET patronymic = $2 WHERE id = $1`, a.DriverID, strings.Repeat("ә", 61)); constraintOf(err) != "23514 driver_profiles_patronymic_length" {
		t.Errorf("long patronymic: %v", err)
	}

	// Leading zeros survive: the IIN is text, never a number.
	if err := setIIN(a, "000301500128"); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := env.Pool.QueryRow(ctx, `SELECT iin FROM driver_profiles WHERE id = $1`, a.DriverID).Scan(&stored); err != nil || stored != "000301500128" {
		t.Fatalf("stored IIN %q, %v", stored, err)
	}

	// Drafts never squat an IIN: two of them may carry a live driver's IIN.
	if err := setIIN(b, "000301500128"); err != nil {
		t.Fatalf("draft with a live driver's IIN: %v", err)
	}
	if err := setIIN(c, "000301500128"); err != nil {
		t.Fatalf("second draft with the same IIN: %v", err)
	}
	// Going live with it fails on the partial unique index, which the code
	// reports as 409 iin_taken.
	_, err := env.Pool.Exec(ctx, `UPDATE driver_profiles SET status = 'pending' WHERE id = $1`, b.DriverID)
	if !db.IsUniqueViolation(err, drivers.IINUniqueIndex) {
		t.Fatalf("second live profile with one IIN: %v", err)
	}
	// A rejected profile frees it.
	if _, err := env.Pool.Exec(ctx, `UPDATE driver_profiles SET status = 'rejected', is_online = false WHERE id = $1`, a.DriverID); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Pool.Exec(ctx, `UPDATE driver_profiles SET status = 'pending' WHERE id = $1`, b.DriverID); err != nil {
		t.Fatalf("IIN of a rejected profile: %v", err)
	}

	// The API side of the same rule: another draft saving the live IIN gets 409 iin_taken.
	d := env.DriverClient()
	if res := d.Do(http.MethodPut, pathRegistration, map[string]any{"first_name": "Ерлан", "iin": "000301500128"}); res.Code != http.StatusConflict ||
		res.ErrorCode() != "iin_taken" {
		t.Fatalf("draft saving a live IIN: %s", res)
	}

	// Down refuses to drop stored IINs; without any it rolls back and forth.
	up, down := iinMigration(t)
	tx, err := env.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, down); err == nil || !strings.Contains(err.Error(), "forward migration") {
		t.Fatalf("down with stored IINs: %v", err)
	}
	_ = tx.Rollback(ctx)
	if _, err := env.Pool.Exec(ctx, `UPDATE driver_profiles SET iin = NULL, patronymic = ''`); err != nil {
		t.Fatal(err)
	}
	tx, err = env.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, down); err != nil {
		t.Fatalf("down without IINs: %v", err)
	}
	if _, err := tx.Exec(ctx, up); err != nil {
		t.Fatalf("reapply: %v", err)
	}
}

func iinMigration(t *testing.T) (up, down string) {
	t.Helper()
	raw, err := migrations.FS.ReadFile("00013_simplified_registration.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(raw), "-- +goose Down")
	if len(parts) != 2 {
		t.Fatal("missing migration down")
	}
	// goose statement markers are SQL comments; the DO block runs as one statement.
	return strings.TrimPrefix(parts[0], "-- +goose Up"), parts[1]
}
