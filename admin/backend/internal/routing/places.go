package routing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/cities"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/geo"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/textutil"
)

// PlacesConfig configures NewPlaces.
type PlacesConfig struct {
	Provider     Geocoder // None{} when nil
	ProviderName string
	Timeout      time.Duration // per provider call; 2.5 s when zero
	CacheTTL     time.Duration // 15 minutes when zero; negative disables
	CacheSize    int           // 2000 when zero
	// The breaker opens after BreakerThreshold consecutive provider failures
	// (default 5) and stays open for BreakerCooldown (default 30 s).
	BreakerThreshold int
	BreakerCooldown  time.Duration
	Log              *slog.Logger
}

// Places answers /geo/search and /geo/reverse: admin-curated places first,
// then the external provider, and only points inside Kazakhstan.
type Places struct {
	q        db.Querier
	provider Geocoder
	name     string
	timeout  time.Duration
	breaker  *breaker
	search   *ttlCache[string, []Place]
	reverse  *ttlCache[string, *Place]
	log      *slog.Logger
}

// Distances used when matching places.
const (
	reverseGazetteerRadiusM = 75.0  // a curated place this close names a map pin
	dupAnyTitleM            = 30.0  // results this close are the same spot
	dupSameTitleM           = 300.0 // same name this close is the same object
)

