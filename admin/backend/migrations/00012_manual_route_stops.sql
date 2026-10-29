-- +goose Up
ALTER TABLE order_stops DROP CONSTRAINT order_stops_coordinate_mode;
ALTER TABLE order_stops ADD CONSTRAINT order_stops_coordinate_mode CHECK (
    (mode = 'geocoded' AND lat IS NOT NULL AND lon IS NOT NULL) OR
    (mode = 'manual' AND seq > 0 AND kind IN ('intermediate', 'destination')
     AND lat IS NULL AND lon IS NULL AND char_length(btrim(address)) > 0)
);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM order_stops WHERE mode = 'manual' AND kind = 'intermediate') THEN
        RAISE EXCEPTION 'Manual intermediate stops exist; retain compatible readers and use a forward migration';
    END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE order_stops DROP CONSTRAINT order_stops_coordinate_mode;
ALTER TABLE order_stops ADD CONSTRAINT order_stops_coordinate_mode CHECK (
    (mode = 'geocoded' AND lat IS NOT NULL AND lon IS NOT NULL) OR
    (mode = 'manual' AND kind = 'destination' AND lat IS NULL AND lon IS NULL AND char_length(btrim(address)) > 0)
);
