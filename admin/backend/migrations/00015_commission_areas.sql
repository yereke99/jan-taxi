-- +goose Up
-- Villages with their own driver commission (ARCHITECTURE §22.9): a percentage
-- of the fare, snapshotted on an order whose pickup A lies inside the village
-- area and charged when a driver takes it. An area is a circle around the
-- village centre or a drawn boundary; the centre also decides overlaps (the
-- nearest village wins). Outside every area the general commission applies.
CREATE TABLE commission_areas (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name           text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120),
    commission_bps integer NOT NULL CHECK (commission_bps BETWEEN 0 AND 5000),
    shape          text NOT NULL CHECK (shape IN ('circle', 'polygon')),
    center_lat     double precision NOT NULL CHECK (center_lat BETWEEN -90 AND 90),
    center_lon     double precision NOT NULL CHECK (center_lon BETWEEN -180 AND 180),
    center         geography(Point, 4326) GENERATED ALWAYS AS (ST_SetSRID(ST_MakePoint(center_lon, center_lat), 4326)::geography) STORED,
    radius_m       integer CHECK (radius_m BETWEEN 200 AND 50000),
    boundary       geometry(MultiPolygon, 4326) CHECK (boundary IS NULL OR ST_IsValid(boundary)),
    is_active      boolean NOT NULL DEFAULT true,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    CHECK ((shape = 'circle' AND radius_m IS NOT NULL AND boundary IS NULL)
        OR (shape = 'polygon' AND boundary IS NOT NULL AND radius_m IS NULL))
);
CREATE UNIQUE INDEX commission_areas_name_unique ON commission_areas (lower(name));
CREATE INDEX commission_areas_center_idx ON commission_areas USING gist (center) WHERE is_active;
CREATE INDEX commission_areas_boundary_idx ON commission_areas USING gist (boundary) WHERE is_active;
CREATE TRIGGER commission_areas_touch BEFORE UPDATE ON commission_areas FOR EACH ROW EXECUTE FUNCTION jt_touch_updated_at();

-- The village whose commission an order got, with its name at that moment
-- (history keeps it after the area is renamed or deleted).
ALTER TABLE orders
    ADD COLUMN commission_area_id uuid REFERENCES commission_areas(id) ON DELETE SET NULL,
    ADD COLUMN commission_area_name text CHECK (char_length(commission_area_name) BETWEEN 1 AND 120);

-- +goose Down
-- Refuse to drop configured villages or the commission history of orders;
-- retire them with a forward migration.
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM commission_areas) OR EXISTS (SELECT 1 FROM orders WHERE commission_area_name IS NOT NULL) THEN
        RAISE EXCEPTION 'village commissions are configured or used by orders; keep them and use a forward migration';
    END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE orders DROP COLUMN commission_area_name, DROP COLUMN commission_area_id;
DROP TABLE commission_areas;
