package driverbot

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/yereke99/jantaxi/admin/backend/pkg/botapi"
	"github.com/yereke99/jantaxi/admin/backend/pkg/botapi/runtime"
	"github.com/yereke99/jantaxi/admin/backend/pkg/telegram"
)

// maxReceiptBytes bounds a receipt downloaded from Telegram (Kaspi receipts
// are about 25 KB; the API accepts at most 5 MiB).
const maxReceiptBytes = 5 << 20

// receiptReasons are the review reasons the bot can explain
// ("receipt_reason_<code>" texts).
var receiptReasons = map[string]bool{
	"amount_mismatch": true, "recipient_mismatch": true, "paid_before_topup": true, "paid_in_future": true,
	"receipt_too_old": true, "daily_count_limit": true, "daily_amount_limit": true, "auto_confirm_disabled": true,
	"receipt_unreadable": true, "receipt_incomplete": true, "receipt_inconsistent": true, "tamper_suspected": true,
}

// isPDF reports a document Telegram describes as a PDF.
func isPDF(d *telegram.Document) bool {
	return strings.EqualFold(d.MimeType, "application/pdf") || strings.HasSuffix(strings.ToLower(d.FileName), ".pdf")
}

// onDocument takes a Kaspi receipt PDF (shared from the Kaspi app to this
// chat), forwards it to the API and tells the driver what happened: credited,
// under review (with the reasons) or rejected as already used.
func (b *Bot) onDocument(ctx context.Context, s *session, d *telegram.Document) error {
	if !s.driver.Registered || s.status() == statusDraft {
		return b.registration(ctx, s)
	}
	if !isPDF(d) {
		return b.send(ctx, s, s.t("receipt_send_pdf"), b.topupKeyboard(ctx, s))
	}
	if d.FileSize > maxReceiptBytes {
		return b.send(ctx, s, s.t("receipt_too_large"), b.keyboard(s))
	}
	f, err := b.tg.GetFile(ctx, d.FileID)
	if err != nil {
		_ = b.send(ctx, s, s.t("error"), b.keyboard(s))
		return fmt.Errorf("get file: %w", err)
	}
	data, err := b.tg.DownloadFile(ctx, f.FilePath, maxReceiptBytes)
	if errors.Is(err, telegram.ErrFileTooBig) {
		return b.send(ctx, s, s.t("receipt_too_large"), b.keyboard(s))
	}
	if err != nil {
		_ = b.send(ctx, s, s.t("error"), b.keyboard(s))
		return fmt.Errorf("download file: %w", err)
	}
	name := d.FileName
	if name == "" {
		name = "receipt.pdf"
	}
	res, err := b.api.UploadTopUpReceipt(ctx, s.tgID, name, data)
	if err != nil {
		return b.receiptError(ctx, s, err)
	}
	return b.receiptResult(ctx, s, res)
}

func (b *Bot) receiptResult(ctx context.Context, s *session, res botapi.TopUpReceiptResult) error {
	switch res.Outcome {
	case botapi.ReceiptCredited:
		amount := res.TopUp.Amount
		if res.TopUp.VerifiedAmount != nil {
			amount = *res.TopUp.VerifiedAmount
		}
		var balance int64
		if res.Balance != nil {
			balance = *res.Balance
		}
		text := s.t("receipt_credited", runtime.Esc(runtime.FormatMoney(amount)), runtime.Esc(res.TopUp.Reference),
			runtime.Esc(runtime.FormatMoney(balance)))
		return b.send(ctx, s, text, runtime.Inline(b.appButton(ctx, s, "btn_wallet", "wallet")))
	case botapi.ReceiptDuplicate:
		return b.send(ctx, s, s.t("receipt_duplicate"), b.keyboard(s))
	}
	lines := []string{s.t("receipt_pending", runtime.Esc(res.TopUp.Reference))}
	var reasons []string
	for _, r := range res.Reasons {
		if receiptReasons[r] {
			reasons = append(reasons, s.t("receipt_reason_line", runtime.Esc(s.t("receipt_reason_"+r))))
		}
	}
	if len(reasons) > 0 {
		lines = append(lines, "", s.t("receipt_reasons_title"))
		lines = append(lines, reasons...)
	}
	kb := runtime.Inline(b.appButton(ctx, s, "btn_topup_open", "topup_"+res.TopUp.ID))
	if kb == nil {
		return b.send(ctx, s, strings.Join(lines, "\n"), b.keyboard(s))
	}
	return b.send(ctx, s, strings.Join(lines, "\n"), kb)
}

// receiptError explains a refused receipt.
func (b *Bot) receiptError(ctx context.Context, s *session, err error) error {
	e, ok := botapi.AsError(err)
	switch {
	case !ok:
	case e.Code == "no_open_topup":
		return b.send(ctx, s, s.t("receipt_no_topup"), b.topupKeyboard(ctx, s))
	case e.Code == "receipt_already_used":
		return b.send(ctx, s, s.t("receipt_duplicate"), b.keyboard(s))
	case e.Code == "file_too_large":
		return b.send(ctx, s, s.t("receipt_too_large"), b.keyboard(s))
	case e.Code == "unsupported_file_type" || e.Code == "pdf_unreadable" || e.Code == "pdf_not_allowed":
		return b.send(ctx, s, s.t("receipt_unreadable"), b.keyboard(s))
	case e.Code == "too_many_requests":
		return b.send(ctx, s, s.t("receipt_slow_down"), b.keyboard(s))
	case e.Code == "driver_not_registered":
		s.driver.Registered = false
		return b.registration(ctx, s)
	case e.DetailString("reason") == "user_blocked":
		return b.blocked(ctx, s)
	}
	_ = b.send(ctx, s, s.t("error"), b.keyboard(s))
	return fmt.Errorf("upload receipt: %w", err)
}

// topupKeyboard opens the top-up screen of the Mini App, or falls back to the
// reply keyboard when no Mini App URL is known.
func (b *Bot) topupKeyboard(ctx context.Context, s *session) any {
	if kb := runtime.Inline(b.appButton(ctx, s, "btn_topup", "topup")); kb != nil {
		return kb
	}
	return b.keyboard(s)
}
