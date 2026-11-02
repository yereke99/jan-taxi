-- +goose Up
-- Marketing (ARCHITECTURE §23): campaigns with immutable versions, the
-- first-ride coupon, the passenger bonus balance, referrals, promotions
-- applied to orders with the driver's compensation, segments and broadcasts.
-- Everything is additive. The seeded launch campaigns start as drafts, so
-- riders and drivers see no change until an administrator with
-- marketing.approve switches a campaign on (and may switch it off any time).

-- Administrators: a marketing role (permissions in auth/context.go).
ALTER TABLE admin_users DROP CONSTRAINT admin_users_role_check;
ALTER TABLE admin_users ADD CONSTRAINT admin_users_role_check
    CHECK (role IN ('super_admin', 'operator', 'driver_moderator', 'finance_manager', 'auditor', 'marketing_manager'));

-- Driver wallet: the compensation of a promotion a passenger used on the
-- driver's order (and its reversal by an operator).
ALTER TABLE wallet_ledger DROP CONSTRAINT wallet_ledger_entry_type_check;
ALTER TABLE wallet_ledger ADD CONSTRAINT wallet_ledger_entry_type_check CHECK (entry_type IN (
    'topup_credit', 'topup_refund', 'commission_reserve', 'commission_release',
    'commission_charge', 'commission_refund', 'adjustment_credit', 'adjustment_debit', 'bonus_credit',
    'promo_compensation', 'promo_compensation_reversal'));

-- Orders: the promotional discount snapshotted at creation. The passenger
-- pays fare - promo_discount; fare, the commission and its basis are unchanged.
ALTER TABLE orders ADD COLUMN promo_discount integer NOT NULL DEFAULT 0
    CONSTRAINT orders_promo_discount_check CHECK (promo_discount >= 0);
ALTER TABLE orders ADD CONSTRAINT orders_promo_discount_fare_check CHECK (promo_discount <= fare);

-- Marketing messages: a person who opted out never receives broadcasts
-- (operational order messages are unaffected).
ALTER TABLE user_preferences ADD COLUMN marketing_opt_out_at timestamptz;

-- Campaigns -------------------------------------------------------------------

CREATE TABLE marketing_campaigns (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    key                text NOT NULL UNIQUE CHECK (key ~ '^[a-z0-9_]{3,40}$'),
    template           text NOT NULL CHECK (template IN ('first_ride_coupon', 'referral_first_ride', 'passenger_credit',
                                                         'driver_milestone_bonus', 'manual_grant', 'broadcast_only')),
    status             text NOT NULL DEFAULT 'draft'
                       CHECK (status IN ('draft', 'scheduled', 'active', 'paused', 'ended', 'archived')),
    is_system          boolean NOT NULL DEFAULT false,
    current_version    integer NOT NULL DEFAULT 1 CHECK (current_version > 0),
    -- Total KZT the campaign may commit (issued coupons, credited bonuses);
    -- NULL = no cap.
    budget_limit       bigint CHECK (budget_limit IS NULL OR budget_limit >= 0),
    -- Existing coupons of the campaign cannot be used while frozen (distinct
    -- from pausing new awards).
    redemptions_frozen boolean NOT NULL DEFAULT false,
    priority           integer NOT NULL DEFAULT 100 CHECK (priority BETWEEN 0 AND 1000),
    exclusivity_group  text CHECK (exclusivity_group ~ '^[a-z0-9_]{1,40}$'),
    scheduled_at       timestamptz,
    -- Start of the current active period: completions before it never earn.
    active_since       timestamptz,
    created_by         uuid REFERENCES admin_users(id),
    approved_by        uuid REFERENCES admin_users(id),
    activated_at       timestamptz,
    paused_at          timestamptz,
    ended_at           timestamptz,
    archived_at        timestamptz,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    CHECK (status <> 'active' OR active_since IS NOT NULL),
    CHECK (status <> 'scheduled' OR scheduled_at IS NOT NULL)
);
-- At most one live campaign per launch template, so a ride never earns two
-- first-ride coupons or two referral rewards.
CREATE UNIQUE INDEX marketing_campaigns_one_live_launch ON marketing_campaigns (template)
    WHERE status IN ('scheduled', 'active', 'paused') AND template IN ('first_ride_coupon', 'referral_first_ride');
