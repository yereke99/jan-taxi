package routing

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/geo"
)

// Nominatim is a client of a self-hosted Nominatim server. The public
// nominatim.openstreetmap.org forbids autocomplete and heavy use, so it is
// rejected by NewNominatim.
type Nominatim struct {
	base   *url.URL
	client *http.Client
}

// nominatimBiasDeg is the half-size of the viewbox used to bias searches.
const nominatimBiasDeg = 0.3

// NewNominatim builds a Nominatim client for a self-hosted baseURL.
func NewNominatim(baseURL string, client *http.Client) (*Nominatim, error) {
	u, err := parseBaseURL(SourceNominatim, baseURL)
	if err != nil {
		return nil, err
	}
	host := strings.ToLower(u.Hostname())
	if host == "openstreetmap.org" || strings.HasSuffix(host, ".openstreetmap.org") {
		return nil, fmt.Errorf("geocoding nominatim: %s is the public OpenStreetMap server, whose usage policy "+
			"forbids autocomplete; set GEOCODING_BASE_URL to a self-hosted Nominatim", host)
	}
	return &Nominatim{base: u, client: client}, nil
}

// nominatimLanguages maps the app language to an Accept-Language list.
func nominatimLanguages(lang string) string {
	switch lang {
	case "ru":
		return "ru,kk"
	case "en":
		return "en,ru"
	default:
		return "kk,ru"
	}
}

type nominatimPlace struct {
	Lat     string            `json:"lat"`
	Lon     string            `json:"lon"`
	Name    string            `json:"name"`
	Display string            `json:"display_name"`
	Address map[string]string `json:"address"`
	Error   string            `json:"error"`
}

// Search implements Geocoder.
func (g *Nominatim) Search(ctx context.Context, query string, near *geo.Point, lang string, limit int) ([]Place, error) {
	q := url.Values{}
	q.Set("q", query)
	q.Set("format", "jsonv2")
	q.Set("addressdetails", "1")
	q.Set("limit", strconv.Itoa(limit))
	q.Set("countrycodes", "kz")
	q.Set("accept-language", nominatimLanguages(lang))
	if near != nil {
		f := func(v float64) string { return strconv.FormatFloat(v, 'f', 6, 64) }
		q.Set("viewbox", f(near.Lon-nominatimBiasDeg)+","+f(near.Lat+nominatimBiasDeg)+","+
			f(near.Lon+nominatimBiasDeg)+","+f(near.Lat-nominatimBiasDeg))
		q.Set("bounded", "0")
	}
	var resp []nominatimPlace
	if err := getJSON(ctx, g.client, SourceNominatim, g.endpoint("/search", q), &resp); err != nil {
		return nil, err
	}
	out := make([]Place, 0, len(resp))
	for _, r := range resp {
		if p, ok := r.place(); ok {
			out = append(out, p)
		}
	}
	return out, nil
}

// Reverse implements Geocoder.
func (g *Nominatim) Reverse(ctx context.Context, p geo.Point, lang string) (*Place, error) {
	q := url.Values{}
	q.Set("lat", strconv.FormatFloat(p.Lat, 'f', 6, 64))
	q.Set("lon", strconv.FormatFloat(p.Lon, 'f', 6, 64))
	q.Set("format", "jsonv2")
	q.Set("addressdetails", "1")
	q.Set("zoom", "18")
	q.Set("accept-language", nominatimLanguages(lang))
	var resp nominatimPlace
	if err := getJSON(ctx, g.client, SourceNominatim, g.endpoint("/reverse", q), &resp); err != nil {
		return nil, err
	}
	if resp.Error != "" { // "Unable to geocode"
		return nil, nil
	}
	pl, ok := resp.place()
	if !ok {
		return nil, nil
	}
	return &pl, nil
}

func (g *Nominatim) endpoint(path string, q url.Values) string {
	u := *g.base
	u.Path += path
	u.RawQuery = q.Encode()
	return u.String()
}

func (r nominatimPlace) place() (Place, bool) {
	lat, err1 := strconv.ParseFloat(r.Lat, 64)
	lon, err2 := strconv.ParseFloat(r.Lon, 64)
	if err1 != nil || err2 != nil {
		return Place{}, false
	}
	a := r.Address
	street := joinParts(nil, a["road"], a["house_number"])
	locality := firstNonEmpty(a["city"], a["town"], a["village"], a["hamlet"], a["county"])
	title := firstNonEmpty(r.Name, street, locality, firstPart(r.Display))
	if title == "" {
		return Place{}, false
	}
	return Place{
		Title:       title,
		Subtitle:    joinParts([]string{title}, street, a["suburb"], locality, a["state"]),
		Point:       geo.Point{Lat: lat, Lon: lon},
		Source:      SourceNominatim,
		CountryCode: strings.ToUpper(a["country_code"]),
	}, true
}

func firstPart(display string) string {
	part, _, _ := strings.Cut(display, ",")
	return strings.TrimSpace(part)
}
