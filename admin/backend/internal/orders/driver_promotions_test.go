package orders_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/marketing"
)

func TestDriverGiftProgressAndSinglePayout(t *testing.T) {
	e := newPromoEnv(t)
	ctx := context.Background()
	var campaignID uuid.UUID
	if err := e.pool.QueryRow(ctx, `INSERT INTO marketing_campaigns(key,template) VALUES('regional_driver_gift','driver_milestone_bonus') RETURNING id`).Scan(&campaignID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `INSERT INTO marketing_campaign_versions(campaign_id,version,title,rules,starts_at,ends_at,city_ids)
 VALUES($1,1,'{"kk":"Сыйлық","ru":"Подарок","en":"Gift"}',
 '{"amount":200,"rides":2,"categories":["taxi"]}',$2,$3,$4::uuid[])`, campaignID, time.Now().Add(-time.Hour), time.Now().Add(time.Hour), []uuid.UUID{e.cityID}); err != nil {
		t.Fatal(err)
	}
	e.activate("regional_driver_gift")
	driver := e.driver(5000, true)
	get := func() []marketing.DriverOffer {
		t.Helper()
		r := e.do(driver.c, http.MethodGet, "/driver/promotions", nil)
		e.expect(r, http.StatusOK, "")
		var result struct {
			Offers []marketing.DriverOffer `json:"offers"`
		}
		r.decode(t, &result)
		return result.Offers
	}
	if offers := get(); len(offers) != 1 || offers[0].Completed != 0 || offers[0].Rewarded {
		t.Fatalf("initial offer: %+v", offers)
	}
	_, passenger := e.passenger()
	e.expect(e.do(passenger, http.MethodGet, "/driver/promotions", nil), http.StatusForbidden, "forbidden")
	if _, err := e.pool.Exec(ctx, `UPDATE app_settings SET value='false'::jsonb WHERE key='marketing.awards_enabled'`); err != nil {
		t.Fatal(err)
	}
	if offers := get(); len(offers) != 0 {
		t.Fatalf("offer while awards disabled: %+v", offers)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE app_settings SET value='true'::jsonb WHERE key='marketing.awards_enabled'`); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 2; i++ {
		id, caller := e.passenger()
		e.ride(driver, id, caller, 800, nil)
		e.process()
		offers := get()
		if len(offers) != 1 || offers[0].Completed != i || offers[0].Rewarded != (i == 2) {
			t.Fatalf("after %d trips: %+v", i, offers)
		}
	}
	e.process()
	if n := e.count(`SELECT count(*) FROM marketing_grants WHERE campaign_id=$1 AND driver_id=$2 AND amount=200 AND status='credited'`, campaignID, driver.DriverID); n != 1 {
		t.Fatalf("driver grants: %d", n)
	}
	e.reconcile()
	if _, err := e.pool.Exec(ctx, `UPDATE driver_profiles SET status='suspended',is_online=false WHERE id=$1`, driver.DriverID); err != nil {
		t.Fatal(err)
	}
	e.expect(e.do(driver.c, http.MethodGet, "/driver/promotions", nil), http.StatusForbidden, "driver_not_approved")
}
