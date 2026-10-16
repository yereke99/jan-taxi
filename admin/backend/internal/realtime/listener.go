package realtime

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// idleCheckEvery bounds how long a silently dead LISTEN connection can go unnoticed.
	idleCheckEvery  = 45 * time.Second
	reconnectMin    = time.Second
	reconnectMax    = 30 * time.Second
	listenerAppName = "jantaxi-api-listener"
)

// resyncMessage tells clients that events may have been missed (the LISTEN
// connection was re-established) and they should re-fetch state over REST.
var resyncMessage = []byte(`{"type":"resync"}`)

// Run LISTENs on Channel over a dedicated connection built from the pool's
// configuration and dispatches notifications until ctx ends. It reconnects with
// jittered exponential backoff. When ctx ends every subscription is closed. Run
// must be started once per API process (every replica listens).
func (h *Hub) Run(ctx context.Context, pool *pgxpool.Pool) error {
	h.running.Store(true)
	defer h.shutdown()
	connCfg := pool.Config().ConnConfig
	connCfg.RuntimeParams["application_name"] = listenerAppName

	delay := reconnectMin
	connectedBefore := false
	for {
		err := h.listen(ctx, connCfg, connectedBefore, func() {
			connectedBefore = true
			delay = reconnectMin
		})
		h.listening.Store(false)
		if ctx.Err() != nil {
			return nil
		}
		wait := delay/2 + rand.N(delay/2+1)
		h.log.Warn("realtime listener disconnected; reconnecting", slog.Any("err", err), slog.Duration("in", wait))
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(wait):
		}
		delay = min(delay*2, reconnectMax)
	}
}

func (h *Hub) listen(ctx context.Context, cfg *pgx.ConnConfig, reconnect bool, onConnected func()) error {
	conn, err := pgx.ConnectConfig(ctx, cfg.Copy())
	if err != nil {
		return err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = conn.Close(closeCtx)
	}()
	if _, err := conn.Exec(ctx, "LISTEN "+Channel); err != nil {
		return err
	}
	h.listening.Store(true)
	onConnected()
	if reconnect {
		h.log.Info("realtime listener reconnected; asking clients to resync")
		h.broadcast(resyncMessage)
	} else {
		h.log.Info("realtime listener started")
	}

	for {
		waitCtx, cancel := context.WithTimeout(ctx, idleCheckEvery)
		n, err := conn.WaitForNotification(waitCtx)
		cancel()
		switch {
		case err == nil:
			h.dispatch([]byte(n.Payload))
		case ctx.Err() != nil:
			return ctx.Err()
		case errors.Is(err, context.DeadlineExceeded):
			// Idle: prove the connection is still alive before waiting again.
			pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := conn.Ping(pingCtx)
			cancel()
			if err != nil {
				return err
			}
		default:
			return err
		}
	}
}
