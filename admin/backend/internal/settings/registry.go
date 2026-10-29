package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/textutil"
)

// Kind is the value type of a setting, exposed to the admin UI.
type Kind string

const (
	KindInt        Kind = "int"
	KindBool       Kind = "bool"
	KindString     Kind = "string"
	KindURL        Kind = "url"
	KindText       Kind = "localized_text" // {"kk","ru","en"}
	KindIntList    Kind = "int_list"
	KindCodeList   Kind = "code_list" // machine codes the apps translate, e.g. "weapons"
	KindStringList Kind = "string_list"
)

// Validation reason codes returned in validation_failed fields.
const (
	ReasonUnknown     = "unknown_setting"
	ReasonInvalidType = "invalid_type"
	ReasonInvalid     = "invalid"
	ReasonOutOfRange  = "out_of_range"
	ReasonTooLong     = "too_long"
	ReasonTooShort    = "too_short"
	ReasonTooMany     = "too_many"
	ReasonDuplicate   = "duplicate"
	ReasonIncomplete  = "incomplete" // localized text filled for some languages only
	ReasonRequired    = "required"
	ReasonReadOnly    = "read_only" // the setting cannot be changed through the API
)

// Def declares one admin-editable setting.
type Def struct {
	Key         string
	Kind        Kind
	Description Localized
	// Default is the built-in value used when neither the database nor the
	// environment provides one; it matches the seed migration.
	Default  any
	Min, Max int64 // bounds for int and int_list values
	MinLen   int   // code points per string (string_list)
	MaxLen   int   // code points per string
	MaxItems int   // list length
	Required bool  // string/url may not be empty
	// ReadOnly settings are shown to admins but cannot be changed through the
	// API (PATCH answers read_only); they bound other settings.
	ReadOnly bool

	// env returns the environment-provided default, if configured.
	env func(Env) (any, bool)
	// check replaces generic text cleaning for strings with a strict format.
	check func(string) (string, bool)
}

// HasEnvDefault reports whether the key falls back to an environment variable.
func (d Def) HasEnvDefault() bool { return d.env != nil }

// DefaultValue returns the env default when configured, else the built-in default.
func (d Def) DefaultValue(e Env) any {
	if d.env != nil {
		if v, ok := d.env(e); ok {
			return v
		}
	}
	return d.Default
}

