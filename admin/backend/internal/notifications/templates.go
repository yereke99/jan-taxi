package notifications

// Template registry. This file is the contract for callers of Enqueue: pass
// exactly the params listed for the template (optional ones may be omitted or
// empty). Values are rendered by kind:
//
//	text      any string; HTML-escaped, newlines collapsed, cut to 300 characters
//	money     integer KZT (int, int64, json.Number); rendered "1 500 ₸"
//	number    integer, rendered without grouping (order numbers: "1042")
//	id        UUID or [A-Za-z0-9_-]{1,64}; used in Mini App start params
//	category  "taxi" | "parcel" | "other"; rendered as a localized label
//
// An optional param that is missing or empty removes every body line that
// references it, so "Reason: {{reason}}" disappears when no reason is given.
//
//	key                          bot        params (* = optional)                                  button → start param
//	order_created_passenger      passenger  order_id, order_number, fare                           order_<order_id>
//	order_available_driver       driver     order_id, order_number, category, pickup,
//	                                        destination, fare                                      order_<order_id>
//	order_accepted_passenger     passenger  order_id, order_number, driver_name, vehicle, plate    order_<order_id>
//	order_accepted_driver        driver     order_number, pickup, fare, commission                 trip
//	order_driver_arriving        passenger  order_id, vehicle, plate                               order_<order_id>
//	order_started                passenger  order_id, order_number                                 order_<order_id>
//	order_stop_completed         passenger  order_id, order_number, stop_address                   order_<order_id>
//	order_completed_passenger    passenger  order_id, order_number, fare                           order_<order_id>
//	order_completed_driver       driver     order_number, fare, commission, balance                wallet
//	order_cancelled_passenger    passenger  order_number, reason*                                  home
//	order_cancelled_driver       driver     order_number, reason*                                  orders
//	order_taken_driver           driver     order_number                                           orders
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
//	wallet_low_balance           driver     available                                              topup
//	dispute_opened               both       order_id, order_number                                 order_<order_id>
//	dispute_resolved             both       order_id, order_number, resolution*                    order_<order_id>
//	phone_verified               driver     phone                                                  register
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

import (
	"fmt"
	"regexp"
	"sort"
)

// ParamKind controls validation and formatting of a template param.
type ParamKind string

