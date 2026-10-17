-- +goose Up
-- Kaspi PDF receipts and automatic top-up crediting (ARCHITECTURE §6.1).

-- Files: Kaspi receipts are kept byte for byte as PDFs (top-up receipts only).
ALTER TABLE files DROP CONSTRAINT files_mime_check;
ALTER TABLE files ADD CONSTRAINT files_mime_check CHECK (mime IN ('image/jpeg', 'application/pdf'));
ALTER TABLE files DROP CONSTRAINT files_width_check;
ALTER TABLE files DROP CONSTRAINT files_height_check;
ALTER TABLE files ADD CONSTRAINT files_kind_check CHECK (
    (mime = 'image/jpeg' AND width > 0 AND height > 0)
    OR (mime = 'application/pdf' AND width = 0 AND height = 0 AND purpose = 'topup_receipt'));

-- One row per accepted PDF receipt. A receipt is used once: the same file,
-- the same receipt number ("№ чека") and the same QR payload can never be
-- attached twice; the unique indexes below are the guarantee.
CREATE TABLE payment_receipts (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    provider        text NOT NULL CHECK (provider IN ('kaspi_pdf')),
    file_id         uuid NOT NULL UNIQUE REFERENCES files(id),
    file_sha256     text NOT NULL CHECK (file_sha256 ~ '^[0-9a-f]{64}$'),
    qr_payload_hash text CHECK (qr_payload_hash ~ '^[0-9a-f]{64}$'),
    receipt_serial  text CHECK (receipt_serial ~ '^[A-Z0-9]{4,40}$'),
    amount          bigint CHECK (amount > 0),
    paid_at         timestamptz,
    recipient_name  text CHECK (char_length(recipient_name) <= 200),
    recipient_bin   text CHECK (recipient_bin ~ '^[0-9]{12}$'),
    payer_masked    text CHECK (char_length(payer_masked) <= 100),
    payment_method  text CHECK (char_length(payment_method) <= 100),
    producer        text CHECK (char_length(producer) <= 200),
    parse_status    text NOT NULL CHECK (parse_status IN ('parsed', 'partial', 'unreadable')),
    flags           jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(flags) = 'array'),
    decision        text NOT NULL CHECK (decision IN ('auto_confirmed', 'manual_review')),
    reasons         jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(reasons) = 'array'),
    driver_id       uuid NOT NULL REFERENCES driver_profiles(id),
    topup_id        uuid NOT NULL REFERENCES top_up_requests(id),
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX payment_receipts_file_sha256_key ON payment_receipts (file_sha256);
-- NULLS DISTINCT: receipts without a QR code (all Kaspi receipts today) do not collide.
CREATE UNIQUE INDEX payment_receipts_qr_payload_key ON payment_receipts (qr_payload_hash) NULLS DISTINCT;
CREATE UNIQUE INDEX payment_receipts_serial_key ON payment_receipts (provider, receipt_serial);
CREATE INDEX payment_receipts_topup_idx ON payment_receipts (topup_id, created_at DESC);
CREATE INDEX payment_receipts_driver_idx ON payment_receipts (driver_id, created_at DESC);
CREATE TRIGGER payment_receipts_append_only BEFORE UPDATE OR DELETE ON payment_receipts FOR EACH ROW EXECUTE FUNCTION jt_forbid_mutation();

-- Top-ups link their latest PDF receipt and may be confirmed by the system.
ALTER TABLE top_up_requests ADD COLUMN payment_receipt_id uuid REFERENCES payment_receipts(id);
ALTER TABLE top_up_requests ADD COLUMN confirmed_by_system boolean NOT NULL DEFAULT false;
ALTER TABLE top_up_requests DROP CONSTRAINT top_up_requests_check;
ALTER TABLE top_up_requests ADD CONSTRAINT top_up_requests_confirmed_check CHECK (
    status <> 'confirmed' OR (verified_amount IS NOT NULL AND provider_transaction_id IS NOT NULL
        AND confirmed_at IS NOT NULL AND (confirmed_by IS NOT NULL OR confirmed_by_system)));
ALTER TABLE top_up_requests ADD CONSTRAINT top_up_requests_confirmer_check CHECK (NOT (confirmed_by_system AND confirmed_by IS NOT NULL));

