// Package pricing owns tariffs (immutable, versioned per city and category),
// fare computation, fare quotes and the commission formula (ARCHITECTURE §6).
// All amounts are whole tenge (KZT); no floating point is used for money.
package pricing

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/cities"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

// Order categories (OpenAPI OrderCategory).
const (
	CategoryTaxi   = "taxi"
	CategoryParcel = "parcel"
	CategoryOther  = "other"
)

// Categories lists the categories in display order.
var Categories = []string{CategoryTaxi, CategoryParcel, CategoryOther}

// ValidCategory reports whether c is a known order category.
func ValidCategory(c string) bool { return slices.Contains(Categories, c) }

// Currency is the only currency the platform handles.
const Currency = "KZT"

// ErrTariffNotFound is returned for unknown tariff ids.
var ErrTariffNotFound = httpx.NewError(http.StatusNotFound, "not_found", "tariff not found")

// ErrNoActiveTariff means the city has no active tariff for the category, so
// the category cannot be booked there.
var ErrNoActiveTariff = &httpx.Error{
	Status: http.StatusUnprocessableEntity, Code: "validation_failed",
	Message: "category is not available in this city", Fields: map[string]string{"category": "not_allowed"},
}

// ExtraFee is a named fixed surcharge added to every fare of the tariff.
type ExtraFee struct {
	Code   string      `json:"code"`
	Name   cities.Name `json:"name"`
	Amount int         `json:"amount"`
}

// AdminRef identifies the administrator who published a tariff version.
type AdminRef struct {
	ID       uuid.UUID `json:"id"`
	Email    string    `json:"email"`
	FullName string    `json:"full_name"`
}

// Tariff is one immutable tariff version.
type Tariff struct {
	ID            uuid.UUID  `json:"id"`
	CityID        uuid.UUID  `json:"city_id"`
	Category      string     `json:"category"`
	Version       int        `json:"version"`
	IsActive      bool       `json:"is_active"`
	MinFare       int        `json:"min_fare"`
	MaxFare       int        `json:"max_fare"`
	FareStep      int        `json:"fare_step"`
	BaseFare      int        `json:"base_fare"`
	PerKm         int        `json:"per_km"`
	PerMinute     int        `json:"per_minute"`
	ExtraStopFee  int        `json:"extra_stop_fee"`
	ExtraFees     []ExtraFee `json:"extra_fees"`
	CommissionBps int        `json:"commission_bps"`
	CreatedBy     *AdminRef  `json:"created_by"`
	CreatedAt     time.Time  `json:"created_at"`
}

const tariffColumns = `t.id, t.city_id, t.category, t.version, t.is_active, t.min_fare, t.max_fare, t.fare_step,
	t.base_fare, t.per_km, t.per_minute, t.extra_stop_fee, t.extra_fees, t.commission_bps, t.created_at,
	a.id, a.email::text, a.full_name`

const tariffFrom = ` FROM tariffs t LEFT JOIN admin_users a ON a.id = t.created_by`

type scanner interface{ Scan(dest ...any) error }

func scanTariff(s scanner) (Tariff, error) {
	var (
		t         Tariff
		fees      []byte
		adminID   *uuid.UUID
		adminMail *string
		adminName *string
	)
	err := s.Scan(&t.ID, &t.CityID, &t.Category, &t.Version, &t.IsActive, &t.MinFare, &t.MaxFare, &t.FareStep,
		&t.BaseFare, &t.PerKm, &t.PerMinute, &t.ExtraStopFee, &fees, &t.CommissionBps, &t.CreatedAt,
		&adminID, &adminMail, &adminName)
	if err != nil {
		return Tariff{}, err
	}
	t.CreatedAt = t.CreatedAt.UTC()
	t.ExtraFees = []ExtraFee{}
	if len(fees) > 0 {
		if err := json.Unmarshal(fees, &t.ExtraFees); err != nil {
			return Tariff{}, fmt.Errorf("decode extra fees of tariff %s: %w", t.ID, err)
		}
	}
	if adminID != nil {
		t.CreatedBy = &AdminRef{ID: *adminID, Email: deref(adminMail), FullName: deref(adminName)}
	}
	return t, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ActiveTariff returns the active tariff version for (cityID, category), or
// ErrNoActiveTariff when none exists.
func ActiveTariff(ctx context.Context, q db.Querier, cityID uuid.UUID, category string) (Tariff, error) {
	t, err := scanTariff(q.QueryRow(ctx, `SELECT `+tariffColumns+tariffFrom+`
		WHERE t.city_id = $1 AND t.category = $2 AND t.is_active`, cityID, category))
	if db.IsNoRows(err) {
		return Tariff{}, ErrNoActiveTariff
	}
	if err != nil {
		return Tariff{}, fmt.Errorf("load active tariff: %w", err)
	}
	return t, nil
}

// GetTariff loads any tariff version by id.
func GetTariff(ctx context.Context, q db.Querier, id uuid.UUID) (Tariff, error) {
	t, err := scanTariff(q.QueryRow(ctx, `SELECT `+tariffColumns+tariffFrom+` WHERE t.id = $1`, id))
	if db.IsNoRows(err) {
		return Tariff{}, ErrTariffNotFound
	}
	if err != nil {
		return Tariff{}, fmt.Errorf("load tariff: %w", err)
	}
	return t, nil
}

// TariffFilter selects tariffs for the admin list.
type TariffFilter struct {
	CityID          *uuid.UUID
	Category        string // empty = all
	IncludeInactive bool
}

// ListTariffs returns tariffs ordered by city, category and newest version first.
func ListTariffs(ctx context.Context, q db.Querier, f TariffFilter) ([]Tariff, error) {
	sql := `SELECT ` + tariffColumns + tariffFrom + `
		WHERE ($1::uuid IS NULL OR t.city_id = $1)
		  AND ($2 = '' OR t.category = $2)
		  AND ($3 OR t.is_active)
		ORDER BY t.city_id, array_position(ARRAY['taxi','parcel','other'], t.category), t.version DESC`
	rows, err := q.Query(ctx, sql, f.CityID, f.Category, f.IncludeInactive)
	if err != nil {
		return nil, fmt.Errorf("list tariffs: %w", err)
	}
	defer rows.Close()
	out := []Tariff{}
	for rows.Next() {
		t, err := scanTariff(rows)
		if err != nil {
			return nil, fmt.Errorf("scan tariff: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