CREATE INDEX marketing_campaigns_status_idx ON marketing_campaigns (status, template);
CREATE TRIGGER marketing_campaigns_touch BEFORE UPDATE ON marketing_campaigns FOR EACH ROW EXECUTE FUNCTION jt_touch_updated_at();

-- Rules and copy are versioned and immutable: coupons and grants keep the
-- version that created them.
CREATE TABLE marketing_campaign_versions (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    campaign_id uuid NOT NULL REFERENCES marketing_campaigns(id),
    version     integer NOT NULL CHECK (version > 0),
    title       jsonb NOT NULL CHECK (jsonb_typeof(title) = 'object'),
    description jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(description) = 'object'),
    rules       jsonb NOT NULL CHECK (jsonb_typeof(rules) = 'object'),
    starts_at   timestamptz,
    ends_at     timestamptz,
    change_note text CHECK (char_length(change_note) <= 300),
    created_by  uuid REFERENCES admin_users(id),
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (campaign_id, version),
    CHECK (ends_at IS NULL OR starts_at IS NULL OR ends_at > starts_at)
);
CREATE TRIGGER marketing_campaign_versions_append_only BEFORE UPDATE OR DELETE ON marketing_campaign_versions
    FOR EACH ROW EXECUTE FUNCTION jt_forbid_mutation();

-- Coupons ---------------------------------------------------------------------

CREATE TABLE marketing_coupons (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    campaign_id       uuid NOT NULL REFERENCES marketing_campaigns(id),
    version_id        uuid NOT NULL REFERENCES marketing_campaign_versions(id),
    user_id           uuid NOT NULL REFERENCES users(id),
    amount            bigint NOT NULL CHECK (amount > 0 AND amount <= 1000000),
    -- The coupon applies only to fares of at least this amount, so the cash
    -- the passenger pays never goes below zero.
    min_order_fare    bigint NOT NULL CHECK (min_order_fare >= amount),
    categories        text[] NOT NULL CHECK (cardinality(categories) BETWEEN 1 AND 3),
    status            text NOT NULL DEFAULT 'issued'
                      CHECK (status IN ('issued', 'reserved', 'redeemed', 'expired', 'voided')),
    source_order_id   uuid REFERENCES orders(id),
    issued_at         timestamptz NOT NULL DEFAULT now(),
    expires_at        timestamptz NOT NULL,
    reserved_order_id uuid REFERENCES orders(id),
    reserved_at       timestamptz,
    redeemed_order_id uuid REFERENCES orders(id),
    redeemed_at       timestamptz,
    closed_at         timestamptz,
    void_reason       text CHECK (char_length(void_reason) <= 300),
    voided_by         uuid REFERENCES admin_users(id),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    -- One coupon per passenger per campaign, ever.
    UNIQUE (campaign_id, user_id),
    CHECK (expires_at > issued_at),
    CHECK ((status = 'reserved') = (reserved_order_id IS NOT NULL)),
    CHECK ((status = 'redeemed') = (redeemed_order_id IS NOT NULL)),
    CHECK (status <> 'redeemed' OR redeemed_at IS NOT NULL),
    CHECK (status NOT IN ('expired', 'voided') OR closed_at IS NOT NULL)
);
CREATE UNIQUE INDEX marketing_coupons_reserved_order ON marketing_coupons (reserved_order_id) WHERE reserved_order_id IS NOT NULL;
CREATE UNIQUE INDEX marketing_coupons_redeemed_order ON marketing_coupons (redeemed_order_id) WHERE redeemed_order_id IS NOT NULL;
CREATE INDEX marketing_coupons_user_idx ON marketing_coupons (user_id, issued_at DESC);
CREATE INDEX marketing_coupons_campaign_idx ON marketing_coupons (campaign_id, status);
CREATE INDEX marketing_coupons_expiry_idx ON marketing_coupons (expires_at) WHERE status = 'issued';
CREATE INDEX marketing_coupons_source_order_idx ON marketing_coupons (source_order_id) WHERE source_order_id IS NOT NULL;
CREATE TRIGGER marketing_coupons_touch BEFORE UPDATE ON marketing_coupons FOR EACH ROW EXECUTE FUNCTION jt_touch_updated_at();

