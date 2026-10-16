-- +goose Up
-- +goose StatementBegin
CREATE EXTENSION IF NOT EXISTS postgis;
CREATE EXTENSION IF NOT EXISTS citext;

-- Shared trigger functions ---------------------------------------------------

CREATE FUNCTION jt_touch_updated_at() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    NEW.updated_at := now();
    RETURN NEW;
END;
$$;

CREATE FUNCTION jt_forbid_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'table % is append-only', TG_TABLE_NAME USING ERRCODE = 'restrict_violation';
END;
$$;
-- +goose StatementEnd

-- Users ----------------------------------------------------------------------

CREATE TABLE users (
    id                        uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    telegram_user_id          bigint NOT NULL UNIQUE CHECK (telegram_user_id > 0),
    first_name                text NOT NULL DEFAULT '',
    last_name                 text NOT NULL DEFAULT '',
    username                  text,
    telegram_language_code    text,
    phone_e164                text CHECK (phone_e164 ~ '^\+7[0-9]{10}$'),
    phone_verified_at         timestamptz,
    passenger_bot_started_at  timestamptz,
    passenger_bot_blocked_at  timestamptz,
    driver_bot_started_at     timestamptz,
    driver_bot_blocked_at     timestamptz,
    is_blocked                boolean NOT NULL DEFAULT false,
    blocked_reason            text,
    last_seen_at              timestamptz,
    created_at                timestamptz NOT NULL DEFAULT now(),
    updated_at                timestamptz NOT NULL DEFAULT now()
);
CREATE TRIGGER users_touch BEFORE UPDATE ON users FOR EACH ROW EXECUTE FUNCTION jt_touch_updated_at();

CREATE TABLE user_preferences (
    user_id            uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    language           text NOT NULL DEFAULT 'kk' CHECK (language IN ('kk', 'ru', 'en')),
    language_chosen_at timestamptz,
    updated_at         timestamptz NOT NULL DEFAULT now()
);
CREATE TRIGGER user_preferences_touch BEFORE UPDATE ON user_preferences FOR EACH ROW EXECUTE FUNCTION jt_touch_updated_at();

-- Admins ---------------------------------------------------------------------

CREATE TABLE admin_users (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email               citext NOT NULL UNIQUE,
    full_name           text NOT NULL DEFAULT '',
    password_hash       text NOT NULL,
    role                text NOT NULL CHECK (role IN ('super_admin', 'operator', 'driver_moderator', 'finance_manager', 'auditor')),
    is_active           boolean NOT NULL DEFAULT true,
    failed_login_count  integer NOT NULL DEFAULT 0,
    locked_until        timestamptz,
    last_login_at       timestamptz,
    password_changed_at timestamptz NOT NULL DEFAULT now(),
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);
CREATE TRIGGER admin_users_touch BEFORE UPDATE ON admin_users FOR EACH ROW EXECUTE FUNCTION jt_touch_updated_at();

CREATE TABLE admin_sessions (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    admin_id     uuid NOT NULL REFERENCES admin_users(id) ON DELETE CASCADE,
    token_hash   bytea NOT NULL UNIQUE,
    ip           inet,
    user_agent   text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    revoked_at   timestamptz
);
CREATE INDEX admin_sessions_admin_idx ON admin_sessions (admin_id);

