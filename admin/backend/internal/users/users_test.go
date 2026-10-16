package users

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

func TestSyncTelegramUserNewUserIsKazakh(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	tg := testutil.NextTelegramID()

	u, isNew, err := SyncTelegramUser(ctx, pool, TelegramUser{ID: tg, FirstName: " Айгүл‏ ", LastName: "Серік", Username: "@aigul_s", LanguageCode: "ru"}, BotPassenger, true)
	if err != nil {
		t.Fatal(err)
	}
	if !isNew || u.Language != "kk" || u.TelegramLanguageCode != "ru" {
		t.Fatalf("new user: isNew=%v language=%s tg_lang=%s", isNew, u.Language, u.TelegramLanguageCode)
	}
	if u.FirstName != "Айгүл" || u.LastName != "Серік" || u.Username != "aigul_s" {
		t.Fatalf("names = %q %q %q", u.FirstName, u.LastName, u.Username)
	}
	if u.PassengerBotStartedAt == nil || u.DriverBotStartedAt != nil {
		t.Fatalf("started: passenger=%v driver=%v", u.PassengerBotStartedAt, u.DriverBotStartedAt)
	}

	// Second sync: not new, language kept even though Telegram now says "en".
	if err := SetLanguage(ctx, pool, u.ID, "ru"); err != nil {
		t.Fatal(err)
	}
	u2, isNew, err := SyncTelegramUser(ctx, pool, TelegramUser{ID: tg, FirstName: "Aigul", LanguageCode: "en", Username: "bad name!"}, BotDriver, false)
	if err != nil {
		t.Fatal(err)
	}
	if isNew || u2.ID != u.ID || u2.Language != "ru" || u2.FirstName != "Aigul" || u2.Username != "" || u2.TelegramLanguageCode != "en" {
		t.Fatalf("resync = %+v isNew=%v", u2, isNew)
	}
	if u2.DriverBotStartedAt != nil {
		t.Fatal("driver_bot_started_at set without started=true")
	}
}

func TestSyncCreatesMissingPreferences(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	tg := testutil.NextTelegramID()
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO users (telegram_user_id, first_name) VALUES ($1, 'X') RETURNING id`, tg).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, _, err := SyncTelegramUser(ctx, pool, TelegramUser{ID: tg, FirstName: "X"}, BotPassenger, false); err != nil {
		t.Fatal(err)
	}
	var lang string
	if err := pool.QueryRow(ctx, `SELECT language FROM user_preferences WHERE user_id = $1`, id).Scan(&lang); err != nil || lang != "kk" {
		t.Fatalf("preferences: %q %v", lang, err)
	}
}

func TestBotBlockedLifecycle(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	_, tg := testutil.CreateUser(t, pool)

	if err := MarkBotBlocked(ctx, pool, tg, BotDriver, true); err != nil {
		t.Fatal(err)
	}
	u, err := GetByTelegramID(ctx, pool, tg)
	if err != nil || u.DriverBotBlockedAt == nil || u.PassengerBotBlockedAt != nil {
		t.Fatalf("after block: %+v %v", u, err)
	}
	first := *u.DriverBotBlockedAt
	if err := MarkBotBlocked(ctx, pool, tg, BotDriver, true); err != nil {
		t.Fatal(err)
	}
	if u, _ = GetByTelegramID(ctx, pool, tg); !u.DriverBotBlockedAt.Equal(first) {
		t.Fatal("repeated block moved the first block time")
	}
	// /start again clears the block.
	if u, _, err = SyncTelegramUser(ctx, pool, TelegramUser{ID: tg, FirstName: "Test"}, BotDriver, true); err != nil {
		t.Fatal(err)
	}
	if u.DriverBotBlockedAt != nil || u.DriverBotStartedAt == nil {
		t.Fatalf("after start: blocked=%v started=%v", u.DriverBotBlockedAt, u.DriverBotStartedAt)
	}
	if err := MarkBotBlocked(ctx, pool, tg, BotPassenger, true); err != nil {
		t.Fatal(err)
	}
	if err := MarkBotBlocked(ctx, pool, tg, BotPassenger, false); err != nil {
		t.Fatal(err)
	}
	if u, _ = GetByTelegramID(ctx, pool, tg); u.PassengerBotBlockedAt != nil {
		t.Fatal("unblock did not clear passenger_bot_blocked_at")
	}

	if err := MarkBotBlocked(ctx, pool, 1, BotDriver, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown user err = %v", err)
	}
	if err := MarkBotBlocked(ctx, pool, tg, "admin", true); !errors.Is(err, ErrInvalidBot) {
		t.Fatalf("bad bot err = %v", err)
	}
}

func TestLanguageFunctions(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	id, _ := testutil.CreateUser(t, pool)

	if lang, err := Language(ctx, pool, id); err != nil || lang != "kk" {
		t.Fatalf("default language %q %v", lang, err)
	}
	if err := SetLanguage(ctx, pool, id, "en"); err != nil {
		t.Fatal(err)
	}
	if lang, _ := Language(ctx, pool, id); lang != "en" {
		t.Fatalf("language = %q", lang)
	}
	if err := SetLanguage(ctx, pool, id, "de"); !errors.Is(err, ErrInvalidLanguage) {
		t.Fatalf("invalid language err = %v", err)
	}
	if err := SetLanguage(ctx, pool, uuid.New(), "ru"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown user err = %v", err)
	}
	if _, err := Language(ctx, pool, uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown user language err = %v", err)
	}
	if _, err := GetByID(ctx, pool, uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetByID err = %v", err)
	}
}

func TestSyncValidatesInput(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	if _, _, err := SyncTelegramUser(ctx, pool, TelegramUser{ID: 0}, BotPassenger, true); !errors.Is(err, ErrInvalidTelegramUser) {
		t.Fatalf("zero id err = %v", err)
	}
	if _, _, err := SyncTelegramUser(ctx, pool, TelegramUser{ID: 5}, "x", true); !errors.Is(err, ErrInvalidBot) {
		t.Fatalf("bad bot err = %v", err)
	}
	long := strings.Repeat("ә", 300)
	u, _, err := SyncTelegramUser(ctx, pool, TelegramUser{ID: testutil.NextTelegramID(), FirstName: long}, BotPassenger, false)
	if err != nil || len([]rune(u.FirstName)) != maxNameRunes {
		t.Fatalf("long name: %d runes, %v", len([]rune(u.FirstName)), err)
	}
}

func TestHelpers(t *testing.T) {
	phones := []struct{ in, want string }{
		{"+77011234567", "+7 701 *** ** 67"},
		{"", ""},
		{"+123", "****"},
		{"+4915112345678", "************78"},
	}
	for _, p := range phones {
		if got := MaskPhone(p.in); got != p.want {
			t.Errorf("MaskPhone(%q) = %q, want %q", p.in, got, p.want)
		}
	}
	codes := []struct {
		in   string
		want any
	}{
		{"ru", "ru"}, {"pt-BR", "pt-br"}, {"", nil}, {"русский", nil}, {"x", nil},
	}
	for _, c := range codes {
		got := cleanLanguageCode(c.in)
		if (got == nil) != (c.want == nil) || (got != nil && *got != c.want) {
			t.Errorf("cleanLanguageCode(%q) = %v", c.in, got)
		}
	}
	for lang, ok := range map[string]bool{"kk": true, "ru": true, "en": true, "KK": false, "kz": false} {
		if ValidLanguage(lang) != ok {
			t.Errorf("ValidLanguage(%q) != %v", lang, ok)
		}
	}
}