-- Passenger bonus balance -----------------------------------------------------
-- Separate from the driver wallets: money the platform owes a passenger as a
-- discount on later rides; never withdrawable. available = balance - reserved.

CREATE TABLE passenger_bonus_accounts (
    user_id    uuid PRIMARY KEY REFERENCES users(id),
    balance    bigint NOT NULL DEFAULT 0 CHECK (balance >= 0),
    reserved   bigint NOT NULL DEFAULT 0 CHECK (reserved >= 0),
    version    bigint NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (reserved <= balance)
);
CREATE TRIGGER passenger_bonus_accounts_touch BEFORE UPDATE ON passenger_bonus_accounts FOR EACH ROW EXECUTE FUNCTION jt_touch_updated_at();

-- Promotions applied to orders: one per order (no stacking). The discount is
-- reserved at creation, released when the order is cancelled and settled once
-- when it completes: the coupon is redeemed or the bonus spent, and the driver
-- receives the same amount as promo_compensation.
CREATE TABLE order_promotions (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id         uuid NOT NULL UNIQUE REFERENCES orders(id),
    user_id          uuid NOT NULL REFERENCES users(id),
    kind             text NOT NULL CHECK (kind IN ('coupon', 'bonus')),
    coupon_id        uuid REFERENCES marketing_coupons(id),
    campaign_id      uuid REFERENCES marketing_campaigns(id),
    version_id       uuid REFERENCES marketing_campaign_versions(id),
    gross_fare       bigint NOT NULL CHECK (gross_fare > 0),
    discount         bigint NOT NULL CHECK (discount > 0 AND discount <= gross_fare),
    status           text NOT NULL DEFAULT 'reserved' CHECK (status IN ('reserved', 'released', 'settled', 'reversed')),
    driver_id        uuid REFERENCES driver_profiles(id),
    compensation     bigint CHECK (compensation IS NULL OR compensation >= 0),
    compensation_key text UNIQUE,
    reserved_at      timestamptz NOT NULL DEFAULT now(),
    released_at      timestamptz,
    release_reason   text CHECK (char_length(release_reason) <= 200),
    settled_at       timestamptz,
    reversed_at      timestamptz,
    reversed_by      uuid REFERENCES admin_users(id),
    reverse_reason   text CHECK (char_length(reverse_reason) <= 300),
    -- Set when the order was cancelled after the promotion settled (a dispute
    -- resolved as cancelled): finance reviews it, nothing moves automatically.
    review_reason    text CHECK (char_length(review_reason) <= 100),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    CHECK ((kind = 'coupon') = (coupon_id IS NOT NULL)),
    CHECK (status <> 'released' OR released_at IS NOT NULL),
    CHECK (status NOT IN ('settled', 'reversed') OR (settled_at IS NOT NULL AND driver_id IS NOT NULL
        AND compensation = discount AND compensation_key IS NOT NULL)),
    CHECK (status <> 'reversed' OR reversed_at IS NOT NULL)
);
CREATE UNIQUE INDEX order_promotions_coupon_live ON order_promotions (coupon_id)
    WHERE coupon_id IS NOT NULL AND status IN ('reserved', 'settled');
CREATE INDEX order_promotions_user_idx ON order_promotions (user_id, reserved_at DESC);
CREATE INDEX order_promotions_status_idx ON order_promotions (status, reserved_at DESC);
CREATE INDEX order_promotions_driver_idx ON order_promotions (driver_id, settled_at DESC) WHERE driver_id IS NOT NULL;
CREATE INDEX order_promotions_review_idx ON order_promotions (reserved_at DESC) WHERE review_reason IS NOT NULL;
CREATE TRIGGER order_promotions_touch BEFORE UPDATE ON order_promotions FOR EACH ROW EXECUTE FUNCTION jt_touch_updated_at();

