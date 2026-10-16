package cities

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"golang.org/x/text/unicode/norm"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/geo"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

// ErrPlaceNotFound is returned for unknown place ids.
var ErrPlaceNotFound = httpx.NewError(http.StatusNotFound, "not_found", "place not found")

// Place is an admin-curated named location (bus station, akimat, bazaar…).
type Place struct {
	ID        uuid.UUID
	CityID    *uuid.UUID
	Name      Name
	Aliases   []string
	Point     geo.Point
	IsActive  bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Title returns the place name in lang (Kazakh fallback).
func (p Place) Title(lang string) string {
	if t := p.Name.Get(lang); t != "" {
		return t
	}
	return p.Name.KK
}

type placeJSON struct {
	ID        uuid.UUID  `json:"id"`
	CityID    *uuid.UUID `json:"city_id"`
	Name      Name       `json:"name"`
	Aliases   []string   `json:"aliases"`
	Lat       float64    `json:"lat"`
	Lon       float64    `json:"lon"`
	IsActive  bool       `json:"is_active"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// MarshalJSON renders the API representation.
func (p Place) MarshalJSON() ([]byte, error) {
	aliases := p.Aliases
	if aliases == nil {
		aliases = []string{}
	}
	ll := toLatLon(p.Point)
	return json.Marshal(placeJSON{ID: p.ID, CityID: p.CityID, Name: p.Name, Aliases: aliases, Lat: ll.Lat, Lon: ll.Lon,
		IsActive: p.IsActive, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt})
}

const placeColumns = `p.id, p.city_id, p.name_kk, p.name_ru, p.name_en, p.aliases, p.lat, p.lon, p.is_active, p.created_at, p.updated_at`

func scanPlace(s scanner) (Place, error) {
	var p Place
	err := s.Scan(&p.ID, &p.CityID, &p.Name.KK, &p.Name.RU, &p.Name.EN, &p.Aliases, &p.Point.Lat, &p.Point.Lon,
		&p.IsActive, &p.CreatedAt, &p.UpdatedAt)
	return p, err
}

// GetPlace loads one place.
func GetPlace(ctx context.Context, q db.Querier, id uuid.UUID) (Place, error) {
	p, err := scanPlace(q.QueryRow(ctx, `SELECT `+placeColumns+` FROM places p WHERE p.id = $1`, id))
	if db.IsNoRows(err) {
		return Place{}, ErrPlaceNotFound
	}
	if err != nil {
		return Place{}, fmt.Errorf("load place: %w", err)
	}
	return p, nil
}

// PlaceFilter selects places for the admin list.
type PlaceFilter struct {
	CityID *uuid.UUID
	Query  string // case-insensitive substring of any name or alias
	Page   httpx.PageParams
}

// ListPlaces returns one page of places (active and inactive) and the total.
func ListPlaces(ctx context.Context, q db.Querier, f PlaceFilter) ([]Place, int, error) {
	var where []string
	var args []any
	if f.CityID != nil {
		args = append(args, *f.CityID)
		where = append(where, fmt.Sprintf("p.city_id = $%d", len(args)))
	}
	if s := strings.TrimSpace(f.Query); s != "" {
		args = append(args, "%"+escapeLike(s)+"%")
		n := len(args)
		where = append(where, fmt.Sprintf(`(p.name_kk ILIKE $%[1]d OR p.name_ru ILIKE $%[1]d OR p.name_en ILIKE $%[1]d
			OR EXISTS (SELECT 1 FROM unnest(p.aliases) a WHERE a ILIKE $%[1]d))`, n))
	}
	cond := ""
	if len(where) > 0 {
		cond = " WHERE " + strings.Join(where, " AND ")
	}
	var total int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM places p`+cond, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count places: %w", err)
	}
	args = append(args, f.Page.PageSize, f.Page.Offset())
	rows, err := q.Query(ctx, `SELECT `+placeColumns+` FROM places p`+cond+
		fmt.Sprintf(` ORDER BY p.name_kk, p.id LIMIT $%d OFFSET $%d`, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list places: %w", err)
	}
	defer rows.Close()
	out := []Place{}
	for rows.Next() {
		p, err := scanPlace(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan place: %w", err)
		}
		out = append(out, p)
	}
	return out, total, rows.Err()
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// SearchPlaces finds active places whose name (any language) or alias matches
// query, ignoring case, Unicode form and Kazakh-specific letters typed with
// their Russian look-alikes (ә→а, қ→к, ү→у…). Whole-name prefix matches rank
// first, then word-prefix matches, then substrings; ties go to the place
// nearer to near (when given), then by name in lang.
func SearchPlaces(ctx context.Context, q db.Querier, query string, near *geo.Point, lang string, limit int) ([]Place, error) {
	needle := Fold(query)
	if needle == "" {
		return []Place{}, nil
	}
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	rows, err := q.Query(ctx, `SELECT `+placeColumns+` FROM places p WHERE p.is_active`)
	if err != nil {
		return nil, fmt.Errorf("search places: %w", err)
	}
	defer rows.Close()
	type hit struct {
		place Place
		rank  int
		dist  float64
		title string
	}
	var hits []hit
	for rows.Next() {
		p, err := scanPlace(rows)
		if err != nil {
			return nil, fmt.Errorf("scan place: %w", err)
		}
		rank := -1
		for _, s := range append([]string{p.Name.KK, p.Name.RU, p.Name.EN}, p.Aliases...) {
			if r := matchRank(Fold(s), needle); r >= 0 && (rank < 0 || r < rank) {
				rank = r
			}
		}
		if rank < 0 {
			continue
		}
		h := hit{place: p, rank: rank, title: Fold(p.Title(lang))}
		if near != nil {
			h.dist = geo.DistanceMeters(*near, p.Point)
		}
		hits = append(hits, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("search places: %w", err)
	}
	slices.SortFunc(hits, func(a, b hit) int {
		return cmp.Or(cmp.Compare(a.rank, b.rank), cmp.Compare(a.dist, b.dist), strings.Compare(a.title, b.title))
	})
	out := make([]Place, 0, min(limit, len(hits)))
	for i := 0; i < len(hits) && i < limit; i++ {
		out = append(out, hits[i].place)
	}
	return out, nil
}

// matchRank: 0 whole-string prefix, 1 word prefix, 2 substring, -1 no match.
func matchRank(hay, needle string) int {
	switch {
	case hay == "":
		return -1
	case strings.HasPrefix(hay, needle):
		return 0
	case strings.Contains(" "+hay, " "+needle):
		return 1
	case strings.Contains(hay, needle):
		return 2
	}
	return -1
}

// kkFold maps Kazakh-specific and other easily substituted letters to the
// letter people type on a Russian keyboard.
var kkFold = map[rune]rune{
	'ә': 'а', 'ғ': 'г', 'қ': 'к', 'ң': 'н', 'ө': 'о', 'ұ': 'у', 'ү': 'у', 'һ': 'х', 'і': 'и', 'ё': 'е',
}

// Fold normalises text for matching: NFC, lower case, Kazakh letters folded,
// punctuation turned into spaces, whitespace collapsed.
func Fold(s string) string {
	s = strings.ToLower(norm.NFC.String(s))
	var b strings.Builder
	space := true
	for _, r := range s {
		if f, ok := kkFold[r]; ok {
			r = f
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			space = false
			continue
		}
		if !space {
			b.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(b.String())
}
