package runtime

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/yereke99/jantaxi/admin/backend/pkg/telegram"
)

// Menu buttons. Every chat uses the bot's default menu button, which each
// bot publishes with the current Mini App URL whenever it starts polling
// (Profile.MenuButton, retried until Telegram accepts it). Per-chat buttons
// are not used: Telegram keeps a per-chat button, URL included, until it is
// replaced, so it goes stale when the Mini App moves, and there is no list of
// chats to refresh on start. Chats that still carry a per-chat button from
// earlier versions (or from manual setChatMenuButton calls) are switched back
// to the default once per process, on their first update.

// ChatMenuAPI clears a chat's own menu button (*telegram.Client).
type ChatMenuAPI interface {
	ResetChatMenuButton(ctx context.Context, chatID int64) error
}

const (
	chatMenuResetTimeout = 5 * time.Second
	maxTrackedChats      = 100_000
)

// ChatMenus resets per-chat menu buttons to the default, once per chat and
// process. It is safe for concurrent use; a nil *ChatMenus does nothing.
type ChatMenus struct {
	api ChatMenuAPI
	log *slog.Logger

	mu   sync.Mutex
	done map[int64]struct{}
}

// NewChatMenus returns a ChatMenus using api.
func NewChatMenus(api ChatMenuAPI, log *slog.Logger) *ChatMenus {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &ChatMenus{api: api, log: log, done: map[int64]struct{}{}}
}

// Ensure makes the private chat use the default menu button unless that was
// done already by this process. Failures are logged; a retryable one is
// tried again with the chat's next update.
func (m *ChatMenus) Ensure(ctx context.Context, chatID int64) {
	if m == nil || m.api == nil || chatID <= 0 || ctx.Err() != nil {
		return
	}
	m.mu.Lock()
	_, seen := m.done[chatID]
	m.mu.Unlock()
	if seen {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, chatMenuResetTimeout)
	err := m.api.ResetChatMenuButton(cctx, chatID)
	cancel()
	if err != nil {
		m.log.Debug("reset chat menu button failed", slog.Int64("chat_id", chatID), slog.String("err", err.Error()))
		if telegram.IsRetryable(err) || ctx.Err() != nil {
			return
		}
	}
	m.mu.Lock()
	if len(m.done) >= maxTrackedChats {
		clear(m.done)
	}
	m.done[chatID] = struct{}{}
	m.mu.Unlock()
}
