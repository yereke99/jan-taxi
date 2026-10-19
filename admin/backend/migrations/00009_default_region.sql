-- +goose Up
-- Nationwide default region (ARCHITECTURE §3): the tariff region "KAZ" covers
-- all of Kazakhstan with the base tariffs. cities.Resolve picks the smallest
-- active zone covering the pickup, so a configured city (e.g. Embi) wins
-- inside its own zone and KAZ prices every other place in the country. Orders
-- keep the real settlement of pickup A in orders.locality.

ALTER TABLE cities ADD COLUMN is_default_region boolean NOT NULL DEFAULT false;
-- At most one default region.
CREATE UNIQUE INDEX cities_one_default_region ON cities (is_default_region) WHERE is_default_region;

INSERT INTO cities (code, name_kk, name_ru, name_en, center_lat, center_lon, default_zoom, timezone,
                    is_active, is_default_region, search_timeout_s)
SELECT 'KAZ', 'Қазақстан', 'Казахстан', 'Kazakhstan', 51.1694, 71.4491, 5, 'Asia/Almaty', true, true,
       COALESCE((SELECT search_timeout_s FROM cities WHERE code = 'EMB'), 900)
ON CONFLICT (code) DO UPDATE SET is_active = true, is_default_region = true;

-- Its single service zone is the Kazakhstan service-area boundary.
INSERT INTO service_zones (city_id, name, geom)
SELECT c.id, 'Қазақстан', ST_Multi(ST_CollectionExtract(ST_MakeValid(b.geom_check), 3))
FROM cities c
JOIN country_boundaries b ON b.code = 'KZ'
WHERE c.code = 'KAZ'
  AND NOT EXISTS (SELECT 1 FROM service_zones z WHERE z.city_id = c.id);

-- The base tariffs: a copy of every active Embi tariff (all categories and
-- vehicle classes).
INSERT INTO tariffs (city_id, category, vehicle_class, version, is_active, min_fare, max_fare, fare_step,
                     base_fare, per_km, per_minute, extra_stop_fee, extra_fees, commission_bps)
SELECT k.id, t.category, t.vehicle_class, 1, true, t.min_fare, t.max_fare, t.fare_step,
       t.base_fare, t.per_km, t.per_minute, t.extra_stop_fee, t.extra_fees, t.commission_bps
FROM tariffs t
JOIN cities e ON e.id = t.city_id AND e.code = 'EMB'
CROSS JOIN cities k
WHERE k.code = 'KAZ' AND t.is_active
ON CONFLICT DO NOTHING;

-- Real settlement of pickup A from reverse geocoding (city, town or village,
-- else the district); NULL when the geocoder had no answer. Never coordinates.
ALTER TABLE fare_quotes ADD COLUMN locality text CHECK (char_length(locality) BETWEEN 1 AND 120);
ALTER TABLE orders ADD COLUMN locality text CHECK (char_length(locality) BETWEEN 1 AND 120);

-- +goose Down
-- Irreversible once orders, quotes or drivers reference the KAZ region.
ALTER TABLE orders DROP COLUMN IF EXISTS locality;
ALTER TABLE fare_quotes DROP COLUMN IF EXISTS locality;
DELETE FROM service_zones WHERE city_id IN (SELECT id FROM cities WHERE code = 'KAZ');
DELETE FROM tariffs WHERE city_id IN (SELECT id FROM cities WHERE code = 'KAZ');
DELETE FROM cities WHERE code = 'KAZ';
DROP INDEX IF EXISTS cities_one_default_region;
ALTER TABLE cities DROP COLUMN IF EXISTS is_default_region;
