-- +goose Up
-- A point whose address is the passenger's own words while the point keeps
-- its coordinates: a typed pickup A (ARCHITECTURE §3). Drivers, admins and the
-- passenger see it marked as written by the passenger. Manual stops are always
-- the passenger's words without coordinates and keep false. Existing rows
-- become false; no backfill.
ALTER TABLE order_stops ADD COLUMN address_typed boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE order_stops DROP COLUMN address_typed;
