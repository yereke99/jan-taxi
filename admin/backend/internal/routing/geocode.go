package routing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/config"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/geo"
)

// Place is a geocoding result.
type Place struct {
	Title    string
	Subtitle string // locality / region line; may be empty
	Point    geo.Point
	Source   string // "place" (admin gazetteer) or the provider name
	// CountryCode is the ISO country reported by the provider ("" if unknown).
	CountryCode string
}

// Geocoder searches addresses and places and resolves a point to an address.
// Reverse returns (nil, nil) when the provider has no result.
type Geocoder interface {
	Search(ctx context.Context, query string, near *geo.Point, lang string, limit int) ([]Place, error)
	Reverse(ctx context.Context, p geo.Point, lang string) (*Place, error)
}

// Provider source names (Place.Source).
const (
	SourcePlace       = "place"
	SourceCoordinates = "coordinates"
	SourcePhoton      = "photon"
	SourceNominatim   = "nominatim"
	SourceTwoGIS      = "twogis"
)

// None is the geocoder used when GEOCODING_PROVIDER=none: only the admin
// gazetteer and coordinates are available.
type None struct{}

func (None) Search(context.Context, string, *geo.Point, string, int) ([]Place, error) {
	return nil, nil
}
func (None) Reverse(context.Context, geo.Point, string) (*Place, error) { return nil, nil }

// ProviderConfig selects and configures the external geocoder.
type ProviderConfig struct {
	Provider   string // config.Geocoder* constant
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
	// DefaultBias is used by providers that need a location when the caller
	// gives none (2GIS); usually the default city centre.
	DefaultBias geo.Point
}

// NewProvider builds the configured geocoder. It returns an error for an
// unknown provider or an unusable configuration (e.g. 2GIS without a key, or
// Nominatim pointed at the public OpenStreetMap server, whose usage policy
// forbids autocomplete).
func NewProvider(cfg ProviderConfig) (Geocoder, string, error) {
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{}
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Provider)) {
	case "", config.GeocoderNone:
		return None{}, config.GeocoderNone, nil
	case config.GeocoderPhoton:
		g, err := NewPhoton(cfg.BaseURL, client)
		return g, SourcePhoton, err
	case config.GeocoderNominatim:
		g, err := NewNominatim(cfg.BaseURL, client)
		return g, SourceNominatim, err
	case config.GeocoderTwoGIS:
		g, err := NewTwoGIS(cfg.BaseURL, cfg.APIKey, cfg.DefaultBias, client)
		return g, SourceTwoGIS, err
	default:
		return nil, "", fmt.Errorf("geocoding: unknown provider %q", cfg.Provider)
	}
}

// providerError is a failure that says the provider is unhealthy (network,
// timeout, 5xx, 429, rejected key); it feeds the circuit breaker.
type providerError struct {
	provider string
	err      error
}

func (e *providerError) Error() string { return e.provider + ": " + e.err.Error() }
func (e *providerError) Unwrap() error { return e.err }

const geocoderMaxResponse = 2 << 20

// getJSON performs a GET and decodes a JSON body into dst. okStatus lists
// non-200 statuses whose body is still decoded (e.g. 2GIS "not found").
func getJSON(ctx context.Context, client *http.Client, provider, endpoint string, dst any, okStatus ...int) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return &providerError{provider, err}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return &providerError{provider, scrubURL(err)}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, geocoderMaxResponse+1))
	if err != nil {
		return &providerError{provider, err}
	}
	if len(body) > geocoderMaxResponse {
		return &providerError{provider, errors.New("response too large")}
	}
	accepted := resp.StatusCode == http.StatusOK
	for _, s := range okStatus {
		accepted = accepted || resp.StatusCode == s
	}
	if !accepted {
		return &providerError{provider, fmt.Errorf("HTTP %d", resp.StatusCode)}
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return &providerError{provider, fmt.Errorf("decode response: %w", err)}
	}
	return nil
}

// scrubURL drops the request URL (which may carry an API key or a user's
// coordinates) from transport errors.
func scrubURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s request failed: %w", ue.Op, ue.Err)
	}
	return err
}

// parseBaseURL validates a provider base URL.
func parseBaseURL(provider, raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("geocoding %s: invalid base URL %q", provider, raw)
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawQuery, u.Fragment = "", ""
	return u, nil
}

// joinParts joins distinct non-empty parts with ", ", skipping any equal
// (case-insensitively) to one of the excluded strings.
func joinParts(exclude []string, parts ...string) string {
	seen := map[string]bool{}
	for _, e := range exclude {
		seen[strings.ToLower(strings.TrimSpace(e))] = true
	}
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		k := strings.ToLower(p)
		if p == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, p)
	}
	return strings.Join(out, ", ")
}

// firstNonEmpty returns the first non-blank value.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

// defaultProviderTimeout bounds one provider call inside a user request.
const defaultProviderTimeout = 1200 * time.Millisecond
