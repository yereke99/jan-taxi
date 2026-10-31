package notifications

// Template registry. This file is the contract for callers of Enqueue: pass
// exactly the params listed for the template (optional ones may be omitted or
// empty). Values are rendered by kind:
//
//	text       any string; HTML-escaped, newlines collapsed, cut to 300 characters
//	money      integer KZT (int, int64, json.Number); rendered "1 500 ₸"
//	number     integer, rendered without grouping (order numbers: "1042")
//	id         UUID or [A-Za-z0-9_-]{1,64}; used in Mini App start params
//	category   "taxi" | "parcel" | "other"; rendered as a localized label
//	localized  {"kk","ru","en"} object; rendered in the recipient's language
//	payment    "cash" | "bank_transfer"; rendered as a localized label
//	cargo_size "s" | "m" | "l" | "xl"; rendered as a localized label
//	distance   integer metres; rendered "850 м" / "1,2 км"
//	duration   integer seconds; rendered "~12 мин" (whole minutes, at least 1)
//	place      address; an empty address or bare coordinates are rendered as
//	           "point on the map" (coordinates are never shown)
//	link       https://t.me/<username> or tg://user?id=<id>, used in <a href>
//	file       files.id (UUID) of a stored image sent as the message photo
//	           (Template.Photo); never part of the text
//
// An optional param that is missing or empty removes every body line that
// references it, so "Reason: {{reason}}" disappears when no reason is given.
//
//	key                          bot        params (* = optional)                                  button → start param
//	order_created_passenger      passenger  order_id, order_number, fare                           order_<order_id>
//	order_available_driver       driver     order_id, order_number, category, vehicle_class,
//	                                        locality*, pickup, via*, destination, pickup_distance*,
//	                                        route_distance, route_duration, fare, payment_method,
//	                                        passengers*, cargo_size*, cargo*, comment*           «Тапсырысты алу» take:<order_id>
//	                                                                                               + order_<order_id>
//	order_offer_won (edit)       driver     order_id                                               order_<order_id>
//	order_offer_taken (edit)     driver     —                                                      — (keyboard removed)
//	order_offer_cancelled (edit) driver     —                                                      — (keyboard removed)
//	order_offer_expired (edit)   driver     —                                                      — (keyboard removed)
//	order_accepted_passenger     passenger  order_id, order_number, driver_name, rating*, vehicle,
//	  (photo: driver's avatar)              plate, driver_phone*, eta_min*, photo*                 order_<order_id>
//	order_accepted_driver        driver     order_id, order_number, category*, vehicle_class*, locality*,
//	                                        pickup, pickup_details*, via*, destination, fare, payment_method,
//	                                        passengers*, cargo_size*, cargo*, comment*, passenger_name,
//	                                        passenger_link, passenger_phone*, recipient_phone*    order_<order_id>
//	order_driver_arriving        passenger  order_id, vehicle, plate                               order_<order_id>
//	order_started                passenger  order_id, order_number                                 order_<order_id>
//	order_stop_completed         passenger  order_id, order_number, stop_address                   order_<order_id>
//	order_completed_passenger    passenger  order_id, order_number, fare                           order_<order_id>
//	order_completed_driver       driver     order_number, fare, balance                            wallet
//	order_cancelled_passenger    passenger  order_number, reason*                                  home
//	order_cancelled_driver       driver     order_number, reason*, refunded*                       orders
//	order_no_driver              passenger  order_number                                           home
//	moderation_submitted         driver     —                                                      register
//	moderation_approved          driver     driver_code                                            home
//	moderation_changes_requested driver     comment*                                               register
//	moderation_rejected          driver     reason*                                                support
//	driver_suspended             driver     reason*                                                support
//	driver_restored              driver     —                                                      home
//	topup_created                driver     topup_id, reference, amount                            topup_<topup_id>
//	topup_confirmed              driver     reference, amount, balance                             wallet
//	topup_rejected               driver     topup_id, reference, reason*                           topup_<topup_id>
//	wallet_low_balance           driver     available, required                                    topup
//	driver_welcome_bonus         driver     amount, balance                                        home
//	driver_bonus_granted         driver     amount, balance, comment*                              wallet
//	dispute_opened               both       order_id, order_number                                 order_<order_id>
//	dispute_resolved             both       order_id, order_number, resolution*                    order_<order_id>
//	phone_verified               driver     phone                                                  register
//
// Edit templates (Template.Edit) never send a message: an outbox row of kind
// "edit" rewrites the message sent for another row (EnqueueEdit). The edited
// text is the edit template's body followed by the original message, and the
// edit template's button replaces the original keyboard (no button removes it).
//
// Param meanings:
//
//	order_id      orders.id (UUID), used only in the button start param
//	order_number  orders.number, e.g. 1042
//	fare          order fare in KZT
//	commission    order commission in KZT (orders.commission_amount)
//	balance       wallet balance in KZT after the operation
//	available     wallet available balance (balance - reserved) in KZT
//	amount        top-up amount in KZT (verified amount when confirmed)
//	category      orders.category
//	pickup        first stop address as shown to users
//	destination   last stop address
//	stop_address  address of the intermediate stop just completed
//	driver_name   driver's first name (optionally with last initial)
//	vehicle       "<make> <model>, <color>"
//	plate         normalised plate, e.g. 123ABC04
//	reason        free-text reason entered by the actor (cancel/reject/suspend)
//	comment       moderator's overall comment
//	resolution    admin's dispute resolution text
//	driver_code   driver_profiles.code, e.g. EMB001
//	topup_id      top_up_requests.id (UUID)
//	reference     top_up_requests.reference, e.g. EMB001-TOPUP-000123
//	phone         phone number as it should be shown (callers may mask it)
//	vehicle_class order vehicle class name {kk, ru, en}
//	locality      settlement of the pickup (city, town, village or district),
//	              e.g. "Алматы"; never coordinates
//	via           intermediate stop addresses joined with " → "
//	pickup_distance  driver's distance to the pickup, metres
//	route_distance / route_duration  route length (metres) and duration (seconds)
//	payment_method   orders.payment_method
//	passengers / cargo_size / cargo  taxi passenger count; delivery size and description
//	refunded      commission refunded to the driver, KZT
//	required      commission of the next order (the balance needed to get offers), KZT
//	passenger_name / passenger_link / passenger_phone  the passenger's first name,
//	              Telegram link (t.me or tg://user) and verified phone

