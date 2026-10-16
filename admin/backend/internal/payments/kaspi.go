package payments

import (
	"context"
	"net/http"
	"strings"

	"github.com/yereke99/jantaxi/admin/backend/internal/notifications"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
)

// KaspiManual is the "kaspi_manual" provider. Kaspi has no public payment
// confirmation API and its payment link is fixed per sales point: the payer
// types the amount and may leave a message. The instructions therefore ask the
// driver to enter the exact amount and paste the top-up reference into the
// message, and a finance admin reconciles the payment in Kaspi Pay → History.
type KaspiManual struct {
	q                 db.Querier
	fallbackURL       string
	fallbackRecipient string
}

// NewKaspiManual reads the Kaspi settings through q (database value, then the
// settings env default). fallbackURL and fallbackRecipient (KASPI_PAY_* env)
// apply when neither is set.
func NewKaspiManual(q db.Querier, fallbackURL, fallbackRecipient string) *KaspiManual {
	return &KaspiManual{q: q, fallbackURL: strings.TrimSpace(fallbackURL), fallbackRecipient: strings.TrimSpace(fallbackRecipient)}
}

// Name implements Provider.
func (k *KaspiManual) Name() string { return ProviderKaspiManual }

// WebhookEnabled implements Provider: Kaspi offers no verifiable callback.
func (k *KaspiManual) WebhookEnabled() bool { return false }

// VerifyWebhook implements Provider.
func (k *KaspiManual) VerifyWebhook(*http.Request) (*ProviderPayment, error) {
	return nil, ErrWebhookUnsupported
}

// kaspiConfig is the effective Kaspi configuration.
type kaspiConfig struct {
	URL, Recipient string
	Instructions   settings.Localized
	Custom         bool // instructions set by an admin or the environment
}

func (k *KaspiManual) config(ctx context.Context) kaspiConfig {
	s := settings.KaspiPayment(ctx, k.q)
	c := kaspiConfig{URL: strings.TrimSpace(s.PaymentURL), Recipient: strings.TrimSpace(s.RecipientName), Instructions: s.Instructions}
	if c.URL == "" {
		c.URL = k.fallbackURL
	}
	if c.Recipient == "" {
		c.Recipient = k.fallbackRecipient
	}
	c.Custom = !c.Instructions.IsZero() && !isBuiltinInstructions(c.Instructions)
	return c
}

// PaymentURL is the current Kaspi payment link ("" when not configured). A new
// top-up stores it as its payment_url snapshot.
func (k *KaspiManual) PaymentURL(ctx context.Context) string { return k.config(ctx).URL }

// Configured reports whether drivers can be told where to pay: a payment link
// or at least a recipient name.
func (k *KaspiManual) Configured(ctx context.Context) bool {
	c := k.config(ctx)
	return c.URL != "" || c.Recipient != ""
}

// Instructions implements Provider. The link stored on the top-up wins over the
// current one so a driver always sees the link the top-up was created with.
func (k *KaspiManual) Instructions(ctx context.Context, t TopUp, lang string) PaymentInstructions {
	c := k.config(ctx)
	url := c.URL
	if t.PaymentURL != nil && *t.PaymentURL != "" {
		url = *t.PaymentURL
	}
	out := PaymentInstructions{
		Method:        ProviderKaspiManual,
		RecipientName: c.Recipient,
		Reference:     t.Reference,
		Amount:        t.RequestedAmount,
		Currency:      Currency,
		Instructions:  kaspiText(lang, url, c, t.RequestedAmount, t.Reference),
	}
	if url != "" {
		out.URL = &url
	}
	return out
}

// isBuiltinInstructions reports whether l is the settings registry default,
// i.e. nobody customised the text.
func isBuiltinInstructions(l settings.Localized) bool {
	def, ok := settings.Lookup("kaspi.instructions")
	if !ok {
		return false
	}
	d, ok := def.Default.(settings.Localized)
	return ok && d == l
}

// Built-in instruction texts. Placeholders: {amount}, {reference}, {recipient}.
var (
	kaspiLinkText = map[string]string{
		"kk": "Kaspi сілтемесі соманы да, төлем кодын да өзі толтырмайды. Сілтемені ашып, {amount} сомасын қолмен дәл енгізіңіз және хабарлама жолына {reference} кодын қойыңыз. Төлегеннен кейін осы жерде төлемді растаңыз. Әкімші төлемді тексерген соң сома балансыңызға түседі.",
		"ru": "Ссылка Kaspi не заполняет ни сумму, ни код платежа. Откройте ссылку, введите вручную точную сумму {amount} и вставьте код {reference} в поле сообщения. После оплаты подтвердите её здесь. Сумма поступит на баланс после проверки платежа администратором.",
		"en": "The Kaspi link fills in neither the amount nor the payment code. Open the link, enter the exact amount {amount} manually and paste the code {reference} into the message field. After paying, confirm it here. The amount reaches your balance once an administrator has verified the payment.",
	}
	kaspiRecipientText = map[string]string{
		"kk": "Kaspi.kz қосымшасында «{recipient}» алушысын тауып, {amount} сомасын қолмен дәл енгізіңіз және хабарлама жолына {reference} кодын қойыңыз. Төлегеннен кейін осы жерде төлемді растаңыз. Әкімші төлемді тексерген соң сома балансыңызға түседі.",
		"ru": "В приложении Kaspi.kz найдите получателя «{recipient}», введите вручную точную сумму {amount} и вставьте код {reference} в поле сообщения. После оплаты подтвердите её здесь. Сумма поступит на баланс после проверки платежа администратором.",
		"en": "In the Kaspi.kz app, find the recipient “{recipient}”, enter the exact amount {amount} manually and paste the code {reference} into the message field. After paying, confirm it here. The amount reaches your balance once an administrator has verified the payment.",
	}
	// kaspiSummaryText precedes custom (admin or env) instructions so the amount
	// and the reference are always stated.
	kaspiSummaryText = map[string]string{
		"kk": "Соманы қолмен енгізіңіз: {amount}. Хабарлама жолына төлем кодын қойыңыз: {reference}.",
		"ru": "Введите сумму вручную: {amount}. Вставьте код платежа в поле сообщения: {reference}.",
		"en": "Enter the amount manually: {amount}. Paste the payment code into the message field: {reference}.",
	}
)

func kaspiText(lang, url string, c kaspiConfig, amount int64, reference string) string {
	if lang != "ru" && lang != "en" {
		lang = "kk"
	}
	tpl := kaspiLinkText[lang]
	switch {
	case c.Custom:
		tpl = kaspiSummaryText[lang] + "\n\n" + c.Instructions.Get(lang)
	case url == "" && c.Recipient != "":
		tpl = kaspiRecipientText[lang]
	}
	return strings.NewReplacer(
		"{amount}", notifications.FormatMoney(amount),
		"{reference}", reference,
		"{recipient}", c.Recipient,
	).Replace(tpl)
}
