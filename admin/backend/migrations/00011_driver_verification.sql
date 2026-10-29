-- +goose Up
-- Identity values never enter public profile DTOs, application JSON snapshots,
-- notifications or ordinary audit payloads. Ciphertext uses the operator key.
CREATE TABLE driver_identity_inputs (
    driver_id uuid PRIMARY KEY REFERENCES driver_profiles(id),
    ciphertext bytea NOT NULL,
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE driver_verification_jobs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    driver_id uuid NOT NULL REFERENCES driver_profiles(id),
    application_id uuid NOT NULL UNIQUE REFERENCES driver_applications(id),
    input_version bigint NOT NULL,
    input_ciphertext bytea NOT NULL,
    documents jsonb NOT NULL CHECK (jsonb_typeof(documents) = 'array'),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','processing','matched','admin_review','approved','superseded')),
    attempts integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    lease_token uuid,
    locked_until timestamptz,
    result_ciphertext bytea,
    confidence double precision,
    reasons jsonb NOT NULL DEFAULT '[]'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX driver_verification_due ON driver_verification_jobs(next_attempt_at,created_at) WHERE status = 'pending';
CREATE INDEX driver_verification_recovery ON driver_verification_jobs(locked_until) WHERE status = 'processing';
CREATE INDEX driver_verification_driver ON driver_verification_jobs(driver_id,created_at DESC);

-- One identity/document belongs to one driver. Historical rounds/results may
-- repeat it; renewals retain previous document claims. FIO is never unique.
CREATE TABLE driver_identity_claims (
    kind text NOT NULL CHECK (kind IN ('iin','document')),
    fingerprint bytea NOT NULL,
    driver_id uuid NOT NULL REFERENCES driver_profiles(id),
    application_id uuid NOT NULL REFERENCES driver_applications(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(kind,fingerprint)
);
CREATE INDEX driver_identity_claims_owner ON driver_identity_claims(driver_id);
ALTER TABLE driver_profiles ADD COLUMN suspended_previous_status text
    CHECK (suspended_previous_status IS NULL OR suspended_previous_status IN ('draft','pending','approved','changes_requested','rejected'));

-- Identity PDFs have their own stricter two-page ingestion limit in code.
ALTER TABLE files DROP CONSTRAINT files_kind_check;
ALTER TABLE files ADD CONSTRAINT files_kind_check CHECK (
    (mime = 'image/jpeg' AND width > 0 AND height > 0)
    OR (mime = 'application/pdf' AND width = 0 AND height = 0 AND purpose IN ('topup_receipt','id_front','id_back')));

-- +goose Down
-- Refuse a destructive rollback after identity data or decisions exist.
-- Roll back application flags/binaries while retaining the additive schema.
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM driver_identity_inputs) OR EXISTS (SELECT 1 FROM driver_verification_jobs)
       OR EXISTS (SELECT 1 FROM driver_identity_claims)
       OR EXISTS (SELECT 1 FROM files WHERE mime='application/pdf' AND purpose IN ('id_front','id_back')) THEN
        RAISE EXCEPTION 'verification data exists; disable feature and retain schema';
    END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE files DROP CONSTRAINT files_kind_check;
ALTER TABLE files ADD CONSTRAINT files_kind_check CHECK (
    (mime = 'image/jpeg' AND width > 0 AND height > 0)
    OR (mime = 'application/pdf' AND width = 0 AND height = 0 AND purpose='topup_receipt'));
ALTER TABLE driver_profiles DROP COLUMN suspended_previous_status;
DROP TABLE driver_identity_claims;
DROP TABLE driver_verification_jobs;
DROP TABLE driver_identity_inputs;
