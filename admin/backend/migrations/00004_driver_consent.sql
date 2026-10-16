-- +goose Up
-- Personal-data processing consent (Law 94-V Art. 7–8): when the driver accepted and
-- which version of the consent text (the text itself ships with the Mini App).
ALTER TABLE driver_profiles
    ADD COLUMN pd_consent_at timestamptz,
    ADD COLUMN pd_consent_version text,
    ADD CONSTRAINT driver_profiles_pd_consent_check CHECK (
        (pd_consent_at IS NULL AND pd_consent_version IS NULL) OR
        (pd_consent_at IS NOT NULL AND char_length(pd_consent_version) BETWEEN 1 AND 32)
    );

-- +goose Down
ALTER TABLE driver_profiles
    DROP CONSTRAINT IF EXISTS driver_profiles_pd_consent_check,
    DROP COLUMN IF EXISTS pd_consent_version,
    DROP COLUMN IF EXISTS pd_consent_at;