var (
	codeRe     = regexp.MustCompile(`^[a-z][a-z0-9_]{1,39}$`)
	tgUserRe   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{3,31}$`)
	digitsOnly = regexp.MustCompile(`^[0-9]+$`)
	kzIBANRe   = regexp.MustCompile(`^KZ[0-9]{2}[0-9A-Z]{16}$`)
	bicRe      = regexp.MustCompile(`^[A-Z]{6}[A-Z0-9]{2}([A-Z0-9]{3})?$`)
	kbeRe      = regexp.MustCompile(`^[0-9]{2}$`)
)

var registry = []Def{
	{
		Key: "orders.manual_destination_enabled", Kind: KindBool, Default: false,
		Description: Localized{
			KK: "Координатсыз соңғы мекенжайды қолмен енгізуге рұқсат беру",
			RU: "Разрешить ручной ввод конечного адреса без координат",
			EN: "Allow a manually entered final destination without coordinates",
		},
	},
	{
		Key: "orders.manual_destination_pricing", Kind: KindString, Default: "passenger_tariff_bounds",
		Description: Localized{
			KK: "Қолмен енгізілген мекенжайдың бағасы: disabled немесе passenger_tariff_bounds",
			RU: "Цена ручного адреса: disabled или passenger_tariff_bounds (пределы тарифа администратора)",
			EN: "Manual destination pricing: disabled or passenger_tariff_bounds (administrator tariff limits)",
		},
		check: func(s string) (string, bool) {
			return s, s == "disabled" || s == "passenger_tariff_bounds"
		},
	},
	{
		Key: "support.telegram_username", Kind: KindString, Default: "",
		Description: Localized{
			KK: "Қолдау қызметінің Telegram пайдаланушы аты (@ белгісінсіз)",
			RU: "Имя пользователя службы поддержки в Telegram (без @)",
			EN: "Support Telegram username (without @)",
		},
		check: checkTelegramUsername,
	},
	{
		Key: "support.phone", Kind: KindString, Default: "",
		Description: Localized{
			KK: "Қолдау қызметінің телефон нөмірі",
			RU: "Телефон службы поддержки",
			EN: "Support phone number",
		},
		check: checkSupportPhone,
	},
	{
		Key: "support.hours", Kind: KindText, MaxLen: 60,
		Default: Localized{KK: "Күн сайын 08:00–23:00", RU: "Ежедневно 08:00–23:00", EN: "Daily 08:00–23:00"},
		Description: Localized{
			KK: "Қолдау қызметінің жұмыс уақыты",
			RU: "Часы работы поддержки",
			EN: "Support working hours",
		},
	},
	{
		Key: "routing.max_points", Kind: KindInt, Min: 2, Max: 5, Default: int64(5),
		Description: Localized{
			KK: "Маршруттағы нүктелердің ең көп саны (A–E)",
			RU: "Максимум точек маршрута (A–E)",
			EN: "Maximum route points (A–E)",
		},
		env: func(e Env) (any, bool) {
			if e.MaxRoutePoints < 2 {
				return nil, false
			}
			return int64(min(e.MaxRoutePoints, 5)), true
		},
	},
	{
		Key: "orders.passenger_cancels_after_accept_per_day", Kind: KindInt, Min: 0, Max: 100, Default: int64(3),
		Description: Localized{
			KK: "Жолаушы 24 сағат ішінде жүргізуші қабылдаған қанша тапсырысты болдырмаса, жаңа тапсырыс бере алмайды (әр осындай болдырмауда жүргізуші комиссиясын жоғалтады; 0 — шектеусіз)",
			RU: "Сколько принятых водителем заказов пассажир может отменить за 24 часа, прежде чем новые заказы будут отклоняться (при каждой такой отмене водитель теряет комиссию; 0 — без ограничения)",
			EN: "How many driver-accepted orders a passenger may cancel within 24 hours before new orders are refused (the driver loses the commission on each; 0 = no limit)",
		},
	},
	{
		Key: "commission.default_bps", Kind: KindInt, Min: 0, Max: 5000, Default: int64(1000),
		Description: Localized{
			KK: "Әдепкі комиссия, базистік пункт (1000 = 10%)",
			RU: "Комиссия по умолчанию, базисные пункты (1000 = 10%)",
			EN: "Default commission, basis points (1000 = 10%)",
		},
		env: func(e Env) (any, bool) {
			if e.DefaultCommissionPercent <= 0 || e.DefaultCommissionPercent > 50 {
				return nil, false
			}
			return int64(e.DefaultCommissionPercent) * 100, true
		},
	},
	{
		Key: "commission.mode", Kind: KindString, Required: true, Default: CommissionFixed,
		Description: Localized{
			KK: "Комиссия түрі: fixed — әр тапсырыстан тұрақты сома, percent — тарифтегі пайыз",
			RU: "Тип комиссии: fixed — фиксированная сумма с заказа, percent — процент из тарифа",
			EN: "Commission type: fixed — a fixed amount per order, percent — the tariff percentage",
		},
		check: checkCommissionMode,
	},
	{
		Key: "commission.fixed_amount", Kind: KindInt, Min: 0, Max: 100_000, Default: int64(50),
		Description: Localized{
			KK: "Жүргізуші тапсырысты алған кезде ұсталатын тұрақты комиссия, ₸ (commission.fixed_min…commission.fixed_max аралығында)",
			RU: "Фиксированная комиссия, списываемая при принятии заказа водителем, ₸ (в пределах commission.fixed_min…commission.fixed_max)",
			EN: "Fixed commission charged when a driver takes an order, ₸ (within commission.fixed_min…commission.fixed_max)",
		},
	},
	{
		Key: "commission.fixed_min", Kind: KindInt, Min: 0, Max: 100_000, Default: int64(50), ReadOnly: true,
		Description: Localized{
			KK: "Тұрақты комиссияның ең аз мөлшері, ₸ (өзгертілмейді)",
			RU: "Минимальная фиксированная комиссия, ₸ (не редактируется)",
			EN: "Lowest fixed commission an admin may set, ₸ (read-only)",
		},
	},
	{
		Key: "commission.fixed_max", Kind: KindInt, Min: 0, Max: 100_000, Default: int64(100), ReadOnly: true,
		Description: Localized{
			KK: "Тұрақты комиссияның ең көп мөлшері, ₸ (өзгертілмейді)",
			RU: "Максимальная фиксированная комиссия, ₸ (не редактируется)",
			EN: "Highest fixed commission an admin may set, ₸ (read-only)",
		},
	},
	{
		Key: "driver.welcome_bonus", Kind: KindInt, Min: 0, Max: 100_000, Default: int64(500),
		Description: Localized{
			KK: "Жаңа жүргізушіге бірінші мақұлдауда берілетін бонус, ₸ (телефон мен көлік нөмірі бойынша бір рет; 0 — өшірулі)",
			RU: "Бонус новому водителю при первом одобрении, ₸ (один раз на телефон и госномер; 0 — выключено)",
			EN: "Welcome bonus for a new driver on first approval, ₸ (once per phone and plate; 0 disables it)",
		},
	},
	{
		Key: "driver.min_online_balance", Kind: KindInt, Min: 0, Max: 1_000_000, Default: int64(50),
		Description: Localized{
			KK: "Желіге шығу үшін қажетті ең аз баланс, ₸",
			RU: "Минимальный баланс для выхода на линию, ₸",
			EN: "Minimum balance to go online, ₸",
		},
	},
	{
		Key: "driver.require_phone_verification", Kind: KindBool, Default: true,
		Description: Localized{
			KK: "Жүргізуші тіркелгенде телефонды растауды талап ету",
			RU: "Требовать подтверждение телефона при регистрации водителя",
			EN: "Require phone verification for driver registration",
		},
	},
	{
		Key: "driver.online_idle_hours", Kind: KindInt, Min: 1, Max: 72, Default: int64(12),
		Description: Localized{
			KK: "Белсенділік болмаған кезде жүргізушіні желіден шығару уақыты, сағат",
			RU: "Через сколько часов без активности водитель снимается с линии",
			EN: "Hours without activity before a driver is set offline",
		},
		env: func(e Env) (any, bool) {
			if e.DriverOnlineIdleTimeout <= 0 {
				return nil, false
			}
			h := int64((e.DriverOnlineIdleTimeout + time.Hour - 1) / time.Hour) // round up to whole hours
			return max(1, min(h, 72)), true
		},
	},
	{
		Key: "dispatch.radius_base_m", Kind: KindInt, Min: 500, Max: 50_000, Default: int64(5000),
		Description: Localized{
			KK: "Жүргізушілерді A нүктесінің айналасынан іздеудің бастапқы радиусы, м",
			RU: "Начальный радиус поиска водителей вокруг точки A, м",
			EN: "Initial driver search radius around pickup point A, m",
		},
	},
	{
		Key: "dispatch.radius_step_m", Kind: KindInt, Min: 0, Max: 20_000, Default: int64(2500),
		Description: Localized{
			KK: "Жүргізуші табылмаса, іздеу радиусы әр қадам сайын қаншаға ұлғаяды, м",
			RU: "На сколько расширяется радиус поиска за один шаг, если водитель не найден, м",
			EN: "Search radius growth per step while no driver is found, m",
		},
	},
	{
		Key: "dispatch.radius_step_s", Kind: KindInt, Min: 10, Max: 600, Default: int64(60),
		Description: Localized{
			KK: "Іздеу радиусын ұлғайту қадамының ұзақтығы, секунд",
			RU: "Интервал между шагами расширения радиуса поиска, секунд",
			EN: "Seconds between search radius steps",
		},
	},
	{
		Key: "dispatch.radius_max_m", Kind: KindInt, Min: 500, Max: 100_000, Default: int64(10_000),
		Description: Localized{
			KK: "Іздеу радиусының ең үлкен мәні, м (бастапқы радиустан кем болмауы керек)",
			RU: "Максимальный радиус поиска, м (не меньше начального)",
			EN: "Maximum search radius, m (not less than the initial radius)",
		},
	},
	{
		Key: "dispatch.location_max_age_s", Kind: KindInt, Min: 30, Max: 3600, Default: int64(300),
		Description: Localized{
			KK: "Жүргізуші геолокациясы қанша уақыт жарамды саналады, секунд",
			RU: "Сколько секунд геопозиция водителя считается актуальной",
			EN: "How long a driver's location stays fresh, seconds",
		},
	},
	{
		Key: "topup.min_amount", Kind: KindInt, Min: 1, Max: 10_000_000, Default: int64(500),
		Description: Localized{
			KK: "Балансты толтырудың ең аз сомасы, ₸",
			RU: "Минимальная сумма пополнения, ₸",
			EN: "Minimum top-up amount, ₸",
		},
	},
	{
		Key: "topup.max_amount", Kind: KindInt, Min: 1, Max: 10_000_000, Default: int64(100_000),
		Description: Localized{
			KK: "Балансты толтырудың ең көп сомасы, ₸",
			RU: "Максимальная сумма пополнения, ₸",
			EN: "Maximum top-up amount, ₸",
		},
	},
	{
		Key: "topup.expiry_hours", Kind: KindInt, Min: 1, Max: 720, Default: int64(48),
		Description: Localized{
			KK: "Толтыру өтінімінің жарамдылық мерзімі, сағат",
			RU: "Срок действия заявки на пополнение, часов",
			EN: "Top-up request lifetime, hours",
		},
	},
	{
		Key: "topup.presets", Kind: KindIntList, Min: 1, Max: 10_000_000, MaxItems: 8,
		Default: []int64{1000, 2000, 5000, 10000},
		Description: Localized{
			KK: "Толтыруға арналған жылдам сомалар, ₸",
			RU: "Быстрые суммы пополнения, ₸",
			EN: "Top-up quick amounts, ₸",
		},
	},
	{
		Key: "topup.auto_confirm_enabled", Kind: KindBool, Default: true,
		Description: Localized{
			KK: "Kaspi PDF чегі барлық тексеруден өтсе, балансты автоматты түрде толтыру",
			RU: "Автоматически зачислять пополнение, если Kaspi PDF-чек прошёл все проверки",
			EN: "Credit top-ups automatically when a Kaspi PDF receipt passes every check",
		},
	},
	{
		Key: "topup.auto_max_per_day", Kind: KindInt, Min: 0, Max: 100, Default: int64(3),
		Description: Localized{
			KK: "Бір жүргізушіге тәулігіне автоматты толтырудың ең көп саны (0 = өшірулі)",
			RU: "Максимум автоматических зачислений на водителя в сутки (0 = выключено)",
			EN: "Maximum automatic credits per driver per day (0 = off)",
		},
	},
	{
		Key: "topup.auto_max_amount_per_day", Kind: KindInt, Min: 0, Max: 10_000_000, Default: int64(50_000),
		Description: Localized{
			KK: "Бір жүргізушіге тәулігіне автоматты түрде түсетін ең көп сома, ₸",
			RU: "Максимальная сумма автоматических зачислений на водителя в сутки, ₸",
			EN: "Maximum automatically credited amount per driver per day, ₸",
		},
	},
	{
		Key: "topup.receipt_max_age_hours", Kind: KindInt, Min: 1, Max: 720, Default: int64(48),
		Description: Localized{
			KK: "Автоматты толтыру үшін чектің ең үлкен жасы, сағат",
			RU: "Максимальный возраст чека для автоматического зачисления, часов",
			EN: "Maximum receipt age for automatic crediting, hours",
		},
	},
	{
		Key: "delivery.prohibited_items", Kind: KindCodeList, MaxItems: 30,
		Default: []string{"weapons", "explosives", "drugs", "alcohol_tobacco", "money_valuables", "animals", "hazardous", "illegal_goods"},
		Description: Localized{
			KK: "Жеткізуге тыйым салынған заттар (кодтар)",
			RU: "Запрещённые к доставке предметы (коды)",
			EN: "Items prohibited for delivery (codes)",
		},
	},
	{
		Key: "delivery.blocked_keywords", Kind: KindStringList, MinLen: 2, MaxLen: 40, MaxItems: 200,
		Default: []string{"қару", "оружие", "weapon", "патрон", "есірткі", "наркот", "drug", "взрывчат", "жарылғыш"},
		Description: Localized{
			KK: "Жүк сипаттамасында тыйым салынған сөздер",
			RU: "Запрещённые слова в описании груза",
			EN: "Blocked words in cargo descriptions",
		},
	},
	{
		Key: "kaspi.payment_url", Kind: KindURL, Default: "",
		Description: Localized{
			KK: "Kaspi төлем сілтемесі",
			RU: "Ссылка на оплату Kaspi",
			EN: "Kaspi payment link",
		},
		check: httpsURLCheck(false, "kaspi.kz"),
		env: func(e Env) (any, bool) {
			s := strings.TrimSpace(e.KaspiPaymentURL)
			return s, s != ""
		},
	},
	{
		Key: "kaspi.recipient_name", Kind: KindString, MaxLen: 120, Default: "",
		Description: Localized{
			KK: "Төлем алушының аты (Kaspi)",
			RU: "Получатель платежа (Kaspi)",
			EN: "Payment recipient name (Kaspi)",
		},
		env: func(e Env) (any, bool) {
			s := strings.TrimSpace(e.KaspiRecipientName)
			return s, s != ""
		},
	},
	{
		Key: "kaspi.instructions", Kind: KindText, MaxLen: 500,
		Default: Localized{
			KK: "Kaspi.kz қосымшасында көрсетілген соманы аударыңыз және хабарламаға төлем кодын жазыңыз. Төлемді әкімші тексергеннен кейін сома балансыңызға түседі.",
			RU: "Переведите указанную сумму в приложении Kaspi.kz и укажите код платежа в сообщении. Сумма поступит на баланс после проверки платежа администратором.",
			EN: "Transfer the shown amount in the Kaspi.kz app and enter the payment code in the message. The amount is added to your balance after an administrator verifies the payment.",
		},
		Description: Localized{
			KK: "Kaspi арқылы төлеу нұсқаулығы",
			RU: "Инструкция по оплате через Kaspi",
			EN: "Kaspi payment instructions",
		},
		env: func(e Env) (any, bool) { return envInstructions(e.KaspiInstructions) },
	},
	{
		Key: "company.bin", Kind: KindString, Default: "000809500289",
		Description: Localized{
			KK: "Төлем алушының ЖСН/БСН (Kaspi чегі осы нөмірмен тексеріледі)",
			RU: "ИИН/БИН получателя (по нему проверяются чеки Kaspi)",
			EN: "Payee IIN/BIN (Kaspi receipts are checked against it)",
		},
		check: checkIIN,
	},
	{
		Key: "company.iban", Kind: KindString, Default: "KZ05722S000057176693",
		Description: Localized{
			KK: "Төлем алушының IBAN шоты",
			RU: "IBAN счёта получателя",
			EN: "Payee IBAN",
		},
		check: checkKZIBAN,
	},
	{
		Key: "company.bank", Kind: KindString, MaxLen: 120, Default: `АО "Kaspi Bank"`,
		Description: Localized{
			KK: "Төлем алушының банкі",
			RU: "Банк получателя",
			EN: "Payee bank",
		},
	},
	{
		Key: "company.bic", Kind: KindString, Default: "CASPKZKA",
		Description: Localized{
			KK: "Банктің БСК (BIC/SWIFT)",
			RU: "БИК банка (BIC/SWIFT)",
			EN: "Bank BIC (SWIFT)",
		},
		check: checkBIC,
	},
	{
		Key: "company.kbe", Kind: KindString, Default: "19",
		Description: Localized{
			KK: "Бенефициар коды (КБе)",
			RU: "Код бенефициара (КБе)",
			EN: "Beneficiary code (KBe)",
		},
		check: checkKBe,
	},
	{
		Key: "company.address", Kind: KindString, MaxLen: 300, Default: "Жалагашский район, с. Жалагаш, ул. Нығмет Мырзалиев, 94",
		Description: Localized{
			KK: "Төлем алушының заңды мекенжайы",
			RU: "Юридический адрес получателя",
			EN: "Payee legal address",
		},
	},
	{
		Key: "navigation.twogis_base_url", Kind: KindURL, Required: true, Default: "https://2gis.ru",
		Description: Localized{
			KK: "2GIS навигация сілтемелерінің негізгі мекенжайы",
			RU: "Базовый адрес ссылок навигации 2GIS",
			EN: "Base URL for 2GIS navigation links",
		},
		check: httpsURLCheck(true, "2gis.ru", "2gis.kz", "2gis.com"),
		env: func(e Env) (any, bool) {
			s := strings.TrimRight(strings.TrimSpace(e.TwoGISBaseURL), "/")
			return s, s != ""
		},
	},
}

var byKey = func() map[string]Def {
	m := make(map[string]Def, len(registry))
	for _, d := range registry {
		if _, dup := m[d.Key]; dup {
			panic("settings: duplicate key " + d.Key)
		}
		m[d.Key] = d
	}
	return m
}()

// Lookup returns the definition of an admin-editable key.
func Lookup(key string) (Def, bool) {
	d, ok := byKey[key]
	return d, ok
}

// Defs returns all definitions in display order.
func Defs() []Def { return slices.Clone(registry) }

// Normalize validates raw JSON against the definition and returns the canonical
// JSON to store, or a reason code.
func (d Def) Normalize(raw json.RawMessage) (json.RawMessage, string) {
	v, reason := d.normalize(raw)
	if reason != "" {
		return nil, reason
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, ReasonInvalid
	}
	return b, ""
}

func (d Def) normalize(raw json.RawMessage) (any, string) {
	v, ok := decodeStrict(raw)
	if !ok {
		return nil, ReasonInvalidType
	}
	switch d.Kind {
	case KindInt:
		n, ok := asInt(v)
		if !ok {
			return nil, ReasonInvalidType
		}
		if n < d.Min || n > d.Max {
			return nil, ReasonOutOfRange
		}
		return n, ""
	case KindBool:
		b, ok := v.(bool)
		if !ok {
			return nil, ReasonInvalidType
		}
		return b, ""
	case KindString, KindURL:
		s, ok := v.(string)
		if !ok {
			return nil, ReasonInvalidType
		}
		return d.cleanString(s)
	case KindText:
		return d.normalizeText(v)
	case KindIntList:
		return d.normalizeIntList(v)
	case KindCodeList, KindStringList:
		return d.normalizeStringList(v)
	}
	return nil, ReasonInvalid
}

func (d Def) cleanString(s string) (string, string) {
	s = strings.TrimSpace(s)
	if d.check != nil {
		if s == "" {
			if d.Required {
				return "", ReasonRequired
			}
			return "", ""
		}
		out, ok := d.check(s)
		if !ok {
			return "", ReasonInvalid
		}
		return out, ""
	}
	out, err := textutil.CleanText(s, d.MaxLen)
	if errors.Is(err, textutil.ErrTooLong) {
		return "", ReasonTooLong
	}
	if err != nil {
		return "", ReasonInvalid
	}
	if out == "" && d.Required {
		return "", ReasonRequired
	}
	return out, ""
}

func (d Def) normalizeText(v any) (any, string) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, ReasonInvalidType
	}
	var out Localized
	filled := 0
	for k, val := range m {
		s, ok := val.(string)
		if !ok {
			return nil, ReasonInvalidType
		}
		clean, reason := d.cleanString(s)
		if reason != "" {
			return nil, reason
		}
		switch k {
		case "kk":
			out.KK = clean
		case "ru":
			out.RU = clean
		case "en":
			out.EN = clean
		default:
			return nil, ReasonInvalid
		}
		if clean != "" {
			filled++
		}
	}
	// Either every language has text or none does, so no screen mixes languages.
	if filled != 0 && filled != 3 {
		return nil, ReasonIncomplete
	}
	return out, ""
}

func (d Def) normalizeIntList(v any) (any, string) {
	arr, ok := v.([]any)
	if !ok {
		return nil, ReasonInvalidType
	}
	if len(arr) > d.MaxItems {
		return nil, ReasonTooMany
	}
	out := make([]int64, 0, len(arr))
	for _, item := range arr {
		n, ok := asInt(item)
		if !ok {
			return nil, ReasonInvalidType
		}
		if n < d.Min || n > d.Max {
			return nil, ReasonOutOfRange
		}
		if slices.Contains(out, n) {
			return nil, ReasonDuplicate
		}
		out = append(out, n)
	}
	slices.Sort(out)
	return out, ""
}

func (d Def) normalizeStringList(v any) (any, string) {
	arr, ok := v.([]any)
	if !ok {
		return nil, ReasonInvalidType
	}
	if len(arr) > d.MaxItems {
		return nil, ReasonTooMany
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		s, ok := item.(string)
		if !ok {
			return nil, ReasonInvalidType
		}
		s = strings.TrimSpace(s)
		if d.Kind == KindCodeList {
			if !codeRe.MatchString(s) {
				return nil, ReasonInvalid
			}
		} else {
			clean, err := textutil.CleanText(s, d.MaxLen)
			if errors.Is(err, textutil.ErrTooLong) {
				return nil, ReasonTooLong
			}
			if err != nil {
				return nil, ReasonInvalid
			}
			s = strings.ToLower(clean)
			if textutil.Len(s) < d.MinLen {
				return nil, ReasonTooShort
			}
		}
		if slices.Contains(out, s) {
			return nil, ReasonDuplicate
		}
		out = append(out, s)
	}
	return out, ""
}

// decodeStrict parses exactly one JSON value, keeping numbers exact.
func decodeStrict(raw json.RawMessage) (any, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil || dec.More() {
		return nil, false
	}
	return v, v != nil
}

func asInt(v any) (int64, bool) {
	num, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	n, err := num.Int64()
	return n, err == nil
}

// checkCommissionMode accepts "fixed" or "percent".
func checkCommissionMode(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	return s, s == CommissionFixed || s == CommissionPercent
}

func checkTelegramUsername(s string) (string, bool) {
	s = strings.TrimPrefix(s, "https://t.me/")
	s = strings.TrimPrefix(s, "@")
	return s, tgUserRe.MatchString(s)
}

// checkSupportPhone accepts Kazakhstan numbers (mobile or landline) written as
// +7…, 8… or 7… and stores them as E.164.
func checkSupportPhone(s string) (string, bool) {
	r := strings.NewReplacer(" ", "", "-", "", "(", "", ")", "", " ", "")
	d := strings.TrimPrefix(r.Replace(s), "+")
	if !digitsOnly.MatchString(d) {
		return "", false
	}
	switch {
	case len(d) == 11 && d[0] == '8':
		d = "7" + d[1:]
	case len(d) == 10 && d[0] == '7':
		d = "7" + d
	}
	if len(d) != 11 || !strings.HasPrefix(d, "77") {
		return "", false
	}
	return "+" + d, true
}

// httpsURLCheck validates an https URL on one of the allowed registrable
// domains (subdomains included). baseOnly additionally forbids paths, queries
// and fragments and strips a trailing slash.
func httpsURLCheck(baseOnly bool, domains ...string) func(string) (string, bool) {
	return func(s string) (string, bool) {
		if len(s) > 500 {
			return "", false
		}
		u, err := url.Parse(s)
		if err != nil || u.Scheme != "https" || u.User != nil || u.Host == "" || u.Port() != "" {
			return "", false
		}
		host := strings.ToLower(u.Hostname())
		allowed := false
		for _, d := range domains {
			if host == d || strings.HasSuffix(host, "."+d) {
				allowed = true
				break
			}
		}
		if !allowed {
			return "", false
		}
		if baseOnly {
			if strings.Trim(u.Path, "/") != "" || u.RawQuery != "" || u.Fragment != "" {
				return "", false
			}
			return "https://" + host, true
		}
		return u.String(), true
	}
}

// checkIIN accepts a 12-digit Kazakhstan IIN/BIN with a valid check digit.
func checkIIN(s string) (string, bool) {
	s = strings.ReplaceAll(s, " ", "")
	if len(s) != 12 || !digitsOnly.MatchString(s) {
		return "", false
	}
	d := make([]int, 12)
	for i := range d {
		d[i] = int(s[i] - '0')
	}
	sum := 0
	for i := 0; i < 11; i++ {
		sum += d[i] * (i + 1)
	}
	check := sum % 11
	if check == 10 {
		sum = 0
		for i := 0; i < 11; i++ {
			sum += d[i] * ((i+2)%11 + 1)
		}
		check = sum % 11
	}
	return s, check != 10 && check == d[11]
}

// checkKZIBAN accepts a Kazakhstan IBAN (KZ, 20 characters) with valid
// ISO 13616 check digits; spaces are removed and letters upper-cased.
func checkKZIBAN(s string) (string, bool) {
	s = strings.ToUpper(strings.ReplaceAll(s, " ", ""))
	if !kzIBANRe.MatchString(s) {
		return "", false
	}
	rearranged := s[4:] + s[:4]
	rem := 0
	for _, c := range rearranged {
		v := int(c - '0')
		if c >= 'A' && c <= 'Z' {
			v = int(c-'A') + 10
		}
		if v >= 10 {
			rem = (rem*100 + v) % 97
		} else {
			rem = (rem*10 + v) % 97
		}
	}
	return s, rem == 1
}

func checkBIC(s string) (string, bool) {
	s = strings.ToUpper(strings.TrimSpace(s))
	return s, bicRe.MatchString(s)
}

func checkKBe(s string) (string, bool) {
	s = strings.TrimSpace(s)
	return s, kbeRe.MatchString(s)
}
