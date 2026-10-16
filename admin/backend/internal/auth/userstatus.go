package auth

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

// userStatusTTL bounds how long a block can take to reach requests that carry a
// still-valid app token.
const userStatusTTL = 15 * time.Second

// ErrUserBlocked is returned when an administrator has blocked the user.
var ErrUserBlocked = httpx.NewError(http.StatusForbidden, "user_blocked", "user is blocked")

type userStatus struct {
	telegramID int64
	blocked    bool
	at         time.Time
}

// statusCache memoises users.is_blocked per user for a short TTL.
type statusCache struct {
	mu  sync.Mutex
	ttl time.Duration
	m   map[uuid.UUID]userStatus
}

func newStatusCache(ttl time.Duration) *statusCache {
	return &statusCache{ttl: ttl, m: make(map[uuid.UUID]userStatus)}
}

func (c *statusCache) get(id uuid.UUID, now time.Time) (userStatus, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	st, ok := c.m[id]
	if !ok || now.Sub(st.at) >= c.ttl || now.Before(st.at) {
		return userStatus{}, false
	}
	return st, true
}

func (c *statusCache) put(id uuid.UUID, st userStatus) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.m) >= 100_000 {
		for k, v := range c.m {
			if st.at.Sub(v.at) >= c.ttl {
				delete(c.m, k)
			}
		}
	}
	c.m[id] = st
}

func (c *statusCache) invalidate(id uuid.UUID) {
	c.mu.Lock()
	delete(c.m, id)
	c.mu.Unlock()
}

// InvalidateUser drops the cached blocked flag so the next request re-reads it.
// Call it after blocking or unblocking a user.
func (s *Service) InvalidateUser(userID uuid.UUID) { s.users.invalidate(userID) }

// userStatus returns the user's Telegram id and blocked flag (cached).
func (s *Service) userStatus(ctx context.Context, userID uuid.UUID) (userStatus, error) {
	now := s.now()
	if st, ok := s.users.get(userID, now); ok {
		return st, nil
	}
	var st userStatus
	err := s.pool.QueryRow(ctx, `SELECT telegram_user_id, is_blocked FROM users WHERE id = $1`, userID).
		Scan(&st.telegramID, &st.blocked)
	if db.IsNoRows(err) {
		return userStatus{}, httpx.Unauthorized()
	}
	if err != nil {
		return userStatus{}, httpx.Internal(err)
	}
	st.at = now
	s.users.put(userID, st)
	return st, nil
}

// AuthenticateAppToken verifies an app token and the user's current status. It
// returns the full principal (with TelegramID) and the token's expiry.
func (s *Service) AuthenticateAppToken(ctx context.Context, token string) (Principal, time.Time, error) {
	p, exp, err := s.verifyAppToken(token, s.now())
	if err != nil {
		return Principal{}, time.Time{}, err
	}
	st, err := s.userStatus(ctx, p.UserID)
	if err != nil {
		return Principal{}, time.Time{}, err
	}
	if st.blocked {
		return Principal{}, time.Time{}, ErrUserBlocked
	}
	p.TelegramID = st.telegramID
	return p, exp, nil
}

// CheckUserActive re-checks that the user still exists and is not blocked.
func (s *Service) CheckUserActive(ctx context.Context, userID uuid.UUID) error {
	st, err := s.userStatus(ctx, userID)
	if err != nil {
		return err
	}
	if st.blocked {
		return ErrUserBlocked
	}
	return nil
}
