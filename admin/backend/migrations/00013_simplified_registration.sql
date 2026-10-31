-- +goose Up
-- Simplified driver registration (ARCHITECTURE §11, §12): the IIN (ЖСН) is a
-- plain column. Admin views mask it; it is never copied into application
-- snapshots, audit payloads, logs, notifications or realtime events. Only
-- live profiles hold an IIN: drafts and rejected profiles never squat one.
-- No backfill: existing drivers add their IIN themselves.
ALTER TABLE driver_profiles
    ADD COLUMN iin text CONSTRAINT driver_profiles_iin_format CHECK (iin IS NULL OR iin ~ '^[0-9]{12}$'),
    ADD COLUMN patronymic text NOT NULL DEFAULT ''
        CONSTRAINT driver_profiles_patronymic_length CHECK (char_length(patronymic) <= 60);
CREATE UNIQUE INDEX driver_profiles_iin_unique ON driver_profiles (iin)
    WHERE iin IS NOT NULL AND status IN ('pending', 'approved', 'changes_requested', 'suspended');

-- +goose Down
-- Refuse to drop stored identity data; retire it with a forward migration.
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM driver_profiles WHERE iin IS NOT NULL OR patronymic <> '') THEN
        RAISE EXCEPTION 'driver IINs or patronymics are stored; keep the columns and use a forward migration';
    END IF;
END $$;
-- +goose StatementEnd
DROP INDEX driver_profiles_iin_unique;
ALTER TABLE driver_profiles DROP COLUMN patronymic, DROP COLUMN iin;
