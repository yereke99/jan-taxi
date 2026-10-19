-- +goose Up
-- Vehicle classes (body types), ARCHITECTURE §21: drivers declare the body type
-- of their vehicle, passengers choose the class they need, dispatch only offers
-- an order to vehicles whose class serves it and whose seats and cargo space fit.

CREATE TABLE vehicle_classes (
    code                   text PRIMARY KEY CHECK (code ~ '^[a-z_]{2,20}$'),
    name_kk                text NOT NULL CHECK (char_length(name_kk) BETWEEN 1 AND 40),
    name_ru                text NOT NULL CHECK (char_length(name_ru) BETWEEN 1 AND 40),
    name_en                text NOT NULL CHECK (char_length(name_en) BETWEEN 1 AND 40),
    examples               text NOT NULL DEFAULT '' CHECK (char_length(examples) <= 80),
    seats_default          smallint NOT NULL,
    seats_max              smallint NOT NULL CHECK (seats_max BETWEEN 1 AND 60),
    cargo_volume_l_default integer NOT NULL CHECK (cargo_volume_l_default BETWEEN 0 AND 100000),
    passenger_capable      boolean NOT NULL,
    cargo_capable          boolean NOT NULL,
    -- Class codes whose orders vehicles of this class may take; always includes the class itself.
    serves                 text[] NOT NULL CHECK (code = ANY (serves) AND cardinality(serves) <= 10),
    icon                   text NOT NULL CHECK (icon IN ('sedan', 'universal', 'minivan', 'microbus', 'cargo_van', 'truck')),
    sort                   integer NOT NULL DEFAULT 0 CHECK (sort BETWEEN 0 AND 1000),
    is_active              boolean NOT NULL DEFAULT true,
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now(),
    CHECK (seats_default BETWEEN 1 AND seats_max),
    CHECK (passenger_capable OR cargo_capable)
);
CREATE TRIGGER vehicle_classes_touch BEFORE UPDATE ON vehicle_classes FOR EACH ROW EXECUTE FUNCTION jt_touch_updated_at();

INSERT INTO vehicle_classes (code, name_kk, name_ru, name_en, examples, seats_default, seats_max,
                             cargo_volume_l_default, passenger_capable, cargo_capable, serves, icon, sort) VALUES
    ('sedan',     'Седан',         'Седан',            'Sedan',     'Toyota Camry, Hyundai Sonata',    4,  4,   400,  true,  true, ARRAY['sedan'],              'sedan',     10),
    ('universal', 'Универсал',     'Универсал',        'Estate',    'Lada Largus, Skoda Octavia Combi', 4,  4,   600,  true,  true, ARRAY['universal', 'sedan'], 'universal', 20),
    ('minivan',   'Минивэн',       'Минивэн',          'Minivan',   'Toyota Alphard, Toyota Sienna',   7,  8,   800,  true,  true, ARRAY['minivan'],            'minivan',   30),
    ('microbus',  'Микроавтобус',  'Микроавтобус',     'Minibus',   'Mercedes Sprinter, Toyota HiAce', 15, 20,  1500, true,  true, ARRAY['microbus'],           'microbus',  40),
    ('cargo_van', 'Жүк фургоны',   'Грузовой фургон',  'Cargo van', 'GAZelle, Ford Transit',           2,  2,   6000, false, true, ARRAY['cargo_van'],          'cargo_van', 50),
    ('truck',     'Жүк көлігі',    'Грузовик',         'Truck',     'Isuzu NQR, Hyundai HD78',         2,  2,   15000, false, true, ARRAY['truck'],             'truck',     60);

-- Vehicles: the class, seats and cargo space are moderated registration fields
-- (correction field vehicle_class), required at submission. Existing vehicles
-- become sedans with 4 seats and 400 L.
ALTER TABLE vehicles
    ADD COLUMN class_code     text DEFAULT 'sedan' REFERENCES vehicle_classes(code),
    ADD COLUMN seats          smallint DEFAULT 4 CHECK (seats BETWEEN 1 AND 60),
    ADD COLUMN cargo_volume_l integer DEFAULT 400 CHECK (cargo_volume_l BETWEEN 0 AND 100000);
ALTER TABLE vehicles
    ALTER COLUMN class_code DROP DEFAULT,
    ALTER COLUMN seats DROP DEFAULT,
    ALTER COLUMN cargo_volume_l DROP DEFAULT,
    ADD CONSTRAINT vehicles_class_fields_check CHECK (
        (class_code IS NULL AND seats IS NULL AND cargo_volume_l IS NULL) OR
        (class_code IS NOT NULL AND seats IS NOT NULL AND cargo_volume_l IS NOT NULL));

-- Tariffs: one active version per (city, category, vehicle class); versions
-- count per class. Existing tariffs become sedan tariffs.
ALTER TABLE tariffs ADD COLUMN vehicle_class text NOT NULL DEFAULT 'sedan' REFERENCES vehicle_classes(code);
ALTER TABLE tariffs ALTER COLUMN vehicle_class DROP DEFAULT;
ALTER TABLE tariffs DROP CONSTRAINT tariffs_city_id_category_version_key;
ALTER TABLE tariffs ADD CONSTRAINT tariffs_city_category_class_version_key UNIQUE (city_id, category, vehicle_class, version);
DROP INDEX tariffs_one_active;
CREATE UNIQUE INDEX tariffs_one_active ON tariffs (city_id, category, vehicle_class) WHERE is_active;

