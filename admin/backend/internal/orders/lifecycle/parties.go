package lifecycle

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/plate"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/textutil"
	"github.com/yereke99/jantaxi/admin/backend/internal/stops"
)

// DriverInfo is the assigned driver as needed by notifications.
type DriverInfo struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Code      string
	FirstName string
	LastName  string
	Make      string
	Model     string
	Color     string
	Plate     string // display form (e.g. 123 ABC 04), raw input as a fallback
	// Phone is the driver's phone (profile, else the user's verified one).
	Phone string
	// AvatarFileID is the driver's photo (files.id), if any.
	AvatarFileID *uuid.UUID
	RatingSum    int
	RatingCount  int
}

// Rating is the average rating with one decimal ("4.9"), or "" before the
// first rating.
func (d DriverInfo) Rating() string {
	if d.RatingCount <= 0 {
		return ""
	}
	return strconv.FormatFloat(math.Round(float64(d.RatingSum)/float64(d.RatingCount)*10)/10, 'f', 1, 64)
}

// Name is the driver's first name with the last-name initial ("Ерлан Б.").
func (d DriverInfo) Name() string {
	name := strings.TrimSpace(d.FirstName)
	if r := []rune(strings.TrimSpace(d.LastName)); len(r) > 0 {
		name = strings.TrimSpace(name + " " + string(r[0]) + ".")
	}
	if name == "" {
		return d.Code
	}
	return name
}

// Vehicle is "<make> <model>, <color>" as shown in messages.
func (d DriverInfo) Vehicle() string {
	v := strings.TrimSpace(strings.TrimSpace(d.Make) + " " + strings.TrimSpace(d.Model))
	if c := strings.TrimSpace(d.Color); c != "" {
		if v == "" {
			return c
		}
		v += ", " + c
	}
	if v == "" {
		return "—"
	}
	return v
}

// PlateLabel is the plate as shown in messages.
func (d DriverInfo) PlateLabel() string {
	if d.Plate == "" {
		return "—"
	}
	return d.Plate
}

// LoadDriverInfo reads a driver with the current (non-archived) vehicle.
func LoadDriverInfo(ctx context.Context, q db.Querier, driverID uuid.UUID) (DriverInfo, error) {
	var d DriverInfo
	var make_, model, color, plateNorm, plateRaw *string
	err := q.QueryRow(ctx, `
		SELECT d.id, d.user_id, d.code, d.first_name, d.last_name, v.make, v.model, v.color, v.plate_normalized, v.plate_raw,
		       COALESCE(d.phone_e164, u.phone_e164, ''), d.avatar_file_id, d.rating_sum, d.rating_count
		FROM driver_profiles d
		JOIN users u ON u.id = d.user_id
		LEFT JOIN LATERAL (
			SELECT make, model, color, plate_normalized, plate_raw FROM vehicles
			WHERE driver_id = d.id AND status <> 'archived'
			ORDER BY (status = 'claimed') DESC, created_at DESC LIMIT 1) v ON true
		WHERE d.id = $1`, driverID).
		Scan(&d.ID, &d.UserID, &d.Code, &d.FirstName, &d.LastName, &make_, &model, &color, &plateNorm, &plateRaw,
			&d.Phone, &d.AvatarFileID, &d.RatingSum, &d.RatingCount)
	if err != nil {
		return DriverInfo{}, fmt.Errorf("orders: load driver %s: %w", driverID, err)
	}
	d.Make, d.Model, d.Color = deref(make_), deref(model), deref(color)
	// Messages show the plate as it is written on the car ("123 ABC 04").
	d.Plate = PlateDisplay(plateNorm, plateRaw)
	return d, nil
}

// PlateDisplay formats a stored plate for display ("123 ABC 04"); a raw value
// is returned as is.
func PlateDisplay(normalized, raw *string) string {
	if n := deref(normalized); n != "" {
		return plate.Format(n)
	}
	return strings.TrimSpace(deref(raw))
}

// Route is the stop addresses of an order as shown in messages. An address
// may be empty: message templates show it as a point on the map, never as
// coordinates.
type Route struct {
	HasManualStops bool
	Pickup         string
	Via            []string // intermediate stops in order
	Destination    string
}

// ViaText joins the intermediate stops with arrows ("" when there are none);
// a stop without an address is called placeholder.
func (r Route) ViaText(placeholder string) string {
	parts := make([]string, 0, len(r.Via))
	for _, v := range r.Via {
		if v == "" {
			v = placeholder
		}
		parts = append(parts, v)
	}
	return strings.Join(parts, " → ")
}

// LoadRoute reads the stop addresses.
func LoadRoute(ctx context.Context, q db.Querier, orderID uuid.UUID) (Route, error) {
	list, err := stops.ForOrder(ctx, q, orderID)
	if err != nil {
		return Route{}, err
	}
	var r Route
	for _, s := range list {
		r.HasManualStops = r.HasManualStops || s.Mode == "manual"
		addr := textutil.Clean(s.Address)
		switch s.Kind {
		case stops.KindPickup:
			r.Pickup = addr
		case stops.KindDestination:
			r.Destination = addr
		default:
			r.Via = append(r.Via, addr)
		}
	}
	return r, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// telegramUsernameRe is a public Telegram username.
var telegramUsernameRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{3,31}$`)

// TelegramLink is how a driver reaches a passenger in Telegram: the public
// profile https://t.me/<username>, or a mention by id (tg://user?id=<id>)
// when the passenger has no username.
func TelegramLink(username *string, telegramUserID int64) string {
	if username != nil {
		if u := strings.TrimPrefix(strings.TrimSpace(*username), "@"); telegramUsernameRe.MatchString(u) {
			return "https://t.me/" + u
		}
	}
	return "tg://user?id=" + strconv.FormatInt(telegramUserID, 10)
}
