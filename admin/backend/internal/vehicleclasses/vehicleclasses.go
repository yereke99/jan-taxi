// Package vehicleclasses owns the vehicle classes (body types) of ARCHITECTURE
// §21: which classes exist, how many passengers and how much cargo each
// carries, and which orders a class may serve. Drivers declare the class of
// their vehicle, passengers choose the class they need, tariffs are priced
// per class and dispatch matches orders to vehicles by class, seats and cargo
// volume.
package vehicleclasses

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"time"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

// Seeded class codes (migration 00007). Admins may add more.
const (
	Sedan     = "sedan"
	Universal = "universal"
	Minivan   = "minivan"
	Microbus  = "microbus"
	CargoVan  = "cargo_van"
	Truck     = "truck"
)

// Default is the class of legacy vehicles, tariffs and orders, and the
// backward-compatible choice of clients that do not send a class.
const Default = Sedan

// Icons are the icon names the apps can draw.
var Icons = []string{Sedan, Universal, Minivan, Microbus, CargoVan, Truck}

// Limits mirrored by the vehicle_classes and vehicles table checks.
const (
	MaxNameLen        = 40
	MaxExamplesLen    = 80
	MaxSeats          = 60
	MaxCargoVolumeL   = 100_000
	MaxServes         = 10
	MaxSort           = 1000
	MaxPassengerCount = 15 // per taxi order (ARCHITECTURE §21.2)
)

// Cargo sizes of parcel and other orders and their volume in litres.
const (
	CargoS  = "s"
	CargoM  = "m"
	CargoL  = "l"
	CargoXL = "xl"
)

// DefaultCargoSize is assumed when a delivery request does not say.
const DefaultCargoSize = CargoS

// CargoSize is a cargo size with its volume (public config).
type CargoSize struct {
	Code    string `json:"code"`
	VolumeL int    `json:"volume_l"`
}

// CargoSizes lists the sizes from small to extra large.
var CargoSizes = []CargoSize{{CargoS, 10}, {CargoM, 60}, {CargoL, 400}, {CargoXL, 1500}}

// CargoVolumeL returns the volume of a cargo size, ok=false for unknown sizes.
func CargoVolumeL(size string) (int, bool) {
	for _, s := range CargoSizes {
		if s.Code == size {
			return s.VolumeL, true
		}
	}
	return 0, false
}

var codeRe = regexp.MustCompile(`^[a-z_]{2,20}$`)

// ValidCode reports whether s has the shape of a class code.
func ValidCode(s string) bool { return codeRe.MatchString(s) }

// ErrNotFound is returned for unknown class codes.
var ErrNotFound = httpx.NewError(http.StatusNotFound, "not_found", "vehicle class not found")

// Name is a class name in every UI language.
type Name struct {
	KK string `json:"kk"`
	RU string `json:"ru"`
	EN string `json:"en"`
}

// Get returns the name for lang, falling back to Kazakh.
func (n Name) Get(lang string) string {
	switch lang {
	case "ru":
		return n.RU
	case "en":
		return n.EN
	default:
		return n.KK
	}
}

