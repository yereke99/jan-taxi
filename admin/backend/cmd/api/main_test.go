package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/password"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

func runCmd(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := run(args, strings.NewReader(stdin), &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestUnknownCommand(t *testing.T) {
	if code, _, stderr := runCmd(t, "", "launch-rockets"); code != 2 || !strings.Contains(stderr, "unknown command") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	if code, out, _ := runCmd(t, "", "help"); code != 0 || !strings.Contains(out, "api migrate up|status") {
		t.Fatalf("help: %d %q", code, out)
	}
}

func TestHealthcheck(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer ok.Close()
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer down.Close()

	if code, _, stderr := runCmd(t, "", "healthcheck", "--url", ok.URL+"/healthz"); code != 0 {
		t.Fatalf("healthy: %d %s", code, stderr)
	}
	if code, _, _ := runCmd(t, "", "healthcheck", "--url", down.URL+"/healthz"); code != 1 {
		t.Fatalf("503 must exit 1, got %d", code)
	}
	if code, _, _ := runCmd(t, "", "healthcheck", "--url", "http://127.0.0.1:1/healthz", "--timeout", "500ms"); code != 1 {
		t.Fatalf("unreachable must exit 1, got %d", code)
	}
}

func TestReadPasswordFromPipe(t *testing.T) {
	pw, err := readPassword(strings.NewReader("correct horse battery\r\nignored\n"), &bytes.Buffer{})
	if err != nil || pw != "correct horse battery" {
		t.Fatalf("got %q %v", pw, err)
	}
	if pw, err := readPassword(strings.NewReader("no-newline-password"), &bytes.Buffer{}); err != nil || pw != "no-newline-password" {
		t.Fatalf("got %q %v", pw, err)
	}
	if _, err := readPassword(strings.NewReader(""), &bytes.Buffer{}); err == nil {
		t.Fatal("empty stdin must fail")
	}
}

func TestValidateAdminInput(t *testing.T) {
	tests := []struct {
		email, role, name string
		ok                bool
	}{
		{"Ops@JanTaxi.kz", "operator", "  Айгерім  Сапарова ", true},
		{"ops@jantaxi.kz", "root", "", false},
		{"not-an-email", "operator", "", false},
		{"Name <ops@jantaxi.kz>", "operator", "", false},
		{"ops@jantaxi.kz", "auditor", strings.Repeat("x", 121), false},
	}
	for _, tc := range tests {
		in, err := validateAdminInput(tc.email, tc.role, tc.name)
		if (err == nil) != tc.ok {
			t.Errorf("validateAdminInput(%q, %q): %v", tc.email, tc.role, err)
		}
		if tc.ok && (in.email != "ops@jantaxi.kz" || in.name != "Айгерім Сапарова") {
			t.Errorf("normalisation: %+v", in)
		}
	}
	if validatePassword("short") == nil || validatePassword("twelve chars") != nil {
		t.Error("password length rules")
	}
}

func TestAdminCreateAndMigrateStatus(t *testing.T) {
	pool := testutil.NewDB(t)
	t.Setenv("DATABASE_URL", pool.Config().ConnString())

	code, out, stderr := runCmd(t, "a-long-enough-password\n",
		"admin", "create", "--email", "Finance@JanTaxi.kz", "--role", "finance_manager", "--name", "Finance Team")
	if code != 0 || !strings.Contains(out, "created administrator") {
		t.Fatalf("create: %d %q %q", code, out, stderr)
	}
	var role, name, hash string
	err := pool.QueryRow(context.Background(),
		`SELECT role, full_name, password_hash FROM admin_users WHERE email = 'finance@jantaxi.kz'`).Scan(&role, &name, &hash)
	if err != nil || role != "finance_manager" || name != "Finance Team" {
		t.Fatalf("row: %v %s %s", err, role, name)
	}
	if ok, _ := password.Verify("a-long-enough-password", hash); !ok {
		t.Fatal("stored hash does not verify")
	}
	var audits int
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM admin_audit_logs WHERE action = 'admin.create'`).Scan(&audits)
	if audits != 1 {
		t.Fatalf("audit rows: %d", audits)
	}

	if code, _, stderr := runCmd(t, "another-long-password\n",
		"admin", "create", "--email", "finance@jantaxi.kz", "--role", "auditor"); code != 1 || !strings.Contains(stderr, "already exists") {
		t.Fatalf("duplicate: %d %q", code, stderr)
	}
	if code, _, _ := runCmd(t, "short\n", "admin", "create", "--email", "x@jantaxi.kz", "--role", "auditor"); code != 2 {
		t.Fatalf("short password must exit 2, got %d", code)
	}
	if code, _, _ := runCmd(t, "a-long-enough-password\n", "admin", "create", "--email", "x@jantaxi.kz", "--role", "god"); code != 2 {
		t.Fatalf("bad role must exit 2, got %d", code)
	}

	if code, out, stderr := runCmd(t, "", "migrate", "status"); code != 0 || !strings.Contains(out, "up to date") {
		t.Fatalf("migrate status: %d %q %q", code, out, stderr)
	}
	if code, _, stderr := runCmd(t, "", "migrate", "up"); code != 0 {
		t.Fatalf("migrate up on a current schema: %d %q", code, stderr)
	}
	if code, _, _ := runCmd(t, "", "migrate", "down"); code != 2 {
		t.Fatal("migrate down is not supported")
	}
}
