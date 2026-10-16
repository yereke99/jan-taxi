package drivers

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/phone"
)

// ApplyVerifiedPhone records a phone number the driver bot verified through a
// Telegram contact (contact.user_id == from.id). It stores the number on the
// user and copies it to the driver profile while the phone is editable (draft,
// or changes_requested with the phone flagged). A number held by another live
// driver is refused with 409 phone_taken. Call it inside a transaction so that
// a refusal leaves nothing half-written; it publishes no events.
func ApplyVerifiedPhone(ctx context.Context, q db.Querier, userID uuid.UUID, phoneE164 string) error {
	num, err := phone.Normalize(phoneE164)
	if err != nil {
		return httpx.Validation(map[string]string{"phone": "invalid"})
	}
	var taken bool
	if err := q.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM driver_profiles
			WHERE phone_e164 = $1 AND user_id <> $2 AND status IN ('pending', 'approved', 'changes_requested', 'suspended'))`,
		num, userID).Scan(&taken); err != nil {
		return fmt.Errorf("drivers: phone check: %w", err)
	}
	if taken {
		return ErrPhoneTaken
	}
	tag, err := q.Exec(ctx, `UPDATE users SET phone_e164 = $2, phone_verified_at = now() WHERE id = $1`, userID, num)
	if err != nil {
		return fmt.Errorf("drivers: store phone: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	_, err = q.Exec(ctx, `
		UPDATE driver_profiles dp SET phone_e164 = $2, phone_verified_at = now()
		WHERE dp.user_id = $1 AND (
			dp.status = 'draft' OR (
				dp.status = 'changes_requested' AND EXISTS (
					SELECT 1 FROM driver_applications a
					WHERE a.driver_id = dp.id AND a.status = 'changes_requested'
					  AND a.round = (SELECT max(round) FROM driver_applications WHERE driver_id = dp.id)
					  AND a.corrections @> '[{"field": "phone"}]')))`, userID, num)
	if db.IsUniqueViolation(err, PhoneUniqueIndex) {
		return ErrPhoneTaken
	}
	if err != nil {
		return fmt.Errorf("drivers: copy phone to profile: %w", err)
	}
	return nil
}
