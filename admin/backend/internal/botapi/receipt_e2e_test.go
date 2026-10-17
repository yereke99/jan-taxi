package botapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/app"
	"github.com/yereke99/jantaxi/admin/backend/internal/payments/receipt/receipttest"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/config"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
	"github.com/yereke99/jantaxi/admin/backend/pkg/botapi"
	"github.com/yereke99/jantaxi/admin/backend/pkg/telegram"
)

const fileToken = "4243:E2E-file-token"

// TestBotReceiptEndToEnd runs the driver bot's receipt path against the real
// internal API: getFile and the file download from a fake Telegram, the
// multipart upload through the internal client, and the credit in the
// database. Then a reuse of the same receipt and a receipt without an open
// top-up.
func TestBotReceiptEndToEnd(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	cfg := config.ForTesting("")
	cfg.FileStorageDir = t.TempDir()
	a, err := app.NewWithPool(ctx, cfg, slog.New(slog.DiscardHandler), pool)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	t.Cleanup(func() { settings.Configure(settings.Env{}) })
	internal := httptest.NewServer(a.InternalHandler())
	t.Cleanup(internal.Close)
	public := a.Handler()

	spec := receipttest.Default()
	spec.Amount = "1 500"
	pdf := receipttest.Build(spec)
	tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bot" + fileToken + "/getFile":
			_, _ = io.WriteString(w, `{"ok":true,"result":{"file_id":"F1","file_unique_id":"U1","file_size":`+strconv.Itoa(len(pdf))+`,"file_path":"documents/file_7.pdf"}}`)
		case "/file/bot" + fileToken + "/documents/file_7.pdf":
			_, _ = w.Write(pdf)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(tg.Close)
	tgc := telegram.New(fileToken, telegram.WithBaseURL(tg.URL))
	api := botapi.New(internal.URL, config.TestServiceSecret, botapi.BotDriver)

	f := testutil.CreateDriver(t, pool, testutil.DriverOptions{Status: "approved"})
	initData := testutil.SignInitData(config.TestDriverBotToken, f.TelegramID, "Test", time.Now(), "wallet")
	token := publicCall(t, public, http.MethodPost, "/api/v1/auth/telegram/driver", "", map[string]any{"init_data": initData}, http.StatusOK)["token"].(string)

	// No open top-up yet.
	file, err := tgc.GetFile(ctx, "F1")
	if err != nil {
		t.Fatal(err)
	}
	data, err := tgc.DownloadFile(ctx, file.FilePath, 5<<20)
	if err != nil || !bytes.Equal(data, pdf) {
		t.Fatalf("download: %v", err)
	}
	if _, err := api.UploadTopUpReceipt(ctx, f.TelegramID, "receipt_QR18149336524.pdf", data); botapi.ErrorCode(err) != "no_open_topup" {
		t.Fatalf("without a top-up: %v", err)
	}

	top := publicCall(t, public, http.MethodPost, "/api/v1/driver/wallet/topups", token,
		map[string]any{"amount": 1500, "idempotency_key": uuid.NewString()}, http.StatusCreated)
	res, err := api.UploadTopUpReceipt(ctx, f.TelegramID, "receipt_QR18149336524.pdf", data)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != botapi.ReceiptCredited || res.TopUp.ID != top["id"] || res.TopUp.Status != "confirmed" || res.Balance == nil || *res.Balance != 1500 {
		t.Fatalf("result = %+v", res)
	}
	var balance int64
	if err := pool.QueryRow(ctx, `SELECT balance FROM wallets WHERE driver_id = $1`, f.DriverID).Scan(&balance); err != nil || balance != 1500 {
		t.Fatalf("balance %d %v", balance, err)
	}

	// The same receipt again for a new top-up: rejected as already used.
	second := publicCall(t, public, http.MethodPost, "/api/v1/driver/wallet/topups", token,
		map[string]any{"amount": 1500, "idempotency_key": uuid.NewString()}, http.StatusCreated)
	_, err = api.UploadTopUpReceipt(ctx, f.TelegramID, "receipt_QR18149336524.pdf", data)
	if e, ok := botapi.AsError(err); !ok || e.Code != "receipt_already_used" || e.DetailString("status") != "rejected" || e.DetailString("topup_id") != second["id"] {
		t.Fatalf("reuse: %v", err)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM top_up_requests WHERE id = $1`, second["id"]).Scan(&status); err != nil || status != "rejected" {
		t.Fatalf("second top-up %s %v", status, err)
	}

	// Unknown users, the passenger bot and non-multipart bodies are refused.
	if _, err := api.UploadTopUpReceipt(ctx, 999_999_999, "r.pdf", data); botapi.ErrorCode(err) != "not_found" {
		t.Fatalf("unknown user: %v", err)
	}
	pass := botapi.New(internal.URL, config.TestServiceSecret, botapi.BotPassenger)
	if _, err := pass.UploadTopUpReceipt(ctx, f.TelegramID, "r.pdf", data); botapi.ErrorCode(err) != "forbidden" {
		t.Fatalf("passenger bot: %v", err)
	}
}

func publicCall(t *testing.T, h http.Handler, method, path, token string, body any, want int) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(body)
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != want {
		t.Fatalf("%s %s: %d %s", method, path, rec.Code, rec.Body)
	}
	out := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out
}
