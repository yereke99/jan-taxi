package notifications

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
	"github.com/yereke99/jantaxi/admin/backend/pkg/telegram"
)

// fakePhotos is an in-memory PhotoStore.
type fakePhotos struct {
	mu     sync.Mutex
	data   map[uuid.UUID][]byte
	cached map[string]string // bot/file → telegram file id
}

func (f *fakePhotos) TelegramFileID(_ context.Context, id uuid.UUID, bot Bot) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cached[string(bot)+"/"+id.String()], nil
}

func (f *fakePhotos) ReadPhoto(_ context.Context, id uuid.UUID) ([]byte, error) {
	if d, ok := f.data[id]; ok {
		return d, nil
	}
	return nil, errors.New("no such photo")
}

func (f *fakePhotos) RememberTelegramFileID(_ context.Context, id uuid.UUID, bot Bot, tgID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cached[string(bot)+"/"+id.String()] = tgID
	return nil
}

// photoSender adds SendPhoto to fakeSender.
type photoSender struct {
	*fakeSender
	photos []telegram.InputPhoto
	fail   error
}

func (p *photoSender) SendPhoto(ctx context.Context, bot Bot, chatID int64, photo telegram.InputPhoto, caption string,
	markup *telegram.InlineKeyboardMarkup) (int64, string, error) {
	p.mu.Lock()
	if p.fail != nil {
		err := p.fail
		p.fail = nil
		p.mu.Unlock()
		return 0, "", err
	}
	p.photos = append(p.photos, photo)
	p.nextID++
	p.sent = append(p.sent, sent{bot: bot, chatID: chatID, text: caption, markup: markup})
	id := 1000 + p.nextID
	p.mu.Unlock()
	return id, "tg-file-1", nil
}

func acceptedParams(photo uuid.UUID) map[string]any {
	return map[string]any{"order_id": uuid.NewString(), "order_number": 7, "driver_name": "Ерлан Б.", "rating": "4.9",
		"vehicle": "Toyota Camry, ақ", "plate": "123ABC04", "driver_phone": "+7 701 555 12 34", "eta_min": 4,
		"photo": photo.String()}
}

// TestPhotoMessages: the driver's photo is uploaded once per bot, then sent
// by file id; a missing or refused photo still delivers the text.
func TestPhotoMessages(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	user, tg := newUser(t, pool, "")
	photo := uuid.New()
	store := &fakePhotos{data: map[uuid.UUID][]byte{photo: []byte("jpeg")}, cached: map[string]string{}}
	ps := &photoSender{fakeSender: newFakeSender()}
	w := NewWorker(pool, ps, Options{InstanceID: "w1", PassengerMiniAppURL: "https://a.kz/p/", PerBotRate: 10000,
		PerChatInterval: 1, Photos: store})

	send := func(key string, params map[string]any) {
		t.Helper()
		if err := Enqueue(ctx, pool, Notification{Bot: PassengerBot, UserID: user, Template: TplOrderAcceptedPassenger,
			DedupeKey: key, Params: params}); err != nil {
			t.Fatal(err)
		}
		if n, err := w.RunOnce(ctx); err != nil || n != 1 {
			t.Fatalf("RunOnce = %d %v", n, err)
		}
	}
	send("a:1", acceptedParams(photo))
	send("a:2", acceptedParams(photo))
	if len(ps.photos) != 2 || string(ps.photos[0].Data) != "jpeg" || ps.photos[1].FileID != "tg-file-1" || ps.photos[1].Data != nil {
		t.Fatalf("photos: %+v", ps.photos)
	}
	got := ps.sentTo(tg)
	if len(got) != 2 || got[0].text != "<b>Жүргізуші табылды · №7</b>\nЕрлан Б.\nРейтинг: ★ 4.9\nToyota Camry, ақ\nКөлік нөмірі: <b>123ABC04</b>\nТелефон: +7 701 555 12 34\n<b>~4 минутта келеді</b>" {
		t.Fatalf("caption: %+v", got)
	}

	// No stored photo: text.
	missing := acceptedParams(uuid.New())
	delete(missing, "eta_min")
	send("a:3", missing)
	if len(ps.photos) != 2 || len(ps.sentTo(tg)) != 3 {
		t.Fatalf("missing photo not sent as text: %d photos", len(ps.photos))
	}
	// Telegram refuses the photo: the text still goes out.
	ps.fail = apiErr(400, "Bad Request: wrong file identifier/HTTP URL specified", 0)
	send("a:4", acceptedParams(photo))
	if n := len(ps.sentTo(tg)); n != 4 {
		t.Fatalf("refused photo: %d messages", n)
	}
	// Without a photo param the message is text.
	p := acceptedParams(photo)
	delete(p, "photo")
	send("a:5", p)
	if len(ps.photos) != 2 {
		t.Fatalf("photo sent without a photo param")
	}
	if err := (&Template{Key: "x", Params: []Param{{Name: "photo", Kind: KindFile}}}).ValidateParams(map[string]any{"photo": "nope"}); err == nil {
		t.Fatal("a non-UUID file param was accepted")
	}
}
