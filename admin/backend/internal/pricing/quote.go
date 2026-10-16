package pricing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

// QuoteTTL is how long a quote can be turned into an order.
const QuoteTTL = 10 * time.Minute

// ErrQuoteExpired is returned for quotes that expired or were already used.
var ErrQuoteExpired = httpx.NewError(http.StatusUnprocessableEntity, "quote_expired", "quote expired")

// ErrQuoteNotFound is returned for unknown quotes and quotes of another user.
var ErrQuoteNotFound = httpx.NewError(http.StatusNotFound, "not_found", "quote not found")

// QuoteStop is one stop as stored in a quote, in route order (pickup first).
type QuoteStop struct {
	Lat            float64 `json:"lat"`
	Lon            float64 `json:"lon"`
	Address        string  `json:"address"`
	AddressDetails string  `json:"address_details"` // "" when not given
}

// Leg is the route between two consecutive stops.
type Leg struct {
	DistanceM int `json:"distance_m"`
	DurationS int `json:"duration_s"`
}

// StoredQuote is a persisted fare quote (table fare_quotes).
type StoredQuote struct {
	ID, UserID, CityID, TariffID uuid.UUID
	Category                     string
	Stops                        []QuoteStop
	StopsHash                    string
	DistanceM, DurationS         int
	Legs                         []Leg
	Geometry                     json.RawMessage // GeoJSON LineString
	BBox                         [4]float64      // [min_lon, min_lat, max_lon, max_lat]
	RecommendedFare              int
	MinFare, MaxFare, FareStep   int
	CommissionBps                int
	ExpiresAt                    time.Time
	UsedAt                       *time.Time
}

// FareOptions returns the fares the passenger may choose for this quote.
func (q StoredQuote) FareOptions() Fare {
	return Fare{Recommended: q.RecommendedFare, Min: q.MinFare, Max: q.MaxFare, Step: q.FareStep,
		Options: Options(q.MinFare, q.MaxFare, q.FareStep)}
}

// ValidateFare checks that fare is one of the quote's options: inside
// [MinFare, MaxFare] and on the tariff's fare_step grid. Otherwise it returns
// 422 fare_not_allowed with details {min, max, step}.
func (q StoredQuote) ValidateFare(fare int) error {
	if q.FareStep > 0 && fare >= q.MinFare && fare <= q.MaxFare && fare%q.FareStep == 0 {
		return nil
	}
	return httpx.NewError(http.StatusUnprocessableEntity, "fare_not_allowed", "fare not allowed").
		WithDetails(map[string]any{"min": q.MinFare, "max": q.MaxFare, "step": q.FareStep})
}

// Commission returns the commission for fare at the quote's rate.
func (q StoredQuote) Commission(fare int) int { return Commission(fare, q.CommissionBps) }

