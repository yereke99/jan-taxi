package botapi

import "time"

// Lease is the polling lease state (POST /bots/{bot}/lease).
type Lease struct {
	Acquired       bool       `json:"acquired"`
	LastUpdateID   int64      `json:"last_update_id"`
	LeaseExpiresAt *time.Time `json:"lease_expires_at"`
}

// SyncUserRequest describes the Telegram user seen in an update.
type SyncUserRequest struct {
	TelegramUserID int64  `json:"telegram_user_id"`
	FirstName      string `json:"first_name"`
	LastName       string `json:"last_name,omitempty"`
	Username       string `json:"username,omitempty"`
	LanguageCode   string `json:"language_code,omitempty"`
	Started        bool   `json:"started,omitempty"`
	// Blocked is set from my_chat_member updates: true when the user blocked
	// the bot, false when they unblocked it; nil leaves the state unchanged.
	Blocked *bool `json:"blocked,omitempty"`
}

// DriverState is the driver part of a driver-bot user sync.
type DriverState struct {
	Registered    bool    `json:"registered"`
	DriverID      *string `json:"driver_id"`
	Code          *string `json:"code"`
	Status        *string `json:"status"`
	IsOnline      bool    `json:"is_online"`
	PhoneVerified bool    `json:"phone_verified"`
}

// SyncUserResponse is the stored user (POST /bots/{bot}/users/sync).
type SyncUserResponse struct {
	UserID         string       `json:"user_id"`
	Language       string       `json:"language"`
	LanguageChosen bool         `json:"language_chosen"`
	IsNew          bool         `json:"is_new"`
	IsBlocked      bool         `json:"is_blocked"`
	Driver         *DriverState `json:"driver,omitempty"`
}

// OrderSummary is a short order description for bot replies.
type OrderSummary struct {
	ID                 string    `json:"id"`
	Number             int64     `json:"number"`
	Status             string    `json:"status"`
	Category           string    `json:"category"`
	Fare               int64     `json:"fare"`
	Currency           string    `json:"currency"`
	PickupAddress      string    `json:"pickup_address"`
	DestinationAddress string    `json:"destination_address"`
	CreatedAt          time.Time `json:"created_at"`
}

