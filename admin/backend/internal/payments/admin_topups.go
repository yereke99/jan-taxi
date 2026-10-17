package payments

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/phone"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/storage"
)

const adminTopUpColumns = topUpColumns + `, d.first_name, d.last_name, COALESCE(d.phone_e164, u.phone_e164),
	ca.email, ca.full_name, ra.email, ra.full_name, fa.email, fa.full_name`

const adminTopUpFrom = topUpFrom + ` JOIN users u ON u.id = d.user_id
	LEFT JOIN admin_users ca ON ca.id = t.confirmed_by
	LEFT JOIN admin_users ra ON ra.id = t.rejected_by
	LEFT JOIN admin_users fa ON fa.id = t.refunded_by`

func scanAdminTopUp(row pgx.Row) (AdminTopUpView, TopUp, error) {
	var (
		t               TopUp
		first, last     string
		phoneNo         *string
		caEmail, caName *string
		raEmail, raName *string
		faEmail, faName *string
	)
	dests := append(t.dests(), &first, &last, &phoneNo, &caEmail, &caName, &raEmail, &raName, &faEmail, &faName)
	if err := row.Scan(dests...); err != nil {
		return AdminTopUpView{}, TopUp{}, err
	}
	v := AdminTopUpView{
		ID:                     t.ID,
		Number:                 t.Number,
		Reference:              t.Reference,
		Status:                 t.Status,
		Provider:               t.Provider,
		Amount:                 t.RequestedAmount,
		VerifiedAmount:         t.VerifiedAmount,
		Currency:               t.Currency,
		Driver:                 DriverRef{ID: t.DriverID, Code: t.DriverCode, FirstName: first, LastName: last, Phone: phoneNo},
		PaymentURL:             t.PaymentURL,
		DriverPaymentReference: t.DriverPaymentReference,
		DriverNote:             t.DriverNote,
		HasReceipt:             t.ReceiptFileID != nil,
		DriverReportedPaidAt:   utc(t.DriverReportedPaidAt),
		ProviderTransactionID:  t.ProviderTransactionID,
		PaidAt:                 utc(t.PaidAt),
		ConfirmedBy:            adminRef(t.ConfirmedBy, caEmail, caName),
		ConfirmedAutomatically: t.ConfirmedBySystem,
		ConfirmedAt:            utc(t.ConfirmedAt),
		RejectedBy:             adminRef(t.RejectedBy, raEmail, raName),
		RejectedAt:             utc(t.RejectedAt),
		RejectedReason:         t.RejectReason,
		RefundedBy:             adminRef(t.RefundedBy, faEmail, faName),
		RefundedAt:             utc(t.RefundedAt),
		RefundReason:           t.RefundReason,
		ExpiresAt:              t.ExpiresAt.UTC(),
		CreatedAt:              t.CreatedAt.UTC(),
		UpdatedAt:              t.UpdatedAt.UTC(),
	}
	if v.HasReceipt {
		u := "/api/v1/admin/topups/" + t.ID.String() + "/receipt"
		v.ReceiptURL = &u
	}
	return v, t, nil
}

func adminRef(id *uuid.UUID, email, name *string) *AdminRef {
	if id == nil || email == nil {
		return nil
	}
	ref := AdminRef{ID: *id, Email: *email}
	if name != nil {
		ref.FullName = *name
	}
	return &ref
}

