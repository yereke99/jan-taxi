// Package stops holds the rules for an order's route points (ARCHITECTURE §5):
// 2–5 points, seq 0 is the pickup, the last one is the destination and the ones
// in between are intermediate stops that the driver completes strictly in seq
// order while the trip is in progress.
package stops

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/textutil"
)

// Stop kinds (order_stops.kind).
const (
	KindPickup       = "pickup"
	KindIntermediate = "intermediate"
	KindDestination  = "destination"
)

// Limits on the number of points per order. The database allows seq 0..4, so
// MaxPoints is a hard ceiling; the runtime limit (settings routing.max_points)
// may be lower.
const (
	MinPoints = 2
	MaxPoints = 5
)

// Text limits (order_stops columns).
const (
	MaxAddressLen = 200
	MaxDetailsLen = textutil.MaxFreeText
)

// KindFor returns the kind of the stop at seq in a route of count points.
func KindFor(seq, count int) string {
	switch {
	case seq == 0:
		return KindPickup
	case seq == count-1:
		return KindDestination
	default:
		return KindIntermediate
	}
}

// ValidateCount checks the number of points against [MinPoints, max] (max is
// clamped to MaxPoints). It returns 422 validation_failed {field: too_few|too_many}.
func ValidateCount(field string, n, max int) error {
	if max <= 0 || max > MaxPoints {
		max = MaxPoints
	}
	switch {
	case n < MinPoints:
		return httpx.Validation(map[string]string{field: "too_few"})
	case n > max:
		return httpx.Validation(map[string]string{field: "too_many"})
	}
	return nil
}

// CleanAddress normalises a display address: free-text cleaning, at most
// MaxAddressLen code points, required.
func CleanAddress(s string) (string, error) {
	return textutil.CleanRequired(s, MaxAddressLen)
}

// CleanDetails normalises optional address details (entrance, floor,
// landmark); blank input yields nil.
func CleanDetails(s *string) (*string, error) {
	return textutil.CleanOptional(s, MaxDetailsLen)
}

// FieldName returns the validation field key for a stop attribute, e.g.
// FieldName("stops", 1, "address") == "stops.1.address".
func FieldName(field string, index int, attr string) string {
	return field + "." + strconv.Itoa(index) + "." + attr
}

// Stop is a stored order stop.
type Stop struct {
	Mode           string     `json:"mode"`
	ID             uuid.UUID  `json:"id"`
	Seq            int        `json:"seq"`
	Kind           string     `json:"kind"`
	Lat            float64    `json:"lat"`
	Lon            float64    `json:"lon"`
	Address        string     `json:"address"`
	AddressDetails *string    `json:"address_details"`
	CompletedAt    *time.Time `json:"completed_at"`
}

// Summary is the compact stop shape used in lists and cards (StopSummary).
type Summary struct {
	Mode    string  `json:"mode"`
	Seq     int     `json:"seq"`
	Kind    string  `json:"kind"`
	Lat     float64 `json:"lat"`
	Lon     float64 `json:"lon"`
	Address string  `json:"address"`
}

// Summary returns the compact form of s.
func (s Stop) Summary() Summary {
	return Summary{Mode: s.Mode, Seq: s.Seq, Kind: s.Kind, Lat: s.Lat, Lon: s.Lon, Address: s.Address}
}

// DisplayAddress returns the address shown in notifications; a stop without a
// text address is shown by its coordinates.
func DisplayAddress(address string, lat, lon float64) string {
	if a := textutil.Clean(address); a != "" {
		return a
	}
	return fmt.Sprintf("%.6f, %.6f", lat, lon)
}

// Check verifies the shape of an ordered route: at least MinPoints, at most
// MaxPoints, consecutive seq from 0 and kinds matching their positions.
func Check(list []Stop) error {
	if len(list) < MinPoints || len(list) > MaxPoints {
		return fmt.Errorf("stops: %d points, want %d..%d", len(list), MinPoints, MaxPoints)
	}
	for i, s := range list {
		if s.Seq != i {
			return fmt.Errorf("stops: point %d has seq %d", i, s.Seq)
		}
		if want := KindFor(i, len(list)); s.Kind != want {
			return fmt.Errorf("stops: point %d is %s, want %s", i, s.Kind, want)
		}
	}
	return nil
}

// PendingIntermediate returns the intermediate stops not completed yet, in seq order.
func PendingIntermediate(list []Stop) []Stop {
	var out []Stop
	for _, s := range list {
		if s.Kind == KindIntermediate && s.CompletedAt == nil {
			out = append(out, s)
		}
	}
	return out
}

// NextIntermediate returns the intermediate stop the driver must complete
// next, or nil when every intermediate stop is done.
func NextIntermediate(list []Stop) *Stop {
	for i := range list {
		if list[i].Kind == KindIntermediate && list[i].CompletedAt == nil {
			return &list[i]
		}
	}
	return nil
}

// NextPending returns the first stop (in seq order) that is not completed, or
// nil when all are completed.
func NextPending(list []Stop) *Stop {
	for i := range list {
		if list[i].CompletedAt == nil {
			return &list[i]
		}
	}
	return nil
}

// Find returns the stop with id, or nil.
func Find(list []Stop, id uuid.UUID) *Stop {
	for i := range list {
		if list[i].ID == id {
			return &list[i]
		}
	}
	return nil
}

// Pickup returns the first stop, or nil for an empty list.
func Pickup(list []Stop) *Stop {
	if len(list) == 0 {
		return nil
	}
	return &list[0]
}

// Destination returns the last stop, or nil for an empty list.
func Destination(list []Stop) *Stop {
	if len(list) == 0 {
		return nil
	}
	return &list[len(list)-1]
}

// Manual coordinates never enter the wire contract as numeric placeholders.
func (s Stop) MarshalJSON() ([]byte, error) {
	type plain Stop
	if s.Mode == "" {
		s.Mode = "geocoded"
	}
	if s.Mode != "manual" {
		return json.Marshal(plain(s))
	}
	return marshalPoint(plain(s), s.Mode)
}
func (s Summary) MarshalJSON() ([]byte, error) {
	type plain Summary
	if s.Mode == "" {
		s.Mode = "geocoded"
	}
	if s.Mode != "manual" {
		return json.Marshal(plain(s))
	}
	return marshalPoint(plain(s), s.Mode)
}
func marshalPoint(v any, mode string) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err = json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if mode == "" {
		mode = "geocoded"
	}
	m["mode"] = mode
	if mode == "manual" {
		m["lat"], m["lon"] = nil, nil
	}
	return json.Marshal(m)
}
func HasManual(list []Stop) bool {
	for _, s := range list {
		if s.Mode == "manual" {
			return true
		}
	}
	return false
}
