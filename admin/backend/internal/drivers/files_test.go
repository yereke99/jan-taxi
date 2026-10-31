package drivers_test

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/drivers/driverstest"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/storage"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

func isPrivateJPEG(res driverstest.Response) bool {
	return res.Code == http.StatusOK && res.Header.Get("Content-Type") == "image/jpeg" &&
		res.Header.Get("Cache-Control") == "private, no-store" && bytes.HasPrefix(res.Raw, []byte{0xFF, 0xD8})
}

func fileID(t *testing.T, v any, path ...string) string {
	t.Helper()
	for _, p := range path {
		m, ok := v.(map[string]any)
		if !ok {
			t.Fatalf("no %q in %v", p, v)
		}
		v = m[p]
	}
	s, ok := v.(string)
	if !ok {
		t.Fatalf("file id missing at %v", path)
	}
	return s
}

func TestDriverFileAccess(t *testing.T) {
	env := driverstest.New(t)
	ctx := context.Background()
	owner, other := env.DriverClient(), env.DriverClient()
	ownerID := owner.CompleteDraft(driverstest.Registration{Plate: "123ABC04"})
	other.CompleteDraft(driverstest.Registration{Plate: "124ABC04"})
	// ID scans and a vehicle photo from before they were retired stay viewable.
	owner.LegacyEvidence(ownerID)

	reg := owner.Do(http.MethodGet, pathRegistration, nil).Body
	avatar := fileID(t, reg, "avatar", "id")
	front := fileID(t, reg, "documents", "id_front", "id")
	photo := fileID(t, reg["vehicle"].(map[string]any)["photos"].([]any)[0], "id")
	receipt, err := env.Store.SaveImage(ctx, env.Pool, owner.UserID, storage.PurposeTopupReceipt,
		bytes.NewReader(driverstest.JPEG(t, 24, 24, 5)))
	if err != nil {
		t.Fatal(err)
	}

	// The owner downloads every own file as a private JPEG.
	for _, id := range []string{avatar, front, photo, receipt.ID.String()} {
		if res := owner.Do(http.MethodGet, "/api/v1/driver/files/"+id, nil); !isPrivateJPEG(res) {
			t.Fatalf("owner file %s: %d %v", id, res.Code, res.Header)
		}
	}
	// Anyone else gets 404, whatever the id.
	for _, id := range []string{avatar, front, photo, receipt.ID.String(), uuid.NewString(), "not-a-uuid"} {
		if res := other.Do(http.MethodGet, "/api/v1/driver/files/"+id, nil); res.Code != http.StatusNotFound {
			t.Fatalf("other driver reads %s: %s", id, res)
		}
	}
	// A valid token of the other app is 403 forbidden (OpenAPI "wrong app for the route").
	if res := env.AppClient(owner.UserID, auth.AppPassenger).Do(http.MethodGet, "/api/v1/driver/files/"+front, nil); res.Code != http.StatusForbidden || res.ErrorCode() != "forbidden" {
		t.Fatalf("passenger token on driver file: %s", res)
	}

	// Admin access depends on the file purpose.
	const ok, denied = http.StatusOK, http.StatusForbidden
	files := map[string]string{"id_front": front, "avatar": avatar, "vehicle_photo": photo, "receipt": receipt.ID.String()}
	matrix := []struct {
		role auth.Role
		want map[string]int
	}{
		{auth.RoleSuperAdmin, map[string]int{"id_front": ok, "avatar": ok, "vehicle_photo": ok, "receipt": ok}},
		{auth.RoleDriverModerator, map[string]int{"id_front": ok, "avatar": ok, "vehicle_photo": ok, "receipt": denied}},
		{auth.RoleOperator, map[string]int{"id_front": denied, "avatar": ok, "vehicle_photo": ok, "receipt": denied}},
		{auth.RoleFinanceManager, map[string]int{"id_front": denied, "avatar": ok, "vehicle_photo": ok, "receipt": ok}},
		{auth.RoleAuditor, map[string]int{"id_front": denied, "avatar": ok, "vehicle_photo": ok, "receipt": ok}},
	}
	viewers := map[auth.Role]uuid.UUID{}
	for _, tt := range matrix {
		admin := env.AdminClient(tt.role)
		viewers[tt.role] = admin.Admin
		for name, id := range files {
			res := admin.Do(http.MethodGet, "/api/v1/admin/files/"+id, nil)
			if res.Code != tt.want[name] || (res.Code == ok && !isPrivateJPEG(res)) {
				t.Errorf("%s reads %s: %d, want %d", tt.role, name, res.Code, tt.want[name])
			}
		}
		if res := admin.Do(http.MethodGet, "/api/v1/admin/files/"+uuid.NewString(), nil); res.Code != http.StatusNotFound {
			t.Errorf("%s reads unknown file: %s", tt.role, res)
		}
	}

	// Every ID-document view is audited against the driver; refused views and
	// other files are not.
	if n := env.Count(`SELECT count(*) FROM admin_audit_logs WHERE action = 'document.view'`); n != 2 {
		t.Fatalf("document views audited: %d, want 2", n)
	}
	for _, role := range []auth.Role{auth.RoleSuperAdmin, auth.RoleDriverModerator} {
		if n := env.Count(`
			SELECT count(*) FROM admin_audit_logs
			WHERE action = 'document.view' AND entity_type = 'driver' AND entity_id = $1 AND admin_id = $2
			  AND after->>'file_id' = $3 AND after->>'purpose' = 'id_front'`,
			ownerID.String(), viewers[role], front); n != 1 {
			t.Errorf("%s document view audit rows: %d", role, n)
		}
	}
}

