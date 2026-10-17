package routing

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/geo"
)

// Photon is a client of the Photon geocoder (komoot/photon). Production
// should run a self-hosted instance; photon.komoot.io is a fair-use public
// service. Results are restricted to Kazakhstan with countrycode=KZ.
type Photon struct {
	base   *url.URL
	client *http.Client
}

// Reverse lookups first look for objects within photonReverseRadiusKm, then
// (e.g. on an empty field or a new street) within photonReverseWideKm, so a pin
// is named after the nearest street or district rather than left unnamed.
const (
	photonReverseRadiusKm = "0.3"
	photonReverseWideKm   = "1"
)

// Search near a position: Photon's location bias at city zoom with a low
// bias scale (prominence matters less than distance), restricted to a box of
// about ±photonNearBoxDeg around the position (~65 km north–south).
const (
	photonNearZoom      = "12"
	photonNearBiasScale = "0.1"
	photonNearBoxDeg    = 0.6
)

// NewPhoton builds a Photon client for baseURL (e.g. http://photon:2322).
func NewPhoton(baseURL string, client *http.Client) (*Photon, error) {
	u, err := parseBaseURL(SourcePhoton, baseURL)
	if err != nil {
		return nil, err
	}
	return &Photon{base: u, client: client}, nil
}

// photonLang maps the app language to Photon's: Photon supports only
// default (local names), en, de and fr, so Kazakh and Russian use local names.
func photonLang(lang string) string {
	if lang == "en" {
		return "en"
	}
	return "default"
}

type photonResponse struct {
	Features []struct {
		Geometry struct {
			Type        string    `json:"type"`
			Coordinates []float64 `json:"coordinates"`
		} `json:"geometry"`
		Properties photonProps `json:"properties"`
	} `json:"features"`
}

type photonProps struct {
	Name        string `json:"name"`
	HouseNumber string `json:"housenumber"`
	Street      string `json:"street"`
	Locality    string `json:"locality"`
	District    string `json:"district"`
	City        string `json:"city"`
	County      string `json:"county"`
	State       string `json:"state"`
	CountryCode string `json:"countrycode"`
}

// Search implements Geocoder. With a position the search is local: results
// inside the box around it, biased by distance. Only when the box has no
// match is the whole country searched (still biased towards the position),
// so a place in another city can be found on purpose.
func (g *Photon) Search(ctx context.Context, query string, near *geo.Point, lang string, limit int) ([]Place, error) {
	q := url.Values{}
	q.Set("q", query)
	q.Set("limit", strconv.Itoa(limit))
	q.Set("lang", photonLang(lang))
	q.Set("countrycode", "KZ")
	if near == nil {
		return g.get(ctx, "/api", q)
	}
	q.Set("lat", strconv.FormatFloat(near.Lat, 'f', 6, 64))
	q.Set("lon", strconv.FormatFloat(near.Lon, 'f', 6, 64))
	q.Set("zoom", photonNearZoom)
	q.Set("location_bias_scale", photonNearBiasScale)
	local := url.Values{}
	for k, v := range q {
		local[k] = v
	}
	local.Set("bbox", photonBox(*near, photonNearBoxDeg))
	places, err := g.get(ctx, "/api", local)
	if err != nil || len(places) > 0 {
		return places, err
	}
	return g.get(ctx, "/api", q)
}

// photonBox is Photon's bbox parameter (minLon,minLat,maxLon,maxLat) of
// ±d degrees around p.
func photonBox(p geo.Point, d float64) string {
	f := func(v float64) string { return strconv.FormatFloat(v, 'f', 4, 64) }
	return f(p.Lon-d) + "," + f(p.Lat-d) + "," + f(p.Lon+d) + "," + f(p.Lat+d)
}

// Reverse implements Geocoder.
func (g *Photon) Reverse(ctx context.Context, p geo.Point, lang string) (*Place, error) {
	for _, radius := range []string{photonReverseRadiusKm, photonReverseWideKm} {
		q := url.Values{}
		q.Set("lat", strconv.FormatFloat(p.Lat, 'f', 6, 64))
		q.Set("lon", strconv.FormatFloat(p.Lon, 'f', 6, 64))
		q.Set("lang", photonLang(lang))
		q.Set("limit", "1")
		q.Set("radius", radius)
		places, err := g.get(ctx, "/reverse", q)
		if err != nil {
			return nil, err
		}
		if len(places) > 0 {
			return &places[0], nil
		}
	}
	return nil, nil
}

func (g *Photon) get(ctx context.Context, path string, q url.Values) ([]Place, error) {
	u := *g.base
	u.Path += path
	u.RawQuery = q.Encode()
	var resp photonResponse
	if err := getJSON(ctx, g.client, SourcePhoton, u.String(), &resp); err != nil {
		return nil, err
	}
	out := make([]Place, 0, len(resp.Features))
	for _, f := range resp.Features {
		if f.Geometry.Type != "Point" || len(f.Geometry.Coordinates) < 2 {
			continue
		}
		title, subtitle := photonLabel(f.Properties)
		if title == "" {
			continue
		}
		out = append(out, Place{
			Title: title, Subtitle: subtitle, Source: SourcePhoton,
			Point:       geo.Point{Lat: f.Geometry.Coordinates[1], Lon: f.Geometry.Coordinates[0]},
			CountryCode: strings.ToUpper(f.Properties.CountryCode),
		})
	}
	return out, nil
}

// photonLabel composes a title (object name or "street, number") and a
// subtitle (street line for named objects, then locality and region).
func photonLabel(p photonProps) (string, string) {
	street := joinParts(nil, p.Street, p.HouseNumber)
	locality := firstNonEmpty(p.City, p.Locality, p.District, p.County)
	switch {
	case p.Name != "":
		return p.Name, joinParts([]string{p.Name}, street, p.District, locality, p.State)
	case p.Street != "":
		return street, joinParts([]string{street}, p.District, locality, p.State)
	default:
		// No object and no street: the district and the town, e.g.
		// "Медеу ауданы, Алматы".
		title := joinParts(nil, p.District, firstNonEmpty(p.City, p.Locality, p.County))
		if title == "" {
			title = p.State
		}
		return title, joinParts([]string{title, p.District, p.City, p.Locality}, p.County, p.State)
	}
}