CREATE TABLE passenger_bonus_ledger (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id         uuid NOT NULL REFERENCES passenger_bonus_accounts(user_id),
    entry_type      text NOT NULL CHECK (entry_type IN ('grant', 'reserve', 'release', 'spend',
                                                         'adjustment_credit', 'adjustment_debit')),
    balance_delta   bigint NOT NULL,
    reserved_delta  bigint NOT NULL,
    balance_after   bigint NOT NULL CHECK (balance_after >= 0),
    reserved_after  bigint NOT NULL CHECK (reserved_after >= 0),
    campaign_id     uuid REFERENCES marketing_campaigns(id),
    version_id      uuid REFERENCES marketing_campaign_versions(id),
    order_id        uuid REFERENCES orders(id),
    application_id  uuid REFERENCES order_promotions(id),
    admin_id        uuid REFERENCES admin_users(id),
    idempotency_key text NOT NULL UNIQUE,
    description     text CHECK (char_length(description) <= 300),
    created_at      timestamptz NOT NULL DEFAULT now(),
    CHECK (balance_delta <> 0 OR reserved_delta <> 0)
);
CREATE INDEX passenger_bonus_ledger_user_idx ON passenger_bonus_ledger (user_id, id DESC);
CREATE INDEX passenger_bonus_ledger_type_idx ON passenger_bonus_ledger (entry_type, created_at DESC);
CREATE INDEX passenger_bonus_ledger_campaign_idx ON passenger_bonus_ledger (campaign_id) WHERE campaign_id IS NOT NULL;
CREATE TRIGGER passenger_bonus_ledger_append_only BEFORE UPDATE OR DELETE ON passenger_bonus_ledger
    FOR EACH ROW EXECUTE FUNCTION jt_forbid_mutation();

-- Referrals -------------------------------------------------------------------

-- A passenger's opaque, stable invitation code (no Telegram id or phone in it).
CREATE TABLE referral_codes (
    user_id     uuid PRIMARY KEY REFERENCES users(id),
    code        text NOT NULL UNIQUE CHECK (code ~ '^[A-Z2-9]{8}$'),
    created_at  timestamptz NOT NULL DEFAULT now(),
    disabled_at timestamptz,
    disabled_by uuid REFERENCES admin_users(id)
);

-- Who invited whom: bound once, on the invitee's first arrival; never rewritten.
CREATE TABLE referral_attributions (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    campaign_id         uuid NOT NULL REFERENCES marketing_campaigns(id),
    version_id          uuid NOT NULL REFERENCES marketing_campaign_versions(id),
    invitee_user_id     uuid NOT NULL UNIQUE REFERENCES users(id),
    referrer_user_id    uuid NOT NULL REFERENCES users(id),
    code                text NOT NULL,
    source              text NOT NULL CHECK (source IN ('bot_start', 'miniapp')),
    status              text NOT NULL DEFAULT 'attributed'
                        CHECK (status IN ('attributed', 'review', 'rewarded', 'capped', 'rejected')),
    reason              text CHECK (char_length(reason) <= 100),
    qualifying_order_id uuid REFERENCES orders(id),
    attributed_at       timestamptz NOT NULL DEFAULT now(),
    qualified_at        timestamptz,
    decided_at          timestamptz,
    decided_by          uuid REFERENCES admin_users(id),
    decision_note       text CHECK (char_length(decision_note) <= 300),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    CHECK (invitee_user_id <> referrer_user_id)
);
CREATE INDEX referral_attributions_referrer_idx ON referral_attributions (referrer_user_id, attributed_at DESC);
CREATE INDEX referral_attributions_status_idx ON referral_attributions (status, attributed_at DESC);
CREATE TRIGGER referral_attributions_touch BEFORE UPDATE ON referral_attributions FOR EACH ROW EXECUTE FUNCTION jt_touch_updated_at();

