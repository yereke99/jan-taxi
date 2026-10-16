package auth

import (
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/password"
)

// Config holds the secrets and lifetimes the auth service needs.
type Config struct {
	PassengerBotToken string
	DriverBotToken    string
	SessionSecret     string // signs app tokens and CSRF tokens; required, 32+ bytes recommended

	InitDataMaxAge   time.Duration // default 24h
	AppTokenTTL      time.Duration // default 12h
	AdminIdleTTL     time.Duration // default 12h
	AdminAbsoluteTTL time.Duration // default 168h
	CookieSecure     bool

	// Audit writes admin audit rows (wired to audit.Log); required.
	Audit AuditFunc

	Log *slog.Logger
}

// MinSessionSecretLen is the minimum accepted SessionSecret length in bytes.
const MinSessionSecretLen = 32

// Service authenticates Mini App users, administrators and bots.
type Service struct {
	pool   *pgxpool.Pool
	cfg    Config
	log    *slog.Logger
	secret []byte
	now    func() time.Time
	audit  AuditFunc

	users *statusCache

	dummyOnce sync.Once
	dummyHash string

	tgLimiters    map[App]*httpx.RateLimiter
	loginIPLimit  *httpx.RateLimiter
	loginMailLim  *httpx.RateLimiter
	passwordLimit *httpx.RateLimiter
}

// NewService validates cfg and builds the service.
func NewService(pool *pgxpool.Pool, cfg Config) (*Service, error) {
	if pool == nil {
		return nil, errors.New("auth: nil pool")
	}
	if cfg.SessionSecret == "" {
		return nil, errors.New("auth: session secret is required")
	}
	if cfg.Audit == nil {
		return nil, errors.New("auth: audit writer is required")
	}
	if cfg.InitDataMaxAge <= 0 {
		cfg.InitDataMaxAge = 24 * time.Hour
	}
	if cfg.AppTokenTTL <= 0 {
		cfg.AppTokenTTL = 12 * time.Hour
	}
	if cfg.AdminIdleTTL <= 0 {
		cfg.AdminIdleTTL = 12 * time.Hour
	}
	if cfg.AdminAbsoluteTTL <= 0 {
		cfg.AdminAbsoluteTTL = 168 * time.Hour
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	s := &Service{
		pool:   pool,
		cfg:    cfg,
		log:    cfg.Log.With(slog.String("component", "auth")),
		secret: []byte(cfg.SessionSecret),
		now:    time.Now,
		audit:  cfg.Audit,
		users:  newStatusCache(userStatusTTL),
		tgLimiters: map[App]*httpx.RateLimiter{
			AppPassenger: httpx.NewRateLimiter(60, 20),
			AppDriver:    httpx.NewRateLimiter(60, 20),
		},
		loginIPLimit:  httpx.NewRateLimiter(20, 10),
		loginMailLim:  httpx.NewRateLimiter(10, 10),
		passwordLimit: httpx.NewRateLimiter(10, 5),
	}
	if len(cfg.SessionSecret) < MinSessionSecretLen {
		// Production config already rejects this; development only warns.
		s.log.Warn("session secret is shorter than 32 bytes; app tokens and CSRF tokens are weakly protected")
	}
	if cfg.PassengerBotToken == "" {
		s.log.Warn("passenger bot token is not configured; passenger Mini App sign-in is disabled")
	}
	if cfg.DriverBotToken == "" {
		s.log.Warn("driver bot token is not configured; driver Mini App sign-in is disabled")
	}
	return s, nil
}

func (s *Service) botToken(app App) string {
	switch app {
	case AppPassenger:
		return s.cfg.PassengerBotToken
	case AppDriver:
		return s.cfg.DriverBotToken
	}
	return ""
}

// dummyPasswordHash is verified against for unknown e-mails so that a login for a
// non-existent account costs as much as one for a real account.
func (s *Service) dummyPasswordHash() string {
	s.dummyOnce.Do(func() {
		h, err := password.Hash("jantaxi-timing-equaliser-not-a-password")
		if err != nil {
			s.log.Error("dummy password hash", slog.Any("err", err))
			return
		}
		s.dummyHash = h
	})
	return s.dummyHash
}