func (s *Service) adminListTopUps(w http.ResponseWriter, r *http.Request) error {
	page, err := httpx.ParsePage(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	status := strings.TrimSpace(q.Get("status"))
	if status != "" && !ValidStatus(status) {
		return httpx.Validation(map[string]string{"status": "invalid"})
	}
	driverID, err := httpx.QueryUUID(r, "driver_id")
	if err != nil {
		return err
	}
	from, to, err := queryRange(r)
	if err != nil {
		return err
	}
	search := strings.TrimSpace(q.Get("q"))
	if len([]rune(search)) > 100 {
		return httpx.Validation(map[string]string{"q": "too_long"})
	}

	var (
		where []string
		args  []any
	)
	arg := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}
	if status != "" {
		where = append(where, "t.status = "+arg(status))
	}
	if driverID != nil {
		where = append(where, "t.driver_id = "+arg(*driverID))
	}
	if from != nil {
		where = append(where, "t.created_at >= "+arg(*from))
	}
	if to != nil {
		where = append(where, "t.created_at < "+arg(*to))
	}
	if search != "" {
		or := []string{
			"t.reference ILIKE " + arg(likePattern(search)) + ` ESCAPE '\'`,
			"d.code = " + arg(strings.ToUpper(search)),
			"t.provider_transaction_id = " + arg(normalizeTransactionID(search)),
		}
		if p, err := phone.Normalize(search); err == nil {
			ph := arg(p)
			or = append(or, "d.phone_e164 = "+ph, "u.phone_e164 = "+ph)
		}
		where = append(where, "("+strings.Join(or, " OR ")+")")
	}
	cond := ""
	if len(where) > 0 {
		cond = " WHERE " + strings.Join(where, " AND ")
	}
	order := " ORDER BY t.created_at DESC, t.number DESC"
	if status == StatusPendingVerification {
		order = " ORDER BY t.created_at ASC, t.number ASC"
	}

	ctx := r.Context()
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*)`+adminTopUpFrom+cond, args...).Scan(&total); err != nil {
		return fmt.Errorf("payments: count top-ups: %w", err)
	}
	rows, err := s.pool.Query(ctx, `SELECT `+adminTopUpColumns+adminTopUpFrom+cond+order+
		` LIMIT `+arg(page.PageSize)+` OFFSET `+arg(page.Offset()), args...)
	if err != nil {
		return fmt.Errorf("payments: list top-ups: %w", err)
	}
	defer rows.Close()
	out := httpx.Page[AdminTopUpView]{Items: []AdminTopUpView{}, Page: page.Page, PageSize: page.PageSize, Total: total}
	for rows.Next() {
		v, _, err := scanAdminTopUp(rows)
		if err != nil {
			return fmt.Errorf("payments: scan top-up: %w", err)
		}
		out.Items = append(out.Items, v)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("payments: list top-ups: %w", err)
	}
	return httpx.OK(w, out)
}

func (s *Service) adminGetTopUp(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	d, err := loadAdminTopUpDetail(r.Context(), s.pool, id)
	if err != nil {
		return err
	}
	return httpx.OK(w, d)
}

// loadAdminTopUpDetail loads the admin view with history, the recorded payment
// and duplicate warnings.
func loadAdminTopUpDetail(ctx context.Context, q db.Querier, id uuid.UUID) (AdminTopUpDetail, error) {
	v, t, err := scanAdminTopUp(q.QueryRow(ctx, `SELECT `+adminTopUpColumns+adminTopUpFrom+` WHERE t.id = $1`, id))
	if db.IsNoRows(err) {
		return AdminTopUpDetail{}, httpx.NotFound()
	}
	if err != nil {
		return AdminTopUpDetail{}, fmt.Errorf("payments: load top-up: %w", err)
	}
	out := AdminTopUpDetail{AdminTopUpView: v}
	if out.Events, err = ListEvents(ctx, q, id); err != nil {
		return AdminTopUpDetail{}, err
	}
	if out.PaymentTransaction, err = paymentForTopUp(ctx, q, id); err != nil {
		return AdminTopUpDetail{}, err
	}
	if out.DuplicateWarnings, err = duplicateWarnings(ctx, q, t); err != nil {
		return AdminTopUpDetail{}, err
	}
	receipts, err := receiptsForTopUp(ctx, q, id)
	if err != nil {
		return AdminTopUpDetail{}, err
	}
	out.Receipts = make([]AdminReceiptView, 0, len(receipts))
	for _, r := range receipts {
		out.Receipts = append(out.Receipts, adminReceiptView(r))
	}
	for i, r := range receipts {
		if t.PaymentReceiptID != nil && r.id == *t.PaymentReceiptID {
			v := out.Receipts[i]
			out.Receipt = &v
			out.ConfirmPrefill = &ConfirmPrefill{VerifiedAmount: r.amount, ProviderTransactionID: r.serial,
				PaidAt: utc(r.paidAt), PayerInfo: r.payerMasked}
		}
	}
	return out, nil
}

const paymentColumns = `p.id, p.provider, p.provider_transaction_id, p.amount, p.currency, p.paid_at, p.payer_info,
	p.source, p.created_at, p.recorded_by, a.email, a.full_name`

func paymentForTopUp(ctx context.Context, q db.Querier, topupID uuid.UUID) (*PaymentTransactionView, error) {
	var (
		p           PaymentTransactionView
		by          *uuid.UUID
		email, name *string
	)
	err := q.QueryRow(ctx, `SELECT `+paymentColumns+`
		FROM payment_transactions p LEFT JOIN admin_users a ON a.id = p.recorded_by
		WHERE p.topup_id = $1`, topupID).Scan(&p.ID, &p.Provider, &p.ProviderTransactionID, &p.Amount, &p.Currency,
		&p.PaidAt, &p.PayerInfo, &p.Source, &p.CreatedAt, &by, &email, &name)
	if db.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("payments: load payment transaction: %w", err)
	}
	p.PaidAt = utc(p.PaidAt)
	p.CreatedAt = p.CreatedAt.UTC()
	p.RecordedBy = adminRef(by, email, name)
	return &p, nil
}

// duplicateWarnings flags evidence that one real payment is claimed twice. The
// identifiers of t are the payment reference its driver entered and, once
// confirmed, the provider transaction id. Other top-ups whose driver entered
// one of them, and recorded payments with one of them as transaction id
// (other than t's own), are reported.
func duplicateWarnings(ctx context.Context, q db.Querier, t TopUp) ([]DuplicateWarning, error) {
	out := []DuplicateWarning{}
	var ids []string
	for _, s := range []*string{t.DriverPaymentReference, t.ProviderTransactionID} {
		if s == nil {
			continue
		}
		if id := normalizeTransactionID(*s); id != "" && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	if rc, err := linkedReceipt(ctx, q, t); err != nil {
		return nil, err
	} else if rc != nil && rc.serial != nil && !slices.Contains(ids, *rc.serial) {
		ids = append(ids, *rc.serial)
	}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `
		SELECT t2.id, t2.reference, t2.status, d2.code
		FROM top_up_requests t2 JOIN driver_profiles d2 ON d2.id = t2.driver_id
		WHERE t2.id <> $1 AND upper(regexp_replace(t2.driver_payment_reference, '\s', '', 'g')) = ANY($2::text[])
		ORDER BY t2.created_at LIMIT 20`, t.ID, ids)
	if err != nil {
		return nil, fmt.Errorf("payments: duplicate references: %w", err)
	}
	for rows.Next() {
		var (
			id                  uuid.UUID
			reference, st, code string
		)
		if err := rows.Scan(&id, &reference, &st, &code); err != nil {
			rows.Close()
			return nil, fmt.Errorf("payments: scan duplicate: %w", err)
		}
		out = append(out, DuplicateWarning{Kind: WarnSameDriverReference, TopUpID: &id, Reference: &reference, Status: &st, DriverCode: &code})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("payments: duplicate references: %w", err)
	}

	rows, err = q.Query(ctx, `
		SELECT r.receipt_serial, t2.id, t2.reference, t2.status, d2.code
		FROM payment_receipts r
		JOIN top_up_requests t2 ON t2.id = r.topup_id
		JOIN driver_profiles d2 ON d2.id = t2.driver_id
		WHERE r.receipt_serial = ANY($1::text[]) AND r.topup_id <> $2
		ORDER BY r.created_at LIMIT 20`, ids, t.ID)
	if err != nil {
		return nil, fmt.Errorf("payments: duplicate receipts: %w", err)
	}
	for rows.Next() {
		var (
			serial, reference, st, code string
			id                          uuid.UUID
		)
		if err := rows.Scan(&serial, &id, &reference, &st, &code); err != nil {
			rows.Close()
			return nil, fmt.Errorf("payments: scan duplicate receipt: %w", err)
		}
		out = append(out, DuplicateWarning{Kind: WarnReceiptSerialUsed, TopUpID: &id, Reference: &reference, Status: &st,
			DriverCode: &code, ProviderTransaction: &serial})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("payments: duplicate receipts: %w", err)
	}

	rows, err = q.Query(ctx, `
		SELECT p.id, p.provider_transaction_id, t2.id, t2.reference, t2.status, d2.code
		FROM payment_transactions p
		LEFT JOIN top_up_requests t2 ON t2.id = p.topup_id
		LEFT JOIN driver_profiles d2 ON d2.id = t2.driver_id
		WHERE p.provider_transaction_id = ANY($1::text[]) AND p.topup_id IS DISTINCT FROM $2
		ORDER BY p.created_at LIMIT 20`, ids, t.ID)
	if err != nil {
		return nil, fmt.Errorf("payments: duplicate transactions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			w                   DuplicateWarning
			pid                 uuid.UUID
			txn                 string
			tid                 *uuid.UUID
			reference, st, code *string
		)
		if err := rows.Scan(&pid, &txn, &tid, &reference, &st, &code); err != nil {
			return nil, fmt.Errorf("payments: scan duplicate transaction: %w", err)
		}
		w = DuplicateWarning{Kind: WarnTransactionRecorded, PaymentTransactionID: &pid, ProviderTransaction: &txn,
			TopUpID: tid, Reference: reference, Status: st, DriverCode: code}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *Service) adminGetReceipt(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	ctx := r.Context()
	t, err := GetTopUp(ctx, s.pool, id, false)
	if err != nil {
		return err
	}
	if t.ReceiptFileID == nil {
		return httpx.NotFound()
	}
	f, rc, err := s.store.Open(ctx, s.pool, *t.ReceiptFileID)
	if err != nil {
		return err
	}
	if f.Purpose != storage.PurposeTopupReceipt {
		_ = rc.Close()
		return httpx.NotFound()
	}
	storage.ServeFile(w, r, f, rc)
	return nil
}