-- Default tariffs of the other classes wherever an active sedan tariff exists:
-- 50 KZT step, 10% commission, taxi only for passenger classes.
INSERT INTO tariffs (city_id, category, vehicle_class, version, is_active, min_fare, max_fare, fare_step,
                     base_fare, per_km, per_minute, extra_stop_fee, commission_bps)
SELECT t.city_id, t.category, d.code, 1, true, d.min_fare, d.max_fare, 50, d.min_fare, 0, 0, 0, 1000
FROM tariffs t
JOIN (VALUES ('universal', 600, 1800), ('minivan', 1000, 3000), ('microbus', 2000, 6000),
             ('cargo_van', 2000, 8000), ('truck', 5000, 20000)) AS d(code, min_fare, max_fare) ON true
JOIN vehicle_classes vc ON vc.code = d.code
WHERE t.vehicle_class = 'sedan' AND t.is_active
  AND (CASE WHEN t.category = 'taxi' THEN vc.passenger_capable ELSE vc.cargo_capable END);

-- Quotes and orders: the requested class, passenger count (taxi, 1..15) and
-- cargo size (parcel/other: s|m|l|xl with its derived volume in litres).
ALTER TABLE fare_quotes
    ADD COLUMN vehicle_class   text NOT NULL DEFAULT 'sedan' REFERENCES vehicle_classes(code),
    ADD COLUMN passenger_count smallint CHECK (passenger_count BETWEEN 1 AND 15),
    ADD COLUMN cargo_size      text CHECK (cargo_size IN ('s', 'm', 'l', 'xl')),
    ADD COLUMN cargo_volume_l  integer NOT NULL DEFAULT 0 CHECK (cargo_volume_l >= 0);
UPDATE fare_quotes SET passenger_count = 1 WHERE category = 'taxi';
UPDATE fare_quotes SET cargo_size = 's', cargo_volume_l = 10 WHERE category <> 'taxi';
ALTER TABLE fare_quotes
    ALTER COLUMN vehicle_class DROP DEFAULT,
    ADD CONSTRAINT fare_quotes_load_check CHECK (
        (category = 'taxi' AND passenger_count IS NOT NULL AND cargo_size IS NULL AND cargo_volume_l = 0) OR
        (category <> 'taxi' AND passenger_count IS NULL AND cargo_size IS NOT NULL AND cargo_volume_l > 0));

ALTER TABLE orders
    ADD COLUMN vehicle_class   text NOT NULL DEFAULT 'sedan' REFERENCES vehicle_classes(code),
    ADD COLUMN passenger_count smallint CHECK (passenger_count BETWEEN 1 AND 15),
    ADD COLUMN cargo_size      text CHECK (cargo_size IN ('s', 'm', 'l', 'xl')),
    ADD COLUMN cargo_volume_l  integer NOT NULL DEFAULT 0 CHECK (cargo_volume_l >= 0);
-- Backfill without touching updated_at.
ALTER TABLE orders DISABLE TRIGGER orders_touch;
UPDATE orders SET passenger_count = 1 WHERE category = 'taxi';
UPDATE orders SET cargo_size = 's', cargo_volume_l = 10 WHERE category <> 'taxi';
ALTER TABLE orders ENABLE TRIGGER orders_touch;
ALTER TABLE orders
    ALTER COLUMN vehicle_class DROP DEFAULT,
    ADD CONSTRAINT orders_load_check CHECK (
        (category = 'taxi' AND passenger_count IS NOT NULL AND cargo_size IS NULL AND cargo_volume_l = 0) OR
        (category <> 'taxi' AND passenger_count IS NULL AND cargo_size IS NOT NULL AND cargo_volume_l > 0));

-- +goose Down
-- Irreversible once orders or quotes reference a non-sedan tariff or class.
ALTER TABLE orders DROP CONSTRAINT IF EXISTS orders_load_check;
ALTER TABLE orders DROP COLUMN IF EXISTS cargo_volume_l, DROP COLUMN IF EXISTS cargo_size,
    DROP COLUMN IF EXISTS passenger_count, DROP COLUMN IF EXISTS vehicle_class;
ALTER TABLE fare_quotes DROP CONSTRAINT IF EXISTS fare_quotes_load_check;
ALTER TABLE fare_quotes DROP COLUMN IF EXISTS cargo_volume_l, DROP COLUMN IF EXISTS cargo_size,
    DROP COLUMN IF EXISTS passenger_count, DROP COLUMN IF EXISTS vehicle_class;
DELETE FROM fare_quotes f WHERE EXISTS (SELECT 1 FROM tariffs t WHERE t.id = f.tariff_id AND t.vehicle_class <> 'sedan');
DELETE FROM tariffs WHERE vehicle_class <> 'sedan';
DROP INDEX IF EXISTS tariffs_one_active;
CREATE UNIQUE INDEX tariffs_one_active ON tariffs (city_id, category) WHERE is_active;
ALTER TABLE tariffs DROP CONSTRAINT IF EXISTS tariffs_city_category_class_version_key;
ALTER TABLE tariffs ADD CONSTRAINT tariffs_city_id_category_version_key UNIQUE (city_id, category, version);
ALTER TABLE tariffs DROP COLUMN IF EXISTS vehicle_class;
ALTER TABLE vehicles DROP CONSTRAINT IF EXISTS vehicles_class_fields_check;
ALTER TABLE vehicles DROP COLUMN IF EXISTS cargo_volume_l, DROP COLUMN IF EXISTS seats, DROP COLUMN IF EXISTS class_code;
DROP TABLE IF EXISTS vehicle_classes;
