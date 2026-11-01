package commissionareas

import (
	"context"
	"math"
	"strings"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/geo"
)

// Place is a search result offered to the administrator.
type Place struct {
	Title    string  `json:"title"`
	Subtitle string  `json:"subtitle"`
	Lat      float64 `json:"lat"`
	Lon      float64 `json:"lon"`
}

// Settlement is a city, town, village or hamlet from the map data
// (OpenStreetMap) near a point. Extent is its bounding box [west, south,
// east, north], nil when the settlement is mapped as a single point.
type Settlement struct {
	Name   string
	Kind   string // OSM place value: city, town, village, hamlet, …; "" when unknown
	Point  geo.Point
	Extent *[4]float64
}

// Geocoder is what the admin endpoints need from the map search: places by
// name, and the settlements around a point. SettlementsNear returns nil when
// the configured provider cannot list them.
type Geocoder interface {
	SearchPlaces(ctx context.Context, query, lang string, limit int) ([]Place, error)
	SettlementsNear(ctx context.Context, p geo.Point, radiusKm float64, limit int) ([]Settlement, error)
}

// Suggestion bases (OpenAPI CommissionAreaSuggestion.basis).
const (
	BasisExtent  = "osm_extent" // the settlement's outline in the map data
	BasisType    = "osm_type"   // a typical size for its kind (city, village…)
	BasisDefault = "default"    // nothing known about the place
)

// Suggestion is the proposed radius of a circle around a village centre.
type Suggestion struct {
	RadiusM    int                 `json:"radius_m"`
	Basis      string              `json:"basis"`
	Settlement *SuggestedPlace     `json:"settlement"`
	Neighbor   *SuggestedNeighbour `json:"neighbor"`
}

// SuggestedPlace is the settlement the centre lies in.
type SuggestedPlace struct {
	Name string  `json:"name"`
	Kind *string `json:"kind"`
}

// SuggestedNeighbour is the nearest other village: a configured area or a
// settlement of the map data.
type SuggestedNeighbour struct {
	Name      string `json:"name"`
	DistanceM int    `json:"distance_m"`
	Source    string `json:"source"` // "area" or "osm"
}

// Proposal tuning.
const (
	// settlementSearchKm and settlementLimit: how far and how many settlements
	// of the map data are considered around the centre.
	settlementSearchKm = 30
	settlementLimit    = 10
	// ownNodeM: a settlement mapped as a point this close to the centre is
	// the village itself.
	ownNodeM = 3_000.0
	// extentMargin widens the outline's circle for the outskirts (farms,
	// roads into the village).
	extentMargin = 1.2
	// defaultRadiusM is the radius when nothing is known about the place.
	defaultRadiusM = 2_500.0
	// minProposalM keeps a proposal usable even between very close villages.
	minProposalM = 500.0
	maxProposalM = 15_000.0
)

// typicalRadiusM is a typical reach of a settlement mapped only as a point,
// by its OpenStreetMap place kind.
var typicalRadiusM = map[string]float64{
	"city":              8_000,
	"town":              4_000,
	"village":           2_000,
	"hamlet":            1_000,
	"isolated_dwelling": 600,
	"farm":              600,
	"locality":          1_000,
	"suburb":            1_500,
	"neighbourhood":     1_000,
	"quarter":           1_000,
}

// Suggest proposes the radius of a village circle centred at c:
//
//  1. its own size — the settlement of the map data whose outline covers c
//     (the smallest one): the circle around c reaching the far corner of the
//     outline's box half-diagonal plus the offset of c from the box centre,
//     widened by extentMargin; a settlement mapped only as a point within
//     ownNodeM of c: the typical reach of its kind; otherwise defaultRadiusM;
//  2. its neighbours — at most half the distance to the nearest other
//     village (a configured area, except exclude, or a settlement of the map
//     data), so neighbouring circles meet halfway instead of overlapping;
//  3. kept within [minProposalM, maxProposalM] and the API limits, rounded to
//     100 m.
//
// A failing geocoder only removes the map data from the computation.
func Suggest(ctx context.Context, q db.Querier, g Geocoder, c geo.Point, exclude uuid.UUID) (Suggestion, error) {
	var settlements []Settlement
	if g != nil {
		settlements, _ = g.SettlementsNear(ctx, c, settlementSearchKm, settlementLimit)
	}
	area, err := NearestCenter(ctx, q, c, exclude)
	if err != nil {
		return Suggestion{}, err
	}
	return propose(c, settlements, area), nil
}