CREATE TABLE admin_audit_logs (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    admin_id    uuid REFERENCES admin_users(id),
    action      text NOT NULL,
    entity_type text NOT NULL,
    entity_id   text,
    before      jsonb,
    after       jsonb,
    ip          inet,
    user_agent  text,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX admin_audit_logs_entity_idx ON admin_audit_logs (entity_type, entity_id, created_at DESC);
CREATE INDEX admin_audit_logs_admin_idx ON admin_audit_logs (admin_id, created_at DESC);
CREATE INDEX admin_audit_logs_created_idx ON admin_audit_logs (created_at DESC);
CREATE TRIGGER admin_audit_logs_append_only BEFORE UPDATE OR DELETE ON admin_audit_logs FOR EACH ROW EXECUTE FUNCTION jt_forbid_mutation();

-- Geography ------------------------------------------------------------------

CREATE TABLE country_boundaries (
    code       text PRIMARY KEY CHECK (code ~ '^[A-Z]{2}$'),
    name       text NOT NULL,
    geom       geometry(MultiPolygon, 4326) NOT NULL,
    -- geom buffered by a few km: Natural Earth 1:10m is generalised, so border towns
    -- (e.g. Saryagash) would otherwise fall outside. Service-area checks use this column.
    geom_check geometry(MultiPolygon, 4326) NOT NULL,
    source     text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX country_boundaries_geom_check_idx ON country_boundaries USING gist (geom_check);

CREATE TABLE cities (
    id                        uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code                      text NOT NULL UNIQUE CHECK (code ~ '^[A-Z]{3}$'),
    name_kk                   text NOT NULL,
    name_ru                   text NOT NULL,
    name_en                   text NOT NULL,
    center_lat                double precision NOT NULL CHECK (center_lat BETWEEN -90 AND 90),
    center_lon                double precision NOT NULL CHECK (center_lon BETWEEN -180 AND 180),
    center                    geography(Point, 4326) GENERATED ALWAYS AS (ST_SetSRID(ST_MakePoint(center_lon, center_lat), 4326)::geography) STORED,
    default_zoom              numeric(4, 2) NOT NULL DEFAULT 13,
    timezone                  text NOT NULL DEFAULT 'Asia/Aqtobe',
    is_active                 boolean NOT NULL DEFAULT true,
    dispatch_radius_m         integer NOT NULL DEFAULT 50000 CHECK (dispatch_radius_m BETWEEN 500 AND 1000000),
    require_driver_location   boolean NOT NULL DEFAULT false,
    driver_location_max_age_s integer NOT NULL DEFAULT 900 CHECK (driver_location_max_age_s BETWEEN 30 AND 86400),
    search_timeout_s          integer NOT NULL DEFAULT 900 CHECK (search_timeout_s BETWEEN 60 AND 86400),
    driver_seq                bigint NOT NULL DEFAULT 0 CHECK (driver_seq >= 0),
    created_at                timestamptz NOT NULL DEFAULT now(),
    updated_at                timestamptz NOT NULL DEFAULT now()
);
CREATE TRIGGER cities_touch BEFORE UPDATE ON cities FOR EACH ROW EXECUTE FUNCTION jt_touch_updated_at();

CREATE TABLE service_zones (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    city_id    uuid NOT NULL REFERENCES cities(id) ON DELETE CASCADE,
    name       text NOT NULL,
    geom       geometry(MultiPolygon, 4326) NOT NULL CHECK (ST_IsValid(geom)),
    is_active  boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX service_zones_geom_idx ON service_zones USING gist (geom);
CREATE INDEX service_zones_city_idx ON service_zones (city_id);
CREATE TRIGGER service_zones_touch BEFORE UPDATE ON service_zones FOR EACH ROW EXECUTE FUNCTION jt_touch_updated_at();

-- Admin-curated named places (bus station, akimat, bazaar...). Searched before the
-- external geocoder because OSM address coverage in small towns is incomplete.
CREATE TABLE places (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    city_id    uuid REFERENCES cities(id) ON DELETE CASCADE,
    name_kk    text NOT NULL CHECK (char_length(name_kk) BETWEEN 1 AND 120),
    name_ru    text NOT NULL CHECK (char_length(name_ru) BETWEEN 1 AND 120),
    name_en    text NOT NULL CHECK (char_length(name_en) BETWEEN 1 AND 120),
    aliases    text[] NOT NULL DEFAULT '{}',
    lat        double precision NOT NULL CHECK (lat BETWEEN -90 AND 90),
    lon        double precision NOT NULL CHECK (lon BETWEEN -180 AND 180),
    is_active  boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX places_city_idx ON places (city_id) WHERE is_active;
CREATE TRIGGER places_touch BEFORE UPDATE ON places FOR EACH ROW EXECUTE FUNCTION jt_touch_updated_at();

-- Runtime-editable settings (non-secret) ------------------------------------

CREATE TABLE app_settings (
    key        text PRIMARY KEY CHECK (key ~ '^[a-z0-9_.]+$'),
    value      jsonb NOT NULL,
    updated_by uuid REFERENCES admin_users(id),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Tariffs and quotes ---------------------------------------------------------

CREATE TABLE tariffs (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    city_id        uuid NOT NULL REFERENCES cities(id),
    category       text NOT NULL CHECK (category IN ('taxi', 'parcel', 'other')),
    version        integer NOT NULL CHECK (version > 0),
    is_active      boolean NOT NULL DEFAULT true,
    min_fare       integer NOT NULL CHECK (min_fare > 0),
    max_fare       integer NOT NULL,
    fare_step      integer NOT NULL CHECK (fare_step > 0),
    base_fare      integer NOT NULL DEFAULT 0 CHECK (base_fare >= 0),
    per_km         integer NOT NULL DEFAULT 0 CHECK (per_km >= 0),
    per_minute     integer NOT NULL DEFAULT 0 CHECK (per_minute >= 0),
    extra_stop_fee integer NOT NULL DEFAULT 0 CHECK (extra_stop_fee >= 0),
    extra_fees     jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(extra_fees) = 'array'),
    commission_bps integer NOT NULL DEFAULT 1000 CHECK (commission_bps BETWEEN 0 AND 5000),
    created_by     uuid REFERENCES admin_users(id),
    created_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (city_id, category, version),
    CHECK (max_fare >= min_fare),
    CHECK (min_fare % fare_step = 0 AND max_fare % fare_step = 0)
);
CREATE UNIQUE INDEX tariffs_one_active ON tariffs (city_id, category) WHERE is_active;

CREATE TABLE fare_quotes (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    city_id          uuid NOT NULL REFERENCES cities(id),
    tariff_id        uuid NOT NULL REFERENCES tariffs(id),
    category         text NOT NULL CHECK (category IN ('taxi', 'parcel', 'other')),
    stops            jsonb NOT NULL,
    stops_hash       text NOT NULL,
    distance_m       integer NOT NULL CHECK (distance_m >= 0),
    duration_s       integer NOT NULL CHECK (duration_s >= 0),
    legs             jsonb NOT NULL,
    geometry         jsonb NOT NULL,
    bbox             jsonb NOT NULL,
    recommended_fare integer NOT NULL CHECK (recommended_fare > 0),
    min_fare         integer NOT NULL CHECK (min_fare > 0),
    max_fare         integer NOT NULL,
    fare_step        integer NOT NULL CHECK (fare_step > 0),
    commission_bps   integer NOT NULL CHECK (commission_bps BETWEEN 0 AND 5000),
    expires_at       timestamptz NOT NULL,
    used_at          timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    CHECK (max_fare >= min_fare AND recommended_fare BETWEEN min_fare AND max_fare)
);
CREATE INDEX fare_quotes_user_idx ON fare_quotes (user_id, created_at DESC);
CREATE INDEX fare_quotes_expires_idx ON fare_quotes (expires_at) WHERE used_at IS NULL;

-- Private files ----------------------------------------------------------------

CREATE TABLE files (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_user_id uuid REFERENCES users(id) ON DELETE SET NULL,
    purpose       text NOT NULL CHECK (purpose IN ('driver_avatar', 'id_front', 'id_back', 'vehicle_photo', 'topup_receipt')),
    storage_key   text NOT NULL UNIQUE,
    mime          text NOT NULL CHECK (mime IN ('image/jpeg')),
    size_bytes    integer NOT NULL CHECK (size_bytes > 0),
    sha256        text NOT NULL,
    width         integer NOT NULL CHECK (width > 0),
    height        integer NOT NULL CHECK (height > 0),
    created_at    timestamptz NOT NULL DEFAULT now(),
    deleted_at    timestamptz
);
CREATE INDEX files_owner_idx ON files (owner_user_id, created_at DESC);

-- Drivers ----------------------------------------------------------------------

CREATE TABLE driver_profiles (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id            uuid NOT NULL UNIQUE REFERENCES users(id),
    code               text NOT NULL UNIQUE CHECK (code ~ '^[A-Z]{3}[0-9]{3,}$'),
    city_id            uuid NOT NULL REFERENCES cities(id),
    first_name         text NOT NULL DEFAULT '' CHECK (char_length(first_name) <= 60),
    last_name          text NOT NULL DEFAULT '' CHECK (char_length(last_name) <= 60),
    phone_e164         text CHECK (phone_e164 ~ '^\+7[0-9]{10}$'),
    phone_verified_at  timestamptz,
    avatar_file_id     uuid REFERENCES files(id),
    status             text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'pending', 'approved', 'changes_requested', 'rejected', 'suspended')),
    is_online          boolean NOT NULL DEFAULT false,
    online_since       timestamptz,
    last_heartbeat_at  timestamptz,
    submitted_at       timestamptz,
    approved_at        timestamptz,
    suspended_at       timestamptz,
    suspended_reason   text,
    rating_sum         integer NOT NULL DEFAULT 0 CHECK (rating_sum >= 0),
    rating_count       integer NOT NULL DEFAULT 0 CHECK (rating_count >= 0),
    completed_trips    integer NOT NULL DEFAULT 0 CHECK (completed_trips >= 0),
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    CHECK (NOT is_online OR status = 'approved')
);
-- One phone per live driver account; draft and rejected profiles do not hold the number.
CREATE UNIQUE INDEX driver_profiles_phone_unique ON driver_profiles (phone_e164)
    WHERE phone_e164 IS NOT NULL AND status IN ('pending', 'approved', 'changes_requested', 'suspended');
CREATE INDEX driver_profiles_status_idx ON driver_profiles (status, city_id);
CREATE INDEX driver_profiles_online_idx ON driver_profiles (city_id) WHERE is_online;
CREATE TRIGGER driver_profiles_touch BEFORE UPDATE ON driver_profiles FOR EACH ROW EXECUTE FUNCTION jt_touch_updated_at();

CREATE TABLE driver_documents (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    driver_id  uuid NOT NULL REFERENCES driver_profiles(id) ON DELETE CASCADE,
    kind       text NOT NULL CHECK (kind IN ('id_front', 'id_back')),
    file_id    uuid NOT NULL REFERENCES files(id),
    is_current boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX driver_documents_current ON driver_documents (driver_id, kind) WHERE is_current;

CREATE TABLE vehicles (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    driver_id        uuid NOT NULL REFERENCES driver_profiles(id) ON DELETE CASCADE,
    make             text NOT NULL DEFAULT '' CHECK (char_length(make) <= 40),
    model            text NOT NULL DEFAULT '' CHECK (char_length(model) <= 40),
    color            text NOT NULL DEFAULT '' CHECK (char_length(color) <= 30),
    plate_raw        text NOT NULL DEFAULT '' CHECK (char_length(plate_raw) <= 20),
    plate_normalized text CHECK (plate_normalized ~ '^[0-9A-Z]{5,10}$'),
    -- Mirrors the moderation state so the partial unique index can protect plates of
    -- pending/approved/changes_requested/suspended drivers.
    status           text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'claimed', 'released', 'archived')),
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX vehicles_plate_unique ON vehicles (plate_normalized) WHERE status = 'claimed';
CREATE UNIQUE INDEX vehicles_one_current_per_driver ON vehicles (driver_id) WHERE status <> 'archived';
CREATE TRIGGER vehicles_touch BEFORE UPDATE ON vehicles FOR EACH ROW EXECUTE FUNCTION jt_touch_updated_at();

CREATE TABLE vehicle_photos (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    vehicle_id uuid NOT NULL REFERENCES vehicles(id) ON DELETE CASCADE,
    kind       text NOT NULL CHECK (kind IN ('main', 'back', 'side', 'plate', 'extra')),
    file_id    uuid NOT NULL REFERENCES files(id),
    is_current boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX vehicle_photos_current_single ON vehicle_photos (vehicle_id, kind) WHERE is_current AND kind <> 'extra';
CREATE INDEX vehicle_photos_vehicle_idx ON vehicle_photos (vehicle_id) WHERE is_current;

CREATE TABLE driver_applications (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    driver_id        uuid NOT NULL REFERENCES driver_profiles(id) ON DELETE CASCADE,
    round            integer NOT NULL CHECK (round > 0),
    status           text NOT NULL CHECK (status IN ('pending', 'approved', 'changes_requested', 'rejected')),
    snapshot         jsonb NOT NULL,
    corrections      jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(corrections) = 'array'),
    decision_comment text,
    submitted_at     timestamptz NOT NULL DEFAULT now(),
    reviewed_at      timestamptz,
    reviewed_by      uuid REFERENCES admin_users(id),
    UNIQUE (driver_id, round)
);
CREATE UNIQUE INDEX driver_applications_one_pending ON driver_applications (driver_id) WHERE status = 'pending';
CREATE INDEX driver_applications_status_idx ON driver_applications (status, submitted_at);

CREATE TABLE driver_moderation_events (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    driver_id      uuid NOT NULL REFERENCES driver_profiles(id) ON DELETE CASCADE,
    application_id uuid REFERENCES driver_applications(id),
    actor_type     text NOT NULL CHECK (actor_type IN ('driver', 'admin', 'system')),
    admin_id       uuid REFERENCES admin_users(id),
    action         text NOT NULL CHECK (action IN ('submitted', 'resubmitted', 'approved', 'changes_requested', 'rejected', 'suspended', 'restored', 'reopened', 'city_changed')),
    from_status    text,
    to_status      text,
    reasons        jsonb NOT NULL DEFAULT '[]'::jsonb,
    comment        text,
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX driver_moderation_events_driver_idx ON driver_moderation_events (driver_id, created_at DESC);
CREATE TRIGGER driver_moderation_events_append_only BEFORE UPDATE OR DELETE ON driver_moderation_events FOR EACH ROW EXECUTE FUNCTION jt_forbid_mutation();

CREATE TABLE driver_locations (
    driver_id   uuid PRIMARY KEY REFERENCES driver_profiles(id) ON DELETE CASCADE,
    lat         double precision NOT NULL CHECK (lat BETWEEN -90 AND 90),
    lon         double precision NOT NULL CHECK (lon BETWEEN -180 AND 180),
    location    geography(Point, 4326) GENERATED ALWAYS AS (ST_SetSRID(ST_MakePoint(lon, lat), 4326)::geography) STORED,
    accuracy_m  real CHECK (accuracy_m IS NULL OR accuracy_m >= 0),
    heading     real,
    speed_mps   real,
    source      text NOT NULL CHECK (source IN ('miniapp', 'bot_live', 'bot_static')),
    recorded_at timestamptz NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX driver_locations_geo_idx ON driver_locations USING gist (location);

CREATE TABLE driver_location_history (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    driver_id   uuid NOT NULL REFERENCES driver_profiles(id) ON DELETE CASCADE,
    order_id    uuid,
    lat         double precision NOT NULL,
    lon         double precision NOT NULL,
    accuracy_m  real,
    source      text NOT NULL,
    recorded_at timestamptz NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX driver_location_history_driver_idx ON driver_location_history (driver_id, recorded_at DESC);
CREATE INDEX driver_location_history_received_idx ON driver_location_history (received_at);

-- Orders -------------------------------------------------------------------------

CREATE SEQUENCE order_number_seq START WITH 1001;

CREATE TABLE orders (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    number            bigint NOT NULL UNIQUE DEFAULT nextval('order_number_seq'),
    passenger_id      uuid NOT NULL REFERENCES users(id),
    city_id           uuid NOT NULL REFERENCES cities(id),
    category          text NOT NULL CHECK (category IN ('taxi', 'parcel', 'other')),
    service_type      text NOT NULL CHECK (service_type IN ('passenger', 'delivery')),
    cargo_type        text NOT NULL CHECK (cargo_type IN ('none', 'parcel', 'other')),
    cargo_description text CHECK (char_length(cargo_description) BETWEEN 1 AND 120),
    recipient_phone   text CHECK (recipient_phone ~ '^\+7[0-9]{10}$'),
    prohibited_items_ack boolean NOT NULL DEFAULT false,
    comment           text CHECK (char_length(comment) <= 120),
    status            text NOT NULL CHECK (status IN ('draft', 'searching', 'accepted', 'driver_arriving', 'in_progress', 'completed', 'cancelled', 'disputed')),
    driver_id         uuid REFERENCES driver_profiles(id),
    vehicle_id        uuid REFERENCES vehicles(id),
    fare              integer NOT NULL CHECK (fare > 0),
    currency          text NOT NULL DEFAULT 'KZT' CHECK (currency = 'KZT'),
    commission_bps    integer NOT NULL CHECK (commission_bps BETWEEN 0 AND 5000),
    commission_amount integer NOT NULL CHECK (commission_amount >= 0 AND commission_amount <= fare),
    payment_method    text NOT NULL CHECK (payment_method IN ('cash', 'bank_transfer')),
    tariff_id         uuid NOT NULL REFERENCES tariffs(id),
    quote_id          uuid REFERENCES fare_quotes(id),
    distance_m        integer NOT NULL CHECK (distance_m >= 0),
    duration_s        integer NOT NULL CHECK (duration_s >= 0),
    stop_count        smallint NOT NULL CHECK (stop_count BETWEEN 2 AND 5),
    pickup_lat        double precision NOT NULL,
    pickup_lon        double precision NOT NULL,
    pickup            geography(Point, 4326) GENERATED ALWAYS AS (ST_SetSRID(ST_MakePoint(pickup_lon, pickup_lat), 4326)::geography) STORED,
    idempotency_key   text NOT NULL CHECK (char_length(idempotency_key) BETWEEN 8 AND 64),
    cancel_reason     text CHECK (char_length(cancel_reason) <= 200),
    cancelled_by      text CHECK (cancelled_by IN ('passenger', 'driver', 'admin', 'system')),
    search_expires_at timestamptz,
    accepted_at       timestamptz,
    arrived_at        timestamptz,
    started_at        timestamptz,
    completed_at      timestamptz,
    cancelled_at      timestamptz,
    version           integer NOT NULL DEFAULT 1,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (passenger_id, idempotency_key),
    CHECK (
        (category = 'taxi'   AND service_type = 'passenger' AND cargo_type = 'none') OR
        (category = 'parcel' AND service_type = 'delivery'  AND cargo_type = 'parcel'
            AND cargo_description IS NOT NULL AND recipient_phone IS NOT NULL AND prohibited_items_ack) OR
        (category = 'other'  AND service_type = 'delivery'  AND cargo_type = 'other'
            AND cargo_description IS NOT NULL AND prohibited_items_ack)
    ),
    CHECK (status IN ('draft', 'searching') OR status = 'cancelled' OR driver_id IS NOT NULL),
    CHECK (status <> 'cancelled' OR (cancelled_at IS NOT NULL AND cancelled_by IS NOT NULL))
);
-- A driver can own at most one active order; enforced by the database, not only by code.
CREATE UNIQUE INDEX orders_one_active_per_driver ON orders (driver_id)
    WHERE status IN ('accepted', 'driver_arriving', 'in_progress');
-- A passenger can have one active ride and one active delivery at a time.
CREATE UNIQUE INDEX orders_one_active_per_passenger_service ON orders (passenger_id, service_type)
    WHERE status IN ('searching', 'accepted', 'driver_arriving', 'in_progress');
CREATE INDEX orders_status_city_idx ON orders (status, city_id, created_at DESC);
CREATE INDEX orders_passenger_idx ON orders (passenger_id, created_at DESC);
CREATE INDEX orders_driver_idx ON orders (driver_id, created_at DESC);
CREATE INDEX orders_created_idx ON orders (created_at DESC);
CREATE INDEX orders_pickup_idx ON orders USING gist (pickup);
CREATE INDEX orders_search_expiry_idx ON orders (search_expires_at) WHERE status = 'searching';
CREATE TRIGGER orders_touch BEFORE UPDATE ON orders FOR EACH ROW EXECUTE FUNCTION jt_touch_updated_at();

CREATE TABLE order_stops (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id        uuid NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    seq             smallint NOT NULL CHECK (seq BETWEEN 0 AND 4),
    kind            text NOT NULL CHECK (kind IN ('pickup', 'intermediate', 'destination')),
    lat             double precision NOT NULL CHECK (lat BETWEEN -90 AND 90),
    lon             double precision NOT NULL CHECK (lon BETWEEN -180 AND 180),
    location        geography(Point, 4326) GENERATED ALWAYS AS (ST_SetSRID(ST_MakePoint(lon, lat), 4326)::geography) STORED,
    address         text NOT NULL DEFAULT '' CHECK (char_length(address) <= 200),
    address_details text CHECK (char_length(address_details) <= 120),
    completed_at    timestamptz,
    UNIQUE (order_id, seq),
    CHECK ((seq = 0) = (kind = 'pickup'))
);

CREATE TABLE order_route_snapshots (
    order_id    uuid PRIMARY KEY REFERENCES orders(id) ON DELETE CASCADE,
    provider    text NOT NULL,
    stops       jsonb NOT NULL,
    stops_hash  text NOT NULL,
    geometry    jsonb NOT NULL,
    legs        jsonb NOT NULL,
    bbox        jsonb NOT NULL,
    distance_m  integer NOT NULL,
    duration_s  integer NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE TRIGGER order_route_snapshots_immutable BEFORE UPDATE ON order_route_snapshots FOR EACH ROW EXECUTE FUNCTION jt_forbid_mutation();

CREATE TABLE order_offers (
    id                   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    order_id             uuid NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    driver_id            uuid NOT NULL REFERENCES driver_profiles(id) ON DELETE CASCADE,
    status               text NOT NULL DEFAULT 'sent' CHECK (status IN ('sent', 'accepted', 'lost', 'expired', 'cancelled')),
    distance_to_pickup_m integer,
    sent_at              timestamptz NOT NULL DEFAULT now(),
    closed_at            timestamptz,
    UNIQUE (order_id, driver_id)
);
CREATE INDEX order_offers_driver_idx ON order_offers (driver_id, sent_at DESC);

CREATE TABLE order_status_events (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    order_id    uuid NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    from_status text,
    to_status   text NOT NULL,
    actor_type  text NOT NULL CHECK (actor_type IN ('passenger', 'driver', 'admin', 'system')),
    actor_id    uuid,
    reason      text,
    meta        jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX order_status_events_order_idx ON order_status_events (order_id, id);
CREATE TRIGGER order_status_events_append_only BEFORE UPDATE OR DELETE ON order_status_events FOR EACH ROW EXECUTE FUNCTION jt_forbid_mutation();

CREATE TABLE order_ratings (
    order_id     uuid PRIMARY KEY REFERENCES orders(id) ON DELETE CASCADE,
    passenger_id uuid NOT NULL REFERENCES users(id),
    driver_id    uuid NOT NULL REFERENCES driver_profiles(id),
    rating       smallint NOT NULL CHECK (rating BETWEEN 1 AND 5),
    comment      text CHECK (char_length(comment) <= 120),
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE disputes (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id        uuid NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    opened_by_type  text NOT NULL CHECK (opened_by_type IN ('passenger', 'driver', 'admin')),
    opened_by_id    uuid,
    reason          text NOT NULL CHECK (char_length(reason) BETWEEN 1 AND 500),
    previous_status text NOT NULL,
    status          text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'resolved')),
    outcome         text CHECK (outcome IN ('completed', 'cancelled')),
    resolution      text CHECK (char_length(resolution) <= 1000),
    resolved_by     uuid REFERENCES admin_users(id),
    created_at      timestamptz NOT NULL DEFAULT now(),
    resolved_at     timestamptz
);
CREATE UNIQUE INDEX disputes_one_open_per_order ON disputes (order_id) WHERE status = 'open';
CREATE INDEX disputes_status_idx ON disputes (status, created_at DESC);

-- Wallets and ledger ---------------------------------------------------------

CREATE TABLE wallets (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    driver_id  uuid NOT NULL UNIQUE REFERENCES driver_profiles(id),
    currency   text NOT NULL DEFAULT 'KZT' CHECK (currency = 'KZT'),
    balance    bigint NOT NULL DEFAULT 0 CHECK (balance >= 0),
    reserved   bigint NOT NULL DEFAULT 0 CHECK (reserved >= 0),
    version    bigint NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (reserved <= balance)
);
CREATE TRIGGER wallets_touch BEFORE UPDATE ON wallets FOR EACH ROW EXECUTE FUNCTION jt_touch_updated_at();

CREATE TABLE wallet_reservations (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    wallet_id  uuid NOT NULL REFERENCES wallets(id),
    order_id   uuid NOT NULL REFERENCES orders(id),
    amount     bigint NOT NULL CHECK (amount >= 0),
    status     text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'settled', 'released')),
    created_at timestamptz NOT NULL DEFAULT now(),
    closed_at  timestamptz,
    CHECK ((status = 'active') = (closed_at IS NULL))
);
CREATE UNIQUE INDEX wallet_reservations_one_active_per_order ON wallet_reservations (order_id) WHERE status = 'active';
CREATE INDEX wallet_reservations_wallet_idx ON wallet_reservations (wallet_id, created_at DESC);

CREATE TABLE wallet_ledger (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    wallet_id       uuid NOT NULL REFERENCES wallets(id),
    entry_type      text NOT NULL CHECK (entry_type IN (
                        'topup_credit', 'topup_refund', 'commission_reserve', 'commission_release',
                        'commission_charge', 'commission_refund', 'adjustment_credit', 'adjustment_debit')),
    balance_delta   bigint NOT NULL,
    reserved_delta  bigint NOT NULL,
    balance_after   bigint NOT NULL CHECK (balance_after >= 0),
    reserved_after  bigint NOT NULL CHECK (reserved_after >= 0),
    order_id        uuid REFERENCES orders(id),
    topup_id        uuid,
    reservation_id  uuid REFERENCES wallet_reservations(id),
    admin_id        uuid REFERENCES admin_users(id),
    idempotency_key text NOT NULL UNIQUE,
    description     text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CHECK (balance_delta <> 0 OR reserved_delta <> 0)
);
CREATE INDEX wallet_ledger_wallet_idx ON wallet_ledger (wallet_id, id DESC);
CREATE INDEX wallet_ledger_type_idx ON wallet_ledger (entry_type, created_at DESC);
CREATE INDEX wallet_ledger_order_idx ON wallet_ledger (order_id) WHERE order_id IS NOT NULL;
CREATE TRIGGER wallet_ledger_append_only BEFORE UPDATE OR DELETE ON wallet_ledger FOR EACH ROW EXECUTE FUNCTION jt_forbid_mutation();

-- Top-ups and payments -------------------------------------------------------

CREATE SEQUENCE topup_number_seq START WITH 1;

CREATE TABLE top_up_requests (
    id                       uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    number                   bigint NOT NULL UNIQUE DEFAULT nextval('topup_number_seq'),
    reference                text NOT NULL UNIQUE,
    driver_id                uuid NOT NULL REFERENCES driver_profiles(id),
    wallet_id                uuid NOT NULL REFERENCES wallets(id),
    requested_amount         bigint NOT NULL CHECK (requested_amount > 0),
    currency                 text NOT NULL DEFAULT 'KZT' CHECK (currency = 'KZT'),
    provider                 text NOT NULL DEFAULT 'kaspi_manual',
    status                   text NOT NULL DEFAULT 'created' CHECK (status IN ('created', 'pending_verification', 'confirmed', 'rejected', 'expired', 'refunded')),
    payment_url              text,
    driver_payment_reference text CHECK (char_length(driver_payment_reference) <= 100),
    driver_note              text CHECK (char_length(driver_note) <= 300),
    receipt_file_id          uuid REFERENCES files(id),
    driver_reported_paid_at  timestamptz,
    verified_amount          bigint CHECK (verified_amount > 0),
    provider_transaction_id  text,
    paid_at                  timestamptz,
    confirmed_by             uuid REFERENCES admin_users(id),
    confirmed_at             timestamptz,
    rejected_by              uuid REFERENCES admin_users(id),
    rejected_at              timestamptz,
    reject_reason            text CHECK (char_length(reject_reason) <= 300),
    refunded_by              uuid REFERENCES admin_users(id),
    refunded_at              timestamptz,
    refund_reason            text CHECK (char_length(refund_reason) <= 300),
    idempotency_key          text NOT NULL CHECK (char_length(idempotency_key) BETWEEN 8 AND 64),
    expires_at               timestamptz NOT NULL,
    created_at               timestamptz NOT NULL DEFAULT now(),
    updated_at               timestamptz NOT NULL DEFAULT now(),
    UNIQUE (driver_id, idempotency_key),
    CHECK (status <> 'confirmed' OR (verified_amount IS NOT NULL AND provider_transaction_id IS NOT NULL AND confirmed_by IS NOT NULL AND confirmed_at IS NOT NULL))
);
CREATE INDEX top_up_requests_status_idx ON top_up_requests (status, created_at DESC);
CREATE INDEX top_up_requests_driver_idx ON top_up_requests (driver_id, created_at DESC);
CREATE TRIGGER top_up_requests_touch BEFORE UPDATE ON top_up_requests FOR EACH ROW EXECUTE FUNCTION jt_touch_updated_at();

ALTER TABLE wallet_ledger ADD CONSTRAINT wallet_ledger_topup_fk FOREIGN KEY (topup_id) REFERENCES top_up_requests(id);

CREATE TABLE top_up_events (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    topup_id    uuid NOT NULL REFERENCES top_up_requests(id) ON DELETE CASCADE,
    from_status text,
    to_status   text NOT NULL,
    actor_type  text NOT NULL CHECK (actor_type IN ('driver', 'admin', 'system', 'provider')),
    actor_id    uuid,
    note        text,
    meta        jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX top_up_events_topup_idx ON top_up_events (topup_id, id);
CREATE TRIGGER top_up_events_append_only BEFORE UPDATE OR DELETE ON top_up_events FOR EACH ROW EXECUTE FUNCTION jt_forbid_mutation();

CREATE TABLE payment_transactions (
    id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    provider                text NOT NULL,
    provider_transaction_id text NOT NULL CHECK (char_length(provider_transaction_id) BETWEEN 1 AND 100),
    amount                  bigint NOT NULL CHECK (amount > 0),
    currency                text NOT NULL DEFAULT 'KZT' CHECK (currency = 'KZT'),
    paid_at                 timestamptz,
    payer_info              text CHECK (char_length(payer_info) <= 200),
    source                  text NOT NULL CHECK (source IN ('manual_admin', 'webhook')),
    raw                     jsonb,
    topup_id                uuid UNIQUE REFERENCES top_up_requests(id),
    recorded_by             uuid REFERENCES admin_users(id),
    created_at              timestamptz NOT NULL DEFAULT now(),
    -- One real payment can never credit two top-ups.
    UNIQUE (provider, provider_transaction_id)
);

-- Notifications (transactional outbox) --------------------------------------

CREATE TABLE notification_outbox (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    bot                 text NOT NULL CHECK (bot IN ('passenger', 'driver')),
    user_id             uuid REFERENCES users(id) ON DELETE CASCADE,
    chat_id             bigint NOT NULL,
    template            text NOT NULL,
    params              jsonb NOT NULL DEFAULT '{}'::jsonb,
    dedupe_key          text NOT NULL UNIQUE,
    status              text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sending', 'sent', 'dead', 'cancelled')),
    attempts            integer NOT NULL DEFAULT 0,
    max_attempts        integer NOT NULL DEFAULT 8,
    next_attempt_at     timestamptz NOT NULL DEFAULT now(),
    locked_until        timestamptz,
    locked_by           text,
    last_error          text,
    telegram_message_id bigint,
    created_at          timestamptz NOT NULL DEFAULT now(),
    sent_at             timestamptz
);
CREATE INDEX notification_outbox_due_idx ON notification_outbox (next_attempt_at) WHERE status IN ('pending', 'sending');
CREATE INDEX notification_outbox_status_idx ON notification_outbox (status, created_at DESC);

CREATE TABLE notification_attempts (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    outbox_id     uuid NOT NULL REFERENCES notification_outbox(id) ON DELETE CASCADE,
    attempt       integer NOT NULL,
    started_at    timestamptz NOT NULL,
    finished_at   timestamptz NOT NULL DEFAULT now(),
    outcome       text NOT NULL CHECK (outcome IN ('sent', 'retry', 'fatal')),
    http_status   integer,
    error         text,
    retry_after_s integer
);
CREATE INDEX notification_attempts_outbox_idx ON notification_attempts (outbox_id, attempt);

CREATE TABLE notification_templates (
    key        text NOT NULL,
    language   text NOT NULL CHECK (language IN ('kk', 'ru', 'en')),
    body       text NOT NULL CHECK (char_length(body) BETWEEN 1 AND 3500),
    updated_by uuid REFERENCES admin_users(id),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (key, language)
);

-- Telegram bot runtime state ------------------------------------------------

CREATE TABLE bot_states (
    bot              text PRIMARY KEY CHECK (bot IN ('passenger', 'driver')),
    last_update_id   bigint NOT NULL DEFAULT 0,
    lease_holder     text,
    lease_expires_at timestamptz,
    last_poll_at     timestamptz,
    updated_at       timestamptz NOT NULL DEFAULT now()
);
INSERT INTO bot_states (bot) VALUES ('passenger'), ('driver');

CREATE TABLE bot_processed_updates (
    bot          text NOT NULL CHECK (bot IN ('passenger', 'driver')),
    update_id    bigint NOT NULL,
    processed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (bot, update_id)
);
CREATE INDEX bot_processed_updates_age_idx ON bot_processed_updates (processed_at);

-- +goose Down
DROP TABLE IF EXISTS bot_processed_updates, bot_states, notification_templates, notification_attempts,
    notification_outbox, payment_transactions, top_up_events, wallet_ledger, top_up_requests,
    wallet_reservations, wallets, disputes, order_ratings, order_status_events, order_offers,
    order_route_snapshots, order_stops, orders, driver_location_history, driver_locations,
    driver_moderation_events, driver_applications, vehicle_photos, vehicles, driver_documents,
    driver_profiles, files, fare_quotes, tariffs, app_settings, places, service_zones, cities,
    country_boundaries, admin_audit_logs, admin_sessions, admin_users, user_preferences, users CASCADE;
DROP SEQUENCE IF EXISTS order_number_seq, topup_number_seq;
DROP FUNCTION IF EXISTS jt_forbid_mutation(), jt_touch_updated_at();
