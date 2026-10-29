package payments

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/notifications"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/textutil"
	"github.com/yereke99/jantaxi/admin/backend/internal/realtime"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
	"github.com/yereke99/jantaxi/admin/backend/internal/users"
	"github.com/yereke99/jantaxi/admin/backend/internal/wallets"
)

// MaxOpenTopUps caps a driver's top-ups waiting for payment or verification.
const MaxOpenTopUps = 5

// Driver statuses that may create top-ups: anyone registered except rejected
// applicants (a suspended driver may still settle a debt).
var topUpDriverStatuses = map[string]bool{"pending": true, "approved": true, "changes_requested": true, "suspended": true}

// driverLang returns the user's interface language (kk when unknown).
func driverLang(ctx context.Context, q db.Querier, userID uuid.UUID) string {
	lang, err := users.Language(ctx, q, userID)
	if err != nil || !users.ValidLanguage(lang) {
		return users.DefaultLanguage
	}
	return lang
}

func (s *Service) render(ctx context.Context, t TopUp, lang string) TopUpView {
	rc, err := latestReceipt(ctx, s.pool, t)
	if err != nil {
		s.log.Warn("load receipt failed", slog.String("topup_id", t.ID.String()), slog.Any("err", err))
	}
	return s.renderWith(ctx, t, lang, rc)
}

func (s *Service) renderWith(ctx context.Context, t TopUp, lang string, rc *receiptRow) TopUpView {
	return driverTopUpView(t, s.provider(t.Provider).Instructions(ctx, t, lang), rc)
}

// publishTopUp sends a top-up hint to the driver's app and, when admin is set,
// to every admin session.
func publishTopUp(ctx context.Context, q db.Querier, t TopUp, typ, status string, admin bool) error {
	scopes := []string{realtime.DriverScope(t.DriverID), realtime.UserScope(t.DriverUserID)}
	if admin {
		scopes = append(scopes, realtime.AdminScope)
	}
	return realtime.Publish(ctx, q, realtime.Event{Type: typ, Scopes: scopes, Data: map[string]any{
		"topup_id": t.ID, "reference": t.Reference, "status": status,
	}})
}

type createTopUpRequest struct {
	Amount         *int64  `json:"amount"`
	IdempotencyKey *string `json:"idempotency_key"`
}

func (s *Service) createTopUp(w http.ResponseWriter, r *http.Request) error {
	d, err := requireWalletDriver(r)
	if err != nil {
		return err
	}
	var in createTopUpRequest
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	fields := map[string]string{}
	if in.Amount == nil {
		fields["amount"] = "required"
	}
	var key uuid.UUID
	if in.IdempotencyKey == nil || strings.TrimSpace(*in.IdempotencyKey) == "" {
		fields["idempotency_key"] = "required"
	} else if key, err = uuid.Parse(strings.TrimSpace(*in.IdempotencyKey)); err != nil || key == uuid.Nil {
		fields["idempotency_key"] = "invalid"
	}
	if len(fields) > 0 {
		return httpx.Validation(fields)
	}
	ctx := r.Context()
	lang := driverLang(ctx, s.pool, d.UserID)

	t, created, err := s.insertTopUp(ctx, d, *in.Amount, key.String())
	if err != nil {
		return err
	}
	v := s.render(ctx, t, lang)
	if created {
		return httpx.Created(w, v)
	}
	return httpx.OK(w, v)
}