const (
	KindText     ParamKind = "text"
	KindMoney    ParamKind = "money"
	KindNumber   ParamKind = "number"
	KindID       ParamKind = "id"
	KindCategory ParamKind = "category"
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

// Template is a registered message.
type Template struct {
	Key    string            `json:"key"`
	Bots   []Bot             `json:"bots"`
	Params []Param           `json:"params"`
	Bodies map[string]string `json:"bodies"` // default HTML bodies by language
	Button *Button           `json:"button,omitempty"`
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
	TplOrderTakenDriver           = "order_taken_driver"
	TplOrderNoDriver              = "order_no_driver"
	TplModerationSubmitted        = "moderation_submitted"
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
)

// Shared params.
var (
	pOrderID     = Param{Name: "order_id", Kind: KindID, Description: "orders.id (UUID), used in the button start param"}
	pOrderNumber = Param{Name: "order_number", Kind: KindNumber, Description: "orders.number, e.g. 1042"}
	pFare        = Param{Name: "fare", Kind: KindMoney, Description: "order fare, KZT"}
	pCommission  = Param{Name: "commission", Kind: KindMoney, Description: "order commission, KZT"}
	pBalance     = Param{Name: "balance", Kind: KindMoney, Description: "wallet balance after the operation, KZT"}
	pPickup      = Param{Name: "pickup", Kind: KindText, Description: "pickup address"}
	pVehicle     = Param{Name: "vehicle", Kind: KindText, Description: "vehicle, e.g. \"Toyota Camry, white\""}
	pPlate       = Param{Name: "plate", Kind: KindText, Description: "normalised plate, e.g. 123ABC04"}
	pReasonOpt   = Param{Name: "reason", Kind: KindText, Optional: true, Description: "free-text reason given by the actor"}
	pTopUpID     = Param{Name: "topup_id", Kind: KindID, Description: "top_up_requests.id (UUID)"}
	pReference   = Param{Name: "reference", Kind: KindText, Description: "top-up reference, e.g. EMB001-TOPUP-000123"}
	pAmount      = Param{Name: "amount", Kind: KindMoney, Description: "top-up amount, KZT"}
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
			pPickup,
			{Name: "destination", Kind: KindText, Description: "final stop address"},
			pFare},
		Bodies: labels(
			"<b>Жаңа тапсырыс №{{order_number}}</b> · {{category}}\nҚайдан: {{pickup}}\nҚайда: {{destination}}\nБағасы: {{fare}}",
			"<b>Новый заказ №{{order_number}}</b> · {{category}}\nОткуда: {{pickup}}\nКуда: {{destination}}\nСтоимость: {{fare}}",
			"<b>New order #{{order_number}}</b> · {{category}}\nFrom: {{pickup}}\nTo: {{destination}}\nFare: {{fare}}"),
		Button: &Button{Label: labels("Тапсырысты көру", "Посмотреть заказ", "View order"), StartParam: "order_{{order_id}}"},
	},
	{
		Key: TplOrderAcceptedPassenger, Bots: passengerOnly,
		Params: []Param{pOrderID, pOrderNumber,
			{Name: "driver_name", Kind: KindText, Description: "driver's first name"},
			pVehicle, pPlate},
		Bodies: labels(
			"<b>Тапсырыс №{{order_number}}: жүргізуші табылды</b>\n{{driver_name}} · {{vehicle}}\nКөлік нөмірі: <b>{{plate}}</b>",
			"<b>Заказ №{{order_number}}: водитель найден</b>\n{{driver_name}} · {{vehicle}}\nГосномер: <b>{{plate}}</b>",
			"<b>Order #{{order_number}}: driver found</b>\n{{driver_name}} · {{vehicle}}\nPlate: <b>{{plate}}</b>"),
		Button: btnOrder,
	},
	{
		Key: TplOrderAcceptedDriver, Bots: driverOnly,
		Params: []Param{pOrderNumber, pPickup, pFare, pCommission},
		Bodies: labels(
			"<b>Тапсырыс №{{order_number}} сізге бекітілді</b>\nҚайдан: {{pickup}}\nБағасы: {{fare}} · Комиссия: {{commission}} (резервтелді)",
			"<b>Заказ №{{order_number}} закреплён за вами</b>\nОткуда: {{pickup}}\nСтоимость: {{fare}} · Комиссия: {{commission}} (зарезервирована)",
			"<b>Order #{{order_number}} is assigned to you</b>\nFrom: {{pickup}}\nFare: {{fare}} · Commission: {{commission}} (reserved)"),
		Button: &Button{Label: labels("Сапарды ашу", "Открыть поездку", "Open trip"), StartParam: "trip"},
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
		Params: []Param{pOrderNumber, pFare, pCommission, pBalance},
		Bodies: labels(
			"<b>Тапсырыс №{{order_number}} аяқталды</b>\nБағасы: {{fare}} · Комиссия: {{commission}}\nБаланс: {{balance}}",
			"<b>Заказ №{{order_number}} завершён</b>\nСтоимость: {{fare}} · Комиссия: {{commission}}\nБаланс: {{balance}}",
			"<b>Order #{{order_number}} completed</b>\nFare: {{fare}} · Commission: {{commission}}\nBalance: {{balance}}"),
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
		Params: []Param{pOrderNumber, pReasonOpt},
		Bodies: labels(
			"<b>Тапсырыс №{{order_number}} жойылды</b>\nСебебі: {{reason}}\nКомиссия резерві қайтарылды.",
			"<b>Заказ №{{order_number}} отменён</b>\nПричина: {{reason}}\nРезерв комиссии возвращён.",
			"<b>Order #{{order_number}} was cancelled</b>\nReason: {{reason}}\nThe commission reserve has been released."),
		Button: &Button{Label: labels("Тапсырыстар", "Заказы", "Orders"), StartParam: "orders"},
	},
	{
		Key: TplOrderTakenDriver, Bots: driverOnly,
		Params: []Param{pOrderNumber},
		Bodies: labels(
			"Тапсырыс №{{order_number}} басқа жүргізушіге берілді.",
			"Заказ №{{order_number}} принял другой водитель.",
			"Order #{{order_number}} was taken by another driver."),
		Button: &Button{Label: labels("Басқа тапсырыстар", "Другие заказы", "Other orders"), StartParam: "orders"},
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
			"<b>Өтінім жіберілді</b>\nДеректеріңізді тексеріп жатырмыз. Нәтижесін осы чатқа жібереміз.",
			"<b>Заявка отправлена</b>\nМы проверяем ваши данные. Результат пришлём в этот чат.",
			"<b>Application submitted</b>\nWe're reviewing your details and will send the result to this chat."),
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
			"<b>Аккаунтыңыз қайта іске қосылды</b>\nЕнді желіге шығып, тапсырыс ала аласыз.",
			"<b>Ваш аккаунт восстановлен</b>\nМожно снова выходить на линию и принимать заказы.",
			"<b>Your account is active again</b>\nYou can go online and take orders."),
		Button: &Button{Label: labels("Қосымшаны ашу", "Открыть приложение", "Open app"), StartParam: "home"},
	},
	{
		Key: TplTopUpCreated, Bots: driverOnly,
		Params: []Param{pTopUpID, pReference, pAmount},
		Bodies: labels(
			"<b>Толтыру өтінімі жасалды</b>\nСомасы: {{amount}}\nТөлем коды: <b>{{reference}}</b>\nKaspi арқылы төлеп, қосымшада төлемді растаңыз.",
			"<b>Заявка на пополнение создана</b>\nСумма: {{amount}}\nКод платежа: <b>{{reference}}</b>\nОплатите через Kaspi и подтвердите оплату в приложении.",
			"<b>Top-up request created</b>\nAmount: {{amount}}\nPayment reference: <b>{{reference}}</b>\nPay via Kaspi, then confirm the payment in the app."),
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
		Params: []Param{{Name: "available", Kind: KindMoney, Description: "available balance (balance - reserved), KZT"}},
		Bodies: labels(
			"<b>Балансыңыз аз қалды</b>\nҚолжетімді: {{available}}\nТапсырыстарды жіберіп алмау үшін балансты толтырыңыз.",
			"<b>Баланс заканчивается</b>\nДоступно: {{available}}\nПополните баланс, чтобы не пропускать заказы.",
			"<b>Your balance is low</b>\nAvailable: {{available}}\nTop up so you don't miss orders."),
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
		}
		m[t.Key] = t
	}
	return m
}
