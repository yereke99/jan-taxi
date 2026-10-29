-- +goose Up
ALTER TABLE order_stops ADD COLUMN mode text NOT NULL DEFAULT 'geocoded'
    CHECK (mode IN ('geocoded', 'manual'));
ALTER TABLE order_stops ALTER COLUMN lat DROP NOT NULL, ALTER COLUMN lon DROP NOT NULL;
ALTER TABLE order_stops ADD CONSTRAINT order_stops_coordinate_mode CHECK (
    (mode = 'geocoded' AND lat IS NOT NULL AND lon IS NOT NULL) OR
    (mode = 'manual' AND kind = 'destination' AND lat IS NULL AND lon IS NULL AND char_length(btrim(address)) > 0)
);
ALTER TABLE fare_quotes ALTER COLUMN distance_m DROP NOT NULL, ALTER COLUMN duration_s DROP NOT NULL;
ALTER TABLE orders ALTER COLUMN distance_m DROP NOT NULL, ALTER COLUMN duration_s DROP NOT NULL;
ALTER TABLE order_route_snapshots ALTER COLUMN distance_m DROP NOT NULL, ALTER COLUMN duration_s DROP NOT NULL;
ALTER TABLE fare_quotes ADD CONSTRAINT fare_quotes_metric_pair CHECK ((distance_m IS NULL) = (duration_s IS NULL));
ALTER TABLE orders ADD CONSTRAINT orders_metric_pair CHECK ((distance_m IS NULL) = (duration_s IS NULL));
ALTER TABLE order_route_snapshots ADD CONSTRAINT order_route_snapshots_metric_pair CHECK ((distance_m IS NULL) = (duration_s IS NULL));

-- +goose Down
-- After manual writes, rollback is feature-disable plus a compatible reader
-- release. Refuse schema rollback rather than destroying or guessing data.
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM order_stops WHERE mode = 'manual')
       OR EXISTS (SELECT 1 FROM fare_quotes WHERE distance_m IS NULL OR duration_s IS NULL)
       OR EXISTS (SELECT 1 FROM orders WHERE distance_m IS NULL OR duration_s IS NULL)
       OR EXISTS (SELECT 1 FROM order_route_snapshots WHERE distance_m IS NULL OR duration_s IS NULL) THEN
        RAISE EXCEPTION 'Manual destination data exists; disable new manual bookings and retain compatible readers. Use a forward migration.';
    END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE order_stops DROP CONSTRAINT order_stops_coordinate_mode;
ALTER TABLE order_stops ALTER COLUMN lat SET NOT NULL, ALTER COLUMN lon SET NOT NULL;
ALTER TABLE order_stops DROP COLUMN mode;
ALTER TABLE fare_quotes DROP CONSTRAINT fare_quotes_metric_pair;
ALTER TABLE orders DROP CONSTRAINT orders_metric_pair;
ALTER TABLE order_route_snapshots DROP CONSTRAINT order_route_snapshots_metric_pair;
ALTER TABLE fare_quotes ALTER COLUMN distance_m SET NOT NULL, ALTER COLUMN duration_s SET NOT NULL;
ALTER TABLE orders ALTER COLUMN distance_m SET NOT NULL, ALTER COLUMN duration_s SET NOT NULL;
ALTER TABLE order_route_snapshots ALTER COLUMN distance_m SET NOT NULL, ALTER COLUMN duration_s SET NOT NULL;
