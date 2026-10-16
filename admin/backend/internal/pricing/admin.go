package pricing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/audit"
	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/cities"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/textutil"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
)

// Limits enforced on new tariff versions (in addition to the database checks).
const (
	maxMoney        = 10_000_000 // tenge; keeps every amount far inside int32
	maxFareOptions  = 500        // (max_fare - min_fare) / fare_step + 1
	maxExtraFees    = 10
	maxFeeNameLen   = 60
	maxCommissionBp = 5000
)

var feeCodeRe = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)

// AdminHandler serves the admin tariff endpoints.
type AdminHandler struct {
	pool *pgxpool.Pool
}

// NewAdminHandler builds the admin tariff handler.
func NewAdminHandler(pool *pgxpool.Pool) *AdminHandler { return &AdminHandler{pool: pool} }

// MountAdmin registers the routes on the admin router (which already requires
// an admin session): GET /tariffs, GET /tariffs/{id} (dashboard.view) and
// POST /tariffs (config.manage).
func (h *AdminHandler) MountAdmin(r chi.Router) {
	r.With(auth.Permit(auth.PermDashboardView)).Get("/tariffs", httpx.H(h.list))
	r.With(auth.Permit(auth.PermDashboardView)).Get("/tariffs/{id}", httpx.H(h.get))
	r.With(auth.Permit(auth.PermConfigManage)).Post("/tariffs", httpx.H(h.create))
}

func (h *AdminHandler) list(w http.ResponseWriter, r *http.Request) error {
	cityID, err := httpx.QueryUUID(r, "city_id")
	if err != nil {
		return err
	}
	f := TariffFilter{CityID: cityID}
	query := r.URL.Query()
	if c := strings.TrimSpace(query.Get("category")); c != "" {
		if !ValidCategory(c) {
			return httpx.Validation(map[string]string{"category": "invalid"})
		}
		f.Category = c
	}
	if v := strings.TrimSpace(query.Get("include_inactive")); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return httpx.Validation(map[string]string{"include_inactive": "invalid"})
		}
		f.IncludeInactive = b
	}
	items, err := ListTariffs(r.Context(), h.pool, f)
	if err != nil {
		return err
	}
	return httpx.OK(w, map[string]any{"items": items})
}

func (h *AdminHandler) get(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	t, err := GetTariff(r.Context(), h.pool, id)
	if err != nil {
		return err
	}
	return httpx.OK(w, t)
}

// tariffRequest is TariffCreateRequest. Pointers distinguish missing fields.
type tariffRequest struct {
	CityID        *string         `json:"city_id"`
	Category      *string         `json:"category"`
	MinFare       *int64          `json:"min_fare"`
	MaxFare       *int64          `json:"max_fare"`
	FareStep      *int64          `json:"fare_step"`
	BaseFare      *int64          `json:"base_fare"`
	PerKm         *int64          `json:"per_km"`
	PerMinute     *int64          `json:"per_minute"`
	ExtraStopFee  *int64          `json:"extra_stop_fee"`
	ExtraFees     []extraFeeInput `json:"extra_fees"`
	CommissionBps *int64          `json:"commission_bps"`
}

type extraFeeInput struct {
	Code   *string      `json:"code"`
	Name   *cities.Name `json:"name"`
	Amount *int64       `json:"amount"`
}

func (h *AdminHandler) create(w http.ResponseWriter, r *http.Request) error {
	var req tariffRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	t, cityID, err := req.validate(ctx, h.pool)
	if err != nil {
		return err
	}
	if _, err := cities.Get(ctx, h.pool, cityID); err != nil {
		return err
	}
	admin, err := auth.RequireAdminPrincipal(r)
	if err != nil {
		return err
	}

	var created Tariff
	err = db.WithTx(ctx, h.pool, func(tx pgx.Tx) error {
		var err error
		created, err = PublishTariff(ctx, tx, r, t, admin.AdminID)
		return err
	})
	if err != nil {
		return err
	}
	return httpx.Created(w, created)
}

// PublishTariff stores t as the next version for (t.CityID, t.Category), makes
// it the active one, deactivates the previous active version and writes an
// audit row — all inside tx. r may be nil for non-HTTP callers.
func PublishTariff(ctx context.Context, tx pgx.Tx, r *http.Request, t Tariff, adminID uuid.UUID) (Tariff, error) {
	// Serialise publishers of the same city and category.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`,
		db.LockKey("tariff:"+t.CityID.String()+":"+t.Category)); err != nil {
		return Tariff{}, fmt.Errorf("lock tariff: %w", err)
	}
	var before *Tariff
	prev, err := ActiveTariff(ctx, tx, t.CityID, t.Category)
	switch {
	case err == nil:
		before = &prev
	case !errors.Is(err, ErrNoActiveTariff):
		return Tariff{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE tariffs SET is_active = false WHERE city_id = $1 AND category = $2 AND is_active`,
		t.CityID, t.Category); err != nil {
		return Tariff{}, fmt.Errorf("deactivate tariff: %w", err)
	}
	fees, err := json.Marshal(t.ExtraFees)
	if err != nil {
		return Tariff{}, fmt.Errorf("encode extra fees: %w", err)
	}
	var createdBy *uuid.UUID
	if adminID != uuid.Nil {
		createdBy = &adminID
	}
	var id uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO tariffs (city_id, category, version, is_active, min_fare, max_fare, fare_step, base_fare, per_km,
		                     per_minute, extra_stop_fee, extra_fees, commission_bps, created_by)
		VALUES ($1, $2, (SELECT COALESCE(max(version), 0) + 1 FROM tariffs WHERE city_id = $1 AND category = $2),
		        true, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING id`,
		t.CityID, t.Category, t.MinFare, t.MaxFare, t.FareStep, t.BaseFare, t.PerKm, t.PerMinute, t.ExtraStopFee,
		fees, t.CommissionBps, createdBy).Scan(&id)
	switch {
	case db.IsUniqueViolation(err, ""):
		return Tariff{}, httpx.Conflict("conflict", "a tariff version was published concurrently").WithCause(err)
	case err != nil:
		return Tariff{}, fmt.Errorf("insert tariff: %w", err)
	}
	created, err := GetTariff(ctx, tx, id)
	if err != nil {
		return Tariff{}, err
	}
	entry := audit.Entry{Action: "tariff.create", EntityType: "tariff", EntityID: id.String(), After: created}
	if before != nil {
		entry.Before = before
	}
	if adminID != uuid.Nil {
		entry.AdminID = &adminID
	}
	if err := audit.Log(ctx, tx, r, entry); err != nil {
		return Tariff{}, err
	}
	return created, nil
}

