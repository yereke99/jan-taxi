package notifications

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
	"github.com/yereke99/jantaxi/admin/backend/pkg/telegram"
)

type queuedContact struct {
	orderID  uuid.UUID
	outboxID uuid.UUID
	driver   testutil.DriverFixture
}

// Match production: the assignment timestamp and card are written together.
func enqueueContact(t *testing.T, pool *pgxpool.Pool) queuedContact {
	t.Helper()
	ctx := context.Background()
	passengerID, _ := testutil.CreateUser(t, pool)
	driver := testutil.CreateDriver(t, pool, testutil.DriverOptions{})
	q := queuedContact{driver: driver}
	key := "test-contact:" + uuid.NewString()
	err := db.WithTx(ctx, pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			INSERT INTO orders (passenger_id, city_id, category, service_type, cargo_type, status, driver_id, vehicle_id,
			                    fare, commission_bps, commission_amount, payment_method, tariff_id, distance_m, duration_s,
			                    stop_count, pickup_lat, pickup_lon, idempotency_key, accepted_at, vehicle_class, passenger_count)
			SELECT $1, c.id, 'taxi', 'passenger', 'none', 'accepted', $2, $3, 800, 1000, 80, 'cash', t.id, 1200, 180,
			       2, 48.8264, 58.1442, $4, now(), 'sedan', 1
			FROM cities c JOIN tariffs t ON t.city_id = c.id AND t.category = 'taxi' AND t.vehicle_class = 'sedan' AND t.is_active
			WHERE c.code = 'EMB' LIMIT 1 RETURNING id`, passengerID, driver.DriverID, driver.VehicleID, uuid.NewString()).Scan(&q.orderID); err != nil {
			return err
		}
		if err := Enqueue(ctx, tx, Notification{
			Bot: DriverBot, UserID: driver.UserID, Template: TplOrderAcceptedDriver, DedupeKey: key,
			Params: map[string]any{"order_id": q.orderID.String(), "order_number": 1042, "pickup": "Pickup", "destination": "Destination",
				"fare": 800, "payment_method": "cash", "passenger_name": "Passenger", "passenger_link": "https://t.me/passenger",
				"passenger_phone": "+7 701 234 56 78"},
		}); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT id FROM notification_outbox WHERE dedupe_key = $1`, key).Scan(&q.outboxID)
	})
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func TestContactDeliveryFollowsCurrentAssignment(t *testing.T) {
	for _, state := range []string{"accepted", "driver_arriving", "in_progress", "cancelled", "completed", "reassigned", "same_driver_new_assignment", "wrong_recipient"} {
		t.Run(state, func(t *testing.T) {
			pool := testutil.NewDB(t)
			ctx := context.Background()
			q := enqueueContact(t, pool)
			var err error
			switch state {
			case "reassigned":
				other := testutil.CreateDriver(t, pool, testutil.DriverOptions{})
				_, err = pool.Exec(ctx, `UPDATE orders SET driver_id = $2, accepted_at = clock_timestamp() WHERE id = $1`, q.orderID, other.DriverID)
			case "same_driver_new_assignment":
				_, err = pool.Exec(ctx, `UPDATE orders SET accepted_at = accepted_at + interval '1 microsecond' WHERE id = $1`, q.orderID)
			case "wrong_recipient":
				other, otherTG := testutil.CreateUser(t, pool)
				_, err = pool.Exec(ctx, `UPDATE notification_outbox SET user_id = $2, chat_id = $3 WHERE id = $1`, q.outboxID, other, otherTG)
			default:
				_, err = pool.Exec(ctx, `UPDATE orders SET status = $2, version = version + 1,
					cancelled_at = CASE WHEN $2 = 'cancelled' THEN now() END,
					cancelled_by = CASE WHEN $2 = 'cancelled' THEN 'passenger' END WHERE id = $1`, q.orderID, state)
			}
			if err != nil {
				t.Fatal(err)
			}
			fs := newFakeSender()
			if n, err := testWorker(pool, fs, "contacts").RunOnce(ctx); err != nil || n != 1 {
				t.Fatalf("RunOnce = %d, %v", n, err)
			}
			want := StatusCancelled
			if state == "accepted" || state == "driver_arriving" || state == "in_progress" {
				want = StatusSent
			}
			if got := readOutbox(t, pool, q.outboxID).status; got != want {
				t.Fatalf("status = %s, want %s", got, want)
			}
			if sent := fs.sentTo(q.driver.TelegramID); (len(sent) == 1) != (want == StatusSent) {
				t.Fatalf("delivered %d messages in state %s", len(sent), state)
			}
		})
	}
}

