package testutil

import (
	"context"
	"testing"
)

func TestNewDBAndFixtures(t *testing.T) {
	pool := NewDB(t)
	d := CreateDriver(t, pool, DriverOptions{Online: true, Balance: 1000})
	var balance int64
	var status string
	if err := pool.QueryRow(context.Background(),
		`SELECT w.balance, p.status FROM wallets w JOIN driver_profiles p ON p.id = w.driver_id WHERE w.id = $1`, d.WalletID).Scan(&balance, &status); err != nil {
		t.Fatal(err)
	}
	if balance != 1000 || status != "approved" || d.Code[:3] != "EMB" {
		t.Fatalf("unexpected fixture: balance=%d status=%s code=%s", balance, status, d.Code)
	}
	var inside bool
	if err := pool.QueryRow(context.Background(),
		`SELECT ST_Covers(geom_check, ST_SetSRID(ST_MakePoint(58.1442, 48.8264), 4326)) FROM country_boundaries WHERE code = 'KZ'`).Scan(&inside); err != nil || !inside {
		t.Fatalf("Embi should be inside KZ: %v %v", inside, err)
	}
}
