package commissionareas

import (
	"testing"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/geo"
)

// A village mapped with its outline: the circle covers the outline from the
// centre plus a margin for the outskirts, and stops halfway to a neighbour.
func TestProposeFromOutlineAndNeighbours(t *testing.T) {
	c := geo.Point{Lat: 48.8300, Lon: 58.1500}
	// About 1.5 km × 1.5 km around the centre.
	village := Settlement{Name: "Ембі", Kind: "town", Point: c, Extent: &[4]float64{58.1400, 48.8367, 58.1600, 48.8233}}

	got := propose(c, []Settlement{village}, nil)
	if got.Basis != BasisExtent || got.Settlement == nil || got.Settlement.Name != "Ембі" || *got.Settlement.Kind != "town" {
		t.Fatalf("basis %+v", got)
	}
	if got.RadiusM < 1100 || got.RadiusM > 1500 || got.RadiusM%100 != 0 || got.Neighbor != nil {
		t.Fatalf("radius %d, neighbour %+v", got.RadiusM, got.Neighbor)
	}

	// A neighbouring village 2 km away: the circle meets it halfway.
	near := Settlement{Name: "Жаңажол", Kind: "village", Point: geo.Point{Lat: 48.8480, Lon: 58.1500}}
	got = propose(c, []Settlement{village, near}, nil)
	if got.Neighbor == nil || got.Neighbor.Name != "Жаңажол" || got.Neighbor.Source != "osm" || got.RadiusM != 1000 {
		t.Fatalf("with neighbour: %+v %+v", got, got.Neighbor)
	}

	// A configured area closer than the map's neighbour wins as the neighbour.
	got = propose(c, []Settlement{village, near}, &Neighbor{Name: "Қарауылкелді", DistanceM: 1400})
	if got.Neighbor.Name != "Қарауылкелді" || got.Neighbor.Source != "area" || got.RadiusM != 700 {
		t.Fatalf("area neighbour: %+v %+v", got, got.Neighbor)
	}
}

// A village mapped as a single point gets the typical reach of its kind;
// nothing known gives the default; very close neighbours never overlap.
func TestProposeFallbacks(t *testing.T) {
	c := geo.Point{Lat: 48.8300, Lon: 58.1500}
	node := Settlement{Name: "Шұбарқұдық", Kind: "village", Point: geo.Point{Lat: 48.8310, Lon: 58.1510}}
	if got := propose(c, []Settlement{node}, nil); got.Basis != BasisType || got.RadiusM != 2000 || got.Settlement.Name != "Шұбарқұдық" {
		t.Fatalf("node: %+v", got)
	}
	if got := propose(c, nil, nil); got.Basis != BasisDefault || got.RadiusM != 2500 || got.Settlement != nil || got.Neighbor != nil {
		t.Fatalf("nothing known: %+v", got)
	}
	// A settlement point farther than ownNodeM is a neighbour, not the village.
	far := Settlement{Name: "Сағыз", Kind: "village", Point: geo.Point{Lat: 48.9000, Lon: 58.1500}}
	if got := propose(c, []Settlement{far}, nil); got.Settlement != nil || got.Neighbor == nil || got.Neighbor.Name != "Сағыз" || got.RadiusM != 2500 {
		t.Fatalf("far settlement: %+v", got)
	}
	// Villages 600 m apart: half the distance, below the usual minimum.
	close := &Neighbor{Name: "Көрші", DistanceM: 600}
	if got := propose(c, nil, close); got.RadiusM != 300 {
		t.Fatalf("close neighbour: %+v", got)
	}
	// Never below the API minimum.
	if got := propose(c, nil, &Neighbor{Name: "Көрші", DistanceM: 100}); got.RadiusM != MinRadiusM {
		t.Fatalf("very close neighbour: %+v", got)
	}
}

// Of several outlines covering the centre the smallest is the village; an
// outline that does not cover it is a neighbour.
func TestOwnSettlementPrefersSmallestOutline(t *testing.T) {
	c := geo.Point{Lat: 48.8300, Lon: 58.1500}
	district := Settlement{Name: "Аудан", Kind: "city", Point: c, Extent: &[4]float64{57.9, 49.0, 58.4, 48.6}}
	village := Settlement{Name: "Ауыл", Kind: "village", Point: c, Extent: &[4]float64{58.14, 48.836, 58.16, 48.824}}
	other := Settlement{Name: "Басқа", Kind: "village", Point: geo.Point{Lat: 48.95, Lon: 58.15}, Extent: &[4]float64{58.14, 48.96, 58.16, 48.94}}
	if s := ownSettlement(c, []Settlement{district, village, other}); s == nil || s.Name != "Ауыл" {
		t.Fatalf("own = %+v", s)
	}
}