-- Every capture attempt, kept for fraud review (the invitation history).
CREATE TABLE referral_events (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    code             text CHECK (char_length(code) <= 64),
    invitee_user_id  uuid NOT NULL REFERENCES users(id),
    referrer_user_id uuid REFERENCES users(id),
    source           text NOT NULL CHECK (source IN ('bot_start', 'miniapp')),
    outcome          text NOT NULL CHECK (outcome IN ('attributed', 'duplicate', 'rejected')),
    reason           text CHECK (char_length(reason) <= 100),
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX referral_events_referrer_idx ON referral_events (referrer_user_id, created_at DESC) WHERE referrer_user_id IS NOT NULL;
CREATE INDEX referral_events_invitee_idx ON referral_events (invitee_user_id, created_at DESC);
CREATE TRIGGER referral_events_append_only BEFORE UPDATE OR DELETE ON referral_events
    FOR EACH ROW EXECUTE FUNCTION jt_forbid_mutation();

-- Grants: bonus awards of a campaign (referral reward, passenger credit,
-- driver milestone, manual credit).
CREATE TABLE marketing_grants (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    campaign_id uuid NOT NULL REFERENCES marketing_campaigns(id),
    version_id  uuid NOT NULL REFERENCES marketing_campaign_versions(id),
    reason      text NOT NULL CHECK (reason IN ('referral_reward', 'passenger_credit', 'driver_milestone', 'manual')),
    user_id     uuid REFERENCES users(id),
    driver_id   uuid REFERENCES driver_profiles(id),
    amount      bigint NOT NULL CHECK (amount > 0 AND amount <= 10000000),
    status      text NOT NULL DEFAULT 'credited' CHECK (status IN ('pending', 'credited', 'skipped')),
    skip_reason text CHECK (char_length(skip_reason) <= 100),
    ledger_key  text UNIQUE,
    order_id    uuid REFERENCES orders(id),
    referral_id uuid UNIQUE REFERENCES referral_attributions(id),
    admin_id    uuid REFERENCES admin_users(id),
    created_at  timestamptz NOT NULL DEFAULT now(),
    credited_at timestamptz,
    CHECK ((user_id IS NULL) <> (driver_id IS NULL)),
    CHECK ((reason = 'driver_milestone') = (driver_id IS NOT NULL)),
    CHECK (status <> 'credited' OR (credited_at IS NOT NULL AND ledger_key IS NOT NULL))
);
-- Once per beneficiary per campaign: the referral reward is capped at one per
-- referrer whatever the number of invited friends.
CREATE UNIQUE INDEX marketing_grants_once_per_passenger ON marketing_grants (campaign_id, user_id)
    WHERE reason IN ('referral_reward', 'passenger_credit');
CREATE UNIQUE INDEX marketing_grants_once_per_driver ON marketing_grants (campaign_id, driver_id)
    WHERE reason = 'driver_milestone';
CREATE INDEX marketing_grants_campaign_idx ON marketing_grants (campaign_id, status);
CREATE INDEX marketing_grants_pending_idx ON marketing_grants (created_at) WHERE status = 'pending';

-- Durable domain events consumed by the marketing worker (first-ride coupon,
-- referral reward, driver milestones). Written in the ride's completion
-- transaction; processed exactly once per (kind, order).
CREATE TABLE marketing_events (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    kind            text NOT NULL CHECK (kind IN ('ride_completed')),
    order_id        uuid NOT NULL REFERENCES orders(id),
    occurred_at     timestamptz NOT NULL,
    status          text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'done', 'failed')),
    attempts        integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    last_error      text CHECK (char_length(last_error) <= 500),
    result          jsonb,
    created_at      timestamptz NOT NULL DEFAULT now(),
    processed_at    timestamptz,
    UNIQUE (kind, order_id)
);
CREATE INDEX marketing_events_due_idx ON marketing_events (next_attempt_at) WHERE status = 'pending';
CREATE INDEX marketing_events_status_idx ON marketing_events (status, created_at DESC);

-- Segments and broadcasts -----------------------------------------------------

CREATE TABLE marketing_segments (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name        text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120),
    audience    text NOT NULL CHECK (audience IN ('passengers', 'drivers')),
    filters     jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(filters) = 'object'),
    created_by  uuid REFERENCES admin_users(id),
    updated_by  uuid REFERENCES admin_users(id),
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    archived_at timestamptz
);
CREATE TRIGGER marketing_segments_touch BEFORE UPDATE ON marketing_segments FOR EACH ROW EXECUTE FUNCTION jt_touch_updated_at();

