-- +goose Up
-- Initial market: Embi (Ембі), Aktobe region. Values are defaults; admins edit them in the panel.
INSERT INTO cities (code, name_kk, name_ru, name_en, center_lat, center_lon, default_zoom, timezone,
                    dispatch_radius_m, require_driver_location, driver_location_max_age_s, search_timeout_s)
VALUES ('EMB', 'Ембі', 'Эмба', 'Embi', 48.8264, 58.1442, 14, 'Asia/Aqtobe', 50000, false, 900, 900);

INSERT INTO service_zones (city_id, name, geom)
SELECT id, 'Ембі — 15 км', ST_Multi(ST_Buffer(center, 15000)::geometry)
FROM cities WHERE code = 'EMB';

-- 500–1500 KZT in 50 KZT steps; no distance coefficients until the business approves them.
INSERT INTO tariffs (city_id, category, version, min_fare, max_fare, fare_step, base_fare, per_km, per_minute, extra_stop_fee, commission_bps)
SELECT c.id, cat, 1, 500, 1500, 50, 500, 0, 0, 0, 1000
FROM cities c CROSS JOIN unnest(ARRAY['taxi', 'parcel', 'other']) AS cat
WHERE c.code = 'EMB';

INSERT INTO app_settings (key, value) VALUES
    ('support.telegram_username', '""'),
    ('support.phone', '""'),
    ('support.hours', '{"kk": "Күн сайын 08:00–23:00", "ru": "Ежедневно 08:00–23:00", "en": "Daily 08:00–23:00"}'),
    ('routing.max_points', '5'),
    ('commission.default_bps', '1000'),
    ('driver.min_online_balance', '50'),
    ('driver.require_phone_verification', 'true'),
    ('topup.min_amount', '500'),
    ('topup.max_amount', '100000'),
    ('topup.expiry_hours', '48'),
    ('topup.presets', '[1000, 2000, 5000, 10000]'),
    ('delivery.prohibited_items', '["weapons", "explosives", "drugs", "alcohol_tobacco", "money_valuables", "animals", "hazardous", "illegal_goods"]'),
    ('delivery.blocked_keywords', '["қару", "оружие", "weapon", "патрон", "есірткі", "наркот", "drug", "взрывчат", "жарылғыш"]');

-- +goose Down
DELETE FROM app_settings;
DELETE FROM tariffs;
DELETE FROM service_zones;
DELETE FROM cities WHERE code = 'EMB';