// insertTopUp creates the top-up, or returns the one created earlier with the
// same idempotency key (created=false).
func (s *Service) insertTopUp(ctx context.Context, d walletDriver, amount int64, key string) (TopUp, bool, error) {
	var (
		id      uuid.UUID
		created bool
	)
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		// One writer per driver, so the replay check and the open cap hold under concurrency.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, db.LockKey("topup:"+d.ID.String())); err != nil {
			return fmt.Errorf("payments: lock driver top-ups: %w", err)
		}
		var prevAmount int64
		err := tx.QueryRow(ctx, `SELECT id, requested_amount FROM top_up_requests WHERE driver_id = $1 AND idempotency_key = $2`,
			d.ID, key).Scan(&id, &prevAmount)
		switch {
		case err == nil:
			if prevAmount != amount {
				return httpx.Conflict("conflict", "idempotency key already used with different parameters").
					WithDetails(map[string]any{"reason": "idempotency_key_reused", "topup_id": id.String()})
			}
			return nil
		case !db.IsNoRows(err):
			return fmt.Errorf("payments: find top-up by key: %w", err)
		}

		if !topUpDriverStatuses[d.Status] {
			return httpx.Forbidden().WithDetails(map[string]any{"reason": "driver_" + d.Status})
		}
		limits := settings.TopUp(ctx, tx)
		switch {
		case amount < limits.Min:
			return httpx.Validation(map[string]string{"amount": "too_small"}).WithDetails(map[string]any{"min": limits.Min, "max": limits.Max})
		case amount > limits.Max:
			return httpx.Validation(map[string]string{"amount": "too_large"}).WithDetails(map[string]any{"min": limits.Min, "max": limits.Max})
		}
		if !s.kaspi.Configured(ctx) {
			return errPaymentNotConfigured
		}
		var open int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM top_up_requests WHERE driver_id = $1 AND status IN ('created', 'pending_verification')`,
			d.ID).Scan(&open); err != nil {
			return fmt.Errorf("payments: count open top-ups: %w", err)
		}
		if open >= MaxOpenTopUps {
			return httpx.Conflict("conflict", "too many open top-ups").
				WithDetails(map[string]any{"reason": "too_many_open_topups", "limit": MaxOpenTopUps})
		}

		walletID, err := wallets.Ensure(ctx, tx, d.ID)
		if err != nil {
			return wallets.HTTPError(err)
		}
		var number int64
		if err := tx.QueryRow(ctx, `SELECT nextval('topup_number_seq')`).Scan(&number); err != nil {
			return fmt.Errorf("payments: next top-up number: %w", err)
		}
		reference := fmt.Sprintf("%s-TOPUP-%06d", d.Code, number)
		var payURL *string
		paymentURL := s.kaspi.PaymentURL(ctx)
		if paymentURL != "" {
			payURL = &paymentURL
		}
		expiry := settings.TopUpExpiry(ctx, tx)
		if err := tx.QueryRow(ctx, `
			INSERT INTO top_up_requests (number, reference, driver_id, wallet_id, requested_amount, provider,
			                             payment_url, idempotency_key, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now() + $9::bigint * interval '1 second')
			RETURNING id`,
			number, reference, d.ID, walletID, amount, ProviderKaspiManual, payURL, key, int64(expiry/time.Second)).Scan(&id); err != nil {
			return fmt.Errorf("payments: insert top-up: %w", err)
		}
		created = true
		actor := d.ID
		if err := addEvent(ctx, tx, id, "", StatusCreated, ActorDriver, &actor, "", map[string]any{"amount": amount}); err != nil {
			return err
		}
		if err := notifications.Enqueue(ctx, tx, notifications.Notification{
			Bot: notifications.DriverBot, UserID: d.UserID, Template: notifications.TplTopUpCreated,
			Params:    map[string]any{"topup_id": id.String(), "reference": reference, "amount": amount, "payment_url": paymentURL},
			DedupeKey: "topup_created:" + id.String(),
		}); err != nil {
			return fmt.Errorf("payments: enqueue topup_created: %w", err)
		}
		t := TopUp{ID: id, Reference: reference, DriverID: d.ID, DriverUserID: d.UserID}
		return publishTopUp(ctx, tx, t, realtime.TopUpUpdated, StatusCreated, true)
	})
	if err != nil {
		return TopUp{}, false, err
	}
	t, err := GetTopUp(ctx, s.pool, id, false)
	return t, created, err
}

func (s *Service) listDriverTopUps(w http.ResponseWriter, r *http.Request) error {
	d, err := requireWalletDriver(r)
	if err != nil {
		return err
	}
	limit, before, err := cursorParams(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	rows, err := s.pool.Query(ctx, `SELECT `+topUpColumns+topUpFrom+`
		WHERE t.driver_id = $1 AND ($2::bigint IS NULL OR t.number < $2)
		ORDER BY t.number DESC LIMIT $3`, d.ID, before, limit+1)
	if err != nil {
		return fmt.Errorf("payments: list driver top-ups: %w", err)
	}
	list, err := scanTopUps(rows)
	if err != nil {
		return err
	}
	lang := driverLang(ctx, s.pool, d.UserID)
	var receiptIDs []uuid.UUID
	for _, t := range list {
		if t.PaymentReceiptID != nil {
			receiptIDs = append(receiptIDs, *t.PaymentReceiptID)
		}
	}
	receipts, err := receiptsByID(ctx, s.pool, receiptIDs)
	if err != nil {
		return err
	}
	out := httpx.CursorList[TopUpView]{Items: make([]TopUpView, 0, min(len(list), limit))}
	for i, t := range list {
		if i == limit {
			out.NextCursor = nextCursor(list[limit-1].Number)
			break
		}
		var rc *receiptRow
		if t.PaymentReceiptID != nil {
			rc = receipts[*t.PaymentReceiptID]
		}
		out.Items = append(out.Items, s.renderWith(ctx, t, lang, rc))
	}
	return httpx.OK(w, out)
}

func (s *Service) getDriverTopUp(w http.ResponseWriter, r *http.Request) error {
	d, err := requireWalletDriver(r)
	if err != nil {
		return err
	}
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	ctx := r.Context()
	t, err := getDriverTopUp(ctx, s.pool, d.ID, id, false)
	if err != nil {
		return err
	}
	return httpx.OK(w, s.render(ctx, t, driverLang(ctx, s.pool, d.UserID)))
}

type submitTopUpRequest struct {
	PaymentReference *string `json:"payment_reference"`
	Note             *string `json:"note"`
}

// submitTopUp records the driver's "I paid". It only moves the top-up to
// pending_verification for a finance admin; it never credits the wallet.
func (s *Service) submitTopUp(w http.ResponseWriter, r *http.Request) error {
	d, err := requireWalletDriver(r)
	if err != nil {
		return err
	}
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	var in submitTopUpRequest
	if err := decodeOptionalJSON(w, r, &in); err != nil {
		return err
	}
	fields := map[string]string{}
	ref, err := textutil.CleanOptional(in.PaymentReference, 100)
	if err != nil {
		fields["payment_reference"] = textutil.Reason(err)
	}
	note, err := textutil.CleanOptional(in.Note, 300)
	if err != nil {
		fields["note"] = textutil.Reason(err)
	}
	if len(fields) > 0 {
		return httpx.Validation(fields)
	}
	ctx := r.Context()
	err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		t, err := getDriverTopUp(ctx, tx, d.ID, id, true)
		if err != nil {
			return err
		}
		switch t.Status {
		case StatusPendingVerification:
			return nil // repeated "I paid": already waiting for verification
		case StatusCreated:
		default:
			return invalidTransition(t.Status, "submit")
		}
		if _, err := tx.Exec(ctx, `
			UPDATE top_up_requests
			SET status = 'pending_verification', driver_reported_paid_at = now(),
			    driver_payment_reference = COALESCE($2, driver_payment_reference),
			    driver_note = COALESCE($3, driver_note)
			WHERE id = $1`, id, ref, note); err != nil {
			return fmt.Errorf("payments: submit top-up: %w", err)
		}
		actor := d.ID
		meta := map[string]any{}
		if ref != nil {
			meta["payment_reference"] = *ref
		}
		if err := addEvent(ctx, tx, id, StatusCreated, StatusPendingVerification, ActorDriver, &actor, "", meta); err != nil {
			return err
		}
		return publishTopUp(ctx, tx, t, realtime.TopUpUpdated, StatusPendingVerification, true)
	})
	if err != nil {
		return err
	}
	return s.respondDriverTopUp(w, r, d, id)
}

// cancelTopUp closes an unpaid top-up. The schema has no cancelled state, so it
// becomes expired with a "cancelled_by_driver" event.
func (s *Service) cancelTopUp(w http.ResponseWriter, r *http.Request) error {
	d, err := requireWalletDriver(r)
	if err != nil {
		return err
	}
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	ctx := r.Context()
	err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		t, err := getDriverTopUp(ctx, tx, d.ID, id, true)
		if err != nil {
			return err
		}
		if t.Status != StatusCreated {
			return invalidTransition(t.Status, "cancel")
		}
		if _, err := tx.Exec(ctx, `UPDATE top_up_requests SET status = 'expired' WHERE id = $1`, id); err != nil {
			return fmt.Errorf("payments: cancel top-up: %w", err)
		}
		actor := d.ID
		if err := addEvent(ctx, tx, id, StatusCreated, StatusExpired, ActorDriver, &actor, NoteCancelledByDriver, nil); err != nil {
			return err
		}
		return publishTopUp(ctx, tx, t, realtime.TopUpUpdated, StatusExpired, true)
	})
	if err != nil {
		return err
	}
	return s.respondDriverTopUp(w, r, d, id)
}

func (s *Service) respondDriverTopUp(w http.ResponseWriter, r *http.Request, d walletDriver, id uuid.UUID) error {
	ctx := r.Context()
	t, err := getDriverTopUp(ctx, s.pool, d.ID, id, false)
	if err != nil {
		return err
	}
	return httpx.OK(w, s.render(ctx, t, driverLang(ctx, s.pool, d.UserID)))
}

// decodeOptionalJSON decodes an optional JSON body: an empty or whitespace-only
// body leaves dst untouched; anything else is decoded strictly.
func decodeOptionalJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	if r.Body == nil || r.ContentLength == 0 {
		return nil
	}
	br := bufio.NewReader(http.MaxBytesReader(w, r.Body, httpx.MaxJSONBody))
	for {
		b, err := br.Peek(1)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				return httpx.NewError(http.StatusRequestEntityTooLarge, "payload_too_large", "request body too large")
			}
			return httpx.BadRequest("unreadable request body").WithCause(err)
		}
		if c := b[0]; c != ' ' && c != '\t' && c != '\r' && c != '\n' {
			break
		}
		_, _ = br.ReadByte()
	}
	r.Body = io.NopCloser(br)
	return httpx.DecodeJSON(w, r, dst)
}