// Class is one vehicle class (OpenAPI VehicleClass).
type Class struct {
	Code                string    `json:"code"`
	Name                Name      `json:"name"`
	Examples            string    `json:"examples"`
	SeatsDefault        int       `json:"seats_default"`
	SeatsMax            int       `json:"seats_max"`
	CargoVolumeLDefault int       `json:"cargo_volume_l_default"`
	PassengerCapable    bool      `json:"passenger_capable"`
	CargoCapable        bool      `json:"cargo_capable"`
	Serves              []string  `json:"serves"`
	Icon                string    `json:"icon"`
	Sort                int       `json:"sort"`
	IsActive            bool      `json:"is_active"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// ServesClass reports whether vehicles of c may take orders of class code.
func (c Class) ServesClass(code string) bool { return slices.Contains(c.Serves, code) }

// CanCarry reports whether a vehicle of this class, with the class defaults,
// can carry an order of category: passengerCount people for a taxi order (the
// class must carry passengers and seats_max must cover them), or cargo of
// cargoVolumeL litres for a parcel/other order (the class must carry cargo
// and its default volume must cover it).
func (c Class) CanCarry(category string, passengerCount, cargoVolumeL int) bool {
	if category == "taxi" {
		return c.PassengerCapable && c.SeatsMax >= passengerCount
	}
	return c.CargoCapable && c.CargoVolumeLDefault >= cargoVolumeL
}

const columns = `vc.code, vc.name_kk, vc.name_ru, vc.name_en, vc.examples, vc.seats_default, vc.seats_max,
	vc.cargo_volume_l_default, vc.passenger_capable, vc.cargo_capable, vc.serves, vc.icon, vc.sort, vc.is_active,
	vc.created_at, vc.updated_at`

type scanner interface{ Scan(dest ...any) error }

func scan(row scanner) (Class, error) {
	var (
		c                       Class
		seatsDefault, seatsMax  int16
		cargoDefault, sortOrder int32
	)
	err := row.Scan(&c.Code, &c.Name.KK, &c.Name.RU, &c.Name.EN, &c.Examples, &seatsDefault, &seatsMax,
		&cargoDefault, &c.PassengerCapable, &c.CargoCapable, &c.Serves, &c.Icon, &sortOrder, &c.IsActive,
		&c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return Class{}, err
	}
	c.SeatsDefault, c.SeatsMax, c.CargoVolumeLDefault, c.Sort = int(seatsDefault), int(seatsMax), int(cargoDefault), int(sortOrder)
	c.CreatedAt, c.UpdatedAt = c.CreatedAt.UTC(), c.UpdatedAt.UTC()
	if c.Serves == nil {
		c.Serves = []string{}
	}
	return c, nil
}

// List returns the classes in display order (sort, then code); activeOnly
// leaves out deactivated classes.
func List(ctx context.Context, q db.Querier, activeOnly bool) ([]Class, error) {
	rows, err := q.Query(ctx, `SELECT `+columns+` FROM vehicle_classes vc
		WHERE vc.is_active OR NOT $1 ORDER BY vc.sort, vc.code`, activeOnly)
	if err != nil {
		return nil, fmt.Errorf("vehicle classes: list: %w", err)
	}
	defer rows.Close()
	out := []Class{}
	for rows.Next() {
		c, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("vehicle classes: scan: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Get returns one class (active or not), or ErrNotFound.
func Get(ctx context.Context, q db.Querier, code string) (Class, error) {
	return get(ctx, q, code, false)
}

func get(ctx context.Context, q db.Querier, code string, lock bool) (Class, error) {
	if !ValidCode(code) {
		return Class{}, ErrNotFound
	}
	sql := `SELECT ` + columns + ` FROM vehicle_classes vc WHERE vc.code = $1`
	if lock {
		sql += ` FOR UPDATE`
	}
	c, err := scan(q.QueryRow(ctx, sql, code))
	if db.IsNoRows(err) {
		return Class{}, ErrNotFound
	}
	if err != nil {
		return Class{}, fmt.Errorf("vehicle classes: get %s: %w", code, err)
	}
	return c, nil
}

// PublicClass is a class as published to the apps (GET /public/config).
type PublicClass struct {
	Code                string `json:"code"`
	Name                Name   `json:"name"`
	Examples            string `json:"examples"`
	Icon                string `json:"icon"`
	SeatsDefault        int    `json:"seats_default"`
	SeatsMax            int    `json:"seats_max"`
	CargoVolumeLDefault int    `json:"cargo_volume_l_default"`
	PassengerCapable    bool   `json:"passenger_capable"`
	CargoCapable        bool   `json:"cargo_capable"`
}

// Public returns the active classes in display order for the apps.
func Public(ctx context.Context, q db.Querier) ([]PublicClass, error) {
	list, err := List(ctx, q, true)
	if err != nil {
		return nil, err
	}
	out := make([]PublicClass, len(list))
	for i, c := range list {
		out[i] = PublicClass{Code: c.Code, Name: c.Name, Examples: c.Examples, Icon: c.Icon,
			SeatsDefault: c.SeatsDefault, SeatsMax: c.SeatsMax, CargoVolumeLDefault: c.CargoVolumeLDefault,
			PassengerCapable: c.PassengerCapable, CargoCapable: c.CargoCapable}
	}
	return out, nil
}
