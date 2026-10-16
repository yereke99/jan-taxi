package routing

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/geo"
)

// TwoGIS is a client of the 2GIS Catalog API 3.0 (suggests for search, with
// items/geocode as a fallback, and items/geocode by coordinates for reverse
// lookups). Every request is billed, so results are cached upstream.
type TwoGIS struct {
	base   *url.URL
	key    string
	bias   geo.Point
	client *http.Client
}

// twoGISSearchRadiusM bounds the geocode fallback around the bias point.
const twoGISSearchRadiusM = "50000"

// NewTwoGIS builds a 2GIS client. bias is used when a search has no location.
func NewTwoGIS(baseURL, key string, bias geo.Point, client *http.Client) (*TwoGIS, error) {
	u, err := parseBaseURL(SourceTwoGIS, baseURL)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(key) == "" {
		return nil, errors.New("geocoding twogis: GEOCODING_API_KEY is required")
	}
	if bias.Valid() != nil {
		return nil, errors.New("geocoding twogis: invalid default bias point")
	}
	return &TwoGIS{base: u, key: key, bias: bias, client: client}, nil
}

// twoGISLocale maps the app language to a 2GIS locale. 2GIS has no English
// locale for Kazakhstan, so English uses Russian names.
func twoGISLocale(lang string) string {
	if lang == "kk" {
		return "kk_KZ"
	}
	return "ru_KZ"
}

const twoGISFields = "items.point,items.adm_div,items.full_address_name"

type twoGISResponse struct {
	Meta struct {
		Code  int `json:"code"`
		Error *struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	} `json:"meta"`
	Result *struct {
		Items []twoGISItem `json:"items"`
	} `json:"result"`
}

type twoGISItem struct {
	Name            string `json:"name"`
	FullName        string `json:"full_name"`
	AddressName     string `json:"address_name"`
	FullAddressName string `json:"full_address_name"`
	Point           *struct {
		Lat float64 `json:"lat"`
		Lon float64 `json:"lon"`
	} `json:"point"`
	AdmDiv []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"adm_div"`
}

func lonLat(p geo.Point) string {
	return strconv.FormatFloat(p.Lon, 'f', 6, 64) + "," + strconv.FormatFloat(p.Lat, 'f', 6, 64)
}

// Search implements Geocoder.
func (g *TwoGIS) Search(ctx context.Context, query string, near *geo.Point, lang string, limit int) ([]Place, error) {
	loc := g.bias
	if near != nil {
		loc = *near
	}
	q := g.params(lang)
	q.Set("q", query)
	q.Set("location", lonLat(loc))
	q.Set("suggest_type", "route_endpoint")
	q.Set("page_size", strconv.Itoa(limit))
	places, err := g.get(ctx, "/3.0/suggests", q)
	if err != nil || len(places) > 0 {
		return places, err
	}
	q = g.params(lang)
	q.Set("q", query)
	q.Set("point", lonLat(loc))
	q.Set("radius", twoGISSearchRadiusM)
	q.Set("location", lonLat(loc))
	q.Set("page_size", strconv.Itoa(limit))
	return g.get(ctx, "/3.0/items/geocode", q)
}

// Reverse implements Geocoder.
func (g *TwoGIS) Reverse(ctx context.Context, p geo.Point, lang string) (*Place, error) {
	q := g.params(lang)
	q.Set("lat", strconv.FormatFloat(p.Lat, 'f', 6, 64))
	q.Set("lon", strconv.FormatFloat(p.Lon, 'f', 6, 64))
	q.Set("page_size", "1")
	places, err := g.get(ctx, "/3.0/items/geocode", q)
	if err != nil || len(places) == 0 {
		return nil, err
	}
	return &places[0], nil
}

func (g *TwoGIS) params(lang string) url.Values {
	q := url.Values{}
	q.Set("key", g.key)
	q.Set("locale", twoGISLocale(lang))
	q.Set("fields", twoGISFields)
	return q
}

func (g *TwoGIS) get(ctx context.Context, path string, q url.Values) ([]Place, error) {
	u := *g.base
	u.Path += path
	u.RawQuery = q.Encode()
	var resp twoGISResponse
	if err := getJSON(ctx, g.client, SourceTwoGIS, u.String(), &resp, http.StatusNotFound); err != nil {
		return nil, err
	}
	switch resp.Meta.Code {
	case http.StatusOK:
	case http.StatusNotFound: // "Object not found"
		return []Place{}, nil
	default:
		msg := ""
		if resp.Meta.Error != nil {
			msg = resp.Meta.Error.Type + ": " + resp.Meta.Error.Message
		}
		return nil, &providerError{SourceTwoGIS, fmt.Errorf("meta.code %d %s", resp.Meta.Code, msg)}
	}
	if resp.Result == nil {
		return []Place{}, nil
	}
	out := make([]Place, 0, len(resp.Result.Items))
	for _, it := range resp.Result.Items {
		if it.Point == nil {
			continue
		}
		var city, region string
		for _, d := range it.AdmDiv {
			switch d.Type {
			case "city", "settlement":
				city = firstNonEmpty(city, d.Name)
			case "region":
				region = firstNonEmpty(region, d.Name)
			}
		}
		title := firstNonEmpty(it.Name, it.AddressName, it.FullName)
		if title == "" {
			continue
		}
		out = append(out, Place{
			Title:    title,
			Subtitle: joinParts([]string{title}, it.AddressName, city, region),
			Point:    geo.Point{Lat: it.Point.Lat, Lon: it.Point.Lon},
			Source:   SourceTwoGIS,
		})
	}
	return out, nil
}