func TestPassengerDriverPhoto(t *testing.T) {
	env := driverstest.New(t)
	ctx := context.Background()
	d := testutil.CreateDriver(t, env.Pool, testutil.DriverOptions{Online: true})
	passengerID, _ := testutil.CreateUser(t, env.Pool)
	strangerID, _ := testutil.CreateUser(t, env.Pool)
	passenger := env.AppClient(passengerID, auth.AppPassenger)
	order := env.CreateOrder(passengerID, d.DriverID, "accepted")
	path := "/api/v1/orders/" + order.String() + "/driver-photo"

	// No avatar on file yet.
	if res := passenger.Do(http.MethodGet, path, nil); res.Code != http.StatusNotFound {
		t.Fatalf("driver without avatar: %s", res)
	}
	f, err := env.Store.SaveImage(ctx, env.Pool, d.UserID, storage.PurposeDriverAvatar, bytes.NewReader(driverstest.JPEG(t, 32, 32, 3)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.Pool.Exec(ctx, `UPDATE driver_profiles SET avatar_file_id = $2 WHERE id = $1`, d.DriverID, f.ID); err != nil {
		t.Fatal(err)
	}
	if res := passenger.Do(http.MethodGet, path, nil); !isPrivateJPEG(res) {
		t.Fatalf("passenger reads assigned driver's photo: %d %v", res.Code, res.Header)
	}
	// Past orders keep the photo available to their passenger.
	if _, err := env.Pool.Exec(ctx, `UPDATE orders SET status = 'completed', completed_at = now() WHERE id = $1`, order); err != nil {
		t.Fatal(err)
	}
	if res := passenger.Do(http.MethodGet, path, nil); !isPrivateJPEG(res) {
		t.Fatalf("photo after completion: %d", res.Code)
	}
	// Other passengers, orders without a driver, unknown orders and driver tokens get nothing.
	if res := env.AppClient(strangerID, auth.AppPassenger).Do(http.MethodGet, path, nil); res.Code != http.StatusNotFound {
		t.Fatalf("stranger: %s", res)
	}
	searching := env.CreateOrder(passengerID, uuid.Nil, "searching")
	if res := passenger.Do(http.MethodGet, "/api/v1/orders/"+searching.String()+"/driver-photo", nil); res.Code != http.StatusNotFound {
		t.Fatalf("order without driver: %s", res)
	}
	if res := passenger.Do(http.MethodGet, "/api/v1/orders/"+uuid.NewString()+"/driver-photo", nil); res.Code != http.StatusNotFound {
		t.Fatalf("unknown order: %s", res)
	}
	if res := env.AppClient(passengerID, auth.AppDriver).Do(http.MethodGet, path, nil); res.Code == http.StatusOK {
		t.Fatalf("driver token on passenger route: %s", res)
	}
}
