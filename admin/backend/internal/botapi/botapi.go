// Package botapi serves the internal HTTP API used by the two Telegram bots
// (internal listener, /internal/v1, service-secret authentication). The bots
// never touch PostgreSQL: polling leases, update offsets and claims, user
// synchronisation and every driver action go through these endpoints.
package botapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/dispatch"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

// Bot names as they appear in X-Jantaxi-Bot and in the {bot} path segment.
const (
	BotPassenger = "passenger"
	BotDriver    = "driver"
)

// ProcessedUpdateRetention is how long update claims are kept (§16).
const ProcessedUpdateRetention = 7 * 24 * time.Hour

// CleanupInterval is the period of the bot_update_cleanup job (§16).
const CleanupInterval = time.Hour

// Location reports from one driver are limited like POST /driver/location.
const (
	locationPerMinute = 30
	locationBurst     = 3
)

// Options configure the Handler.
type Options struct {
	Pool *pgxpool.Pool
	Log  *slog.Logger

	PassengerMiniAppURL, DriverMiniAppURL   string
	PassengerBotUsername, DriverBotUsername string
	DefaultLanguage                         string

	// Receipts attaches Kaspi receipts sent to the driver bot; without it
	// POST /driver/{tg_id}/topup-receipt answers 503.
	Receipts ReceiptIntake
	// MaxReceiptBytes bounds one receipt (default DefaultMaxReceiptBytes).
	MaxReceiptBytes int64
	// ArrivalETA refines the arrival estimate after «Тапсырысты алу» (§22.6); nil
	// keeps the straight-line estimate.
	ArrivalETA *dispatch.ArrivalETA
}

// Handler serves the internal bot endpoints.
type Handler struct {
	pool            *pgxpool.Pool
	log             *slog.Logger
	opts            Options
	locationLimiter *httpx.RateLimiter
	receiptLimiter  *httpx.RateLimiter
}

// New validates opts and builds the Handler.
func New(opts Options) (*Handler, error) {
	if opts.Pool == nil {
		return nil, errors.New("botapi: pool is required")
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	if opts.DefaultLanguage == "" {
		opts.DefaultLanguage = "kk"
	}
	if opts.MaxReceiptBytes <= 0 {
		opts.MaxReceiptBytes = DefaultMaxReceiptBytes
	}
	return &Handler{
		pool: opts.Pool, log: opts.Log, opts: opts,
		locationLimiter: httpx.NewRateLimiter(locationPerMinute, locationBurst),
		receiptLimiter:  httpx.NewRateLimiter(receiptsPerMinute, receiptBurst),
	}, nil
}

// Mount registers every internal route on r, which is mounted at
// /internal/v1 behind auth.RequireService.
func (h *Handler) Mount(r chi.Router) {
	bots := r.With(requirePathBot)
	bots.Post("/bots/{bot}/lease", httpx.H(h.acquireLease))
	bots.Post("/bots/{bot}/offset", httpx.H(h.saveOffset))
	bots.Post("/bots/{bot}/updates/{update_id}/claim", httpx.H(h.claimUpdate))
	bots.Post("/bots/{bot}/users/sync", httpx.H(h.syncUser))
	bots.Put("/bots/{bot}/users/{tg_id}/language", httpx.H(h.setLanguage))

	pas := r.With(requireBot(BotPassenger))
	pas.Get("/passenger/{tg_id}/orders", httpx.H(h.passengerOrders))
	pas.Post("/passenger/{tg_id}/contact", httpx.H(h.passengerContact))

	drv := r.With(requireBot(BotDriver))
	drv.Get("/driver/{tg_id}/summary", httpx.H(h.driverSummary))
	drv.Post("/driver/{tg_id}/availability", httpx.H(h.driverAvailability))
	drv.Get("/driver/{tg_id}/orders", httpx.H(h.driverOrders))
	drv.Post("/driver/{tg_id}/orders/{id}/accept", httpx.H(h.driverAccept))
	drv.Post("/driver/{tg_id}/contact", httpx.H(h.driverContact))
	drv.Post("/driver/{tg_id}/location", httpx.H(h.driverLocation))
	drv.Post("/driver/{tg_id}/topup-receipt", httpx.H(h.driverTopUpReceipt))

	r.Get("/config", httpx.H(h.config))
}

// requirePathBot rejects /bots/{bot}/... calls whose path segment differs
// from the authenticated bot (X-Jantaxi-Bot).
func requirePathBot(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if bot := chi.URLParam(r, "bot"); bot == "" || bot != auth.BotFrom(r.Context()) {
			httpx.WriteError(w, r, wrongBot())
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireBot restricts a route to one bot.
func requireBot(bot string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if auth.BotFrom(r.Context()) != bot {
				httpx.WriteError(w, r, wrongBot())
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func wrongBot() *httpx.Error {
	return httpx.Forbidden().WithDetails(map[string]any{"reason": "wrong_bot"})
}

// int64Param parses a non-negative integer path parameter; anything else is
// 404 so malformed ids look like unknown ones.
func int64Param(r *http.Request, name string, min int64) (int64, error) {
	n, err := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	if err != nil || n < min {
		return 0, httpx.NotFound()
	}
	return n, nil
}

// tgIDParam parses {tg_id} (a positive Telegram user id).
func tgIDParam(r *http.Request) (int64, error) { return int64Param(r, "tg_id", 1) }
