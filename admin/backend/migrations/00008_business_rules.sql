-- +goose Up
-- Business rules v2 (ARCHITECTURE §22): welcome balance for new drivers, the
-- commission charged when a driver takes an order, cancellation reasons of
-- passengers, driver-bot offers that are edited once an order is taken or
-- cancelled, and the passenger push with the driver's photo and arrival time.

-- §22.1 Welcome balance: granted once per driver, phone and plate, ever.
CREATE TABLE welcome_bonus_grants (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    driver_id        uuid NOT NULL UNIQUE REFERENCES driver_profiles(id),
    phone_e164       text NOT NULL UNIQUE CHECK (phone_e164 ~ '^\+7[0-9]{10}$'),
    plate_normalized text NOT NULL UNIQUE CHECK (plate_normalized ~ '^[0-9A-Z]{5,10}$'),
    amount           bigint NOT NULL CHECK (amount > 0),
    admin_id         uuid REFERENCES admin_users(id),
    created_at       timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE wallet_ledger DROP CONSTRAINT wallet_ledger_entry_type_check;
ALTER TABLE wallet_ledger ADD CONSTRAINT wallet_ledger_entry_type_check CHECK (entry_type IN (
    'topup_credit', 'topup_refund', 'commission_reserve', 'commission_release',
    'commission_charge', 'commission_refund', 'adjustment_credit', 'adjustment_debit', 'bonus_credit'));

-- §22.2 Commission: fixed amount (default) or the tariff percentage, snapshotted
-- on the order. Orders created before this migration used the percentage.
ALTER TABLE orders ADD COLUMN commission_mode text NOT NULL DEFAULT 'percent'
    CHECK (commission_mode IN ('fixed', 'percent'));
ALTER TABLE orders ALTER COLUMN commission_mode SET DEFAULT 'fixed';

-- §22.3 Passenger cancellation reasons.
ALTER TABLE orders ADD COLUMN cancel_reason_code text
    CHECK (cancel_reason_code IN ('long_wait', 'driver_issue', 'vehicle_issue', 'changed_plans', 'other'));
CREATE INDEX orders_cancel_reason_idx ON orders (cancelled_at, cancel_reason_code)
    WHERE status = 'cancelled' AND cancel_reason_code IS NOT NULL;

-- §22.6 Driver arrival estimate shown to the passenger.
ALTER TABLE orders
    ADD COLUMN driver_eta_s  integer CHECK (driver_eta_s >= 0),
    ADD COLUMN driver_eta_at timestamptz;

-- §22.4 Outbox edits: a row of kind 'edit' rewrites the Telegram message sent
-- for ref_outbox_id (e.g. an offer that was taken or cancelled).
ALTER TABLE notification_outbox
    ADD COLUMN kind text NOT NULL DEFAULT 'send' CHECK (kind IN ('send', 'edit')),
    ADD COLUMN ref_outbox_id uuid REFERENCES notification_outbox(id) ON DELETE CASCADE,
    ADD CONSTRAINT notification_outbox_edit_ref_check CHECK ((kind = 'edit') = (ref_outbox_id IS NOT NULL));
CREATE INDEX notification_outbox_ref_idx ON notification_outbox (ref_outbox_id) WHERE ref_outbox_id IS NOT NULL;

-- §22.6 Telegram file ids of uploaded private files, cached per bot so a photo
-- is uploaded once.
ALTER TABLE files
    ADD COLUMN telegram_file_id_passenger text CHECK (char_length(telegram_file_id_passenger) <= 255),
    ADD COLUMN telegram_file_id_driver    text CHECK (char_length(telegram_file_id_driver) <= 255);

INSERT INTO app_settings (key, value) VALUES
    ('driver.welcome_bonus', '500'),
    ('commission.mode', '"fixed"'),
    ('commission.fixed_amount', '50'),
    -- §22.7: admins may set the fixed commission only within these bounds.
    ('commission.fixed_min', '50'),
    ('commission.fixed_max', '100')
ON CONFLICT (key) DO NOTHING;

-- +goose Down
DELETE FROM app_settings WHERE key IN ('driver.welcome_bonus', 'commission.mode', 'commission.fixed_amount',
    'commission.fixed_min', 'commission.fixed_max');
ALTER TABLE files DROP COLUMN IF EXISTS telegram_file_id_driver, DROP COLUMN IF EXISTS telegram_file_id_passenger;
DELETE FROM notification_outbox WHERE kind = 'edit';
DROP INDEX IF EXISTS notification_outbox_ref_idx;
ALTER TABLE notification_outbox DROP CONSTRAINT IF EXISTS notification_outbox_edit_ref_check,
    DROP COLUMN IF EXISTS ref_outbox_id, DROP COLUMN IF EXISTS kind;
ALTER TABLE orders DROP COLUMN IF EXISTS driver_eta_at, DROP COLUMN IF EXISTS driver_eta_s;
DROP INDEX IF EXISTS orders_cancel_reason_idx;
ALTER TABLE orders DROP COLUMN IF EXISTS cancel_reason_code, DROP COLUMN IF EXISTS commission_mode;
-- Irreversible once bonuses were credited: the ledger is append-only.
ALTER TABLE wallet_ledger DROP CONSTRAINT wallet_ledger_entry_type_check;
ALTER TABLE wallet_ledger ADD CONSTRAINT wallet_ledger_entry_type_check CHECK (entry_type IN (
    'topup_credit', 'topup_refund', 'commission_reserve', 'commission_release',
    'commission_charge', 'commission_refund', 'adjustment_credit', 'adjustment_debit'));
DROP TABLE IF EXISTS welcome_bonus_grants;
