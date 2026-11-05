package orders_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/marketing"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

func configureRegionalLaunch(t *testing.T, e *promoEnv, city uuid.UUID) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(), `INSERT INTO marketing_campaign_versions (campaign_id,version,title,description,rules,city_ids)
 SELECT c.id,2,v.title,v.description,CASE WHEN c.template='referral_first_ride'
 THEN '{"reward_amount":200,"categories":["taxi"],"attribution_window_minutes":60,"unlock":"immediate","min_ride_seconds":60}'::jsonb ELSE v.rules END,$1::uuid[]
 FROM marketing_campaigns c JOIN marketing_campaign_versions v ON v.campaign_id=c.id AND v.version=c.current_version
 WHERE c.key IN ('first_ride_coupon_400','referral_300_once')`, []uuid.UUID{city}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(context.Background(), `UPDATE marketing_campaigns SET current_version=2 WHERE key IN ('first_ride_coupon_400','referral_300_once')`); err != nil {
		t.Fatal(err)
	}
	e.activate("first_ride_coupon_400")
	e.activate("referral_300_once")
}

func TestRegionalReferralChecksTheCompletedTrip(t *testing.T) {
	for _, outside := range []bool{false, true} {
		name := "inside"
		if outside {
			name = "outside"
		}
		t.Run(name, func(t *testing.T) {
			e := newPromoEnv(t)
			ctx := context.Background()
			configureRegionalLaunch(t, e, e.cityID)
			owner, oc := e.passenger()
			e.expect(e.do(oc, http.MethodPost, "/promotions/presence", map[string]any{"active": true, "position": map[string]float64{"lat": 48.8264, "lon": 58.1442}}), http.StatusNoContent, "")
			if p := e.promotions(oc); p.Referral.State != "unlocked" {
				t.Fatalf("owner region: %+v", p.Referral)
			}
			response := e.do(oc, http.MethodGet, "/promotions/referral", nil)
			e.expect(response, http.StatusOK, "")
			var view marketing.ReferralView
			response.decode(t, &view)
			friend, fc := e.passenger()
			if res, err := e.mk.CaptureReferral(ctx, friend, "ref_"+view.Code, "bot_start"); err != nil || !res.Attributed {
				t.Fatalf("capture: %+v %v", res, err)
			}
			driver := e.driver(5000, true)
			order := e.ride(driver, friend, fc, 800, nil)
			if outside {
				// The physical pickup, not the selected tariff, owns the region.
				if _, err := e.pool.Exec(ctx, `UPDATE orders SET pickup_lat=45.0808963,pickup_lon=64.6809179 WHERE id=$1`, order.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				// A nationwide fallback tariff must not exclude an in-town trip.
				if _, err := e.pool.Exec(ctx, `UPDATE orders SET city_id=$2 WHERE id=$1`, order.ID, testutil.CityID(t, e.pool, "KAZ")); err != nil {
					t.Fatal(err)
				}
			}
			e.process()
			balance, err := marketing.GetBonus(ctx, e.pool, owner)
			if err != nil {
				t.Fatal(err)
			}
			want := int64(200)
			if outside {
				want = 0
			}
			if balance.Balance != want {
				t.Fatalf("bonus %d, want %d", balance.Balance, want)
			}
			var status string
			var reason *string
			if err := e.pool.QueryRow(ctx, `SELECT status,reason FROM referral_attributions WHERE invitee_user_id=$1`, friend).Scan(&status, &reason); err != nil {
				t.Fatal(err)
			}
			if outside && (status != "rejected" || reason == nil || *reason != "region_not_eligible") {
				t.Fatalf("outside result: %s %v", status, reason)
			}
			if !outside && status != "rewarded" {
				t.Fatalf("inside result: %s", status)
			}
			coupons := e.count(`SELECT count(*) FROM marketing_coupons WHERE user_id=$1`, friend)
			if outside && coupons != 0 || !outside && coupons != 1 {
				t.Fatalf("regional coupons: %d", coupons)
			}
			e.reconcile()
		})
	}
}

func TestGiftRegionUsesTheTownBoundary(t *testing.T) {
	e := newPromoEnv(t)
	configureRegionalLaunch(t, e, testutil.CityID(t, e.pool, "ZHL"))
	_, passenger := e.passenger()
	for _, tc := range []struct {
		lon  float64
		want string
	}{
		{64.6809179, "unlocked"},
		// Nearby land is in the dispatch radius, but outside the town polygon.
		{64.7809179, "unavailable"},
	} {
		e.expect(e.do(passenger, http.MethodPost, "/promotions/presence", map[string]any{"active": true, "position": map[string]float64{"lat": 45.0808963, "lon": tc.lon}}), http.StatusNoContent, "")
		if p := e.promotions(passenger); p.Referral.State != tc.want {
			t.Fatalf("longitude %f: %+v", tc.lon, p.Referral)
		}
	}
}