// NewPlaces builds the search/reverse service on q (the API pool).
func NewPlaces(q db.Querier, cfg PlacesConfig) *Places {
	s := &Places{q: q, provider: cfg.Provider, name: cfg.ProviderName, timeout: cfg.Timeout, log: cfg.Log}
	if s.provider == nil {
		s.provider = None{}
	}
	if s.name == "" {
		s.name = "none"
	}
	if s.timeout <= 0 {
		s.timeout = defaultProviderTimeout
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	ttl, size := cfg.CacheTTL, cfg.CacheSize
	if ttl == 0 {
		ttl = 15 * time.Minute
	}
	if size <= 0 {
		size = 2000
	}
	if ttl > 0 {
		s.search = newTTLCache[string, []Place](ttl, size)
		s.reverse = newTTLCache[string, *Place](ttl, size)
	}
	threshold, cooldown := cfg.BreakerThreshold, cfg.BreakerCooldown
	if threshold <= 0 {
		threshold = 5
	}
	if cooldown <= 0 {
		cooldown = 30 * time.Second
	}
	s.breaker = newBreaker(threshold, cooldown)
	return s
}

// Search returns up to limit places matching query, curated places first.
// A failing provider is tolerated: the curated results are still returned.
func (s *Places) Search(ctx context.Context, query string, near *geo.Point, lang string, limit int) ([]Place, error) {
	curated, err := cities.SearchPlaces(ctx, s.q, query, near, lang, limit)
	if err != nil {
		return nil, err
	}
	results, err := s.fromGazetteer(ctx, curated, lang)
	if err != nil {
		return nil, err
	}
	if len(results) < limit {
		results = append(results, s.providerSearch(ctx, query, near, lang, limit)...)
	}
	inside, err := inKazakhstan(ctx, s.q, results)
	if err != nil {
		return nil, err
	}
	kept := results[:0]
	for i, p := range results {
		if inside[i] {
			kept = append(kept, p)
		}
	}
	out := localFirst(dedupe(kept), near)
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Local search (passenger near a position).
const (
	// nearRadiusM keeps results within this distance of the position.
	nearRadiusM = 60_000.0
	// nearFallback is how many of the nearest results are kept when none is
	// within nearRadiusM (e.g. the passenger searches another city).
	nearFallback = 5
	// rankStepM: this much extra distance costs one place in the ranking, so
	// a nearby match beats a slightly better-ranked one across town.
	rankStepM = 5_000.0
)

// localFirst orders results by provider rank combined with the distance to
// near and drops results farther than nearRadiusM; if that leaves nothing,
// the nearestFallback closest results are kept. Without a position the
// provider order is kept.
func localFirst(in []Place, near *geo.Point) []Place {
	if near == nil || len(in) == 0 {
		return in
	}
	type scored struct {
		p     Place
		dist  float64
		score float64
	}
	all := make([]scored, len(in))
	for i, p := range in {
		d := geo.DistanceMeters(*near, p.Point)
		all[i] = scored{p: p, dist: d, score: float64(i) + d/rankStepM}
	}
	var kept []scored
	for _, s := range all {
		if s.dist <= nearRadiusM {
			kept = append(kept, s)
		}
	}
	if len(kept) == 0 {
		sort.SliceStable(all, func(a, b int) bool { return all[a].dist < all[b].dist })
		kept = all[:min(nearFallback, len(all))]
	}
	sort.SliceStable(kept, func(a, b int) bool { return kept[a].score < kept[b].score })
	out := make([]Place, len(kept))
	for i, s := range kept {
		out[i] = s.p
	}
	return out
}

// ReverseResult is the answer of Reverse.
type ReverseResult struct {
	Place
	InServiceArea bool
}

// Reverse names the point p. It never fails because the provider has
// nothing: the last resort is Source "coordinates" with an empty title (the
// apps show their own "point on the map" label; coordinates stay in the
// lat/lon fields and are never presented as an address).
func (s *Places) Reverse(ctx context.Context, p geo.Point, lang string) (ReverseResult, error) {
	p = p.Rounded()
	inArea, err := geo.InServiceArea(ctx, s.q, p)
	if err != nil {
		return ReverseResult{}, err
	}
	res := ReverseResult{InServiceArea: inArea}
	if !inArea {
		res.Place = coordinatesPlace(p, msgOutsideArea.get(lang))
		return res, nil
	}
	if pl, err := s.nearestCurated(ctx, p, lang); err != nil {
		return ReverseResult{}, err
	} else if pl != nil {
		res.Place = *pl
		res.Point = p
		return res, nil
	}
	if pl := s.providerReverse(ctx, p, lang); pl != nil {
		res.Place = *pl
		res.Point = p
		return res, nil
	}
	res.Place = coordinatesPlace(p, msgMapPoint.get(lang))
	return res, nil
}

// coordinatesPlace is the unnamed point: no title, a localized subtitle.
func coordinatesPlace(p geo.Point, subtitle string) Place {
	return Place{Subtitle: subtitle, Point: p, Source: SourceCoordinates}
}

// fromGazetteer converts curated places; the subtitle is the city name.
func (s *Places) fromGazetteer(ctx context.Context, in []cities.Place, lang string) ([]Place, error) {
	names := map[uuid.UUID]string{}
	out := make([]Place, 0, len(in))
	for _, cp := range in {
		sub := ""
		if cp.CityID != nil {
			name, ok := names[*cp.CityID]
			if !ok {
				c, err := cities.Get(ctx, s.q, *cp.CityID)
				if err != nil && !errors.Is(err, cities.ErrNotFound) {
					return nil, err
				}
				name = c.Name.Get(lang)
				names[*cp.CityID] = name
			}
			sub = name
		}
		out = append(out, Place{Title: cp.Title(lang), Subtitle: sub, Point: cp.Point, Source: SourcePlace})
	}
	return out, nil
}

// nearestCurated returns the closest active curated place within
// reverseGazetteerRadiusM of p, if any.
func (s *Places) nearestCurated(ctx context.Context, p geo.Point, lang string) (*Place, error) {
	const dLat, dLon = 0.001, 0.0015 // ~110 m; prefilter only
	rows, err := s.q.Query(ctx, `
		SELECT name_kk, name_ru, name_en, city_id, lat, lon FROM places
		WHERE is_active AND lat BETWEEN $1 AND $2 AND lon BETWEEN $3 AND $4`,
		p.Lat-dLat, p.Lat+dLat, p.Lon-dLon, p.Lon+dLon)
	if err != nil {
		return nil, fmt.Errorf("nearest place: %w", err)
	}
	defer rows.Close()
	var (
		best     *cities.Place
		bestDist = reverseGazetteerRadiusM
	)
	for rows.Next() {
		var cp cities.Place
		if err := rows.Scan(&cp.Name.KK, &cp.Name.RU, &cp.Name.EN, &cp.CityID, &cp.Point.Lat, &cp.Point.Lon); err != nil {
			return nil, fmt.Errorf("scan place: %w", err)
		}
		if d := geo.DistanceMeters(p, cp.Point); d <= bestDist {
			best, bestDist = &cp, d
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("nearest place: %w", err)
	}
	if best == nil {
		return nil, nil
	}
	conv, err := s.fromGazetteer(ctx, []cities.Place{*best}, lang)
	if err != nil {
		return nil, err
	}
	return &conv[0], nil
}

func (s *Places) providerSearch(ctx context.Context, query string, near *geo.Point, lang string, limit int) []Place {
	if _, ok := s.provider.(None); ok {
		return nil
	}
	nearKey := "-"
	if near != nil {
		nearKey = strconv.FormatFloat(near.Lat, 'f', 3, 64) + "," + strconv.FormatFloat(near.Lon, 'f', 3, 64)
	}
	key := strings.Join([]string{lang, strconv.Itoa(limit), nearKey, strings.ToLower(textutil.Clean(query))}, "|")
	if v, ok := s.search.get(key); ok {
		return append([]Place(nil), v...)
	}
	var out []Place
	ok := s.call(ctx, "search", func(cctx context.Context) error {
		var err error
		out, err = s.provider.Search(cctx, query, near, lang, limit)
		return err
	})
	if !ok {
		return nil
	}
	s.search.set(key, append([]Place(nil), out...))
	return out
}

func (s *Places) providerReverse(ctx context.Context, p geo.Point, lang string) *Place {
	return s.providerReverseWithin(ctx, p, lang, s.timeout)
}

// LocalityTimeout bounds the reverse lookup that names the locality of a
// pickup during a route preview.
const LocalityTimeout = 1500 * time.Millisecond

// localityLang is the language localities are stored in (one per order):
// Kazakh, which local-name providers answer with the official names.
const localityLang = "kk"

// maxLocalityLen matches the orders.locality check constraint.
const maxLocalityLen = 120

// Locality returns the settlement of p (city, town or village, else the
// district) from a reverse lookup of the nearest address at the external
// geocoder, cached and bounded by LocalityTimeout. It returns nil when the provider is off, fails,
// times out or has no answer; it never returns coordinates.
//
// A provider that can name the settlement covering p (SettlementFinder) is
// asked at the same time, within the same budget, and its answer wins: it
// is the settlement's own official name («Ембі»), where the nearest address
// may carry another form («Эмба»).
func (s *Places) Locality(ctx context.Context, p geo.Point) *string {
	p = p.Rounded()
	timeout := min(s.timeout, LocalityTimeout)
	sf, ok := s.provider.(SettlementFinder)
	if !ok {
		return s.addressLocality(ctx, p, timeout)
	}
	key := settlementKey(p)
	if v, hit := s.reverse.get(key); hit {
		if v != nil {
			if name := CleanLocality(v.Locality); name != nil {
				return name
			}
		}
		return s.addressLocality(ctx, p, timeout)
	}
	actx, cancel := context.WithCancel(ctx)
	defer cancel()
	address := make(chan *string, 1)
	go func() { address <- s.addressLocality(actx, p, timeout) }()
	if v := s.settlement(ctx, sf, key, p, timeout); v != nil {
		return v
	}
	return <-address
}

func settlementKey(p geo.Point) string {
	return "settlement|" + localityLang + "|" + strconv.FormatFloat(p.Lat, 'f', 5, 64) + "," + strconv.FormatFloat(p.Lon, 'f', 5, 64)
}

// addressLocality is the locality of the nearest address to p.
func (s *Places) addressLocality(ctx context.Context, p geo.Point, timeout time.Duration) *string {
	pl := s.providerReverseWithin(ctx, p, localityLang, timeout)
	if pl == nil {
		return nil
	}
	return CleanLocality(pl.Locality)
}

// settlement asks the provider for the settlement covering p and caches the
// answer under key like reverse lookups (also "none"); nil when none covers
// p or the provider failed.
func (s *Places) settlement(ctx context.Context, sf SettlementFinder, key string, p geo.Point, timeout time.Duration) *string {
	var name string
	ok := s.callWithin(ctx, "settlement", timeout, func(cctx context.Context) error {
		var err error
		name, err = sf.Settlement(cctx, p, localityLang)
		return err
	})
	if !ok {
		return nil
	}
	var found *Place
	if name != "" {
		found = &Place{Locality: name}
	}
	s.reverse.set(key, found)
	return CleanLocality(name)
}

// coordsLikeRe matches text made of numbers, separators and hemisphere
// letters with at least one digit, such as "43.2389, 76.8897" or
// "43.2° N 76.9° E": a locality is a name, never coordinates.
var coordsLikeRe = regexp.MustCompile(`^[\s.,;:°'"+\-NSEWnsew]*\d[\d\s.,;:°'"+\-NSEWnsew]*$`)

// CleanLocality normalises a locality name: cleaned text cut to 120 code
// points, nil when empty or when it looks like coordinates.
func CleanLocality(name string) *string {
	v := textutil.Clean(name)
	if v == "" || coordsLikeRe.MatchString(v) {
		return nil
	}
	if r := []rune(v); len(r) > maxLocalityLen {
		v = strings.TrimSpace(string(r[:maxLocalityLen]))
	}
	return &v
}

func (s *Places) providerReverseWithin(ctx context.Context, p geo.Point, lang string, timeout time.Duration) *Place {
	if _, ok := s.provider.(None); ok {
		return nil
	}
	key := lang + "|" + strconv.FormatFloat(p.Lat, 'f', 5, 64) + "," + strconv.FormatFloat(p.Lon, 'f', 5, 64)
	if v, ok := s.reverse.get(key); ok {
		return copyPlace(v)
	}
	var out *Place
	ok := s.callWithin(ctx, "reverse", timeout, func(cctx context.Context) error {
		var err error
		out, err = s.provider.Reverse(cctx, p, lang)
		return err
	})
	if !ok {
		return nil
	}
	if out != nil && out.CountryCode != "" && out.CountryCode != "KZ" {
		out = nil
	}
	s.reverse.set(key, copyPlace(out))
	return out
}

func copyPlace(p *Place) *Place {
	if p == nil {
		return nil
	}
	c := *p
	return &c
}

// call runs fn through the circuit breaker with the provider timeout and
// reports whether it succeeded. Failures are logged, never returned.
func (s *Places) call(ctx context.Context, op string, fn func(context.Context) error) bool {
	return s.callWithin(ctx, op, s.timeout, fn)
}

// callWithin is call with its own timeout.
func (s *Places) callWithin(ctx context.Context, op string, timeout time.Duration, fn func(context.Context) error) bool {
	if !s.breaker.allow() {
		return false
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	err := fn(cctx)
	switch {
	case err == nil:
		s.breaker.success()
		return true
	case ctx.Err() != nil: // the client went away; says nothing about the provider
		s.breaker.abort()
	default:
		s.breaker.failure()
		s.log.Warn("geocoding provider failed", slog.String("provider", s.name), slog.String("op", op),
			slog.Any("err", err), slog.Bool("breaker_open", s.breaker.open()))
	}
	return false
}

// inKazakhstanSQL returns the 1-based ordinals of the points inside the
// Kazakhstan service-area boundary.
const inKazakhstanSQL = `
SELECT p.i
FROM unnest($1::float8[], $2::float8[]) WITH ORDINALITY AS p(lat, lon, i)
WHERE EXISTS (
    SELECT 1 FROM country_boundaries b
    WHERE b.code = 'KZ' AND ST_Covers(b.geom_check, ST_SetSRID(ST_MakePoint(p.lon, p.lat), 4326))
)`

// inKazakhstan checks all places in one query; result[i] is true when
// places[i] lies inside Kazakhstan. Invalid coordinates are outside.
func inKazakhstan(ctx context.Context, q db.Querier, places []Place) ([]bool, error) {
	out := make([]bool, len(places))
	var lats, lons []float64
	var idx []int
	for i, p := range places {
		if p.Point.Valid() != nil {
			continue
		}
		lats = append(lats, p.Point.Lat)
		lons = append(lons, p.Point.Lon)
		idx = append(idx, i)
	}
	if len(idx) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, inKazakhstanSQL, lats, lons)
	if err != nil {
		return nil, fmt.Errorf("service area filter: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var ord int64
		if err := rows.Scan(&ord); err != nil {
			return nil, fmt.Errorf("service area filter: %w", err)
		}
		if ord >= 1 && int(ord) <= len(idx) {
			out[idx[ord-1]] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("service area filter: %w", err)
	}
	return out, nil
}

// dedupe drops near-duplicates, keeping the first (higher-priority) result.
func dedupe(in []Place) []Place {
	out := make([]Place, 0, len(in))
	folded := make([]string, 0, len(in))
next:
	for _, p := range in {
		f := cities.Fold(p.Title)
		for i, k := range out {
			d := geo.DistanceMeters(p.Point, k.Point)
			if d <= dupAnyTitleM || (d <= dupSameTitleM && f == folded[i]) {
				continue next
			}
		}
		out = append(out, p)
		folded = append(folded, f)
	}
	return out
}

// localized is a short UI string in the three app languages.
type localized struct{ kk, ru, en string }

func (l localized) get(lang string) string {
	switch lang {
	case "ru":
		return l.ru
	case "en":
		return l.en
	default:
		return l.kk
	}
}

var (
	msgMapPoint    = localized{kk: "Картадағы нүкте", ru: "Точка на карте", en: "Point on the map"}
	msgOutsideArea = localized{kk: "Қызмет көрсету аймағынан тыс", ru: "Вне зоны обслуживания", en: "Outside the service area"}
)