func TestContactRetryRechecksRevocation(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	q := enqueueContact(t, pool)
	fs := newFakeSender()
	fs.fail(q.driver.TelegramID, apiErr(500, "upstream unavailable", 0))
	w := testWorker(pool, fs, "contact-retry")
	if _, err := w.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := readOutbox(t, pool, q.outboxID).status; got != StatusPending {
		t.Fatalf("failed delivery status = %s", got)
	}
	if _, err := pool.Exec(ctx, `UPDATE orders SET status = 'cancelled', cancelled_at = now(), cancelled_by = 'passenger' WHERE id = $1`, q.orderID); err != nil {
		t.Fatal(err)
	}
	makeDue(t, pool, q.outboxID)
	if _, err := w.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := readOutbox(t, pool, q.outboxID).status; got != StatusCancelled || len(fs.sentTo(q.driver.TelegramID)) != 0 {
		t.Fatalf("revoked retry status = %s, delivered = %d", got, len(fs.sentTo(q.driver.TelegramID)))
	}
}

func TestContactCancellationSupersedesPendingCardsTransactionally(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	q := enqueueContact(t, pool)
	other := enqueueContact(t, pool)
	if err := db.WithTx(ctx, pool, func(tx pgx.Tx) error { return CancelOrderContacts(ctx, tx, q.orderID) }); err != nil {
		t.Fatal(err)
	}
	if readOutbox(t, pool, q.outboxID).status != StatusCancelled || readOutbox(t, pool, other.outboxID).status != StatusPending {
		t.Fatal("supersession affected the wrong cards")
	}
}

func TestContactAuthorizationLockIsReleasedOnSendTimeout(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	q := enqueueContact(t, pool)
	fs := newFakeSender()
	fs.latency = time.Second
	w := testWorker(pool, fs, "contact-timeout")
	w.opt.SendTimeout = 20 * time.Millisecond
	if _, err := w.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	bounded, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if _, err := pool.Exec(bounded, `UPDATE orders SET status = 'cancelled', cancelled_at = now(), cancelled_by = 'passenger' WHERE id = $1`, q.orderID); err != nil {
		t.Fatalf("contact send retained its lock: %v", err)
	}
	if len(fs.sentTo(q.driver.TelegramID)) != 0 || readOutbox(t, pool, q.outboxID).status != StatusPending {
		t.Fatal("timed-out contact was delivered or cannot retry")
	}
}

type heldContactSender struct {
	entered chan struct{}
	release chan struct{}
}

func (s heldContactSender) Send(ctx context.Context, _ Bot, _ int64, _ string, _ *telegram.InlineKeyboardMarkup) (int64, error) {
	close(s.entered)
	select {
	case <-s.release:
		return 1001, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

func TestContactSendSerializesRevocation(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	q := enqueueContact(t, pool)
	sender := heldContactSender{entered: make(chan struct{}), release: make(chan struct{})}
	w := testWorker(pool, sender, "contact-race")
	done := make(chan error, 1)
	go func() { _, err := w.RunOnce(ctx); done <- err }()
	select {
	case <-sender.entered:
	case <-time.After(time.Second):
		t.Fatal("contact send did not begin")
	}
	blocked, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	_, err := pool.Exec(blocked, `UPDATE orders SET accepted_at = clock_timestamp() WHERE id = $1`, q.orderID)
	close(sender.release)
	if err == nil {
		t.Error("assignment changed between contact authorization and delivery")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("contact send did not release its authorization lock")
	}
}
