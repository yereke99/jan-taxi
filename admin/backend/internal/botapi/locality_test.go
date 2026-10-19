package botapi

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

// TestBotOrderPlace: the order summaries the bots read carry the locality of
// the pickup and the tariff region (here Embi, priced by EMB, and the same
// order moved to the nationwide region).
func TestBotOrderPlace(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	d := testutil.CreateDriver(t, e.pool, testutil.DriverOptions{Status: "approved", Online: true, Balance: 1000})
	pid, ptg := testutil.CreateUser(t, e.pool)
	oid := insertOrder(t, e.pool, pid, &d.DriverID, "accepted", 800, time.Now())

	check := func(name string, o map[string]any, locality any, region, regionKK string) {
		t.Helper()
		r, _ := o["tariff_region"].(map[string]any)
		n, _ := r["name"].(map[string]any)
		if o["locality"] != locality || r["code"] != region || n["kk"] != regionKK {
			t.Errorf("%s: locality %v, tariff_region %v", name, o["locality"], o["tariff_region"])
		}
	}
	_, b := e.call(t, "driver", http.MethodGet, "/driver/"+itoa(d.TelegramID)+"/summary", nil)
	check("summary without locality", b["active_order"].(map[string]any), nil, "EMB", "Ембі")

	if _, err := e.pool.Exec(ctx, `UPDATE orders SET locality = 'Алматы',
		city_id = (SELECT id FROM cities WHERE code = 'KAZ') WHERE id = $1`, oid); err != nil {
		t.Fatal(err)
	}
	_, b = e.call(t, "driver", http.MethodGet, "/driver/"+itoa(d.TelegramID)+"/summary", nil)
	check("driver summary", b["active_order"].(map[string]any), "Алматы", "KAZ", "Қазақстан")
	_, b = e.call(t, "passenger", http.MethodGet, "/passenger/"+itoa(ptg)+"/orders", nil)
	items, _ := b["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("passenger orders: %v", b)
	}
	check("passenger orders", items[0].(map[string]any), "Алматы", "KAZ", "Қазақстан")
}
