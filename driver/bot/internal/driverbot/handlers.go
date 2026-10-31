package driverbot

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/yereke99/jantaxi/admin/backend/pkg/botapi"
	"github.com/yereke99/jantaxi/admin/backend/pkg/botapi/runtime"
	"github.com/yereke99/jantaxi/admin/backend/pkg/telegram"
)

// Driver statuses (driver_profiles.status).
const (
	statusDraft            = "draft"
	statusPending          = "pending"
	statusApproved         = "approved"
	statusChangesRequested = "changes_requested"
	statusRejected         = "rejected"
	statusSuspended        = "suspended"
)

// Start parameters the driver Mini App understands (§18).
var (
	staticStartParams = map[string]bool{
		"home": true, "register": true, "orders": true, "trip": true, "wallet": true, "topup": true,
		"profile": true, "support": true,
	}
	idStartParamRe = regexp.MustCompile(`^(order|topup)_[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

func driverStartParam(payload string) string {
	if staticStartParams[payload] || idStartParamRe.MatchString(payload) {
		return payload
	}
	return "home"
}

// registering reports a driver who has to fill in or fix the registration.
func (s *session) registering() bool {
	st := s.status()
	return !s.driver.Registered || st == statusDraft || st == statusChangesRequested
}

// start greets the driver according to the registration state, sends the
// keyboard and, until the user has picked a language, the language choice.
func (b *Bot) start(ctx context.Context, s *session, payload string) error {
	param := driverStartParam(payload)
	var err error
	switch st := s.status(); {
	case s.registering():
		if err = b.send(ctx, s, s.t("welcome"), b.keyboard(s)); err == nil {
			err = b.registration(ctx, s)
		}
	case st == statusApproved:
		text := s.t("welcome_approved", runtime.Esc(code(s.driver.Code)))
		if s.driver.IsOnline {
			text = s.t("welcome_online", runtime.Esc(code(s.driver.Code)))
		}
		err = b.send(ctx, s, text, b.keyboard(s))
		if kb := runtime.Inline(b.appButton(ctx, s, "btn_open_app", param)); err == nil && kb != nil {
			err = b.send(ctx, s, s.t("open_app"), kb)
		}
	default:
		err = b.statusInfo(ctx, s)
	}
	if err != nil || s.user.LanguageChosen {
		return err
	}
	return b.send(ctx, s, s.t("pick_language"), runtime.Inline(languageRow()))
}

func code(c *string) string {
	if c == nil {
		return ""
	}
	return *c
}

// registration explains how to register (or what to fix) and opens the
// registration screen of the Mini App.
func (b *Bot) registration(ctx context.Context, s *session) error {
	var lines []string
	button := "btn_register"
	switch {
	case s.status() == statusChangesRequested:
		lines = append(lines, s.t("status_changes_text"))
		if sum, err := b.api.DriverSummary(ctx, s.tgID); err == nil {
			lines = append(lines, b.corrections(s, sum.Corrections)...)
		} else {
			b.log.Warn("driver summary failed", "err", err.Error())
		}
		button = "btn_fix"
	case s.driver.PhoneVerified:
		lines = append(lines, s.t("register_verified"))
	default:
		lines = append(lines, s.t("register"))
	}
	if s.status() == statusDraft {
		button = "btn_continue"
	}
	return b.send(ctx, s, strings.Join(lines, "\n"), runtime.Inline(b.appButton(ctx, s, button, "register")))
}

// corrections renders the moderator remarks.
func (b *Bot) corrections(s *session, items []botapi.CorrectionItem) []string {
	if len(items) == 0 {
		return nil
	}
	lines := []string{"", s.t("corrections_title")}
	for _, c := range items {
		lines = append(lines, s.t("correction_line", runtime.Esc(s.t("field_"+c.Field)), runtime.Esc(s.t("reason_"+c.Reason))))
		if c.Comment != nil && strings.TrimSpace(*c.Comment) != "" {
			lines = append(lines, s.t("correction_comment", runtime.Esc(strings.TrimSpace(*c.Comment))))
		}
	}
	return lines
}

// statusInfo explains a registration that is not (or no longer) approved:
// pending review, declined or suspended.
func (b *Bot) statusInfo(ctx context.Context, s *session) error {
	switch s.status() {
	case statusPending:
		return b.send(ctx, s, s.t("status_pending_text"), b.keyboard(s))
	case statusRejected:
		text, kb := b.supportText(ctx, s)
		if err := b.send(ctx, s, s.t("status_rejected_text"), b.keyboard(s)); err != nil {
			return err
		}
		return b.send(ctx, s, text, kb)
	case statusSuspended:
		text, kb := b.supportText(ctx, s)
		if err := b.send(ctx, s, s.t("status_suspended_text"), b.keyboard(s)); err != nil {
			return err
		}
		return b.send(ctx, s, text, kb)
	case statusApproved:
		return b.send(ctx, s, s.t("help"), b.keyboard(s))
	}
	return b.registration(ctx, s)
}

// requireApproved answers for drivers who cannot work yet; it returns true
// when the driver is approved.
func (b *Bot) requireApproved(ctx context.Context, s *session) (bool, error) {
	if s.status() == statusApproved {
		return true, nil
	}
	return false, b.statusInfo(ctx, s)
}

func (b *Bot) goOnline(ctx context.Context, s *session) error {
	if ok, err := b.requireApproved(ctx, s); !ok {
		return err
	}
	av, err := b.api.SetAvailability(ctx, s.tgID, true)
	if err != nil {
		return b.availabilityError(ctx, s, err)
	}
	s.driver.IsOnline = av.IsOnline
	if err := b.send(ctx, s, s.t("online_on"), b.keyboard(s)); err != nil {
		return err
	}
	return b.send(ctx, s, s.t("live_location_hint"), runtime.Inline(b.appButton(ctx, s, "btn_orders", "orders")))
}

func (b *Bot) goOffline(ctx context.Context, s *session) error {
	if !s.driver.Registered {
		return b.registration(ctx, s)
	}
	av, err := b.api.SetAvailability(ctx, s.tgID, false)
	if err != nil {
		return b.availabilityError(ctx, s, err)
	}
	s.driver.IsOnline = av.IsOnline
	return b.send(ctx, s, s.t("online_off"), b.keyboard(s))
}

// availabilityError explains why the driver cannot go online.
func (b *Bot) availabilityError(ctx context.Context, s *session, err error) error {
	e, ok := botapi.AsError(err)
	switch {
	case !ok:
	case e.Code == "insufficient_balance":
		required, _ := e.DetailInt64("required")
		available, _ := e.DetailInt64("available")
		return b.send(ctx, s, s.t("need_balance", runtime.Esc(runtime.FormatMoney(required)), runtime.Esc(runtime.FormatMoney(available))),
			runtime.Inline(b.appButton(ctx, s, "btn_topup", "topup")))
	case e.Code == "location_required":
		return b.needLocation(ctx, s, "need_location")
	case e.Code == "driver_not_approved":
		if st := e.DetailString("status"); st != "" {
			s.driver.Status = &st
		}
		return b.statusInfo(ctx, s)
	case e.Code == "driver_not_registered":
		s.driver.Registered = false
		return b.registration(ctx, s)
	case e.DetailString("reason") == "user_blocked":
		return b.blocked(ctx, s)
	}
	_ = b.send(ctx, s, s.t("error"), b.keyboard(s))
	return fmt.Errorf("set availability: %w", err)
}

// orders lists up to five orders the driver can accept now, each with
// «✅ №N алу» and a button that opens it in the Mini App.
func (b *Bot) orders(ctx context.Context, s *session) error {
	if ok, err := b.requireApproved(ctx, s); !ok {
		return err
	}
	res, err := b.api.DriverOrders(ctx, s.tgID, 5)
	if err != nil {
		_ = b.send(ctx, s, s.t("error"), b.keyboard(s))
		return fmt.Errorf("driver orders: %w", err)
	}
	if !res.Eligibility.Eligible {
		return b.notEligible(ctx, s, res.Eligibility.ReasonValue())
	}
	if len(res.Items) == 0 {
		return b.send(ctx, s, s.t("orders_empty"), b.keyboard(s))
	}
	blocks := []string{s.t("orders_title")}
	var rows [][]telegram.InlineKeyboardButton
	for _, o := range res.Items {
		blocks = append(blocks, orderCard(s, o))
		rows = append(rows, []telegram.InlineKeyboardButton{
			runtime.CallbackButton(s.t("btn_take_order", o.Number), takePrefix+o.ID),
			runtime.WebAppButton(s.t("btn_map"), b.miniAppURL(ctx, "order_"+o.ID)),
		})
	}
	rows = append(rows, b.appButton(ctx, s, "btn_all_orders", "orders"))
	return b.send(ctx, s, strings.Join(blocks, "\n\n"), runtime.Inline(rows...))
}

// notEligible explains the first reason the driver cannot take orders.
func (b *Bot) notEligible(ctx context.Context, s *session, reason string) error {
	switch reason {
	case "offline":
		return b.send(ctx, s, s.t("go_online_first"), b.keyboard(s))
	case "has_active_order":
		return b.send(ctx, s, s.t("finish_trip_first"), runtime.Inline(b.appButton(ctx, s, "btn_trip", "trip")))
	case "insufficient_balance":
		return b.send(ctx, s, s.t("orders_need_balance"), runtime.Inline(b.appButton(ctx, s, "btn_topup", "topup")))
	case "location_required":
		return b.needLocation(ctx, s, "need_location")
	case "location_stale":
		return b.needLocation(ctx, s, "location_stale")
	}
	return b.statusInfo(ctx, s)
}

// needLocation explains that orders are dispatched by distance to the pickup
// and how to send GPS: a live location in this chat, or the Mini App (which
// reports the location while it is open), opened with the inline button.
func (b *Bot) needLocation(ctx context.Context, s *session, key string) error {
	if kb := runtime.Inline(b.appButton(ctx, s, "btn_location_app", "home")); kb != nil {
		return b.send(ctx, s, s.t(key), kb)
	}
	return b.send(ctx, s, s.t(key), b.keyboard(s))
}

// orderCard renders an available order (no passenger contact data: the API
// withholds it until the driver accepts).
func orderCard(s *session, o botapi.OrderCard) string {
	lines := []string{s.t("order_head", o.Number, runtime.Esc(s.t("category_"+o.Category)), runtime.Esc(runtime.FormatMoney(o.Fare)))}
	if l := localityLine(o.Locality); l != "" {
		lines = append(lines, l)
	}
	if len(o.Stops) > 0 {
		lines = append(lines, s.t("order_from", runtime.Esc(stopLabel(s, o.Stops[0]))))
		lines = append(lines, s.t("order_to", runtime.Esc(stopLabel(s, o.Stops[len(o.Stops)-1]))))
	}
	if o.StopCount > 2 {
		lines = append(lines, s.t("order_stops", o.StopCount-2))
	}
	if o.DistanceToPickupM != nil {
		lines = append(lines, s.t("order_pickup_dist", runtime.Esc(runtime.FormatDistance(*o.DistanceToPickupM, s.lang))))
	}
	if o.DistanceM != nil {
		lines = append(lines, s.t("order_meta", runtime.Esc(runtime.FormatDistance(*o.DistanceM, s.lang))))
	}
	if o.CargoDescription != nil && *o.CargoDescription != "" {
		lines = append(lines, s.t("order_cargo", runtime.Esc(*o.CargoDescription)))
	}
	if o.Comment != nil && *o.Comment != "" {
		lines = append(lines, s.t("order_comment", runtime.Esc(*o.Comment)))
	}
	return strings.Join(lines, "\n")
}

// localityLine is "📍 <settlement of the pickup>", or "" when the locality is
// unknown (coordinates are never shown).
func localityLine(l *string) string {
	if name := runtime.PlaceLabel(botapi.LocalityOf(l), ""); name != "" {
		return "📍 " + runtime.Esc(name)
	}
	return ""
}

// stopLabel is the stop address, or "point on the map" when it has none
// (coordinates are never shown).
func stopLabel(s *session, st botapi.StopSummary) string {
	return runtime.PlaceLabel(st.Address, s.t("map_point"))
}

// summary loads the driver summary, answering with an error message on failure.
func (b *Bot) summary(ctx context.Context, s *session) (botapi.DriverSummary, bool, error) {
	sum, err := b.api.DriverSummary(ctx, s.tgID)
	if err != nil {
		_ = b.send(ctx, s, s.t("error"), b.keyboard(s))
		return sum, false, fmt.Errorf("driver summary: %w", err)
	}
	s.driver.IsOnline = sum.IsOnline
	s.driver.PhoneVerified = sum.PhoneVerified
	return sum, true, nil
}

// trip shows the current order and opens it in the Mini App.
func (b *Bot) trip(ctx context.Context, s *session) error {
	if !s.driver.Registered {
		return b.registration(ctx, s)
	}
	sum, ok, err := b.summary(ctx, s)
	if !ok {
		return err
	}
	o := sum.ActiveOrder
	if o == nil {
		return b.send(ctx, s, s.t("trip_none"), b.keyboard(s))
	}
	lines := []string{
		s.t("trip_title", o.Number),
		s.t("trip_status", runtime.Esc(s.t("order_status_"+o.Status))),
	}
	if l := localityLine(o.Locality); l != "" {
		lines = append(lines, l)
	}
	lines = append(lines,
		s.t("order_from", runtime.Esc(runtime.PlaceLabel(o.PickupAddress, s.t("map_point")))),
		s.t("order_to", runtime.Esc(runtime.PlaceLabel(o.DestinationAddress, s.t("map_point")))),
		s.t("trip_fare", runtime.Esc(runtime.FormatMoney(o.Fare))),
	)
	return b.send(ctx, s, strings.Join(lines, "\n"), runtime.Inline(b.appButton(ctx, s, "btn_trip", "trip")))
}

// balance shows the wallet.
func (b *Bot) balance(ctx context.Context, s *session) error {
	if !s.driver.Registered {
		return b.registration(ctx, s)
	}
	sum, ok, err := b.summary(ctx, s)
	if !ok {
		return err
	}
	w := sum.Wallet
	if w == nil {
		w = &botapi.Wallet{}
	}
	text := s.t("balance", runtime.Esc(runtime.FormatMoney(w.Balance)), runtime.Esc(runtime.FormatMoney(w.Reserved)),
		runtime.Esc(runtime.FormatMoney(w.Available)))
	if w.MinOnlineBalance > 0 {
		text += "\n" + s.t("balance_min", runtime.Esc(runtime.FormatMoney(w.MinOnlineBalance)))
	}
	return b.send(ctx, s, text, runtime.Inline(
		[]telegram.InlineKeyboardButton{
			runtime.WebAppButton(s.t("btn_topup"), b.miniAppURL(ctx, "topup")),
			runtime.WebAppButton(s.t("btn_wallet"), b.miniAppURL(ctx, "wallet")),
		}))
}

// topup opens the top-up screen of the Mini App.
func (b *Bot) topup(ctx context.Context, s *session) error {
	if !s.driver.Registered {
		return b.registration(ctx, s)
	}
	kb := runtime.Inline(b.appButton(ctx, s, "btn_topup", "topup"))
	if kb == nil {
		return b.send(ctx, s, s.t("topup")+"\n\n"+s.t("error"), b.keyboard(s))
	}
	return b.send(ctx, s, s.t("topup"), kb)
}

// profile shows the registration state, vehicle and open corrections.
func (b *Bot) profile(ctx context.Context, s *session) error {
	if !s.driver.Registered {
		return b.registration(ctx, s)
	}
	sum, ok, err := b.summary(ctx, s)
	if !ok {
		return err
	}
	name := strings.TrimSpace(sum.FirstName + " " + sum.LastName)
	lines := []string{s.t("profile_title")}
	if name != "" {
		lines = append(lines, s.t("profile_name", runtime.Esc(name)))
	}
	lines = append(lines,
		s.t("profile_code", runtime.Esc(code(sum.Code))),
		s.t("profile_status", runtime.Esc(s.t("driver_status_"+sum.StatusValue()))))
	if v := sum.Vehicle; v != nil && (v.Make != "" || v.Plate != "") {
		car := strings.TrimSpace(strings.Join([]string{v.Make, v.Model, v.Color}, " "))
		if v.Plate != "" {
			car = strings.TrimSpace(car + " · " + v.Plate)
		}
		lines = append(lines, s.t("profile_vehicle", runtime.Esc(car)))
	}
	if sum.Rating != nil {
		lines = append(lines, s.t("profile_rating", runtime.Esc(formatRating(*sum.Rating, s.lang)), sum.CompletedTrips))
	} else {
		lines = append(lines, s.t("profile_trips", sum.CompletedTrips))
	}
	if sum.PhoneVerified {
		lines = append(lines, s.t("profile_phone_ok"))
	} else {
		lines = append(lines, s.t("profile_phone_no"))
	}
	if sum.StatusValue() == statusChangesRequested {
		lines = append(lines, b.corrections(s, sum.Corrections)...)
	}
	startParam, button := "profile", "btn_profile"
	if s.registering() {
		startParam, button = "register", "btn_fix"
		if sum.StatusValue() == statusDraft {
			button = "btn_continue"
		}
	}
	return b.send(ctx, s, strings.Join(lines, "\n"), runtime.Inline(b.appButton(ctx, s, button, startParam)))
}

// formatRating renders 4.86 as "4,86" (kk, ru) or "4.86" (en).
func formatRating(r float64, lang string) string {
	out := strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", r), "0"), ".")
	if runtime.Lang(lang) != "en" {
		out = strings.ReplaceAll(out, ".", ",")
	}
	return out
}

// supportText renders the configured support contacts.
func (b *Bot) supportText(ctx context.Context, s *session) (string, *telegram.InlineKeyboardMarkup) {
	c, ok := b.conf.Get(ctx)
	if !ok {
		return s.t("error"), nil
	}
	lines := []string{s.t("support_title")}
	var kb *telegram.InlineKeyboardMarkup
	if u := c.Support.TelegramUsername; u != nil && *u != "" {
		lines = append(lines, s.t("support_tg", runtime.Esc(*u)))
		kb = runtime.Inline([]telegram.InlineKeyboardButton{runtime.URLButton(s.t("btn_support"), runtime.BotLink(*u, ""))})
	}
	if p := c.Support.Phone; p != nil && *p != "" {
		lines = append(lines, s.t("support_phone", runtime.Esc(runtime.FormatPhone(*p))))
	}
	if len(lines) == 1 {
		lines = append(lines, s.t("support_none"))
	}
	if h := c.Support.Hours.Get(s.lang); h != "" {
		lines = append(lines, s.t("support_hours", runtime.Esc(h)))
	}
	return strings.Join(lines, "\n"), kb
}

func (b *Bot) support(ctx context.Context, s *session) error {
	text, kb := b.supportText(ctx, s)
	if kb == nil {
		return b.send(ctx, s, text, b.keyboard(s))
	}
	return b.send(ctx, s, text, kb)
}

// blocked tells an admin-blocked user to contact support and nothing else.
func (b *Bot) blocked(ctx context.Context, s *session) error {
	text, kb := b.supportText(ctx, s)
	var markup any = &telegram.ReplyKeyboardRemove{RemoveKeyboard: true}
	if kb != nil {
		markup = kb
	}
	return b.send(ctx, s, s.t("blocked")+"\n\n"+text, markup)
}

func (b *Bot) languagePicker(ctx context.Context, s *session) error {
	return b.send(ctx, s, s.t("pick_language"), runtime.Inline(languageRow()))
}

// onCallback handles inline button presses: «✅ Тапсырысты алу» on offers and the
// language picker.
func (b *Bot) onCallback(ctx context.Context, q *telegram.CallbackQuery) error {
	if orderID, ok := strings.CutPrefix(q.Data, takePrefix); ok {
		return b.onTake(ctx, q, orderID)
	}
	lang, ok := strings.CutPrefix(q.Data, "lang:")
	if !ok || runtime.Lang(lang) != lang {
		return b.tg.AnswerCallbackQuery(ctx, telegram.AnswerCallbackQueryParams{CallbackQueryID: q.ID})
	}
	user, err := b.api.SyncUser(ctx, syncRequest(&q.From))
	if err != nil {
		_ = b.tg.AnswerCallbackQuery(ctx, telegram.AnswerCallbackQueryParams{CallbackQueryID: q.ID, Text: texts.T(runtime.DefaultLanguage, "error")})
		return fmt.Errorf("sync user: %w", err)
	}
	chatID := q.From.ID
	if q.Message != nil {
		chatID = q.Message.Chat.ID
	}
	s := &session{chatID: chatID, tgID: q.From.ID}
	s.apply(user)
	if user.IsBlocked {
		_ = b.tg.AnswerCallbackQuery(ctx, telegram.AnswerCallbackQueryParams{CallbackQueryID: q.ID})
		return b.blocked(ctx, s)
	}
	defer b.menus.Ensure(ctx, s.chatID)
	if _, err := b.api.SetLanguage(ctx, q.From.ID, lang); err != nil {
		_ = b.tg.AnswerCallbackQuery(ctx, telegram.AnswerCallbackQueryParams{CallbackQueryID: q.ID, Text: s.t("error")})
		return fmt.Errorf("set language: %w", err)
	}
	s.lang = lang
	_ = b.tg.AnswerCallbackQuery(ctx, telegram.AnswerCallbackQueryParams{CallbackQueryID: q.ID, Text: s.t("language_set")})
	return b.send(ctx, s, s.t("language_set"), b.keyboard(s))
}
