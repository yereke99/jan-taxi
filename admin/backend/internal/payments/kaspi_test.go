package payments

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/notifications"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
)

func TestKaspiInstructions(t *testing.T) {
	pool := testutil.NewDB(t)
	settings.Configure(settings.Env{}) // only the explicit fallbacks below apply
	ctx := context.Background()
	top := TopUp{Reference: "EMB001-TOPUP-000123", RequestedAmount: 2000}
	money := notifications.FormatMoney(2000)

	k := NewKaspiManual(pool, testPaymentURL, "ЖШС Jan Taxi")
	if k.Name() != ProviderKaspiManual || k.WebhookEnabled() {
		t.Fatal("kaspi_manual must not accept webhooks")
	}
	if _, err := k.VerifyWebhook(&http.Request{}); err != ErrWebhookUnsupported {
		t.Fatalf("VerifyWebhook = %v", err)
	}
	want := map[string][]string{
		"kk": {"қолмен", "хабарлама", "өзі толтырмайды"},
		"ru": {"вручную", "поле сообщения", "не заполняет"},
		"en": {"manually", "message field", "fills in neither"},
	}
	for lang, words := range want {
		p := k.Instructions(ctx, top, lang)
		if p.URL == nil || *p.URL != testPaymentURL || p.RecipientName != "ЖШС Jan Taxi" || p.Reference != top.Reference ||
			p.Amount != 2000 || p.Currency != "KZT" || p.Method != ProviderKaspiManual {
			t.Fatalf("%s: %+v", lang, p)
		}
		if !strings.Contains(p.Instructions, money) || !strings.Contains(p.Instructions, top.Reference) {
			t.Fatalf("%s: instructions lack amount or reference: %q", lang, p.Instructions)
		}
		for _, w := range words {
			if !strings.Contains(p.Instructions, w) {
				t.Fatalf("%s: %q missing in %q", lang, w, p.Instructions)
			}
		}
	}
	if got := k.Instructions(ctx, top, "de").Instructions; got != k.Instructions(ctx, top, "kk").Instructions {
		t.Fatal("unknown language must fall back to Kazakh")
	}

	// Without a link the driver is pointed at the recipient.
	noLink := NewKaspiManual(pool, "", "ЖШС Jan Taxi")
	p := noLink.Instructions(ctx, top, "kk")
	if p.URL != nil || !strings.Contains(p.Instructions, "«ЖШС Jan Taxi»") || !noLink.Configured(ctx) {
		t.Fatalf("no link: %+v", p)
	}
	if NewKaspiManual(pool, "", "").Configured(ctx) {
		t.Fatal("nothing configured must report false")
	}

	// Admin settings win over the environment; custom text keeps the concrete summary.
	if err := settings.Set(ctx, pool, "kaspi.payment_url", "https://pay.kaspi.kz/pay/admin", uuid.Nil); err != nil {
		t.Fatal(err)
	}
	custom := settings.Localized{KK: "Төлем 10 минут ішінде тексеріледі.", RU: "Платёж проверяется в течение 10 минут.", EN: "Payments are checked within 10 minutes."}
	if err := settings.Set(ctx, pool, "kaspi.instructions", custom, uuid.Nil); err != nil {
		t.Fatal(err)
	}
	if k.PaymentURL(ctx) != "https://pay.kaspi.kz/pay/admin" {
		t.Fatalf("payment url = %q", k.PaymentURL(ctx))
	}
	p = k.Instructions(ctx, top, "ru")
	if *p.URL != "https://pay.kaspi.kz/pay/admin" || !strings.HasSuffix(p.Instructions, custom.RU) ||
		!strings.Contains(p.Instructions, money) || !strings.Contains(p.Instructions, top.Reference) {
		t.Fatalf("custom: %+v", p)
	}
	// A top-up keeps the link it was created with.
	snap := testPaymentURL
	top.PaymentURL = &snap
	if p := k.Instructions(ctx, top, "en"); *p.URL != testPaymentURL {
		t.Fatalf("snapshot url = %s", *p.URL)
	}
}