import (
	"fmt"
	"regexp"
	"sort"
)

// ParamKind controls validation and formatting of a template param.
type ParamKind string

const (
	KindText      ParamKind = "text"
	KindMoney     ParamKind = "money"
	KindNumber    ParamKind = "number"
	KindID        ParamKind = "id"
	KindCategory  ParamKind = "category"
	KindLocalized ParamKind = "localized"
	KindPayment   ParamKind = "payment"
	KindCargoSize ParamKind = "cargo_size"
	KindDistance  ParamKind = "distance"
	KindDuration  ParamKind = "duration"
	KindPlace     ParamKind = "place"
	KindLink      ParamKind = "link"
	KindHTTPSLink ParamKind = "https_link"
	KindFile      ParamKind = "file"
)

// Param documents one template parameter.
type Param struct {
	Name        string    `json:"name"`
	Kind        ParamKind `json:"kind"`
	Optional    bool      `json:"optional"`
	Description string    `json:"description"`
}

// Button is the inline web_app button attached to a message. StartParam may use
// {{param}} placeholders (rendered unescaped and checked against §18's charset).
type Button struct {
	Label      map[string]string `json:"label"`
	StartParam string            `json:"start_param"`
}

// CallbackButton is an inline button that sends callback data to the bot
// (e.g. «✅ Алу» → "take:<order_id>"). Data may use {{param}} placeholders;
// the result must match callbackDataRe (at most 64 bytes).
type CallbackButton struct {
	Label map[string]string `json:"label"`
	Data  string            `json:"data"`
}

// Template is a registered message.
type Template struct {
	Key    string            `json:"key"`
	Bots   []Bot             `json:"bots"`
	Params []Param           `json:"params"`
	Bodies map[string]string `json:"bodies"` // default HTML bodies by language
	// Callback is shown in its own row above Button.
	Callback *CallbackButton `json:"callback,omitempty"`
	Button   *Button         `json:"button,omitempty"`
	// Edit marks a template that rewrites an earlier message (outbox kind
	// "edit") instead of sending a new one.
	Edit bool `json:"edit,omitempty"`
	// Photo names the file param sent as the message photo (the text becomes
	// its caption); the message is text when the param is empty.
	Photo string `json:"photo,omitempty"`
}

// AllowsBot reports whether the template may be sent by b.
func (t *Template) AllowsBot(b Bot) bool {
	for _, x := range t.Bots {
		if x == b {
			return true
		}
	}
	return false
}

// Param returns the documented param by name.
func (t *Template) Param(name string) (Param, bool) {
	for _, p := range t.Params {
		if p.Name == name {
			return p, true
		}
	}
	return Param{}, false
}

// DefaultBody returns the built-in body for lang (falling back to Kazakh).
func (t *Template) DefaultBody(lang string) string {
	if b, ok := t.Bodies[lang]; ok {
		return b
	}
	return t.Bodies[DefaultLanguage]
}

// Lookup returns the registered template for key.
func Lookup(key string) (*Template, bool) {
	t, ok := registry[key]
	return t, ok
}

