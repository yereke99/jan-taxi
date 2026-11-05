package marketing_test

import (
	"context"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/marketing"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

func TestCampaignAnnouncementOnceAndOnlyOutsideMiniApp(t *testing.T) {
	for _, scheduled := range []bool{false, true} {
		name := "activate"
		if scheduled {
			name = "schedule"
		}
		t.Run(name, func(t *testing.T) {
			e := newAdminEnv(t)
			ctx := context.Background()
			inactive, inactiveTG := testutil.CreateUser(t, e.pool)
			active, activeTG := testutil.CreateUser(t, e.pool)
			optedOut, optedOutTG := testutil.CreateUser(t, e.pool)
			if err := marketing.SetMarketingOptOut(ctx, e.pool, optedOut, true); err != nil {
				t.Fatal(err)
			}
			if _, err := e.pool.Exec(ctx, `INSERT INTO marketing_app_presence (user_id,app,active) VALUES($1,'passenger',true)`, active); err != nil {
				t.Fatal(err)
			}
			base := "/marketing/campaigns/" + e.campaignID("referral_300_once")
			if code, body := e.do(auth.RoleSuperAdmin, http.MethodPost, base+"/versions", map[string]any{
				"rules":       map[string]any{"reward_amount": 200, "categories": []string{"taxi"}, "attribution_window_minutes": 60, "unlock": "immediate", "min_ride_seconds": 60},
				"change_note": "Open the invitation promotion"}); code != 200 {
				t.Fatalf("version: %d %v", code, body)
			}
			launch := map[string]any{"action": name, "reason": "Approved campaign launch", "confirm": true, "expected_version": 2}
			if scheduled {
				launch["scheduled_at"] = time.Now().Add(time.Hour)
			}
			if code, body := e.do(auth.RoleSuperAdmin, http.MethodPost, base+"/transition", launch); code != 200 {
				t.Fatalf("launch: %d %v", code, body)
			}
			if scheduled {
				if _, err := e.pool.Exec(ctx, `UPDATE marketing_campaigns SET scheduled_at=now()-interval '1 second' WHERE key='referral_300_once'`); err != nil {
					t.Fatal(err)
				}
				svc := marketing.New(e.pool, marketing.Options{Log: slog.New(slog.DiscardHandler)})
				errs := make(chan error, 2)
				for range 2 {
					go func() { errs <- svc.ActivateDue(ctx) }()
				}
				for range 2 {
					if err := <-errs; err != nil {
						t.Fatal(err)
					}
				}
				if err := svc.ActivateDue(ctx); err != nil {
					t.Fatal(err)
				}
			}
			sender := &fakeSender{sent: map[int64]int{}, fail: map[int64]error{}}
			worker := marketing.NewBroadcastWorker(e.pool, sender, marketing.WorkerOptions{PassengerMiniAppURL: "https://jan-taxi.test/passenger/", Log: slog.New(slog.DiscardHandler)})
			if _, err := worker.RunOnce(ctx); err != nil {
				t.Fatal(err)
			}
			if sender.sent[inactiveTG] != 1 || sender.sent[activeTG] != 0 || sender.sent[optedOutTG] != 0 {
				t.Fatalf("sent: %+v", sender.sent)
			}
			var messages, audits int
			if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM marketing_broadcasts WHERE is_automatic`).Scan(&messages); err != nil || messages != 1 {
				t.Fatalf("announcements: %d %v", messages, err)
			}
			if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM admin_audit_logs WHERE action='marketing.broadcast.automatic'`).Scan(&audits); err != nil || audits != 1 {
				t.Fatalf("announcement audit: %d %v", audits, err)
			}
			for _, action := range []string{"pause", "resume"} {
				if code, body := e.do(auth.RoleSuperAdmin, http.MethodPost, base+"/transition", map[string]any{"action": action, "reason": "Resume the same campaign", "confirm": true, "expected_version": 2}); code != 200 {
					t.Fatalf("%s: %d %v", action, code, body)
				}
			}
			if _, err := worker.RunOnce(ctx); err != nil {
				t.Fatal(err)
			}
			if sender.sent[inactiveTG] != 1 {
				t.Fatal("resuming sent another announcement")
			}
			var status string
			if err := e.pool.QueryRow(ctx, `SELECT status FROM marketing_broadcast_recipients WHERE user_id=$1 AND NOT is_test`, inactive).Scan(&status); err != nil || status != "sent" {
				t.Fatalf("delivery status %s %v", status, err)
			}
		})
	}
}

