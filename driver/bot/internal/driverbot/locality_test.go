package driverbot

import (
	"strings"
	"testing"
)

var kazRegion = map[string]any{"code": "KAZ", "name": map[string]any{"kk": "Қазақстан", "ru": "Казахстан", "en": "Kazakhstan"}}

func almatyCard(locality any) map[string]any {
	return map[string]any{"id": orderID, "number": 1034, "category": "taxi", "fare": 900, "currency": "KZT",
		"commission_amount": 50, "distance_m": 4200, "distance_to_pickup_m": 800, "stop_count": 2,
		"locality": locality, "tariff_region": kazRegion,
		"stops": []any{
			map[string]any{"seq": 0, "kind": "pickup", "lat": 43.2389, "lon": 76.8897, "address": "Абай даңғылы, 10"},
			map[string]any{"seq": 1, "kind": "destination", "lat": 43.2567, "lon": 76.9286, "address": "Достық даңғылы, 50"},
		}}
}

// TestLocalityInOrderTexts: the offer list and the current trip name the
// settlement of the pickup («📍 Алматы»), never the tariff region and never
// coordinates (the trip card after acceptance comes from the API's outbox).
func TestLocalityInOrderTexts(t *testing.T) {
	e := newEnv(t)
	e.approved(true)
	var res any
	e.api.Handle("GET "+tgPath+"/orders", func(map[string]any, string) (int, any) { return 200, res })

	res = map[string]any{"eligibility": map[string]any{"eligible": true, "reason": nil}, "items": []any{almatyCard("Алматы")}}
	text := msgText(e.text(t, "/orders")[0])
	if !strings.Contains(text, "<b>№1034</b> · Такси · <b>900 ₸</b>\n📍 Алматы\nҚайдан: Абай даңғылы, 10") {
		t.Fatalf("offer list %q", text)
	}
	if strings.Contains(text, "Қазақстан") || strings.Contains(text, "Ембі") {
		t.Fatalf("offer list names the tariff region: %q", text)
	}
	for _, loc := range []any{nil, "", "43.2389, 76.8897"} {
		res = map[string]any{"eligibility": map[string]any{"eligible": true, "reason": nil}, "items": []any{almatyCard(loc)}}
		text := msgText(e.text(t, "/orders")[0])
		if strings.Contains(text, "📍") || strings.Contains(text, "43.2389") {
			t.Fatalf("locality %v: %q", loc, text)
		}
	}

	summary := map[string]any{"registered": true, "status": "approved", "code": "KAZ001", "is_online": true, "phone_verified": true,
		"first_name": "Ерлан", "last_name": "Сейіт", "corrections": []any{},
		"wallet": map[string]any{"balance": 500, "reserved": 0, "available": 500, "currency": "KZT", "min_online_balance": 50},
		"active_order": map[string]any{"id": orderID, "number": 1034, "status": "accepted", "category": "taxi", "fare": 900,
			"currency": "KZT", "pickup_address": "Абай даңғылы, 10", "destination_address": "Достық даңғылы, 50",
			"locality": "Алматы", "tariff_region": kazRegion},
	}
	e.api.Handle("GET "+tgPath+"/summary", func(map[string]any, string) (int, any) { return 200, summary })
	if text := msgText(e.text(t, "/trip")[0]); !strings.Contains(text, "📍 Алматы\nҚайдан: Абай даңғылы, 10") {
		t.Fatalf("trip %q", text)
	}
}
