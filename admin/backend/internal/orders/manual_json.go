package orders

import (
	"encoding/json"

	"github.com/yereke99/jantaxi/admin/backend/internal/stops"
)

func unknownMetrics(v any, unknown bool) ([]byte, error) {
	b, e := json.Marshal(v)
	if e != nil {
		return nil, e
	}
	if !unknown {
		return b, nil
	}
	var m map[string]json.RawMessage
	if e = json.Unmarshal(b, &m); e != nil {
		return nil, e
	}
	if _, ok := m["distance_m"]; ok {
		m["distance_m"] = json.RawMessage(`null`)
	}
	if _, ok := m["duration_s"]; ok {
		m["duration_s"] = json.RawMessage(`null`)
	}
	return json.Marshal(m)
}
func (o Order) MarshalJSON() ([]byte, error) {
	type plain Order
	return unknownMetrics(plain(o), stops.HasManual(o.Stops))
}
func (o DriverOrderDetail) MarshalJSON() ([]byte, error) {
	type plain DriverOrderDetail
	return unknownMetrics(plain(o), stops.HasManual(o.Stops))
}
func (o AdminOrder) MarshalJSON() ([]byte, error) {
	type plain AdminOrder
	return unknownMetrics(plain(o), stops.HasManual(o.Stops))
}
func (o OrderSummary) MarshalJSON() ([]byte, error) {
	type plain OrderSummary
	return unknownMetrics(plain(o), o.Unknown || o.Destination.Mode == "manual")
}
func (o DriverOrderSummary) MarshalJSON() ([]byte, error) {
	type plain DriverOrderSummary
	return unknownMetrics(plain(o), o.Unknown || o.Destination.Mode == "manual")
}
func (o AdminOrderListItem) MarshalJSON() ([]byte, error) {
	type plain AdminOrderListItem
	return unknownMetrics(plain(o), o.Unknown || o.Destination.Mode == "manual")
}
func (o RouteSnapshot) MarshalJSON() ([]byte, error) {
	type plain RouteSnapshot
	return unknownMetrics(plain(o), o.Unknown)
}
