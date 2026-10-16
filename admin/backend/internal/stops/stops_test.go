package stops

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

func route(n int) []Stop {
	out := make([]Stop, n)
	for i := range out {
		out[i] = Stop{ID: uuid.New(), Seq: i, Kind: KindFor(i, n), Lat: 48.8 + float64(i)/1000, Lon: 58.1}
	}
	return out
}

func TestKindFor(t *testing.T) {
	want := []string{KindPickup, KindIntermediate, KindIntermediate, KindIntermediate, KindDestination}
	for i, k := range want {
		if got := KindFor(i, 5); got != k {
			t.Errorf("KindFor(%d, 5) = %s, want %s", i, got, k)
		}
	}
	if KindFor(0, 2) != KindPickup || KindFor(1, 2) != KindDestination {
		t.Error("two-point route")
	}
}

func TestValidateCount(t *testing.T) {
	cases := []struct {
		n, max int
		reason string
	}{
		{1, 5, "too_few"}, {2, 5, ""}, {5, 5, ""}, {6, 5, "too_many"}, {4, 3, "too_many"}, {6, 0, "too_many"}, {5, 9, ""},
	}
	for _, c := range cases {
		got := ""
		if err := ValidateCount("stops", c.n, c.max); err != nil {
			got = httpx.AsError(err).Fields["stops"]
		}
		if got != c.reason {
			t.Errorf("ValidateCount(%d, %d) reason %q, want %q", c.n, c.max, got, c.reason)
		}
	}
}

func TestCheck(t *testing.T) {
	if err := Check(route(3)); err != nil {
		t.Fatalf("valid route: %v", err)
	}
	bad := route(3)
	bad[1].Kind = KindDestination
	if Check(bad) == nil {
		t.Error("wrong kind accepted")
	}
	gap := route(3)
	gap[2].Seq = 3
	if Check(gap) == nil {
		t.Error("seq gap accepted")
	}
	if Check(route(1)) == nil || Check(route(6)) == nil {
		t.Error("point count not checked")
	}
}

func TestNextStops(t *testing.T) {
	list := route(5)
	if n := NextIntermediate(list); n == nil || n.Seq != 1 {
		t.Fatalf("next = %+v", n)
	}
	now := time.Now()
	list[0].CompletedAt = &now
	list[1].CompletedAt = &now
	if n := NextIntermediate(list); n == nil || n.Seq != 2 {
		t.Fatalf("next after 1 = %+v", n)
	}
	if p := PendingIntermediate(list); len(p) != 2 || p[0].Seq != 2 || p[1].Seq != 3 {
		t.Fatalf("pending = %+v", p)
	}
	if n := NextPending(list); n == nil || n.Seq != 2 {
		t.Fatalf("next pending = %+v", n)
	}
	list[2].CompletedAt, list[3].CompletedAt = &now, &now
	if NextIntermediate(list) != nil || len(PendingIntermediate(list)) != 0 {
		t.Fatal("intermediate stops left")
	}
	if n := NextPending(list); n == nil || n.Kind != KindDestination {
		t.Fatalf("destination pending = %+v", n)
	}
	if NextIntermediate(route(2)) != nil {
		t.Fatal("two-point route has no intermediate stops")
	}
	if Find(list, list[3].ID) != &list[3] || Find(list, uuid.New()) != nil {
		t.Fatal("Find")
	}
	if Pickup(nil) != nil || Destination(nil) != nil || Pickup(list).Seq != 0 || Destination(list).Seq != 4 {
		t.Fatal("Pickup/Destination")
	}
}

func TestTextHelpers(t *testing.T) {
	if got := DisplayAddress("  Теміржол\tвокзалы ", 1, 2); got != "Теміржол вокзалы" {
		t.Errorf("DisplayAddress = %q", got)
	}
	if got := DisplayAddress("", 48.8264, 58.1442); got != "48.826400, 58.144200" {
		t.Errorf("coordinates = %q", got)
	}
	if _, err := CleanAddress(" "); err == nil {
		t.Error("blank address accepted")
	}
	if _, err := CleanAddress(strings.Repeat("ә", MaxAddressLen+1)); err == nil {
		t.Error("long address accepted")
	}
	blank := "  "
	if d, err := CleanDetails(&blank); d != nil || err != nil {
		t.Errorf("blank details = %v, %v", d, err)
	}
	if got := FieldName("stops", 1, "address"); got != "stops.1.address" {
		t.Errorf("FieldName = %q", got)
	}
	if got := truncate("әбвгд", 3); got != "әбв" {
		t.Errorf("truncate = %q", got)
	}
}
