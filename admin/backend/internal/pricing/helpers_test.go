package pricing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

func httpxError(t *testing.T, err error) *httpx.Error {
	t.Helper()
	var e *httpx.Error
	if !errors.As(err, &e) {
		t.Fatalf("expected *httpx.Error, got %T %v", err, err)
	}
	return e
}

// inTx runs fn in a transaction that is always rolled back.
func inTx(t *testing.T, pool *pgxpool.Pool, fn func(tx pgx.Tx) error) error {
	t.Helper()
	sentinel := errors.New("rollback")
	err := db.WithTx(context.Background(), pool, func(tx pgx.Tx) error {
		if err := fn(tx); err != nil {
			return err
		}
		return sentinel
	})
	if errors.Is(err, sentinel) {
		return nil
	}
	return err
}

type adminClient struct {
	t *testing.T
	h http.Handler
}

func newAdminClient(t *testing.T, pool *pgxpool.Pool, role auth.Role) (adminClient, uuid.UUID) {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO admin_users (email, full_name, password_hash, role) VALUES ($1, 'Тариф Әкімші', 'x', $2) RETURNING id`,
		uuid.NewString()+"@jantaxi.kz", string(role)).Scan(&id); err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(auth.WithAdmin(req.Context(), auth.AdminPrincipal{AdminID: id, Role: role})))
		})
	})
	NewAdminHandler(pool).MountAdmin(r)
	return adminClient{t: t, h: r}, id
}

func (c adminClient) do(method, path, body string) (int, map[string]any) {
	c.t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, req)
	var out map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			c.t.Fatalf("decode %q: %v", rec.Body.String(), err)
		}
	}
	return rec.Code, out
}

func errOf(body map[string]any) (string, map[string]any) {
	e, _ := body["error"].(map[string]any)
	code, _ := e["code"].(string)
	f, _ := e["fields"].(map[string]any)
	return code, f
}