CREATE TABLE marketing_broadcasts (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    campaign_id     uuid REFERENCES marketing_campaigns(id),
    title           text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 120),
    audience        text NOT NULL CHECK (audience IN ('passengers', 'drivers')),
    segment_id      uuid REFERENCES marketing_segments(id),
    -- Targeting in force when the recipients were snapshotted.
    filters         jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(filters) = 'object'),
    content         jsonb NOT NULL CHECK (jsonb_typeof(content) = 'object'),
    button          jsonb CHECK (button IS NULL OR jsonb_typeof(button) = 'object'),
    status          text NOT NULL DEFAULT 'draft'
                    CHECK (status IN ('draft', 'scheduled', 'sending', 'paused', 'completed', 'cancelled', 'failed')),
    scheduled_at    timestamptz,
    started_at      timestamptz,
    completed_at    timestamptz,
    cancelled_at    timestamptz,
    recipient_count integer CHECK (recipient_count IS NULL OR recipient_count >= 0),
    created_by      uuid REFERENCES admin_users(id),
    updated_by      uuid REFERENCES admin_users(id),
    approved_by     uuid REFERENCES admin_users(id),
    approval_note   text CHECK (char_length(approval_note) <= 300),
    version         integer NOT NULL DEFAULT 1,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CHECK (status <> 'scheduled' OR scheduled_at IS NOT NULL)
);
CREATE INDEX marketing_broadcasts_status_idx ON marketing_broadcasts (status, scheduled_at);
CREATE TRIGGER marketing_broadcasts_touch BEFORE UPDATE ON marketing_broadcasts FOR EACH ROW EXECUTE FUNCTION jt_touch_updated_at();

-- One delivery per recipient and broadcast (test sends are extra rows).
CREATE TABLE marketing_broadcast_recipients (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    broadcast_id        uuid NOT NULL REFERENCES marketing_broadcasts(id),
    user_id             uuid NOT NULL REFERENCES users(id),
    chat_id             bigint NOT NULL,
    language            text NOT NULL CHECK (language IN ('kk', 'ru', 'en')),
    is_test             boolean NOT NULL DEFAULT false,
    status              text NOT NULL DEFAULT 'queued'
                        CHECK (status IN ('queued', 'sending', 'sent', 'failed', 'blocked', 'suppressed', 'cancelled')),
    attempts            integer NOT NULL DEFAULT 0,
    next_attempt_at     timestamptz NOT NULL DEFAULT now(),
    locked_until        timestamptz,
    http_status         integer,
    last_error          text CHECK (char_length(last_error) <= 500),
    telegram_message_id bigint,
    created_at          timestamptz NOT NULL DEFAULT now(),
    sent_at             timestamptz,
    updated_at          timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX marketing_broadcast_recipients_once ON marketing_broadcast_recipients (broadcast_id, user_id) WHERE NOT is_test;
CREATE INDEX marketing_broadcast_recipients_due ON marketing_broadcast_recipients (next_attempt_at) WHERE status IN ('queued', 'sending');
CREATE INDEX marketing_broadcast_recipients_status_idx ON marketing_broadcast_recipients (broadcast_id, status);
CREATE TRIGGER marketing_broadcast_recipients_touch BEFORE UPDATE ON marketing_broadcast_recipients FOR EACH ROW EXECUTE FUNCTION jt_touch_updated_at();

-- Settings: the global switches are emergency stops (on by default); nothing
-- is awarded or sent until an administrator activates a campaign or sends a
-- broadcast (§23.9).
INSERT INTO app_settings (key, value) VALUES
    ('marketing.awards_enabled', 'true'),
    ('marketing.redemptions_enabled', 'true'),
    ('marketing.broadcasts_enabled', 'true'),
    ('marketing.broadcast_rate_per_s', '4'),
    ('marketing.broadcast_approval_threshold', '100')
ON CONFLICT (key) DO NOTHING;

-- The two launch campaigns from the customer's assignment (drafts: they never
-- run before an administrator activates them) and the bucket for manual
-- passenger bonus credits.
INSERT INTO marketing_campaigns (key, template, status, is_system, budget_limit, priority, exclusivity_group)
VALUES ('first_ride_coupon_400', 'first_ride_coupon', 'draft', true, 100000, 10, 'launch_2026'),
       ('referral_300_once', 'referral_first_ride', 'draft', true, 100000, 20, 'launch_2026'),
       ('manual_adjustments', 'manual_grant', 'draft', true, NULL, 900, NULL)
ON CONFLICT (key) DO NOTHING;

