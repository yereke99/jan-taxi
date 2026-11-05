-- +goose Up
-- Existing campaigns keep all regions and are not announced retroactively.
ALTER TABLE marketing_campaign_versions ADD COLUMN city_ids uuid[] NOT NULL DEFAULT '{}';
ALTER TABLE marketing_broadcasts ADD COLUMN is_automatic boolean NOT NULL DEFAULT false;
CREATE UNIQUE INDEX marketing_broadcast_campaign_announcement
    ON marketing_broadcasts (campaign_id) WHERE is_automatic;
CREATE TABLE marketing_app_presence (
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    app text NOT NULL CHECK (app IN ('passenger', 'driver')),
    active boolean NOT NULL,
    seen_at timestamptz NOT NULL DEFAULT now(),
    city_id uuid REFERENCES cities(id),
    city_seen_at timestamptz,
    PRIMARY KEY (user_id, app)
);
CREATE INDEX marketing_app_presence_seen ON marketing_app_presence (seen_at);

-- Latest observed service region; a driver's registration city is a fallback.
-- Physical service zones are independent of the tariff fallback (KAZ).
-- +goose StatementBegin
CREATE FUNCTION jt_marketing_region(p_lat double precision,p_lon double precision) RETURNS uuid
LANGUAGE sql STABLE AS $$
 SELECT c.id FROM cities c JOIN service_zones z ON z.city_id=c.id AND z.is_active
 WHERE c.is_active AND c.code<>'KAZ' AND ST_Covers(z.geom,ST_SetSRID(ST_MakePoint(p_lon,p_lat),4326))
 ORDER BY ST_Area(z.geom),c.id LIMIT 1;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION jt_marketing_city(uid uuid, app_name text) RETURNS uuid
LANGUAGE sql STABLE AS $$
 SELECT city_id FROM (
  SELECT city_id,city_seen_at AS observed_at FROM marketing_app_presence
   WHERE user_id=uid AND app=app_name AND city_seen_at IS NOT NULL
  UNION ALL
  (SELECT jt_marketing_region(o.pickup_lat,o.pickup_lon),o.created_at FROM orders o LEFT JOIN driver_profiles d ON d.id=o.driver_id
   WHERE ((app_name='passenger' AND o.passenger_id=uid) OR (app_name='driver' AND d.user_id=uid))
    AND o.status NOT IN ('draft','cancelled') ORDER BY o.created_at DESC LIMIT 1)
  UNION ALL
  SELECT jt_marketing_region(l.lat,l.lon),l.recorded_at FROM driver_locations l JOIN driver_profiles d ON d.id=l.driver_id
   WHERE app_name='driver' AND d.user_id=uid
  UNION ALL
  SELECT city_id,'1970-01-01'::timestamptz FROM driver_profiles WHERE app_name='driver' AND user_id=uid
 ) x ORDER BY observed_at DESC NULLS LAST LIMIT 1;
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION jt_marketing_city(uuid,text);
DROP FUNCTION jt_marketing_region(double precision,double precision);
DROP TABLE marketing_app_presence;
DROP INDEX marketing_broadcast_campaign_announcement;
ALTER TABLE marketing_broadcasts DROP COLUMN is_automatic;
ALTER TABLE marketing_campaign_versions DROP COLUMN city_ids;