// propose is the pure part of Suggest.
func propose(c geo.Point, settlements []Settlement, area *Neighbor) Suggestion {
	own := ownSettlement(c, settlements)
	out := Suggestion{Basis: BasisDefault}
	radius := defaultRadiusM
	if own != nil {
		out.Settlement = &SuggestedPlace{Name: own.Name}
		if own.Kind != "" {
			kind := own.Kind
			out.Settlement.Kind = &kind
		}
		switch r, ok := typicalRadiusM[own.Kind]; {
		case own.Extent != nil:
			radius, out.Basis = extentRadius(c, *own.Extent), BasisExtent
		case ok:
			radius, out.Basis = r, BasisType
		}
	}

	// The nearest other village: a configured area or a settlement nearby.
	var near *SuggestedNeighbour
	nearM := math.Inf(1)
	if area != nil {
		near, nearM = &SuggestedNeighbour{Name: area.Name, Source: "area"}, area.DistanceM
	}
	for i := range settlements {
		s := &settlements[i]
		if s == own || strings.TrimSpace(s.Name) == "" {
			continue
		}
		if d := geo.DistanceMeters(c, s.Point); d < nearM && d > 0 {
			near, nearM = &SuggestedNeighbour{Name: s.Name, Source: "osm"}, d
		}
	}
	if near != nil {
		near.DistanceM = int(nearM + 0.5)
		out.Neighbor = near
		radius = math.Min(radius, nearM/2)
	}

	radius = math.Max(radius, minProposalM)
	radius = math.Min(radius, maxProposalM)
	if near != nil && nearM/2 < minProposalM {
		// Two villages closer than a kilometre: meet halfway, never overlap.
		radius = math.Max(nearM/2, MinRadiusM)
	}
	out.RadiusM = clampRadius(int(math.Round(radius/100) * 100))
	return out
}

// ownSettlement is the settlement c lies in: the smallest outline covering c,
// else the nearest settlement mapped as a point within ownNodeM.
func ownSettlement(c geo.Point, list []Settlement) *Settlement {
	var best *Settlement
	bestArea := math.Inf(1)
	for i := range list {
		s := &list[i]
		if s.Extent == nil || strings.TrimSpace(s.Name) == "" {
			continue
		}
		w, so, e, n := box(*s.Extent)
		if c.Lon < w || c.Lon > e || c.Lat < so || c.Lat > n {
			continue
		}
		if a := (e - w) * (n - so); a < bestArea {
			best, bestArea = s, a
		}
	}
	if best != nil {
		return best
	}
	nearest := ownNodeM
	for i := range list {
		s := &list[i]
		if s.Extent != nil || strings.TrimSpace(s.Name) == "" {
			continue
		}
		if d := geo.DistanceMeters(c, s.Point); d <= nearest {
			best, nearest = s, d
		}
	}
	return best
}

// extentRadius is the circle around c that covers the settlement's box from
// its centre (half-diagonal) plus the offset of c from that centre, widened
// for the outskirts.
func extentRadius(c geo.Point, extent [4]float64) float64 {
	w, s, e, n := box(extent)
	center := geo.Point{Lat: (s + n) / 2, Lon: (w + e) / 2}
	half := geo.DistanceMeters(geo.Point{Lat: s, Lon: w}, geo.Point{Lat: n, Lon: e}) / 2
	return (half + geo.DistanceMeters(c, center)) * extentMargin
}

// box orders an extent as west, south, east, north whatever the provider's
// corner order.
func box(x [4]float64) (w, s, e, n float64) {
	return math.Min(x[0], x[2]), math.Min(x[1], x[3]), math.Max(x[0], x[2]), math.Max(x[1], x[3])
}

func clampRadius(r int) int {
	return min(max(r, MinRadiusM), MaxRadiusM)
}
