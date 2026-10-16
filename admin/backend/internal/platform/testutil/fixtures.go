package testutil

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

var tgSeq atomic.Int64

func init() { tgSeq.Store(time.Now().UnixNano() % 1_000_000_000) }

// NextTelegramID returns a unique positive Telegram user id for fixtures.
func NextTelegramID() int64 { return 100_000_000 + tgSeq.Add(1) }

// CityID returns the id of the seeded city with the given code (e.g. "EMB").
func CityID(t testing.TB, pool *pgxpool.Pool, code string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(), `SELECT id FROM cities WHERE code = $1`, code).Scan(&id); err != nil {
		t.Fatalf("city %s: %v", code, err)
	}
	return id
}

// CreateUser inserts a user (+ preferences) and returns its id and Telegram id.
func CreateUser(t testing.TB, pool *pgxpool.Pool) (uuid.UUID, int64) {
	t.Helper()
	ctx := context.Background()
	tg := NextTelegramID()
	var id uuid.UUID
	err := pool.QueryRow(ctx, `
		INSERT INTO users (telegram_user_id, first_name, last_name, username, passenger_bot_started_at, driver_bot_started_at)
		VALUES ($1, 'Test', 'User', $2, now(), now()) RETURNING id`, tg, fmt.Sprintf("u%d", tg)).Scan(&id)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_preferences (user_id) VALUES ($1)`, id); err != nil {
		t.Fatalf("create preferences: %v", err)
	}
	return id, tg
}

// DriverFixture describes a driver created by CreateDriver.
type DriverFixture struct {
	UserID     uuid.UUID
	TelegramID int64
	DriverID   uuid.UUID
	VehicleID  uuid.UUID
	WalletID   uuid.UUID
	Code       string
	Phone      string
	Plate      string
}

// DriverOptions customise CreateDriver.
type DriverOptions struct {
	Status  string // default "approved"
	Online  bool
	Balance int64  // initial wallet balance (credited through a ledger entry)
	City    string // default "EMB"
}

// CreateDriver inserts a complete driver: user, profile, claimed vehicle and a wallet
// whose balance is backed by a ledger entry, so ledger reconciliation holds.
func CreateDriver(t testing.TB, pool *pgxpool.Pool, opt DriverOptions) DriverFixture {
	t.Helper()
	ctx := context.Background()
	if opt.Status == "" {
		opt.Status = "approved"
	}
	if opt.City == "" {
		opt.City = "EMB"
	}
	userID, tg := CreateUser(t, pool)
	cityID := CityID(t, pool, opt.City)

	n := tg % 1_000_000_000
	phone := fmt.Sprintf("+7701%07d", n%10_000_000)
	plate := fmt.Sprintf("%03d%s04", n%1000, letters(n))

	var seq int64
	var cityCode string
	if err := pool.QueryRow(ctx, `UPDATE cities SET driver_seq = driver_seq + 1 WHERE id = $1 RETURNING driver_seq, code`, cityID).Scan(&seq, &cityCode); err != nil {
		t.Fatalf("driver seq: %v", err)
	}
	code := fmt.Sprintf("%s%03d", cityCode, seq)

	f := DriverFixture{UserID: userID, TelegramID: tg, Code: code, Phone: phone, Plate: plate}
	approvedAt := any(nil)
	if opt.Status == "approved" || opt.Status == "suspended" {
		approvedAt = time.Now()
	}
	err := pool.QueryRow(ctx, `
		INSERT INTO driver_profiles (user_id, code, city_id, first_name, last_name, phone_e164, phone_verified_at,
		                             status, is_online, online_since, approved_at, submitted_at)
		VALUES ($1, $2, $3, 'Test', 'Driver', $4, now(), $5, $6, CASE WHEN $6 THEN now() END, $7, now())
		RETURNING id`, userID, code, cityID, phone, opt.Status, opt.Online, approvedAt).Scan(&f.DriverID)
	if err != nil {
		t.Fatalf("create driver profile: %v", err)
	}
	vstatus := "claimed"
	if opt.Status == "draft" || opt.Status == "rejected" {
		vstatus = map[string]string{"draft": "draft", "rejected": "released"}[opt.Status]
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO vehicles (driver_id, make, model, color, plate_raw, plate_normalized, status)
		VALUES ($1, 'Toyota', 'Camry', 'White', $2, $2, $3) RETURNING id`, f.DriverID, plate, vstatus).Scan(&f.VehicleID); err != nil {
		t.Fatalf("create vehicle: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO wallets (driver_id, balance) VALUES ($1, $2) RETURNING id`, f.DriverID, opt.Balance).Scan(&f.WalletID); err != nil {
		t.Fatalf("create wallet: %v", err)
	}
	if opt.Balance > 0 {
		if _, err := pool.Exec(ctx, `
			INSERT INTO wallet_ledger (wallet_id, entry_type, balance_delta, reserved_delta, balance_after, reserved_after, idempotency_key, description)
			VALUES ($1, 'adjustment_credit', $2, 0, $2, 0, $3, 'test fixture')`, f.WalletID, opt.Balance, "fixture:"+f.WalletID.String()); err != nil {
			t.Fatalf("seed ledger: %v", err)
		}
	}
	return f
}

func letters(n int64) string {
	const abc = "ABCEHKMOPTXY"
	var b strings.Builder
	for i := 0; i < 3; i++ {
		b.WriteByte(abc[n%int64(len(abc))])
		n /= int64(len(abc))
	}
	return b.String()
}

// SignInitData builds a Telegram Mini App initData string signed with botToken,
// exactly as Telegram does, for auth and end-to-end tests.
func SignInitData(botToken string, telegramID int64, firstName string, authDate time.Time, startParam string) string {
	user, _ := json.Marshal(map[string]any{
		"id": telegramID, "first_name": firstName, "last_name": "", "username": fmt.Sprintf("u%d", telegramID),
		"language_code": "ru", "allows_write_to_pm": true,
	})
	v := url.Values{}
	v.Set("user", string(user))
	v.Set("auth_date", strconv.FormatInt(authDate.Unix(), 10))
	v.Set("query_id", "AAH"+strconv.FormatInt(telegramID, 10))
	v.Set("signature", "test-signature")
	if startParam != "" {
		v.Set("start_param", startParam)
	}
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, k+"="+v.Get(k))
	}
	secret := hmac.New(sha256.New, []byte("WebAppData"))
	secret.Write([]byte(botToken))
	mac := hmac.New(sha256.New, secret.Sum(nil))
	mac.Write([]byte(strings.Join(lines, "\n")))
	v.Set("hash", hex.EncodeToString(mac.Sum(nil)))
	return v.Encode()
}
