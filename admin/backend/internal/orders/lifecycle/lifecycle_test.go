package lifecycle

import "testing"

func TestDriverInfoLabels(t *testing.T) {
	d := DriverInfo{Code: "EMB001", FirstName: " Ерлан ", LastName: "Бекенов", Make: "Toyota", Model: "Camry", Color: "Ақ", Plate: "123ABC04"}
	if got := d.Name(); got != "Ерлан Б." {
		t.Errorf("Name = %q", got)
	}
	if got := d.Vehicle(); got != "Toyota Camry, Ақ" {
		t.Errorf("Vehicle = %q", got)
	}
	if got := (DriverInfo{Code: "EMB002"}).Name(); got != "EMB002" {
		t.Errorf("Name without names = %q", got)
	}
	if got := (DriverInfo{Color: "Қара"}).Vehicle(); got != "Қара" {
		t.Errorf("Vehicle colour only = %q", got)
	}
	if got := (DriverInfo{}).Vehicle(); got != "—" {
		t.Errorf("empty Vehicle = %q", got)
	}
	if got := (DriverInfo{}).PlateLabel(); got != "—" {
		t.Errorf("empty PlateLabel = %q", got)
	}
}

func TestStatusAndCategoryHelpers(t *testing.T) {
	for _, s := range ActiveStatuses {
		if !IsActive(s) || !ValidStatus(s) {
			t.Errorf("%s should be active", s)
		}
	}
	if IsActive(StatusCompleted) || IsActive(StatusDisputed) || OccupiesDriver(StatusSearching) || !OccupiesDriver(StatusInProgress) {
		t.Error("active/occupying sets")
	}
	if ValidStatus("paid") || ValidCategory("cargo") || ValidPaymentMethod("card") {
		t.Error("unknown values accepted")
	}
	cases := []struct{ category, service, cargo string }{
		{CategoryTaxi, ServicePassenger, CargoNone},
		{CategoryParcel, ServiceDelivery, CargoParcel},
		{CategoryOther, ServiceDelivery, CargoOther},
	}
	for _, c := range cases {
		if ServiceType(c.category) != c.service || CargoType(c.category) != c.cargo {
			t.Errorf("%s → %s/%s", c.category, ServiceType(c.category), CargoType(c.category))
		}
	}
}

func TestErrors(t *testing.T) {
	e := InvalidTransition(StatusCompleted, "cancel")
	if e.Status != 409 || e.Code != "invalid_transition" || e.Details["from"] != StatusCompleted || e.Details["action"] != "cancel" {
		t.Errorf("InvalidTransition = %+v", e)
	}
	ib := InsufficientBalance(100, 40, 500)
	if ib.Status != 402 || ib.Details["required"] != int64(100) || ib.Details["available"] != int64(40) || ib.Details["currency"] != "KZT" {
		t.Errorf("InsufficientBalance = %+v", ib)
	}
	if ErrDriverOffline().Status != 409 || ErrDriverNotApproved().Status != 403 || ErrOrderTaken().Code != "order_already_taken" {
		t.Error("accept errors")
	}
}

// TestViaTextKeepsOrderAndHidesCoordinates: intermediate stops are joined in
// route order; a stop with no address or bare coordinates is the placeholder.
func TestViaTextKeepsOrderAndHidesCoordinates(t *testing.T) {
	r := Route{Via: []string{"Базар", "", "48.826400, 58.144200", "Мектеп №3"}}
	if got := r.ViaText("…"); got != "Базар → … → … → Мектеп №3" {
		t.Fatalf("via = %q", got)
	}
	if got := (Route{}).ViaText("…"); got != "" {
		t.Fatalf("no stops = %q", got)
	}
}
