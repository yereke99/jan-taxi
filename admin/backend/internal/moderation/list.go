package moderation

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/cities"
	"github.com/yereke99/jantaxi/admin/backend/internal/drivers"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/phone"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/plate"
	"github.com/yereke99/jantaxi/admin/backend/internal/vehicles"
)

// ListItem is one row of the admin driver list (OpenAPI AdminDriverListItem).
type ListItem struct {
	ID              uuid.UUID            `json:"id"`
	Code            string               `json:"code"`
	Status          string               `json:"status"`
	FirstName       string               `json:"first_name"`
	LastName        string               `json:"last_name"`
	Phone           *string              `json:"phone"`
	PhoneVerified   bool                 `json:"phone_verified"`
	City            drivers.CityRef      `json:"city"`
	IsOnline        bool                 `json:"is_online"`
	LastHeartbeatAt *time.Time           `json:"last_heartbeat_at"`
	Vehicle         *drivers.VehicleView `json:"vehicle"`
	Rating          *float64             `json:"rating"`
	RatingCount     int                  `json:"rating_count"`
	CompletedTrips  int                  `json:"completed_trips"`
	WalletAvailable *int64               `json:"wallet_available"`
	SubmittedAt     *time.Time           `json:"submitted_at"`
	ApprovedAt      *time.Time           `json:"approved_at"`
	CreatedAt       time.Time            `json:"created_at"`
}

// ListFilter selects drivers for the admin list.
type ListFilter struct {
	Status string
	CityID *uuid.UUID
	Online *bool
	Query  string
	Page   httpx.PageParams
}

func parseListFilter(r *http.Request) (ListFilter, error) {
	var f ListFilter
	fields := map[string]string{}
	q := r.URL.Query()
	if s := strings.TrimSpace(q.Get("status")); s != "" {
		if !drivers.ValidStatus(s) {
			fields["status"] = "invalid"
		}
		f.Status = s
	}
	if id, err := httpx.QueryUUID(r, "city_id"); err != nil {
		fields["city_id"] = "invalid"
	} else {
		f.CityID = id
	}
	if s := strings.TrimSpace(q.Get("online")); s != "" {
		b, err := strconv.ParseBool(s)
		if err != nil {
			fields["online"] = "invalid"
		}
		f.Online = &b
	}
	f.Query = strings.TrimSpace(q.Get("q"))
	if len([]rune(f.Query)) > 100 {
		fields["q"] = "too_long"
	}
	page, err := httpx.ParsePage(r)
	if err != nil {
		return f, err
	}
	f.Page = page
	if len(fields) > 0 {
		return f, httpx.Validation(fields)
	}
	return f, nil
}

// whereClause builds the filter conditions; q matches the driver code, name,
// phone (in any common spelling) or plate (Cyrillic or Latin letters).
func (f ListFilter) whereClause() (string, []any) {
	var (
		conds []string
		args  []any
	)
	arg := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}
	if f.Status != "" {
		conds = append(conds, "dp.status = "+arg(f.Status))
	}
	if f.CityID != nil {
		conds = append(conds, "dp.city_id = "+arg(*f.CityID))
	}
	if f.Online != nil {
		conds = append(conds, "dp.is_online = "+arg(*f.Online))
	}
	if q := f.Query; q != "" {
		like := "%" + escapeLike(q) + "%"
		or := []string{
			"dp.code ILIKE " + arg(escapeLike(strings.ToUpper(q))+"%"),
			"(dp.first_name || ' ' || dp.last_name) ILIKE " + arg(like),
			"(dp.last_name || ' ' || dp.first_name) ILIKE " + arg(like),
		}
		if num, err := phone.Normalize(q); err == nil {
			or = append(or, "dp.phone_e164 = "+arg(num))
		} else if digits := onlyDigits(q); len(digits) >= 4 && len(digits) == countDigitsAndSeparators(q) {
			if strings.HasPrefix(digits, "8") {
				digits = "7" + digits[1:]
			}
			or = append(or, "dp.phone_e164 LIKE "+arg("%"+digits+"%"))
		}
		if norm, err := plate.Normalize(q); err == nil {
			or = append(or, "v.plate_normalized = "+arg(norm))
		} else if compact := compactPlate(q); len(compact) >= 3 {
			or = append(or, "v.plate_normalized LIKE "+arg("%"+escapeLike(compact)+"%"))
		}
		conds = append(conds, "("+strings.Join(or, " OR ")+")")
	}
	if len(conds) == 0 {
		return "", args
	}
	return "WHERE " + strings.Join(conds, " AND "), args
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func onlyDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// countDigitsAndSeparators returns the number of digits when s consists only
// of digits and phone separators, otherwise -1.
func countDigitsAndSeparators(s string) int {
	n := 0
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			n++
		case r == '+' || r == ' ' || r == '-' || r == '(' || r == ')':
		default:
			return -1
		}
	}
	return n
}