INSERT INTO marketing_campaign_versions (campaign_id, version, title, description, rules, change_note)
SELECT c.id, 1, v.title::jsonb, v.description::jsonb, v.rules::jsonb, 'seed'
FROM marketing_campaigns c
JOIN (VALUES
    ('first_ride_coupon_400',
     '{"kk": "Алғашқы сапар", "ru": "Первая поездка", "en": "First ride"}',
     '{"kk": "Алғашқы сапарыңыз аяқталған соң 400 ₸ купон беріледі. Купон 72 сағат жарамды, келесі сапарда қолданылады.", "ru": "После первой завершённой поездки — купон 400 ₸. Действует 72 часа, применяется к следующей поездке.", "en": "After your first completed ride you get a 400 ₸ coupon. Valid for 72 hours on your next ride."}',
     '{"amount": 400, "validity_hours": 72, "min_order_fare": 400, "categories": ["taxi"]}'),
    ('referral_300_once',
     '{"kk": "Досыңды шақыр", "ru": "Пригласи друга", "en": "Invite a friend"}',
     '{"kk": "Досыңды Jan Taxi-ге шақыр! Ол алғашқы сапарын аяқтаған соң, сенің бонус балансыңа 300 теңге түседі. Бонус тек шақырған адамға беріледі. Акция бойынша бір рет қана бонус алуға болады.", "ru": "Пригласите друга в Jan Taxi! Когда он завершит первую поездку, на ваш бонусный баланс поступит 300 ₸. Бонус получает только пригласивший, и только один раз.", "en": "Invite a friend to Jan Taxi! When they finish their first ride, 300 ₸ goes to your bonus balance. Only the inviter gets the bonus, and only once."}',
     '{"reward_amount": 300, "categories": ["taxi"], "attribution_window_minutes": 60, "unlock": "coupon_redeemed", "min_ride_seconds": 60}'),
    ('manual_adjustments',
     '{"kk": "Қолмен бонус түзету", "ru": "Ручные корректировки бонусов", "en": "Manual bonus adjustments"}',
     '{}',
     '{"max_amount": 50000}')
) AS v(key, title, description, rules) ON v.key = c.key
ON CONFLICT (campaign_id, version) DO NOTHING;

-- +goose Down
-- Refuse to drop issued coupons, bonus balances, referrals or applied
-- promotions; retire them with a forward migration.
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM marketing_coupons) OR EXISTS (SELECT 1 FROM passenger_bonus_ledger)
       OR EXISTS (SELECT 1 FROM order_promotions) OR EXISTS (SELECT 1 FROM referral_attributions)
       OR EXISTS (SELECT 1 FROM marketing_grants) OR EXISTS (SELECT 1 FROM marketing_broadcast_recipients)
       OR EXISTS (SELECT 1 FROM wallet_ledger WHERE entry_type IN ('promo_compensation', 'promo_compensation_reversal'))
       OR EXISTS (SELECT 1 FROM admin_users WHERE role = 'marketing_manager') THEN
        RAISE EXCEPTION 'marketing data exists; keep it and use a forward migration';
    END IF;
END $$;
-- +goose StatementEnd
DELETE FROM app_settings WHERE key LIKE 'marketing.%';
DROP TABLE marketing_broadcast_recipients, marketing_broadcasts, marketing_segments, marketing_events,
    marketing_grants, referral_events, referral_attributions, referral_codes, passenger_bonus_ledger,
    order_promotions, passenger_bonus_accounts, marketing_coupons, marketing_campaign_versions, marketing_campaigns;
ALTER TABLE user_preferences DROP COLUMN marketing_opt_out_at;
ALTER TABLE orders DROP CONSTRAINT orders_promo_discount_fare_check, DROP COLUMN promo_discount;
ALTER TABLE wallet_ledger DROP CONSTRAINT wallet_ledger_entry_type_check;
ALTER TABLE wallet_ledger ADD CONSTRAINT wallet_ledger_entry_type_check CHECK (entry_type IN (
    'topup_credit', 'topup_refund', 'commission_reserve', 'commission_release',
    'commission_charge', 'commission_refund', 'adjustment_credit', 'adjustment_debit', 'bonus_credit'));
ALTER TABLE admin_users DROP CONSTRAINT admin_users_role_check;
ALTER TABLE admin_users ADD CONSTRAINT admin_users_role_check
    CHECK (role IN ('super_admin', 'operator', 'driver_moderator', 'finance_manager', 'auditor'));