// Eligibility is checked when the queued message is sent: the person may have
// moved, or the operator may have stopped the campaign since activation.
func TestCampaignAnnouncementRechecksRegionAndCampaign(t *testing.T) {
	for _, action := range []string{"active", "pause", "end"} {
		t.Run(action, func(t *testing.T) {
			e := newAdminEnv(t)
			ctx := context.Background()
			inside, insideTG := testutil.CreateUser(t, e.pool)
			outside, outsideTG := testutil.CreateUser(t, e.pool)
			unknown, unknownTG := testutil.CreateUser(t, e.pool)
			zhalagash := testutil.CityID(t, e.pool, "ZHL")
			embi := testutil.CityID(t, e.pool, "EMB")
			// Both known people initially qualify. One moves away before send.
			if _, err := e.pool.Exec(ctx, `INSERT INTO marketing_app_presence(user_id,app,active,city_id,city_seen_at)
 VALUES($1,'passenger',false,$3,now()),($2,'passenger',false,$3,now())`, inside, outside, zhalagash); err != nil {
				t.Fatal(err)
			}
			base := "/marketing/campaigns/" + e.campaignID("referral_300_once")
			if code, body := e.do(auth.RoleSuperAdmin, http.MethodPost, base+"/versions", map[string]any{
				"rules":    map[string]any{"reward_amount": 200, "categories": []string{"taxi"}, "attribution_window_minutes": 60, "unlock": "immediate", "min_ride_seconds": 60},
				"city_ids": []uuid.UUID{zhalagash},
			}); code != http.StatusOK {
				t.Fatalf("version: %d %v", code, body)
			}
			if code, body := e.do(auth.RoleSuperAdmin, http.MethodPost, base+"/transition", map[string]any{
				"action": "activate", "reason": "Approved regional launch", "confirm": true, "expected_version": 2,
			}); code != http.StatusOK {
				t.Fatalf("activate: %d %v", code, body)
			}
			if _, err := e.pool.Exec(ctx, `UPDATE marketing_app_presence SET city_id=$2,city_seen_at=now() WHERE user_id=$1`, outside, embi); err != nil {
				t.Fatal(err)
			}
			if action != "active" {
				if code, body := e.do(auth.RoleSuperAdmin, http.MethodPost, base+"/transition", map[string]any{
					"action": action, "reason": "Stop the queued promotion", "confirm": true, "expected_version": 2,
				}); code != http.StatusOK {
					t.Fatalf("%s: %d %v", action, code, body)
				}
			}
			sender := &fakeSender{sent: map[int64]int{}, fail: map[int64]error{}}
			worker := marketing.NewBroadcastWorker(e.pool, sender, marketing.WorkerOptions{
				PassengerMiniAppURL: "https://jan-taxi.test/passenger/", Log: slog.New(slog.DiscardHandler),
			})
			if _, err := worker.RunOnce(ctx); err != nil {
				t.Fatal(err)
			}
			want := 0
			if action == "active" {
				want = 1
			}
			if sender.sent[insideTG] != want || sender.sent[outsideTG] != 0 || sender.sent[unknownTG] != 0 {
				t.Fatalf("regional deliveries after %s: %+v", action, sender.sent)
			}
			for _, id := range []uuid.UUID{outside, unknown} {
				var status string
				if err := e.pool.QueryRow(ctx, `SELECT status FROM marketing_broadcast_recipients WHERE user_id=$1 AND NOT is_test`, id).Scan(&status); err != nil || status != "suppressed" {
					t.Fatalf("outside/unknown recipient: %s %v", status, err)
				}
			}
		})
	}
}

func TestCampaignRegionValidationAndVersionHistory(t *testing.T) {
	e := newAdminEnv(t)
	ctx := context.Background()
	embi := testutil.CityID(t, e.pool, "EMB")
	base := "/marketing/campaigns/" + e.campaignID("first_ride_coupon_400")
	for _, ids := range [][]uuid.UUID{{uuid.New()}, {embi, embi}, {testutil.CityID(t, e.pool, "KAZ")}} {
		if code, _ := e.do(auth.RoleSuperAdmin, http.MethodPost, base+"/versions", map[string]any{"city_ids": ids}); code != 422 {
			t.Fatalf("invalid region accepted: %d", code)
		}
	}
	if code, body := e.do(auth.RoleSuperAdmin, http.MethodPost, base+"/versions", map[string]any{"city_ids": []uuid.UUID{embi}}); code != 200 {
		t.Fatalf("region version: %d %v", code, body)
	}
	if code, body := e.do(auth.RoleSuperAdmin, http.MethodPost, base+"/versions", map[string]any{"city_ids": []uuid.UUID{}}); code != 200 {
		t.Fatalf("all regions: %d %v", code, body)
	}
	var current, scoped, original int
	if err := e.pool.QueryRow(ctx, `SELECT
 (SELECT cardinality(city_ids) FROM marketing_campaign_versions v JOIN marketing_campaigns c ON c.id=v.campaign_id WHERE c.key='first_ride_coupon_400' AND v.version=c.current_version),
 (SELECT cardinality(city_ids) FROM marketing_campaign_versions v JOIN marketing_campaigns c ON c.id=v.campaign_id WHERE c.key='first_ride_coupon_400' AND v.version=2),
 (SELECT cardinality(city_ids) FROM marketing_campaign_versions v JOIN marketing_campaigns c ON c.id=v.campaign_id WHERE c.key='first_ride_coupon_400' AND v.version=1)`).Scan(&current, &scoped, &original); err != nil || current != 0 || scoped != 1 || original != 0 {
		t.Fatalf("regions history %d/%d/%d: %v", current, scoped, original, err)
	}
	if code, _ := e.do(auth.RoleMarketingManager, http.MethodGet, "/marketing/regions", nil); code != 200 {
		t.Fatalf("marketing manager region list: %d", code)
	}
}
