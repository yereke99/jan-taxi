package cities

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

func TestPublicConfigVehicleClasses(t *testing.T) {
	pool := testutil.NewDB(t)
	s := NewPublicConfig(pool, testOptions)
	for _, app := range []string{"passenger", "driver", "admin"} {
		rec, body := getConfig(t, s, "?app="+app)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d", app, rec.Code)
		}
		classes := body["vehicle_classes"].([]any)
		codes := make([]string, len(classes))
		for i, c := range classes {
			codes[i] = c.(map[string]any)["code"].(string)
		}
		if fmt.Sprint(codes) != "[sedan universal minivan microbus cargo_van truck]" {
			t.Fatalf("%s classes = %v", app, codes)
		}
		minivan := classes[2].(map[string]any)
		if minivan["seats_default"] != float64(7) || minivan["seats_max"] != float64(8) || minivan["cargo_volume_l_default"] != float64(800) ||
			minivan["passenger_capable"] != true || minivan["icon"] != "minivan" || minivan["name"].(map[string]any)["kk"] != "Минивэн" ||
			minivan["examples"] == "" {
			t.Fatalf("%s minivan = %v", app, minivan)
		}
		if truck := classes[5].(map[string]any); truck["passenger_capable"] != false || truck["cargo_capable"] != true {
			t.Fatalf("%s truck = %v", app, truck)
		}
		if fmt.Sprint(body["cargo_sizes"]) != "[map[code:s volume_l:10] map[code:m volume_l:60] map[code:l volume_l:400] map[code:xl volume_l:1500]]" ||
			body["max_passenger_count"] != float64(15) {
			t.Fatalf("%s cargo sizes / passengers = %v %v", app, body["cargo_sizes"], body["max_passenger_count"])
		}
	}

	// Deactivated classes disappear; the passenger fare hint stays the sedan taxi tariff.
	if _, err := pool.Exec(context.Background(), `UPDATE vehicle_classes SET is_active = false WHERE code = 'truck'`); err != nil {
		t.Fatal(err)
	}
	_, body := getConfig(t, s, "?app=passenger")
	if n := len(body["vehicle_classes"].([]any)); n != 5 {
		t.Fatalf("active classes = %d", n)
	}
	if fare := body["fare"].(map[string]any); fare["min"] != float64(500) || fare["max"] != float64(1500) {
		t.Fatalf("fare = %v", fare)
	}
}
