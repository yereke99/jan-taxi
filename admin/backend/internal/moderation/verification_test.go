package moderation_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/drivers/driverstest"
	"github.com/yereke99/jantaxi/admin/backend/internal/moderation"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/verification"
	"net/http"
	"sync"
	"testing"
	"time"
)

func verificationOptions() verification.Options {
	return verification.Options{Enabled: true, Key: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{33}, 32)), AutoApprove: true, MinConfidence: 98, QueueLimit: 1000}
}

var syntheticIdentity = verification.Input{DocumentType: "id_card", IIN: "999999999990", DocumentNumber: "001234567"}

type matchingEngine struct{}

func (matchingEngine) Read(_ context.Context, j verification.Job) (verification.Result, error) {
	return verification.Result{Extracted: j.Snapshot.Input, FirstName: j.Snapshot.FirstName, LastName: j.Snapshot.LastName, DocumentType: j.Snapshot.DocumentType, Fields: map[string]float64{"document_type": 99, "iin": 99, "document_number": 99, "first_name": 99, "last_name": 99, "patronymic": 99}}, nil
}
func TestVerificationAutoApprovalPreservesFinancialEffectsAndRedacts(t *testing.T) {
	env := driverstest.New(t, verificationOptions())
	c := env.DriverClient()
	id := c.CompleteDraft(driverstest.Registration{Phone: "+77015550105", Plate: "105AAA04"})
	saved := c.Do(http.MethodPut, pathRegistration, map[string]any{"first_name": "Ерлан", "last_name": "Бекенов", "vehicle": map[string]any{"make": "Toyota", "model": "Camry", "color": "Ақ", "plate": "105AAA04"}, "identity": syntheticIdentity})
	if saved.Code != 200 {
		t.Fatal(saved)
	}
	if res := c.Submit(); res.Code != 200 {
		t.Fatal(res)
	}
	if n := env.Count(`SELECT count(*) FROM driver_verification_jobs WHERE driver_id=$1 AND status='pending'`, id); n != 1 {
		t.Fatal("no durable job")
	}
	if n := env.Count(`SELECT count(*) FROM driver_applications WHERE snapshot::text LIKE '%999999999990%' OR snapshot::text LIKE '%001234567%'`); n != 0 {
		t.Fatal("identity leaked into historical JSON")
	}
	mh := moderation.NewHandler(env.Pool).WithVerification(env.Verification)
	did, err := env.Verification.RunOnce(context.Background(), matchingEngine{}, mh.AutoApprove, verification.WorkerOptions{})
	if err != nil || !did {
		t.Fatal(err)
	}
	if n := env.Count(`SELECT count(*) FROM driver_profiles WHERE id=$1 AND status='approved'`, id); n != 1 {
		t.Fatal("full match not approved")
	}
	if n := env.Count(`SELECT count(*) FROM wallet_ledger l JOIN wallets w ON w.id=l.wallet_id WHERE w.driver_id=$1 AND l.entry_type='bonus_credit' AND l.admin_id IS NULL`, id); n != 1 {
		t.Fatal("system approval lost welcome bonus")
	}
	if n := env.Count(`SELECT count(*) FROM driver_identity_claims WHERE driver_id=$1`, id); n != 2 {
		t.Fatal("identity/document ownership not claimed")
	}
	if _, err = env.Verification.RunOnce(context.Background(), matchingEngine{}, mh.AutoApprove, verification.WorkerOptions{}); err != nil {
		t.Fatal(err)
	}
	if n := env.Count(`SELECT count(*) FROM welcome_bonus_grants WHERE driver_id=$1`, id); n != 1 {
		t.Fatal("bonus repeated")
	}
	operator := env.AdminClient(auth.RoleOperator).Do(http.MethodGet, adminPath(id, ""), nil)
	if operator.Code != 200 || bytes.Contains(operator.Raw, []byte(syntheticIdentity.IIN)) || bytes.Contains(operator.Raw, []byte(syntheticIdentity.DocumentNumber)) {
		t.Fatal("sensitive identity exposed to operator")
	}
	moderator := env.AdminClient(auth.RoleDriverModerator).Do(http.MethodGet, adminPath(id, ""), nil)
	if moderator.Code != 200 || !bytes.Contains(moderator.Raw, []byte(syntheticIdentity.IIN)) {
		t.Fatal("authorized comparison unavailable")
	}
	if n := env.Count(`SELECT count(*) FROM admin_audit_logs WHERE action='driver.identity_view' AND entity_id=$1`, id.String()); n != 1 {
		t.Fatal("sensitive read not audited")
	}
	if n := env.Count(`SELECT count(*) FROM admin_audit_logs WHERE after::text LIKE '%999999999990%'`); n != 0 {
		t.Fatal("IIN leaked into audit")
	}
}
func TestLegacyPendingApprovalAndPendingSuspensionRestore(t *testing.T) {
	env := driverstest.New(t, verificationOptions())
	c := env.DriverClient()
	id := c.CompleteDraft(driverstest.Registration{Phone: "+77015550106", Plate: "106AAA04"})
	if res := c.Submit(); res.Code != 200 {
		t.Fatal(res)
	}
	// Emulate an application submitted before verification rollout.
	if _, err := env.Pool.Exec(context.Background(), `DELETE FROM driver_verification_jobs WHERE driver_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	admin := env.AdminClient(auth.RoleDriverModerator)
	if res := admin.Do(http.MethodPost, adminPath(id, "suspend"), map[string]any{"reason": "review"}); res.Code != 200 {
		t.Fatal(res)
	}
	if res := admin.Do(http.MethodPost, adminPath(id, "restore"), nil); res.Code != 200 || res.Body["status"] != "pending" {
		t.Fatal("pending driver accidentally approved by restore", res)
	}
	if res := admin.Do(http.MethodPost, adminPath(id, "approve"), nil); res.Code != 200 {
		t.Fatal("historical application blocked", res)
	}
}
func TestTwentyConcurrentRegistrationSubmissionsAreDurable(t *testing.T) {
	env := driverstest.New(t, verificationOptions())
	clients := make([]*driverstest.Client, 20)
	ids := make([]uuid.UUID, 20)
	ctx := context.Background()
	for i := range clients {
		clients[i] = env.DriverClient()
		ids[i] = clients[i].CompleteDraft(driverstest.Registration{Phone: fmt.Sprintf("+77015552%03d", i), Plate: fmt.Sprintf("%03dAAA04", 200+i)})
		if err := db.WithTx(ctx, env.Pool, func(tx pgx.Tx) error { return env.Verification.SaveInput(ctx, tx, ids[i], syntheticIdentity) }); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	results := make([]driverstest.Response, 20)
	start := time.Now()
	for i := range clients {
		wg.Add(1)
		go func(i int) { defer wg.Done(); results[i] = clients[i].Submit() }(i)
	}
	wg.Wait()
	for _, res := range results {
		if res.Code != 200 {
			t.Fatal(res)
		}
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("requests waited unexpectedly: %s", elapsed)
	} else {
		t.Logf("20 synthetic concurrent registration submissions returned in %s", elapsed)
	}
	if n := env.Count(`SELECT count(*) FROM driver_verification_jobs WHERE status='pending'`); n != 20 {
		t.Fatalf("lost jobs: %d", n)
	}
	if n := env.Count(`SELECT count(*) FROM notification_outbox WHERE template='moderation_submitted'`); n != 20 {
		t.Fatalf("lost acknowledgments: %d", n)
	}
}
