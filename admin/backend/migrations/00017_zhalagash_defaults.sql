-- +goose Up
-- Launch defaults for Жалағаш (Жалагаш), Kyzylorda region, Kazakhstan.
-- Source, checked 2026-11-04: OpenStreetMap place node 2264861871 (v26)
-- https://www.openstreetmap.org/node/2264861871
-- and settlement outline way 295767919 (v10, 2026-04-23):
-- https://www.openstreetmap.org/way/295767919
-- © OpenStreetMap contributors, ODbL https://www.openstreetmap.org/copyright
-- This is the mapped settlement footprint, not a cadastral boundary. Use its
-- polygon rather than a 5 km circle: nearby Ақсу is a separate settlement and
-- must keep its current pricing. The farthest outline vertex is about 2.63 km
-- from the place node; a broad circle would include neighbouring land.
-- Fixed IDs identify only rows owned by this migration. Existing admin-created
-- records win conflicts and are never overwritten; no existing orders change.
INSERT INTO cities (id, code, name_kk, name_ru, name_en, center_lat, center_lon,
                    default_zoom, timezone)
VALUES ('7a1a0000-0017-4000-8000-000000000001', 'ZHL', 'Жалағаш', 'Жалагаш', 'Zhalagash',
        45.0808963, 64.6809179, 14, 'Asia/Almaty')
ON CONFLICT DO NOTHING;

INSERT INTO service_zones (id, city_id, name, geom)
SELECT '7a1a0000-0017-4000-8000-000000000002', c.id, 'Жалағаш',
       ST_GeomFromText('MULTIPOLYGON(((
         64.6534359 45.0888689, 64.654026 45.0853996, 64.6531141 45.0811271,
         64.6531463 45.0794529, 64.6538114 45.0786195, 64.6549702 45.0777407,
         64.6561074 45.0766345, 64.6573734 45.0759375, 64.6577167 45.0753163,
         64.6576738 45.0736949, 64.6607852 45.0735206, 64.6599269 45.06805,
         64.6628451 45.0632912, 64.6630275 45.0608661, 64.6730053 45.0597142,
         64.6832728 45.0591231, 64.6859336 45.0641399, 64.6875214 45.0652766,
         64.6917701 45.0649735, 64.7003317 45.0677924, 64.699084 45.0844891,
         64.6965563 45.0890098, 64.6760416 45.0933605, 64.6690464 45.0917321,
         64.659441 45.0903655, 64.6590022 45.0901416, 64.6534359 45.0888689
       )))', 4326)
FROM cities c
WHERE c.id = '7a1a0000-0017-4000-8000-000000000001' AND c.code = 'ZHL'
ON CONFLICT DO NOTHING;

-- Ordinary taxi starts at 400 ₸; retain the existing default ceiling and step.
-- Other classes/categories continue using their nationwide fallback tariffs.
INSERT INTO tariffs (id, city_id, category, vehicle_class, version, min_fare,
                     max_fare, fare_step, base_fare, commission_bps)
SELECT '7a1a0000-0017-4000-8000-000000000004', c.id, 'taxi', 'sedan', 1,
       400, 1500, 50, 400, 500
FROM cities c
WHERE c.id = '7a1a0000-0017-4000-8000-000000000001' AND c.code = 'ZHL'
ON CONFLICT DO NOTHING;

-- The same footprint determines the 5% driver commission by pickup A, across
-- categories/classes. The admin map reads this active row immediately.
INSERT INTO commission_areas (id, name, commission_bps, shape, center_lat,
                              center_lon, boundary)
SELECT '7a1a0000-0017-4000-8000-000000000003', 'Жалағаш', 500, 'polygon',
       45.0808963, 64.6809179, z.geom
FROM service_zones z
WHERE z.id = '7a1a0000-0017-4000-8000-000000000002'
  AND NOT EXISTS (SELECT 1 FROM commission_areas
                  WHERE lower(name) IN ('жалағаш', 'жалагаш', 'zhalagash', 'jalagash'))
ON CONFLICT DO NOTHING;

-- +goose Down
-- A pristine, unused seed can be rolled back. Once admins change it or drivers,
-- quotes or orders use it, preserve configuration and history with a forward
-- migration instead. Never remove pre-existing records skipped by Up.
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM cities WHERE id = '7a1a0000-0017-4000-8000-000000000001'
               AND (updated_at <> created_at OR driver_seq <> 0))
       OR EXISTS (SELECT 1 FROM service_zones WHERE id = '7a1a0000-0017-4000-8000-000000000002'
                  AND updated_at <> created_at)
       OR EXISTS (SELECT 1 FROM commission_areas WHERE id = '7a1a0000-0017-4000-8000-000000000003'
                  AND updated_at <> created_at)
       OR EXISTS (SELECT 1 FROM driver_profiles WHERE city_id = '7a1a0000-0017-4000-8000-000000000001')
       OR EXISTS (SELECT 1 FROM fare_quotes WHERE city_id = '7a1a0000-0017-4000-8000-000000000001')
       OR EXISTS (SELECT 1 FROM orders WHERE city_id = '7a1a0000-0017-4000-8000-000000000001'
                  OR commission_area_id = '7a1a0000-0017-4000-8000-000000000003')
       OR EXISTS (SELECT 1 FROM places WHERE city_id = '7a1a0000-0017-4000-8000-000000000001')
       OR EXISTS (SELECT 1 FROM service_zones WHERE city_id = '7a1a0000-0017-4000-8000-000000000001'
                  AND id <> '7a1a0000-0017-4000-8000-000000000002')
       OR EXISTS (SELECT 1 FROM tariffs WHERE city_id = '7a1a0000-0017-4000-8000-000000000001'
                  AND (id <> '7a1a0000-0017-4000-8000-000000000004' OR NOT is_active)) THEN
        RAISE EXCEPTION 'Zhalagash defaults are configured or used; keep them and use a forward migration';
    END IF;
END $$;
-- +goose StatementEnd
DELETE FROM commission_areas WHERE id = '7a1a0000-0017-4000-8000-000000000003';
DELETE FROM tariffs WHERE id = '7a1a0000-0017-4000-8000-000000000004';
DELETE FROM service_zones WHERE id = '7a1a0000-0017-4000-8000-000000000002';
DELETE FROM cities WHERE id = '7a1a0000-0017-4000-8000-000000000001';