// StopsHash fingerprints the stops: coordinates rounded to 6 decimals plus
// address and details, in order, hashed with SHA-256 (hex).
func StopsHash(stops []QuoteStop) string {
	h := sha256.New()
	for i, s := range stops {
		fmt.Fprintf(h, "%d|%s|%s|%s|%s\n", i,
			strconv.FormatFloat(round6(s.Lat), 'f', 6, 64), strconv.FormatFloat(round6(s.Lon), 'f', 6, 64),
			strconv.Quote(s.Address), strconv.Quote(s.AddressDetails))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// NewQuote is the input of CreateQuote.
type NewQuote struct {
	UserID    uuid.UUID
	CityID    uuid.UUID
	Tariff    Tariff
	Stops     []QuoteStop
	DistanceM int
	DurationS int
	Legs      []Leg
	Geometry  json.RawMessage
	BBox      [4]float64
	Fare      Fare
	TTL       time.Duration // QuoteTTL when zero
}

// storedStop is the JSON shape of fare_quotes.stops.
type storedStop struct {
	Lat            float64 `json:"lat"`
	Lon            float64 `json:"lon"`
	Address        string  `json:"address"`
	AddressDetails *string `json:"address_details"`
}

// CreateQuote stores a quote priced with in.Tariff. Coordinates are rounded to
// 6 decimals; expires_at is computed by the database clock.
func CreateQuote(ctx context.Context, q db.Querier, in NewQuote) (StoredQuote, error) {
	if len(in.Stops) < 2 || len(in.Legs) != len(in.Stops)-1 {
		return StoredQuote{}, fmt.Errorf("create quote: %d stops and %d legs", len(in.Stops), len(in.Legs))
	}
	ttl := in.TTL
	if ttl <= 0 {
		ttl = QuoteTTL
	}
	stops := make([]QuoteStop, len(in.Stops))
	raw := make([]storedStop, len(in.Stops))
	for i, s := range in.Stops {
		s.Lat, s.Lon = round6(s.Lat), round6(s.Lon)
		stops[i] = s
		raw[i] = storedStop{Lat: s.Lat, Lon: s.Lon, Address: s.Address}
		if s.AddressDetails != "" {
			d := s.AddressDetails
			raw[i].AddressDetails = &d
		}
	}
	stopsJSON, err := json.Marshal(raw)
	if err != nil {
		return StoredQuote{}, fmt.Errorf("encode stops: %w", err)
	}
	legsJSON, err := json.Marshal(in.Legs)
	if err != nil {
		return StoredQuote{}, fmt.Errorf("encode legs: %w", err)
	}
	bboxJSON, err := json.Marshal(in.BBox)
	if err != nil {
		return StoredQuote{}, fmt.Errorf("encode bbox: %w", err)
	}
	sq := StoredQuote{
		UserID: in.UserID, CityID: in.CityID, TariffID: in.Tariff.ID, Category: in.Tariff.Category,
		Stops: stops, StopsHash: StopsHash(stops), DistanceM: in.DistanceM, DurationS: in.DurationS,
		Legs: append([]Leg(nil), in.Legs...), Geometry: in.Geometry, BBox: in.BBox,
		RecommendedFare: in.Fare.Recommended, MinFare: in.Fare.Min, MaxFare: in.Fare.Max, FareStep: in.Fare.Step,
		CommissionBps: in.Tariff.CommissionBps,
	}
	err = q.QueryRow(ctx, `
		INSERT INTO fare_quotes (user_id, city_id, tariff_id, category, stops, stops_hash, distance_m, duration_s,
		                         legs, geometry, bbox, recommended_fare, min_fare, max_fare, fare_step, commission_bps,
		                         expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16,
		        now() + make_interval(secs => $17))
		RETURNING id, expires_at`,
		sq.UserID, sq.CityID, sq.TariffID, sq.Category, stopsJSON, sq.StopsHash, sq.DistanceM, sq.DurationS,
		legsJSON, []byte(sq.Geometry), bboxJSON, sq.RecommendedFare, sq.MinFare, sq.MaxFare, sq.FareStep,
		sq.CommissionBps, ttl.Seconds(),
	).Scan(&sq.ID, &sq.ExpiresAt)
	if err != nil {
		return StoredQuote{}, fmt.Errorf("insert quote: %w", err)
	}
	sq.ExpiresAt = sq.ExpiresAt.UTC()
	return sq, nil
}

// LoadQuoteForUpdate locks and returns the user's quote inside tx. It returns
// 404 not_found for unknown quotes or quotes of another user, and 422
// quote_expired when the quote expired or was already used.
func LoadQuoteForUpdate(ctx context.Context, tx pgx.Tx, quoteID, userID uuid.UUID) (StoredQuote, error) {
	var (
		sq                        StoredQuote
		stopsJSON, legsJSON, bbox []byte
		geometry                  []byte
		expired                   bool
	)
	err := tx.QueryRow(ctx, `
		SELECT id, user_id, city_id, tariff_id, category, stops, stops_hash, distance_m, duration_s, legs, geometry,
		       bbox, recommended_fare, min_fare, max_fare, fare_step, commission_bps, expires_at, used_at,
		       expires_at <= now()
		FROM fare_quotes
		WHERE id = $1 AND user_id = $2
		FOR UPDATE`, quoteID, userID).Scan(
		&sq.ID, &sq.UserID, &sq.CityID, &sq.TariffID, &sq.Category, &stopsJSON, &sq.StopsHash, &sq.DistanceM,
		&sq.DurationS, &legsJSON, &geometry, &bbox, &sq.RecommendedFare, &sq.MinFare, &sq.MaxFare, &sq.FareStep,
		&sq.CommissionBps, &sq.ExpiresAt, &sq.UsedAt, &expired)
	if db.IsNoRows(err) {
		return StoredQuote{}, ErrQuoteNotFound
	}
	if err != nil {
		return StoredQuote{}, fmt.Errorf("load quote: %w", err)
	}
	if expired || sq.UsedAt != nil {
		return StoredQuote{}, ErrQuoteExpired
	}
	sq.ExpiresAt = sq.ExpiresAt.UTC()

	var raw []storedStop
	if err := json.Unmarshal(stopsJSON, &raw); err != nil {
		return StoredQuote{}, fmt.Errorf("decode quote stops: %w", err)
	}
	sq.Stops = make([]QuoteStop, len(raw))
	for i, s := range raw {
		sq.Stops[i] = QuoteStop{Lat: s.Lat, Lon: s.Lon, Address: s.Address}
		if s.AddressDetails != nil {
			sq.Stops[i].AddressDetails = *s.AddressDetails
		}
	}
	if err := json.Unmarshal(legsJSON, &sq.Legs); err != nil {
		return StoredQuote{}, fmt.Errorf("decode quote legs: %w", err)
	}
	if err := json.Unmarshal(bbox, &sq.BBox); err != nil {
		return StoredQuote{}, fmt.Errorf("decode quote bbox: %w", err)
	}
	sq.Geometry = json.RawMessage(geometry)
	return sq, nil
}

// MarkQuoteUsed marks the quote as consumed by an order. A quote can be used
// once; a second call returns 422 quote_expired.
func MarkQuoteUsed(ctx context.Context, tx pgx.Tx, quoteID uuid.UUID) error {
	tag, err := tx.Exec(ctx, `UPDATE fare_quotes SET used_at = now() WHERE id = $1 AND used_at IS NULL`, quoteID)
	if err != nil {
		return fmt.Errorf("mark quote used: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrQuoteExpired
	}
	return nil
}

// cleanupBatch bounds how many rows one DELETE statement removes.
const cleanupBatch = 5000

// CleanupQuotes deletes unused quotes that expired more than olderThan ago and
// are not referenced by any order. It returns the number of deleted quotes.
func CleanupQuotes(ctx context.Context, q db.Querier, olderThan time.Duration) (int64, error) {
	var total int64
	for {
		tag, err := q.Exec(ctx, `
			DELETE FROM fare_quotes
			WHERE id IN (
			    SELECT f.id FROM fare_quotes f
			    WHERE f.used_at IS NULL
			      AND f.expires_at < now() - make_interval(secs => $1)
			      AND NOT EXISTS (SELECT 1 FROM orders o WHERE o.quote_id = f.id)
			    LIMIT $2)`, olderThan.Seconds(), cleanupBatch)
		if err != nil {
			return total, fmt.Errorf("cleanup quotes: %w", err)
		}
		total += tag.RowsAffected()
		if tag.RowsAffected() < cleanupBatch {
			return total, nil
		}
		if err := ctx.Err(); err != nil {
			return total, err
		}
	}
}

func round6(v float64) float64 { return math.Round(v*1e6) / 1e6 }