// Keys returns all template keys, sorted.
func Keys() []string {
	keys := make([]string, 0, len(registry))
	for k := range registry {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Template keys (ARCHITECTURE §8).
const (
	TplOrderCreatedPassenger      = "order_created_passenger"
	TplOrderAvailableDriver       = "order_available_driver"
	TplOrderAcceptedPassenger     = "order_accepted_passenger"
	TplOrderAcceptedDriver        = "order_accepted_driver"
	TplOrderDriverArriving        = "order_driver_arriving"
	TplOrderStarted               = "order_started"
	TplOrderStopCompleted         = "order_stop_completed"
	TplOrderCompletedPassenger    = "order_completed_passenger"
	TplOrderCompletedDriver       = "order_completed_driver"
	TplOrderCancelledPassenger    = "order_cancelled_passenger"
	TplOrderCancelledDriver       = "order_cancelled_driver"
	TplOrderNoDriver              = "order_no_driver"
	TplModerationSubmitted        = "moderation_submitted"
	TplModerationReviewRequired   = "moderation_review_required"
	TplModerationApproved         = "moderation_approved"
	TplModerationChangesRequested = "moderation_changes_requested"
	TplModerationRejected         = "moderation_rejected"
	TplDriverSuspended            = "driver_suspended"
	TplDriverRestored             = "driver_restored"
	TplTopUpCreated               = "topup_created"
	TplTopUpConfirmed             = "topup_confirmed"
	TplTopUpRejected              = "topup_rejected"
	TplWalletLowBalance           = "wallet_low_balance"
	TplDisputeOpened              = "dispute_opened"
	TplDisputeResolved            = "dispute_resolved"
	TplPhoneVerified              = "phone_verified"
	TplDriverWelcomeBonus         = "driver_welcome_bonus"
	TplDriverBonusGranted         = "driver_bonus_granted"
	TplOrderOfferWon              = "order_offer_won"
	TplOrderOfferTaken            = "order_offer_taken"
	TplOrderOfferCancelled        = "order_offer_cancelled"
	TplOrderOfferExpired          = "order_offer_expired"
)

// Shared params.
var (
	pOrderID     = Param{Name: "order_id", Kind: KindID, Description: "orders.id (UUID), used in the button start param"}
	pOrderNumber = Param{Name: "order_number", Kind: KindNumber, Description: "orders.number, e.g. 1042"}
	pFare        = Param{Name: "fare", Kind: KindMoney, Description: "order fare, KZT"}
	pBalance     = Param{Name: "balance", Kind: KindMoney, Description: "wallet balance after the operation, KZT"}
	pPickup      = Param{Name: "pickup", Kind: KindPlace, Description: "pickup address"}
	pDestination = Param{Name: "destination", Kind: KindPlace, Description: "final stop address"}
	pPayment     = Param{Name: "payment_method", Kind: KindPayment, Description: "orders.payment_method: cash | bank_transfer"}
	pVehicle     = Param{Name: "vehicle", Kind: KindText, Description: "vehicle, e.g. \"Toyota Camry, white\""}
	pPlate       = Param{Name: "plate", Kind: KindText, Description: "normalised plate, e.g. 123ABC04"}
	pReasonOpt   = Param{Name: "reason", Kind: KindText, Optional: true, Description: "free-text reason given by the actor"}
	pTopUpID     = Param{Name: "topup_id", Kind: KindID, Description: "top_up_requests.id (UUID)"}
	pReference   = Param{Name: "reference", Kind: KindText, Description: "top-up reference, e.g. EMB001-TOPUP-000123"}
	pAmount      = Param{Name: "amount", Kind: KindMoney, Description: "top-up amount, KZT"}
	pLocalityOpt = Param{Name: "locality", Kind: KindText, Optional: true, Description: "settlement of the pickup, e.g. Алматы"}
)

func labels(kk, ru, en string) map[string]string {
	return map[string]string{LangKK: kk, LangRU: ru, LangEN: en}
}

var (
	btnOrder    = &Button{Label: labels("Тапсырысты ашу", "Открыть заказ", "Open order"), StartParam: "order_{{order_id}}"}
	btnSupport  = &Button{Label: labels("Қолдау қызметі", "Поддержка", "Support"), StartParam: "support"}
	btnWallet   = &Button{Label: labels("Әмиянды ашу", "Открыть кошелёк", "Open wallet"), StartParam: "wallet"}
	btnRegister = &Button{Label: labels("Өтінімді ашу", "Открыть заявку", "Open application"), StartParam: "register"}
)

var passengerOnly, driverOnly, bothBots = []Bot{PassengerBot}, []Bot{DriverBot}, []Bot{PassengerBot, DriverBot}

var registry = buildRegistry([]*Template{
	{
		Key: TplOrderCreatedPassenger, Bots: passengerOnly,
		Params: []Param{pOrderID, pOrderNumber, pFare},
		Bodies: labels(
			"<b>Тапсырыс №{{order_number}} рәсімделді</b>\nЖүргізуші іздеп жатырмыз. Бағасы: {{fare}}",
			"<b>Заказ №{{order_number}} оформлен</b>\nИщем водителя. Стоимость: {{fare}}",
			"<b>Order #{{order_number}} placed</b>\nLooking for a driver. Fare: {{fare}}"),
		Button: btnOrder,
	},
	{
		Key: TplOrderAvailableDriver, Bots: driverOnly,
		Params: []Param{pOrderID, pOrderNumber,
			{Name: "category", Kind: KindCategory, Description: "orders.category: taxi | parcel | other"},
			{Name: "vehicle_class", Kind: KindLocalized, Description: "vehicle class name {kk, ru, en}"},
			pLocalityOpt, pPickup,
			{Name: "via", Kind: KindText, Optional: true, Description: "intermediate stop addresses joined with \" → \""},
			pDestination,
			{Name: "pickup_distance", Kind: KindDistance, Optional: true, Description: "driver's distance to the pickup, metres"},
			{Name: "route_distance", Kind: KindDistance, Optional: true, Description: "route length, metres"},
			{Name: "route_duration", Kind: KindDuration, Optional: true, Description: "route duration, seconds"},
			pFare, pPayment,
			{Name: "passengers", Kind: KindNumber, Optional: true, Description: "taxi passenger count"},
			{Name: "cargo_size", Kind: KindCargoSize, Optional: true, Description: "delivery cargo size: s | m | l | xl"},
			{Name: "cargo", Kind: KindText, Optional: true, Description: "delivery cargo description"},
			{Name: "comment", Kind: KindText, Optional: true, Description: "passenger's comment"}},
		// No commission: drivers see the fare only (it is still charged at
		// acceptance, §22.2). An admin override that still names
		// {{commission}} no longer validates, so the default body is sent.
		Bodies: labels(
			"🚕 <b>Жаңа тапсырыс №{{order_number}}</b> · {{category}}\n💰 Бағасы: <b>{{fare}}</b> · {{payment_method}}\n🏙 {{locality}}\n📍 Қайдан (A): {{pickup}}\n🛣 Аялдамалар: {{via}}\n🏁 Қайда (B): {{destination}}\n🧭 A нүктесіне дейін: {{pickup_distance}}\n🗺 Маршрут: {{route_distance}} · {{route_duration}}\n🚗 Көлік: {{vehicle_class}}\n👤 Жолаушылар: {{passengers}}\n📦 Жүк көлемі: {{cargo_size}}\n📦 Жүк: {{cargo}}\n📝 Пікір: {{comment}}",
			"🚕 <b>Новый заказ №{{order_number}}</b> · {{category}}\n💰 Цена: <b>{{fare}}</b> · {{payment_method}}\n🏙 {{locality}}\n📍 Откуда (A): {{pickup}}\n🛣 Остановки: {{via}}\n🏁 Куда (B): {{destination}}\n🧭 До точки A: {{pickup_distance}}\n🗺 Маршрут: {{route_distance}} · {{route_duration}}\n🚗 Машина: {{vehicle_class}}\n👤 Пассажиров: {{passengers}}\n📦 Размер груза: {{cargo_size}}\n📦 Груз: {{cargo}}\n📝 Комментарий: {{comment}}",
			"🚕 <b>New order #{{order_number}}</b> · {{category}}\n💰 Fare: <b>{{fare}}</b> · {{payment_method}}\n🏙 {{locality}}\n📍 Pickup (A): {{pickup}}\n🛣 Stops: {{via}}\n🏁 Destination (B): {{destination}}\n🧭 To pickup: {{pickup_distance}}\n🗺 Route: {{route_distance}} · {{route_duration}}\n🚗 Vehicle: {{vehicle_class}}\n👤 Passengers: {{passengers}}\n📦 Cargo size: {{cargo_size}}\n📦 Cargo: {{cargo}}\n📝 Comment: {{comment}}"),
		Callback: &CallbackButton{Label: labels("✅ Тапсырысты алу", "✅ Забрать заказ", "✅ Accept order"), Data: "take:{{order_id}}"},
		Button:   &Button{Label: labels("📱 Қосымшаны ашу", "📱 Открыть мини-приложение", "📱 Open Mini App"), StartParam: "order_{{order_id}}"},
	},
	{
		Key: TplOrderOfferWon, Bots: driverOnly, Edit: true,
		Params: []Param{pOrderID},
		Bodies: labels("<b>✅ Сіз алдыңыз</b>", "<b>✅ Вы взяли заказ</b>", "<b>✅ You took this order</b>"),
		Button: btnOrder,
	},
	{
		Key: TplOrderOfferTaken, Bots: driverOnly, Edit: true,
		Bodies: labels("<b>❌ Тапсырысты басқа жүргізуші алды</b>", "<b>❌ Заказ взял другой водитель</b>",
			"<b>❌ Another driver took this order</b>"),
	},
	{
		Key: TplOrderOfferCancelled, Bots: driverOnly, Edit: true,
		Bodies: labels("<b>❌ Тапсырыс жойылды</b>", "<b>❌ Заказ отменён</b>", "<b>❌ The order was cancelled</b>"),
	},
	{
		// The search timed out without a driver: not a cancellation by anyone.
		Key: TplOrderOfferExpired, Bots: driverOnly, Edit: true,
		Bodies: labels("<b>⌛ Іздеу уақыты бітті</b>", "<b>⌛ Поиск водителя завершён</b>", "<b>⌛ The search has ended</b>"),
	},
	{
		Key: TplOrderAcceptedPassenger, Bots: passengerOnly, Photo: "photo",
		Params: []Param{pOrderID, pOrderNumber,
			{Name: "driver_name", Kind: KindText, Description: "driver's first name with the last-name initial"},
			{Name: "rating", Kind: KindText, Optional: true, Description: "driver's average rating, e.g. 4.9"},
			pVehicle, pPlate,
			{Name: "driver_phone", Kind: KindText, Optional: true, Description: "driver's phone, formatted"},
			{Name: "eta_min", Kind: KindNumber, Optional: true, Description: "minutes until the driver reaches the pickup"},
			{Name: "photo", Kind: KindFile, Optional: true, Description: "files.id of the driver's photo, sent as the message photo"}},
		Bodies: labels(
			"<b>Жүргізуші табылды · №{{order_number}}</b>\n{{driver_name}}\nРейтинг: ★ {{rating}}\n{{vehicle}}\nКөлік нөмірі: <b>{{plate}}</b>\nТелефон: {{driver_phone}}\n<b>~{{eta_min}} минутта келеді</b>",
			"<b>Водитель найден · №{{order_number}}</b>\n{{driver_name}}\nРейтинг: ★ {{rating}}\n{{vehicle}}\nГосномер: <b>{{plate}}</b>\nТелефон: {{driver_phone}}\n<b>Приедет через ~{{eta_min}} мин</b>",
			"<b>Driver found · #{{order_number}}</b>\n{{driver_name}}\nRating: ★ {{rating}}\n{{vehicle}}\nPlate: <b>{{plate}}</b>\nPhone: {{driver_phone}}\n<b>Arrives in ~{{eta_min}} min</b>"),
		Button: &Button{Label: labels("Картада қарау", "Смотреть на карте", "View on the map"), StartParam: "order_{{order_id}}"},
	},
	{
		Key: TplOrderAcceptedDriver, Bots: driverOnly,
		Params: []Param{pOrderID, pOrderNumber,
			// Optional so that cards queued before these lines existed still render.
			{Name: "category", Kind: KindCategory, Optional: true, Description: "orders.category: taxi | parcel | other"},
			{Name: "vehicle_class", Kind: KindLocalized, Optional: true, Description: "vehicle class name {kk, ru, en}"},
			pLocalityOpt, pPickup,
			{Name: "pickup_details", Kind: KindText, Optional: true, Description: "passenger's note for pickup A (entrance, floor)"},
			{Name: "via", Kind: KindText, Optional: true, Description: "intermediate stop addresses joined with \" → \""},
			pDestination, pFare, pPayment,
			{Name: "passengers", Kind: KindNumber, Optional: true, Description: "taxi passenger count"},
			{Name: "cargo_size", Kind: KindCargoSize, Optional: true, Description: "delivery cargo size: s | m | l | xl"},
			{Name: "cargo", Kind: KindText, Optional: true, Description: "delivery cargo description"},
			{Name: "comment", Kind: KindText, Optional: true, Description: "passenger's comment"},
			{Name: "passenger_name", Kind: KindText, Description: "passenger's first name"},
			{Name: "passenger_link", Kind: KindLink, Description: "https://t.me/<username> or tg://user?id=<id>"},
			{Name: "passenger_phone", Kind: KindText, Optional: true, Description: "passenger's verified phone, formatted"},
			{Name: "recipient_phone", Kind: KindText, Optional: true, Description: "parcel recipient's phone, formatted"}},
		// The trip card of the driver who took the order, whichever channel
		// they used (§22.4); the only message that carries the passenger's
		// contact, revalidated against the assignment when it is sent.
		Bodies: labels(
			"✅ <b>Тапсырыс №{{order_number}} сізге бекітілді</b>\n🚗 {{category}} · {{vehicle_class}}\n💰 Бағасы: <b>{{fare}}</b> · {{payment_method}}\n🏙 {{locality}}\n📍 Қайдан (A): {{pickup}}\n📌 Нақтылау: {{pickup_details}}\n🛣 Аялдамалар: {{via}}\n🏁 Қайда (B): {{destination}}\n👤 Жолаушылар: {{passengers}}\n📦 Жүк көлемі: {{cargo_size}}\n📦 Жүк: {{cargo}}\n📝 Пікір: {{comment}}\n🙋 Клиент: <a href=\"{{passenger_link}}\">{{passenger_name}}</a>\n📞 Телефон: {{passenger_phone}}\n📦 Алушының телефоны: {{recipient_phone}}",
			"✅ <b>Заказ №{{order_number}} закреплён за вами</b>\n🚗 {{category}} · {{vehicle_class}}\n💰 Цена: <b>{{fare}}</b> · {{payment_method}}\n🏙 {{locality}}\n📍 Откуда (A): {{pickup}}\n📌 Уточнение: {{pickup_details}}\n🛣 Остановки: {{via}}\n🏁 Куда (B): {{destination}}\n👤 Пассажиров: {{passengers}}\n📦 Размер груза: {{cargo_size}}\n📦 Груз: {{cargo}}\n📝 Комментарий: {{comment}}\n🙋 Клиент: <a href=\"{{passenger_link}}\">{{passenger_name}}</a>\n📞 Телефон: {{passenger_phone}}\n📦 Телефон получателя: {{recipient_phone}}",
			"✅ <b>Order #{{order_number}} is yours</b>\n🚗 {{category}} · {{vehicle_class}}\n💰 Fare: <b>{{fare}}</b> · {{payment_method}}\n🏙 {{locality}}\n📍 Pickup (A): {{pickup}}\n📌 Details: {{pickup_details}}\n🛣 Stops: {{via}}\n🏁 Destination (B): {{destination}}\n👤 Passengers: {{passengers}}\n📦 Cargo size: {{cargo_size}}\n📦 Cargo: {{cargo}}\n📝 Comment: {{comment}}\n🙋 Customer: <a href=\"{{passenger_link}}\">{{passenger_name}}</a>\n📞 Phone: {{passenger_phone}}\n📦 Recipient's phone: {{recipient_phone}}"),
		Button: btnOrder,
	},
	{
		Key: TplOrderDriverArriving, Bots: passengerOnly,
		Params: []Param{pOrderID, pVehicle, pPlate},
		Bodies: labels(
			"<b>Жүргізуші келді</b>\n{{vehicle}}, нөмірі <b>{{plate}}</b>\nСізді күтіп тұр.",
			"<b>Водитель на месте</b>\n{{vehicle}}, номер <b>{{plate}}</b>\nОжидает вас.",
			"<b>Your driver has arrived</b>\n{{vehicle}}, plate <b>{{plate}}</b>\nWaiting for you."),
		Button: btnOrder,
	},
	{
		Key: TplOrderStarted, Bots: passengerOnly,
		Params: []Param{pOrderID, pOrderNumber},
		Bodies: labels(
			"<b>Тапсырыс №{{order_number}} орындалуда</b>\nСапар басталды.",
			"<b>Заказ №{{order_number}} выполняется</b>\nПоездка началась.",
			"<b>Order #{{order_number}} is in progress</b>\nThe trip has started."),
		Button: btnOrder,
	},
	{
		Key: TplOrderStopCompleted, Bots: passengerOnly,
		Params: []Param{pOrderID, pOrderNumber,
			{Name: "stop_address", Kind: KindText, Description: "address of the intermediate stop just completed"}},
		Bodies: labels(
			"<b>Аялдама өтілді</b>\nТапсырыс №{{order_number}}: {{stop_address}}",
			"<b>Остановка пройдена</b>\nЗаказ №{{order_number}}: {{stop_address}}",
			"<b>Stop completed</b>\nOrder #{{order_number}}: {{stop_address}}"),
		Button: btnOrder,
	},
	{
		Key: TplOrderCompletedPassenger, Bots: passengerOnly,
		Params: []Param{pOrderID, pOrderNumber, pFare},
		Bodies: labels(
			"<b>Тапсырыс №{{order_number}} аяқталды</b>\nТөленетін сома: {{fare}}\nБізді таңдағаныңызға рахмет!",
			"<b>Заказ №{{order_number}} завершён</b>\nК оплате: {{fare}}\nСпасибо, что выбрали нас!",
			"<b>Order #{{order_number}} completed</b>\nAmount to pay: {{fare}}\nThank you for choosing us!"),
		Button: &Button{Label: labels("Жүргізушіні бағалау", "Оценить водителя", "Rate your driver"), StartParam: "order_{{order_id}}"},
	},
	{
		Key: TplOrderCompletedDriver, Bots: driverOnly,
		Params: []Param{pOrderNumber, pFare, pBalance},
		Bodies: labels(
			"<b>Тапсырыс №{{order_number}} аяқталды</b>\nБағасы: {{fare}}\nБаланс: {{balance}}",
			"<b>Заказ №{{order_number}} завершён</b>\nСтоимость: {{fare}}\nБаланс: {{balance}}",
			"<b>Order #{{order_number}} completed</b>\nFare: {{fare}}\nBalance: {{balance}}"),
		Button: btnWallet,
	},
	{
		Key: TplOrderCancelledPassenger, Bots: passengerOnly,
		Params: []Param{pOrderNumber, pReasonOpt},
		Bodies: labels(
			"<b>Тапсырыс №{{order_number}} жойылды</b>\nСебебі: {{reason}}",
			"<b>Заказ №{{order_number}} отменён</b>\nПричина: {{reason}}",
			"<b>Order #{{order_number}} was cancelled</b>\nReason: {{reason}}"),
		Button: &Button{Label: labels("Жаңа тапсырыс", "Новый заказ", "New order"), StartParam: "home"},
	},
	{
		Key: TplOrderCancelledDriver, Bots: driverOnly,
		Params: []Param{pOrderNumber, pReasonOpt,
			{Name: "refunded", Kind: KindMoney, Optional: true, Description: "commission refunded to the driver, KZT"}},
		Bodies: labels(
			"<b>Тапсырыс №{{order_number}} жойылды</b>\nСебебі: {{reason}}\nБалансқа қайтарылды: {{refunded}}",
			"<b>Заказ №{{order_number}} отменён</b>\nПричина: {{reason}}\nВозвращено на баланс: {{refunded}}",
			"<b>Order #{{order_number}} was cancelled</b>\nReason: {{reason}}\nReturned to your balance: {{refunded}}"),
		Button: &Button{Label: labels("Тапсырыстар", "Заказы", "Orders"), StartParam: "orders"},
	},
	{
		Key: TplOrderNoDriver, Bots: passengerOnly,
		Params: []Param{pOrderNumber},
		Bodies: labels(
			"<b>Жүргізуші табылмады</b>\nТапсырыс №{{order_number}} жойылды. Кейінірек қайталап көріңіз.",
			"<b>Водитель не найден</b>\nЗаказ №{{order_number}} отменён. Попробуйте ещё раз чуть позже.",
			"<b>No driver found</b>\nOrder #{{order_number}} was cancelled. Please try again a bit later."),
		Button: &Button{Label: labels("Қайта тапсырыс беру", "Заказать снова", "Order again"), StartParam: "home"},
	},
	{
		Key: TplModerationSubmitted, Bots: driverOnly,
		Bodies: labels(
			"<b>Өтініміңіз қабылданды!</b>\nҚұжаттарыңыз тексеріліп жатыр. Мақсатымыз — шамамен 5 минут ішінде жауап беру. Кезек немесе қосымша тексеру болса, ұзағырақ уақыт қажет болуы мүмкін. Нәтижесін осы чатқа жібереміз.",
			"<b>Ваша заявка принята!</b>\nДокументы проверяются. Цель — ответить примерно за 5 минут. Очередь или дополнительная проверка могут занять больше времени. Результат пришлём в этот чат.",
			"<b>Your application has been received!</b>\nWe aim to review your documents in about 5 minutes. A queue or additional review may take longer. We will send the result to this chat."),
		Button: btnRegister,
	},
	{
		Key: TplModerationReviewRequired, Bots: driverOnly,
		Bodies: labels(
			"<b>Қосымша тексеру қажет</b>\nӨтініміңіз әкімшіге жіберілді. Нәтижесін осы чатқа хабарлаймыз.",
			"<b>Нужна дополнительная проверка</b>\nВаша заявка передана администратору. Результат сообщим в этом чате.",
			"<b>Additional review required</b>\nYour application has been sent to an administrator. We will notify you here."),
		Button: btnRegister,
	},
	{
		Key: TplModerationApproved, Bots: driverOnly,
		Params: []Param{{Name: "driver_code", Kind: KindText, Description: "driver_profiles.code, e.g. EMB001"}},
		Bodies: labels(
			"<b>Өтінім мақұлданды</b>\nСіздің кодыңыз: <b>{{driver_code}}</b>\nТапсырыс алу үшін балансты толтырып, желіге шығыңыз.",
			"<b>Заявка одобрена</b>\nВаш код: <b>{{driver_code}}</b>\nПополните баланс и выходите на линию, чтобы получать заказы.",
			"<b>Application approved</b>\nYour driver code: <b>{{driver_code}}</b>\nTop up your balance and go online to receive orders."),
		Button: &Button{Label: labels("Бастау", "Начать", "Get started"), StartParam: "home"},
	},
	{
		Key: TplModerationChangesRequested, Bots: driverOnly,
		Params: []Param{{Name: "comment", Kind: KindText, Optional: true, Description: "moderator's overall comment"}},
		Bodies: labels(
			"<b>Өтінімді түзету керек</b>\nТүсініктеме: {{comment}}\nДеректерді түзетіп, өтінімді қайта жіберіңіз.",
			"<b>Заявку нужно исправить</b>\nКомментарий: {{comment}}\nИсправьте данные и отправьте заявку снова.",
			"<b>Your application needs changes</b>\nComment: {{comment}}\nPlease correct the details and resubmit."),
		Button: &Button{Label: labels("Түзету", "Исправить", "Edit application"), StartParam: "register"},
	},
	{
		Key: TplModerationRejected, Bots: driverOnly,
		Params: []Param{pReasonOpt},
		Bodies: labels(
			"<b>Өтінім қабылданбады</b>\nСебебі: {{reason}}\nСұрақтарыңыз болса, қолдау қызметіне жазыңыз.",
			"<b>Заявка отклонена</b>\nПричина: {{reason}}\nЕсли остались вопросы, напишите в поддержку.",
			"<b>Application declined</b>\nReason: {{reason}}\nIf you have questions, contact support."),
		Button: btnSupport,
	},
	{
		Key: TplDriverSuspended, Bots: driverOnly,
		Params: []Param{pReasonOpt},
		Bodies: labels(
			"<b>Аккаунтыңыз уақытша тоқтатылды</b>\nСебебі: {{reason}}\nТолығырақ ақпаратты қолдау қызметінен біліңіз.",
			"<b>Ваш аккаунт временно приостановлен</b>\nПричина: {{reason}}\nПодробности можно узнать в поддержке.",
			"<b>Your account is suspended</b>\nReason: {{reason}}\nContact support for details."),
		Button: btnSupport,
	},
	{
		Key: TplDriverRestored, Bots: driverOnly,
		Bodies: labels(
			"<b>Аккаунт шектеуі алынды</b>\nӨтініміңіздің қазіргі күйін қосымшадан тексеріңіз.",
			"<b>Ограничение аккаунта снято</b>\nПроверьте текущий статус заявки в приложении.",
			"<b>Your account restriction was removed</b>\nCheck the current application status in the app."),
		Button: &Button{Label: labels("Қосымшаны ашу", "Открыть приложение", "Open app"), StartParam: "home"},
	},
	{
		Key: TplTopUpCreated, Bots: driverOnly,
		Params: []Param{pTopUpID, pReference, pAmount, {Name: "payment_url", Kind: KindHTTPSLink, Optional: true, Description: "Configured HTTPS Kaspi Pay payment URL, snapshotted on the top-up"}},
		Bodies: labels(
			"<b>Толтыру өтінімі жасалды</b>\nСомасы: {{amount}}\nТөлем коды: <b>{{reference}}</b>\n<a href=\"{{payment_url}}\">Kaspi Pay арқылы төлеу</a>\n\n1. Kaspi-де дәл {{amount}} төлеңіз.\n2. «Чекті бөлісу» → Telegram → осы бот.\n<b>3. Чекті осы чатқа PDF файл ретінде жіберіңіз.</b>\nФото мен скриншот автоматты толтыруға жарамайды. Чек сәйкес келсе, сома бір рет түседі; күмәнді төлемді әкімші тексереді.",
			"<b>Заявка на пополнение создана</b>\nСумма: {{amount}}\nКод платежа: <b>{{reference}}</b>\n<a href=\"{{payment_url}}\">Оплатить через Kaspi Pay</a>\n\n1. Оплатите в Kaspi ровно {{amount}}.\n2. «Поделиться чеком» → Telegram → этот бот.\n<b>3. Отправьте оригинальный PDF-чек в этот чат.</b>\nФото и скриншот не подходят для автоматического зачисления. Если чек совпадает, сумма зачислится один раз; сомнительный платёж проверит администратор.",
			"<b>Top-up request created</b>\nAmount: {{amount}}\nPayment reference: <b>{{reference}}</b>\n<a href=\"{{payment_url}}\">Pay via Kaspi Pay</a>\n\n1. Pay exactly {{amount}} in Kaspi.\n2. Share receipt → Telegram → this bot.\n<b>3. Send the original PDF receipt to this chat.</b>\nPhotos and screenshots cannot trigger automatic credit. A matching receipt credits the amount once; uncertain payments go to an administrator."),
		Button: &Button{Label: labels("Төлемді ашу", "Открыть платёж", "Open payment"), StartParam: "topup_{{topup_id}}"},
	},
	{
		Key: TplTopUpConfirmed, Bots: driverOnly,
		Params: []Param{pReference, pAmount, pBalance},
		Bodies: labels(
			"<b>Баланс толтырылды</b>\n+{{amount}} · {{reference}}\nБаланс: {{balance}}",
			"<b>Баланс пополнен</b>\n+{{amount}} · {{reference}}\nБаланс: {{balance}}",
			"<b>Balance topped up</b>\n+{{amount}} · {{reference}}\nBalance: {{balance}}"),
		Button: btnWallet,
	},
	{
		Key: TplTopUpRejected, Bots: driverOnly,
		Params: []Param{pTopUpID, pReference, pReasonOpt},
		Bodies: labels(
			"<b>Толтыру расталмады</b>\nТөлем коды: {{reference}}\nСебебі: {{reason}}\nСұрақтарыңыз болса, қолдау қызметіне жазыңыз.",
			"<b>Пополнение не подтверждено</b>\nКод платежа: {{reference}}\nПричина: {{reason}}\nЕсли остались вопросы, напишите в поддержку.",
			"<b>Top-up not confirmed</b>\nPayment reference: {{reference}}\nReason: {{reason}}\nIf you have questions, contact support."),
		Button: &Button{Label: labels("Толығырақ", "Подробнее", "Details"), StartParam: "topup_{{topup_id}}"},
	},
	{
		Key: TplWalletLowBalance, Bots: driverOnly,
		Params: []Param{{Name: "available", Kind: KindMoney, Description: "available balance (balance - reserved), KZT"},
			{Name: "required", Kind: KindMoney, Description: "commission of the next order, KZT"}},
		Bodies: labels(
			"<b>Баланс жеткіліксіз</b>\nҚолжетімді: {{available}} · Тапсырыс алу үшін керек: {{required}}\nБаланс толтырылмайынша жаңа тапсырыстар келмейді.",
			"<b>Недостаточно средств</b>\nДоступно: {{available}} · Нужно для заказа: {{required}}\nПока баланс не пополнен, новые заказы приходить не будут.",
			"<b>Balance too low</b>\nAvailable: {{available}} · Needed to take orders: {{required}}\nYou won't receive new orders until you top up."),
		Button: &Button{Label: labels("Толтыру", "Пополнить", "Top up"), StartParam: "topup"},
	},
	{
		Key: TplDisputeOpened, Bots: bothBots,
		Params: []Param{pOrderID, pOrderNumber},
		Bodies: labels(
			"<b>Тапсырыс №{{order_number}} бойынша дау ашылды</b>\nОператор жағдайды тексеріп, шешімін хабарлайды.",
			"<b>По заказу №{{order_number}} открыт спор</b>\nОператор проверит ситуацию и сообщит решение.",
			"<b>Dispute opened for order #{{order_number}}</b>\nAn operator will review it and let you know the decision."),
		Button: btnOrder,
	},
	{
		Key: TplDisputeResolved, Bots: bothBots,
		Params: []Param{pOrderID, pOrderNumber,
			{Name: "resolution", Kind: KindText, Optional: true, Description: "admin's resolution text"}},
		Bodies: labels(
			"<b>Тапсырыс №{{order_number}} бойынша дау шешілді</b>\n{{resolution}}",
			"<b>Спор по заказу №{{order_number}} решён</b>\n{{resolution}}",
			"<b>Dispute for order #{{order_number}} resolved</b>\n{{resolution}}"),
		Button: btnOrder,
	},
	{
		Key: TplDriverWelcomeBonus, Bots: driverOnly,
		Params: []Param{{Name: "amount", Kind: KindMoney, Description: "welcome bonus, KZT"}, pBalance},
		Bodies: labels(
			"<b>Сізге {{amount}} бонус есептелді</b>\nБаланс: {{balance}}\nЖеліге шығып, алғашқы тапсырыстарыңызды алыңыз.",
			"<b>Вам начислен бонус {{amount}}</b>\nБаланс: {{balance}}\nВыходите на линию и берите первые заказы.",
			"<b>A welcome bonus of {{amount}} is credited</b>\nBalance: {{balance}}\nGo online and take your first orders."),
		Button: &Button{Label: labels("Желіге шығу", "Выйти на линию", "Go online"), StartParam: "home"},
	},
	{
		Key: TplDriverBonusGranted, Bots: driverOnly,
		Params: []Param{{Name: "amount", Kind: KindMoney, Description: "bonus credited by the company, KZT"}, pBalance,
			{Name: "comment", Kind: KindText, Optional: true, Description: "operator's comment shown to the driver"}},
		Bodies: labels(
			"<b>Jan Taxi компаниясынан сізге {{amount}} бонус есептелді</b>\n{{comment}}\nБаланс: {{balance}}",
			"<b>Компания Jan Taxi начислила вам бонус {{amount}}</b>\n{{comment}}\nБаланс: {{balance}}",
			"<b>Jan Taxi has credited you a bonus of {{amount}}</b>\n{{comment}}\nBalance: {{balance}}"),
		Button: btnWallet,
	},
	{
		Key: TplPhoneVerified, Bots: driverOnly,
		Params: []Param{{Name: "phone", Kind: KindText, Description: "phone number as it should be shown (may be masked)"}},
		Bodies: labels(
			"<b>Телефон нөмірі расталды</b>\n{{phone}}\nТіркеуді жалғастыра аласыз.",
			"<b>Номер телефона подтверждён</b>\n{{phone}}\nМожно продолжить регистрацию.",
			"<b>Phone number verified</b>\n{{phone}}\nYou can continue your registration."),
		Button: &Button{Label: labels("Тіркеуді жалғастыру", "Продолжить регистрацию", "Continue registration"), StartParam: "register"},
	},
})

var keyRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// buildRegistry indexes templates and fails fast on a malformed definition, so
// a broken default can never reach production.
func buildRegistry(ts []*Template) map[string]*Template {
	m := make(map[string]*Template, len(ts))
	for _, t := range ts {
		if !keyRe.MatchString(t.Key) {
			panic("notifications: bad template key " + t.Key)
		}
		if _, dup := m[t.Key]; dup {
			panic("notifications: duplicate template " + t.Key)
		}
		for _, lang := range Languages {
			body, ok := t.Bodies[lang]
			if !ok {
				panic(fmt.Sprintf("notifications: template %s has no %s body", t.Key, lang))
			}
			if err := t.ValidateBody(body); err != nil {
				panic(fmt.Sprintf("notifications: template %s/%s: %v", t.Key, lang, err))
			}
			if t.Button != nil && t.Button.Label[lang] == "" {
				panic(fmt.Sprintf("notifications: template %s has no %s button label", t.Key, lang))
			}
			if t.Callback != nil && t.Callback.Label[lang] == "" {
				panic(fmt.Sprintf("notifications: template %s has no %s callback label", t.Key, lang))
			}
		}
		if t.Edit && t.Callback != nil {
			panic("notifications: edit template " + t.Key + " cannot carry a callback button")
		}
		m[t.Key] = t
	}
	return m
}
