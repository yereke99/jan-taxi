package moderation

import (
	"context"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/drivers"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

// Two drivers whose phone (or plate) is the same are approved at the same
// moment: the unique keys of welcome_bonus_grants let exactly one of them
// have the bonus.
func TestConcurrentGrantsSharingPhoneOrPlate(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	var adminID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO admin_users (email, full_name, password_hash, role)
		VALUES ('mod@jantaxi.test', 'Mod', 'x', 'driver_moderator') RETURNING id`).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	admin := auth.AdminPrincipal{AdminID: adminID, Role: auth.RoleDriverModerator}
	newDriver := func(phone string) drivers.Profile {
		t.Helper()
		d := testutil.CreateDriver(t, pool, testutil.DriverOptions{})
		// Draft profiles are outside the live-phone uniqueness index, so two
		// of them can carry one number, as on a re-registration race.
		if _, err := pool.Exec(ctx, `UPDATE driver_profiles SET status = 'draft', is_online = false, phone_e164 = $2 WHERE id = $1`,
			d.DriverID, phone); err != nil {
			t.Fatal(err)
		}
		p, err := drivers.GetProfile(ctx, pool, d.DriverID)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	race := func(ps []drivers.Profile, plates []string) int64 {
		t.Helper()
		var (
			wg    sync.WaitGroup
			mu    sync.Mutex
			total int64
		)
		start := make(chan struct{})
		for i := range ps {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				r := httptest.NewRequest("POST", "/", nil)
				err := db.WithTx(ctx, pool, func(tx pgx.Tx) error {
					got, err := grantWelcomeBonus(auth.WithAdmin(ctx, admin), tx, r, admin, ps[i], plates[i])
					mu.Lock()
					total += got
					mu.Unlock()
					return err
				})
				if err != nil {
					t.Errorf("grant: %v", err)
				}
			}(i)
		}
		close(start)
		wg.Wait()
		return total
	}
	if got := race([]drivers.Profile{newDriver("+77015550401"), newDriver("+77015550401")}, []string{"401DDD04", "402DDD04"}); got != 500 {
		t.Fatalf("same phone: %d credited", got)
	}
	if got := race([]drivers.Profile{newDriver("+77015550403"), newDriver("+77015550404")}, []string{"403DDD04", "403DDD04"}); got != 500 {
		t.Fatalf("same plate: %d credited", got)
	}
	var grants, credits int
	_ = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM welcome_bonus_grants), (SELECT count(*) FROM wallet_ledger WHERE entry_type = 'bonus_credit')`).
		Scan(&grants, &credits)
	if grants != 2 || credits != 2 {
		t.Fatalf("grants %d credits %d", grants, credits)
	}
}