-- Payments credited from a receipt, and one Kaspi payment number across the
-- manual (kaspi_manual) and receipt (kaspi_pdf) paths.
ALTER TABLE payment_transactions DROP CONSTRAINT payment_transactions_source_check;
ALTER TABLE payment_transactions ADD CONSTRAINT payment_transactions_source_check
    CHECK (source IN ('manual_admin', 'webhook', 'receipt_auto'));
ALTER TABLE payment_transactions ADD COLUMN receipt_id uuid UNIQUE REFERENCES payment_receipts(id);
CREATE UNIQUE INDEX payment_transactions_kaspi_txn_key ON payment_transactions (provider_transaction_id)
    WHERE provider IN ('kaspi_manual', 'kaspi_pdf');
CREATE INDEX payment_transactions_auto_idx ON payment_transactions (created_at) WHERE source = 'receipt_auto';

-- Payee details (ИП "JAN-TAXI") and the automatic crediting policy. Values an
-- admin already set are kept.
INSERT INTO app_settings (key, value) VALUES
    ('kaspi.payment_url', '"https://pay.kaspi.kz/pay/xbp1imhd"'),
    ('kaspi.recipient_name', '"ИП \"JAN-TAXI\""'),
    ('company.bin', '"000809500289"'),
    ('company.iban', '"KZ05722S000057176693"'),
    ('company.bank', '"АО \"Kaspi Bank\""'),
    ('company.bic', '"CASPKZKA"'),
    ('company.kbe', '"19"'),
    ('company.address', '"Жалагашский район, с. Жалагаш, ул. Нығмет Мырзалиев, 94"'),
    ('topup.auto_confirm_enabled', 'true'),
    ('topup.auto_max_per_day', '3'),
    ('topup.auto_max_amount_per_day', '50000'),
    ('topup.receipt_max_age_hours', '48')
ON CONFLICT (key) DO NOTHING;

-- +goose Down
DELETE FROM app_settings WHERE key IN ('company.bin', 'company.iban', 'company.bank', 'company.bic', 'company.kbe',
    'company.address', 'topup.auto_confirm_enabled', 'topup.auto_max_per_day', 'topup.auto_max_amount_per_day',
    'topup.receipt_max_age_hours');
DROP INDEX IF EXISTS payment_transactions_auto_idx;
DROP INDEX IF EXISTS payment_transactions_kaspi_txn_key;
ALTER TABLE payment_transactions DROP COLUMN IF EXISTS receipt_id;
DELETE FROM payment_transactions WHERE source = 'receipt_auto';
ALTER TABLE payment_transactions DROP CONSTRAINT payment_transactions_source_check;
ALTER TABLE payment_transactions ADD CONSTRAINT payment_transactions_source_check CHECK (source IN ('manual_admin', 'webhook'));
ALTER TABLE top_up_requests DROP CONSTRAINT IF EXISTS top_up_requests_confirmer_check;
ALTER TABLE top_up_requests DROP CONSTRAINT IF EXISTS top_up_requests_confirmed_check;
ALTER TABLE top_up_requests ADD CONSTRAINT top_up_requests_check CHECK (
    status <> 'confirmed' OR (verified_amount IS NOT NULL AND provider_transaction_id IS NOT NULL
        AND confirmed_by IS NOT NULL AND confirmed_at IS NOT NULL)) NOT VALID;
ALTER TABLE top_up_requests DROP COLUMN IF EXISTS confirmed_by_system;
ALTER TABLE top_up_requests DROP COLUMN IF EXISTS payment_receipt_id;
DROP TABLE IF EXISTS payment_receipts;
ALTER TABLE files DROP CONSTRAINT IF EXISTS files_kind_check;
ALTER TABLE files ADD CONSTRAINT files_width_check CHECK (width > 0) NOT VALID;
ALTER TABLE files ADD CONSTRAINT files_height_check CHECK (height > 0) NOT VALID;
ALTER TABLE files DROP CONSTRAINT files_mime_check;
ALTER TABLE files ADD CONSTRAINT files_mime_check CHECK (mime IN ('image/jpeg')) NOT VALID;