// cyrillicPlateLetters maps Cyrillic look-alikes typed on a Russian or Kazakh
// keyboard to the Latin letters used on plates.
var cyrillicPlateLetters = strings.NewReplacer(
	"А", "A", "В", "B", "Е", "E", "К", "K", "М", "M", "Н", "H", "О", "O", "Р", "P", "С", "C", "Т", "T",
	"У", "Y", "Х", "X", "Ү", "Y", "І", "I",
)

// compactPlate turns a partial plate into its stored spelling (upper case,
// Latin letters, no separators); "" when it holds anything else.
func compactPlate(s string) string {
	s = cyrillicPlateLetters.Replace(strings.ToUpper(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == ' ' || r == '-':
		case r < unicode.MaxASCII && (unicode.IsDigit(r) || unicode.IsUpper(r)):
			b.WriteRune(r)
		default:
			return ""
		}
	}
	return b.String()
}

// List returns one page of drivers and the total count.
func List(ctx context.Context, q db.Querier, f ListFilter) ([]ListItem, int, error) {
	where, args := f.whereClause()
	from := `
		FROM driver_profiles dp
		JOIN cities c ON c.id = dp.city_id
		LEFT JOIN vehicles v ON v.driver_id = dp.id AND v.status <> 'archived'
		LEFT JOIN vehicle_classes vc ON vc.code = v.class_code
		LEFT JOIN wallets w ON w.driver_id = dp.id ` + where
	var total int
	if err := q.QueryRow(ctx, `SELECT count(*) `+from, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("moderation: count drivers: %w", err)
	}
	order := "dp.created_at DESC, dp.id"
	if f.Status == drivers.StatusPending {
		order = "dp.submitted_at ASC NULLS LAST, dp.id"
	}
	args = append(args, f.Page.PageSize, f.Page.Offset())
	rows, err := q.Query(ctx, `
		SELECT dp.id, dp.code, dp.status, dp.first_name, dp.last_name, dp.phone_e164, dp.phone_verified_at,
		       c.id, c.code, c.name_kk, c.name_ru, c.name_en, dp.is_online, dp.last_heartbeat_at,
		       v.id, v.make, v.model, v.color, v.plate_raw, v.plate_normalized,
		       v.class_code, v.seats, v.cargo_volume_l, vc.name_kk, vc.name_ru, vc.name_en,
		       dp.rating_sum, dp.rating_count, dp.completed_trips, w.balance - w.reserved,
		       dp.submitted_at, dp.approved_at, dp.created_at `+from+`
		ORDER BY `+order+`
		LIMIT $`+strconv.Itoa(len(args)-1)+` OFFSET $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, 0, fmt.Errorf("moderation: list drivers: %w", err)
	}
	defer rows.Close()
	items := []ListItem{}
	for rows.Next() {
		var (
			it                        ListItem
			phoneAt                   *time.Time
			name                      cities.Name
			vehicleID                 *uuid.UUID
			make_, model, color, raw  *string
			plateNorm                 *string
			classCode                 *string
			seats                     *int16
			cargoL                    *int32
			classKK, classRU, classEN *string
			ratingSum                 int
		)
		if err := rows.Scan(&it.ID, &it.Code, &it.Status, &it.FirstName, &it.LastName, &it.Phone, &phoneAt,
			&it.City.ID, &it.City.Code, &name.KK, &name.RU, &name.EN, &it.IsOnline, &it.LastHeartbeatAt,
			&vehicleID, &make_, &model, &color, &raw, &plateNorm,
			&classCode, &seats, &cargoL, &classKK, &classRU, &classEN,
			&ratingSum, &it.RatingCount, &it.CompletedTrips, &it.WalletAvailable,
			&it.SubmittedAt, &it.ApprovedAt, &it.CreatedAt); err != nil {
			return nil, 0, fmt.Errorf("moderation: list drivers: %w", err)
		}
		it.City.Name = name
		it.PhoneVerified = it.Phone != nil && phoneAt != nil
		it.Rating = drivers.Rating(ratingSum, it.RatingCount)
		it.LastHeartbeatAt, it.SubmittedAt, it.ApprovedAt = drivers.UTCPtr(it.LastHeartbeatAt), drivers.UTCPtr(it.SubmittedAt), drivers.UTCPtr(it.ApprovedAt)
		it.CreatedAt = it.CreatedAt.UTC()
		if vehicleID != nil {
			v := vehicles.Vehicle{Make: deref(make_), Model: deref(model), Color: deref(color), PlateRaw: deref(raw), PlateNormalized: deref(plateNorm),
				ClassCode: deref(classCode), ClassName: vehicles.ClassName{KK: deref(classKK), RU: deref(classRU), EN: deref(classEN)}}
			if seats != nil && cargoL != nil {
				v.Seats, v.CargoVolumeL = int(*seats), int(*cargoL)
			}
			it.Vehicle = drivers.NewVehicleView(&v)
		}
		items = append(items, it)
	}
	return items, total, rows.Err()
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) error {
	f, err := parseListFilter(r)
	if err != nil {
		return err
	}
	items, total, err := List(r.Context(), h.pool, f)
	if err != nil {
		return err
	}
	return httpx.OK(w, httpx.Page[ListItem]{Items: items, Page: f.Page.Page, PageSize: f.Page.PageSize, Total: total})
}