// Wallet is a driver wallet.
type Wallet struct {
	Balance          int64     `json:"balance"`
	Reserved         int64     `json:"reserved"`
	Available        int64     `json:"available"`
	Currency         string    `json:"currency"`
	MinOnlineBalance int64     `json:"min_online_balance"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// CorrectionItem is one moderator remark on a registration field.
type CorrectionItem struct {
	Field   string  `json:"field"`
	Reason  string  `json:"reason"`
	Comment *string `json:"comment"`
}

// Vehicle is the driver's current vehicle.
type Vehicle struct {
	Make  string `json:"make"`
	Model string `json:"model"`
	Color string `json:"color"`
	Plate string `json:"plate"`
}

// DriverSummary is the driver state for the bot menu.
type DriverSummary struct {
	Registered      bool             `json:"registered"`
	DriverID        *string          `json:"driver_id"`
	Code            *string          `json:"code"`
	Status          *string          `json:"status"`
	IsOnline        bool             `json:"is_online"`
	PhoneVerified   bool             `json:"phone_verified"`
	Language        string           `json:"language"`
	Wallet          *Wallet          `json:"wallet"`
	ActiveOrder     *OrderSummary    `json:"active_order"`
	Corrections     []CorrectionItem `json:"corrections"`
	AvailableOrders int              `json:"available_orders"`
	FirstName       string           `json:"first_name"`
	LastName        string           `json:"last_name"`
	Vehicle         *Vehicle         `json:"vehicle"`
	Rating          *float64         `json:"rating"`
	CompletedTrips  int              `json:"completed_trips"`
}

// StatusValue returns the driver status or "" when not registered.
func (s DriverSummary) StatusValue() string {
	if s.Status == nil {
		return ""
	}
	return *s.Status
}

// Availability is the driver's online state after a change.
type Availability struct {
	IsOnline         bool       `json:"is_online"`
	OnlineSince      *time.Time `json:"online_since"`
	CanGoOnline      bool       `json:"can_go_online"`
	BlockedReason    *string    `json:"blocked_reason"`
	Available        int64      `json:"available"`
	MinOnlineBalance int64      `json:"min_online_balance"`
	Currency         string     `json:"currency"`
}

// Eligibility tells whether the driver can take orders now.
type Eligibility struct {
	Eligible bool    `json:"eligible"`
	Reason   *string `json:"reason"`
}

// ReasonValue returns the blocking reason or "".
func (e Eligibility) ReasonValue() string {
	if e.Reason == nil {
		return ""
	}
	return *e.Reason
}

// StopSummary is one stop of an order card.
type StopSummary struct {
	Seq     int     `json:"seq"`
	Kind    string  `json:"kind"`
	Lat     float64 `json:"lat"`
	Lon     float64 `json:"lon"`
	Address string  `json:"address"`
}

// OrderCard is an available order as listed to an eligible driver.
type OrderCard struct {
	ID                string        `json:"id"`
	Number            int64         `json:"number"`
	Category          string        `json:"category"`
	ServiceType       string        `json:"service_type"`
	CargoType         string        `json:"cargo_type"`
	Stops             []StopSummary `json:"stops"`
	StopCount         int           `json:"stop_count"`
	Comment           *string       `json:"comment"`
	CargoDescription  *string       `json:"cargo_description"`
	Fare              int64         `json:"fare"`
	Currency          string        `json:"currency"`
	CommissionAmount  int64         `json:"commission_amount"`
	PaymentMethod     string        `json:"payment_method"`
	DistanceM         int           `json:"distance_m"`
	DurationS         int           `json:"duration_s"`
	DistanceToPickupM *int          `json:"distance_to_pickup_m"`
	CreatedAt         time.Time     `json:"created_at"`
	SearchExpiresAt   *time.Time    `json:"search_expires_at"`
}

// AvailableOrders is the driver's order list plus eligibility.
type AvailableOrders struct {
	Items       []OrderCard `json:"items"`
	Eligibility Eligibility `json:"eligibility"`
}

// ContactResult is the outcome of a phone verification.
type ContactResult struct {
	Phone         string  `json:"phone"`
	PhoneVerified bool    `json:"phone_verified"`
	DriverID      *string `json:"driver_id"`
}

// LocationReport is a location shared in the driver bot.
type LocationReport struct {
	Lat       float64  `json:"lat"`
	Lon       float64  `json:"lon"`
	AccuracyM *float64 `json:"accuracy_m,omitempty"`
	Heading   *float64 `json:"heading,omitempty"`
	Live      bool     `json:"live"`
}

// Localized is a text in the three UI languages.
type Localized struct {
	KK string `json:"kk"`
	RU string `json:"ru"`
	EN string `json:"en"`
}

// Get returns the text in lang, falling back to Kazakh.
func (l Localized) Get(lang string) string {
	switch {
	case lang == "ru" && l.RU != "":
		return l.RU
	case lang == "en" && l.EN != "":
		return l.EN
	}
	return l.KK
}

// Support are the operator's support contacts.
type Support struct {
	TelegramUsername *string   `json:"telegram_username"`
	Phone            *string   `json:"phone"`
	Hours            Localized `json:"hours"`
}

// Config is the bots' runtime configuration.
type Config struct {
	PassengerMiniAppURL  string  `json:"passenger_miniapp_url"`
	DriverMiniAppURL     string  `json:"driver_miniapp_url"`
	PassengerBotUsername string  `json:"passenger_bot_username"`
	DriverBotUsername    string  `json:"driver_bot_username"`
	Support              Support `json:"support"`
	DefaultLanguage      string  `json:"default_language"`
}

// Receipt outcomes (TopUpReceiptResult.Outcome).
const (
	ReceiptCredited  = "credited"
	ReceiptPending   = "pending_verification"
	ReceiptDuplicate = "duplicate_rejected"
)

// ReceiptTopUp is the top-up a receipt was attached to.
type ReceiptTopUp struct {
	ID             string `json:"id"`
	Reference      string `json:"reference"`
	Amount         int64  `json:"amount"`
	VerifiedAmount *int64 `json:"verified_amount"`
	Status         string `json:"status"`
}

// TopUpReceiptResult is the outcome of a receipt sent to the driver bot.
// Reasons explain a manual review (e.g. "amount_mismatch").
type TopUpReceiptResult struct {
	Outcome string       `json:"outcome"`
	Reasons []string     `json:"reasons"`
	TopUp   ReceiptTopUp `json:"topup"`
	Balance *int64       `json:"balance"`
}
