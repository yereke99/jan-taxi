package passengerbot

import "github.com/yereke99/jantaxi/admin/backend/pkg/botapi/runtime"

// texts are all passenger bot messages (Telegram HTML). Arguments are
// escaped by the caller.
var texts = runtime.Catalog{
	"kk": {
		"kb_taxi":        "🚕 Такси шақыру",
		"kb_parcel":      "📦 Сәлемдеме жіберу",
		"kb_orders":      "📋 Менің тапсырыстарым",
		"kb_driver":      "🚗 Жүргізуші болу",
		"kb_support":     "💬 Қолдау",
		"kb_language":    "🌐 Тіл",
		"kb_placeholder": "Әрекетті таңдаңыз",

		"cmd_start":    "Бастау",
		"cmd_taxi":     "Такси шақыру",
		"cmd_parcel":   "Сәлемдеме жіберу",
		"cmd_orders":   "Менің тапсырыстарым",
		"cmd_driver":   "Жүргізуші болу",
		"cmd_support":  "Қолдау қызметі",
		"cmd_language": "Тілді өзгерту",

		"description":       "Jan Taxi — Қазақстандағы такси және сәлемдеме жеткізу қызметі. Бару мекенжайын көрсетіп, бағаны таңдаңыз — жақын жердегі жүргізуші тапсырысты қабылдайды.",
		"short_description": "Такси шақыру және сәлемдеме жеткізу",
		"map_point":         "Картадағы нүкте",

		"welcome":       "<b>Jan Taxi-ге қош келдіңіз!</b>\nТакси шақыру мен сәлемдеме жіберу — бір қосымшада.\nТөмендегі мәзірден әрекетті таңдаңыз.",
		"welcome_open":  "Қосымшаны ашу үшін батырманы басыңыз:",
		"btn_open_app":  "Jan Taxi ашу",
		"pick_language": "Тілді таңдаңыз:",

		"taxi":       "<b>Такси шақыру</b>\nҚосымшада қайда баратыныңызды көрсетіп, бағаны таңдаңыз. Жүргізуші тапсырысты қабылдағанда осы чатқа хабарлаймыз.",
		"btn_taxi":   "Такси шақыру",
		"parcel":     "<b>Сәлемдеме жіберу</b>\nҚосымшада қайдан және қайда жеткізу керегін, алушының телефон нөмірін және не жіберетініңізді көрсетіңіз. Тыйым салынған заттар қабылданбайды.",
		"btn_parcel": "Сәлемдеме жіберу",

		"orders_title":   "<b>Соңғы тапсырыстарыңыз</b>",
		"orders_empty":   "Сізде әзірге тапсырыс жоқ.",
		"order_line":     "<b>№%d</b> · %s · %s\n%s → %s",
		"btn_order":      "№%d",
		"btn_all_orders": "Барлық тапсырыстар",

		"status_draft":           "Жоба",
		"status_searching":       "Жүргізуші ізделуде",
		"status_accepted":        "Жүргізуші табылды",
		"status_driver_arriving": "Жүргізуші келді",
		"status_in_progress":     "Жолда",
		"status_completed":       "Аяқталды",
		"status_cancelled":       "Жойылды",
		"status_disputed":        "Шағым қаралуда",

		"category_taxi":   "Такси",
		"category_parcel": "Сәлемдеме",
		"category_other":  "Басқа жеткізу",

		"driver":             "<b>Jan Taxi-де жүргізуші болыңыз</b>\nТіркелу жүргізушілерге арналған бөлек ботта өтеді: өз деректеріңізді, куәлік пен көлік суреттерін жібересіз, модератор тексергеннен кейін тапсырыс ала бастайсыз.\n\nТөмендегі батырма арқылы ботты ашып, <b>Start</b> («Бастау») батырмасын басыңыз — Telegram ботпен сөйлесуді осылай бастауды талап етеді.",
		"btn_driver_bot":     "Жүргізуші ботын ашу",
		"driver_unavailable": "Жүргізушілерге арналған бот әзірге қолжетімсіз. Қолдау қызметіне жазыңыз.",

		"support_title": "<b>Қолдау қызметі</b>",
		"support_tg":    "Telegram: @%s",
		"support_phone": "Телефон: %s",
		"support_hours": "Жұмыс уақыты: %s",
		"support_none":  "Қолдау қызметінің байланыс деректері әлі жарияланбаған.",
		"btn_support":   "Қолдауға жазу",

		"language_set": "Тіл өзгертілді: қазақша.",
		"help":         "Төмендегі мәзірден әрекетті таңдаңыз немесе қосымшаны ашыңыз.",
		"blocked":      "Аккаунтыңыз шектелген. Толығырақ ақпаратты қолдау қызметінен біліңіз.",
		"error":        "Қызмет уақытша қолжетімсіз. Біраздан соң қайталап көріңіз.",
	},
	"ru": {
		"kb_taxi":        "🚕 Вызвать такси",
		"kb_parcel":      "📦 Отправить посылку",
		"kb_orders":      "📋 Мои заказы",
		"kb_driver":      "🚗 Стать водителем",
		"kb_support":     "💬 Поддержка",
		"kb_language":    "🌐 Язык",
		"kb_placeholder": "Выберите действие",

		"cmd_start":    "Начать",
		"cmd_taxi":     "Вызвать такси",
		"cmd_parcel":   "Отправить посылку",
		"cmd_orders":   "Мои заказы",
		"cmd_driver":   "Стать водителем",
		"cmd_support":  "Поддержка",
		"cmd_language": "Сменить язык",

		"description":       "Jan Taxi — такси и доставка посылок в Казахстане. Укажите, куда ехать, выберите цену — ближайший водитель примет заказ.",
		"short_description": "Такси и доставка посылок",
		"map_point":         "Точка на карте",

		"welcome":       "<b>Добро пожаловать в Jan Taxi!</b>\nТакси и отправка посылок — в одном приложении.\nВыберите действие в меню ниже.",
		"welcome_open":  "Нажмите кнопку, чтобы открыть приложение:",
		"btn_open_app":  "Открыть Jan Taxi",
		"pick_language": "Выберите язык:",

		"taxi":       "<b>Вызов такси</b>\nУкажите в приложении, куда ехать, и выберите цену. Когда водитель примет заказ, мы напишем в этот чат.",
		"btn_taxi":   "Вызвать такси",
		"parcel":     "<b>Отправка посылки</b>\nУкажите в приложении, откуда и куда доставить, телефон получателя и что вы отправляете. Запрещённые предметы не принимаются.",
		"btn_parcel": "Отправить посылку",

		"orders_title":   "<b>Ваши последние заказы</b>",
		"orders_empty":   "У вас пока нет заказов.",
		"order_line":     "<b>№%d</b> · %s · %s\n%s → %s",
		"btn_order":      "№%d",
		"btn_all_orders": "Все заказы",

		"status_draft":           "Черновик",
		"status_searching":       "Поиск водителя",
		"status_accepted":        "Водитель найден",
		"status_driver_arriving": "Водитель на месте",
		"status_in_progress":     "В пути",
		"status_completed":       "Завершён",
		"status_cancelled":       "Отменён",
		"status_disputed":        "Жалоба на рассмотрении",

		"category_taxi":   "Такси",
		"category_parcel": "Посылка",
		"category_other":  "Другая доставка",

		"driver":             "<b>Станьте водителем Jan Taxi</b>\nРегистрация проходит в отдельном боте для водителей: вы отправляете свои данные, фото удостоверения и автомобиля, а после проверки модератором начинаете получать заказы.\n\nОткройте бота кнопкой ниже и нажмите <b>Запустить</b> (Start) — так Telegram требует начинать диалог с ботом.",
		"btn_driver_bot":     "Открыть бот для водителей",
		"driver_unavailable": "Бот для водителей пока недоступен. Напишите в поддержку.",

		"support_title": "<b>Поддержка</b>",
		"support_tg":    "Telegram: @%s",
		"support_phone": "Телефон: %s",
		"support_hours": "Часы работы: %s",
		"support_none":  "Контакты поддержки ещё не опубликованы.",
		"btn_support":   "Написать в поддержку",

		"language_set": "Язык изменён: русский.",
		"help":         "Выберите действие в меню ниже или откройте приложение.",
		"blocked":      "Ваш аккаунт ограничен. Подробности можно узнать в поддержке.",
		"error":        "Сервис временно недоступен. Попробуйте чуть позже.",
	},
	"en": {
		"kb_taxi":        "🚕 Order a taxi",
		"kb_parcel":      "📦 Send a parcel",
		"kb_orders":      "📋 My orders",
		"kb_driver":      "🚗 Become a driver",
		"kb_support":     "💬 Support",
		"kb_language":    "🌐 Language",
		"kb_placeholder": "Choose an action",

		"cmd_start":    "Start",
		"cmd_taxi":     "Order a taxi",
		"cmd_parcel":   "Send a parcel",
		"cmd_orders":   "My orders",
		"cmd_driver":   "Become a driver",
		"cmd_support":  "Support",
		"cmd_language": "Change language",

		"description":       "Jan Taxi — rides and parcel delivery in Kazakhstan. Set where you're going, choose a fare, and a nearby driver accepts your order.",
		"short_description": "Rides and parcel delivery",
		"map_point":         "Point on the map",

		"welcome":       "<b>Welcome to Jan Taxi!</b>\nRides and parcel delivery in one app.\nChoose an action from the menu below.",
		"welcome_open":  "Tap the button to open the app:",
		"btn_open_app":  "Open Jan Taxi",
		"pick_language": "Choose your language:",

		"taxi":       "<b>Order a taxi</b>\nSet your destination in the app and choose a fare. We'll message you here when a driver accepts.",
		"btn_taxi":   "Order a taxi",
		"parcel":     "<b>Send a parcel</b>\nIn the app, set where to pick up and deliver, the recipient's phone number and what you're sending. Prohibited items are not accepted.",
		"btn_parcel": "Send a parcel",

		"orders_title":   "<b>Your recent orders</b>",
		"orders_empty":   "You have no orders yet.",
		"order_line":     "<b>#%d</b> · %s · %s\n%s → %s",
		"btn_order":      "#%d",
		"btn_all_orders": "All orders",

		"status_draft":           "Draft",
		"status_searching":       "Finding a driver",
		"status_accepted":        "Driver assigned",
		"status_driver_arriving": "Driver has arrived",
		"status_in_progress":     "On the way",
		"status_completed":       "Completed",
		"status_cancelled":       "Cancelled",
		"status_disputed":        "Complaint under review",

		"category_taxi":   "Taxi",
		"category_parcel": "Parcel",
		"category_other":  "Other delivery",

		"driver":             "<b>Drive with Jan Taxi</b>\nRegistration happens in a separate bot for drivers: you send your details and photos of your ID and car, and once a moderator approves you, you start receiving orders.\n\nOpen the bot with the button below and tap <b>Start</b> — Telegram requires this to begin a chat with a bot.",
		"btn_driver_bot":     "Open the driver bot",
		"driver_unavailable": "The driver bot is not available yet. Please contact support.",

		"support_title": "<b>Support</b>",
		"support_tg":    "Telegram: @%s",
		"support_phone": "Phone: %s",
		"support_hours": "Hours: %s",
		"support_none":  "Support contacts have not been published yet.",
		"btn_support":   "Message support",

		"language_set": "Language changed: English.",
		"help":         "Choose an action from the menu below or open the app.",
		"blocked":      "Your account is restricted. Please contact support for details.",
		"error":        "The service is temporarily unavailable. Please try again shortly.",
	},
}