// validate checks the request, mirroring the database constraints, and
// returns the tariff to publish (without id/version) and its city.
func (req tariffRequest) validate(ctx context.Context, q db.Querier) (Tariff, uuid.UUID, error) {
	fields := map[string]string{}
	var t Tariff

	var cityID uuid.UUID
	switch {
	case req.CityID == nil || strings.TrimSpace(*req.CityID) == "":
		fields["city_id"] = "required"
	default:
		id, err := uuid.Parse(strings.TrimSpace(*req.CityID))
		if err != nil {
			fields["city_id"] = "invalid"
		}
		cityID = id
	}
	switch {
	case req.Category == nil || *req.Category == "":
		fields["category"] = "required"
	case !ValidCategory(*req.Category):
		fields["category"] = "invalid"
	default:
		t.Category = *req.Category
	}

	t.MinFare = money(fields, "min_fare", req.MinFare, 1, true)
	t.MaxFare = money(fields, "max_fare", req.MaxFare, 1, true)
	t.FareStep = money(fields, "fare_step", req.FareStep, 1, true)
	t.BaseFare = money(fields, "base_fare", req.BaseFare, 0, false)
	t.PerKm = money(fields, "per_km", req.PerKm, 0, false)
	t.PerMinute = money(fields, "per_minute", req.PerMinute, 0, false)
	t.ExtraStopFee = money(fields, "extra_stop_fee", req.ExtraStopFee, 0, false)

	_, minBad := fields["min_fare"]
	_, maxBad := fields["max_fare"]
	_, stepBad := fields["fare_step"]
	if !minBad && !maxBad && t.MaxFare < t.MinFare {
		fields["max_fare"] = "too_small"
		maxBad = true
	}
	if !stepBad {
		if !minBad && t.MinFare%t.FareStep != 0 {
			fields["min_fare"] = "invalid"
		}
		if !maxBad && t.MaxFare%t.FareStep != 0 {
			fields["max_fare"] = "invalid"
		}
		if len(fields) == 0 && (t.MaxFare-t.MinFare)/t.FareStep+1 > maxFareOptions {
			fields["fare_step"] = "too_small"
		}
	}

	switch {
	case req.CommissionBps == nil:
		t.CommissionBps = int(min(max(settings.DefaultCommissionBps(ctx, q), 0), maxCommissionBp))
	case *req.CommissionBps < 0:
		fields["commission_bps"] = "too_small"
	case *req.CommissionBps > maxCommissionBp:
		fields["commission_bps"] = "too_large"
	default:
		t.CommissionBps = int(*req.CommissionBps)
	}

	t.ExtraFees = validateFees(fields, req.ExtraFees)
	if len(fields) > 0 {
		return Tariff{}, uuid.Nil, httpx.Validation(fields)
	}
	t.CityID = cityID
	return t, cityID, nil
}

// money validates an amount in [lo, maxMoney]; missing optional values are 0.
func money(fields map[string]string, name string, v *int64, lo int64, required bool) int {
	switch {
	case v == nil && required:
		fields[name] = "required"
	case v == nil:
		return 0
	case *v < lo:
		fields[name] = "too_small"
	case *v > maxMoney:
		fields[name] = "too_large"
	default:
		return int(*v)
	}
	return 0
}

func validateFees(fields map[string]string, in []extraFeeInput) []ExtraFee {
	out := []ExtraFee{}
	if len(in) > maxExtraFees {
		fields["extra_fees"] = "too_many"
		return out
	}
	seen := map[string]bool{}
	for i, f := range in {
		p := "extra_fees." + strconv.Itoa(i)
		var fee ExtraFee
		switch {
		case f.Code == nil || *f.Code == "":
			fields[p+".code"] = "required"
		case !feeCodeRe.MatchString(*f.Code):
			fields[p+".code"] = "invalid"
		case seen[*f.Code]:
			fields[p+".code"] = "duplicate"
		default:
			fee.Code = *f.Code
			seen[fee.Code] = true
		}
		if f.Name == nil {
			fields[p+".name"] = "required"
		} else {
			fee.Name.KK = feeName(fields, p+".name.kk", f.Name.KK)
			fee.Name.RU = feeName(fields, p+".name.ru", f.Name.RU)
			fee.Name.EN = feeName(fields, p+".name.en", f.Name.EN)
		}
		fee.Amount = money(fields, p+".amount", f.Amount, 1, true)
		out = append(out, fee)
	}
	return out
}

func feeName(fields map[string]string, key, s string) string {
	c, err := textutil.CleanRequired(s, maxFeeNameLen)
	if err != nil {
		fields[key] = textutil.Reason(err)
	}
	return c
}
