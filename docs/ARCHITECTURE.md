# Jan Taxi — Architecture and Engineering Contract

This document is the single source of truth for how Jan Taxi is built. Code that
contradicts it is a bug in either the code or this document; fix one of them.

## 1. System overview

| Process | Source | Port(s) | Talks to |
|---|---|---|---|
| `api` — central Go REST + WebSocket API, notification worker, background jobs | `admin/backend/cmd/api` | `8080` public (behind Caddy), `8081` internal (bots only, never published) | PostgreSQL/PostGIS, OSRM, geocoder, Telegram Bot API (send only) |
| `passenger-bot` — Go long-polling bot | `passenger/bot/cmd` | `8090` health | Telegram (getUpdates), `api:8081` |
| `driver-bot` — Go long-polling bot | `driver/bot/cmd` | `8090` health | Telegram (getUpdates), `api:8081` |
| Passenger Mini App — Vue 3 SPA | `passenger/miniapp` | static via Caddy at `https://APP_DOMAIN/passenger/` | `https://APP_DOMAIN/api/v1` + WS |
| Driver Mini App — Vue 3 SPA | `driver/miniapp` | static via Caddy at `https://APP_DOMAIN/driver/` | same |
| Admin panel — Vue 3 SPA | `admin/frontend` | static via Caddy at `https://ADMIN_DOMAIN/` | `https://ADMIN_DOMAIN/api/v1` (same API, cookie session) |
| `db` — PostgreSQL 17 + PostGIS 3.5 | image `postgis/postgis` | 5432 internal | — |
| `osrm` — self-hosted OSRM (Kazakhstan extract) | image `ghcr.io/project-osrm/osrm-backend` | 5000 internal | — |
| `caddy` — TLS + reverse proxy + static files | `Caddyfile` | 80/443 | api, static dirs |
| `db-backup` — scheduled `pg_dump` + file archive | compose service | — | db |

Only the API touches PostgreSQL. Bots call the internal API with
`Authorization: Bearer $INTERNAL_BOT_SERVICE_SECRET` and `X-Jantaxi-Bot: passenger|driver`.
Bots never import `admin/backend/internal/...` (Go forbids it anyway); they import
only the public client libraries in `admin/backend/pkg/` (`pkg/telegram`, `pkg/botapi`).

The Telegram *sending* side (notification worker) lives in the API process: sending
with a bot token is safe from many processes; only `getUpdates` is exclusive.

## 2. Repository layout

```
go.mod                         single module github.com/yereke99/jantaxi
admin/backend/cmd/api          main: `api serve | api migrate up|status | api admin create`
admin/backend/migrations       goose SQL migrations, embedded (package migrations)
admin/backend/pkg/telegram     Bot API client (HTTP, retry_after, typed errors) + long poller
admin/backend/pkg/botapi       typed client for the internal bot API (used by both bots)
admin/backend/internal/
  platform/config              env config loading/validation
  platform/db                  pgxpool, WithTx, error helpers, advisory-lock jobs
  platform/httpx               JSON IO, error model, middleware, pagination, params
  platform/textutil            NFC normalisation, 120-code-point limits, control-char stripping
  platform/phone               KZ phone normalisation (E.164)
  platform/plate               KZ plate normalisation and validation
  platform/geo                 Kazakhstan boundary check, Point type, haversine
  platform/storage             private file storage (local volume) + image sanitising
  platform/testutil            test DB creation from template, fixtures, initData signer
  app                          wiring: builds services, mounts routers, runs jobs
  auth                         Telegram initData, app tokens, admin sessions/CSRF/RBAC, service auth
  users                        users + user_preferences, language, bot-chat state
  cities                       cities, service zones, settings (app_settings), public config
  routing                      OSRM client, geocoding adapters, /routes/preview, /geo/*
  pricing                      tariffs, fare quotes, commission calculation
  orders                       order lifecycle state machine, passenger + driver order endpoints, ratings, disputes
  stops                        stop validation/normalisation helpers (2–5 points, ordering)
  dispatch                     eligibility queries, offers, atomic accept, search expiry job
  drivers                      driver profiles, registration drafts, availability, locations
  vehicles                     vehicles + photos, plate uniqueness
  moderation                   applications, approve/reject/corrections/suspend/restore
  wallets                      ledger: reserve/settle/release/credit/adjust, balances
  payments                     top-up requests, provider abstraction (kaspi_manual), admin verification
  notifications                transactional outbox, worker, templates (kk/ru/en), Telegram rendering
  realtime                     pg_notify publisher, LISTEN fan-out, WebSocket hub
  reports                      dashboard metrics, CSV exports
  audit                        admin audit log writer + query
  botapi                       internal HTTP endpoints for bots (port 8081)
admin/frontend                 Vue 3 admin SPA
passenger/bot/{cmd,internal}   passenger bot
passenger/miniapp              passenger Vue 3 Mini App
driver/bot/{cmd,internal}      driver bot
driver/miniapp                 driver Vue 3 Mini App
docs/                          architecture, API (openapi.yaml), deployment, diagrams
```

Shared frontend core (`src/telegram/`, `src/maps/`, `src/styles/tokens.css`,
`src/services/http.ts`, `src/services/realtime.ts`) is intentionally duplicated between
the two Mini Apps (all source must live in the three app directories); `make verify-shared`
fails if the copies drift.

## 3. Service area (geofence)

* **Every** passenger stop and every driver location must lie inside Kazakhstan.
  Points outside Kazakhstan are rejected with `outside_service_area` (HTTP 422).
* Boundary: Natural Earth 1:10m admin-0 (public domain) Kazakhstan incl. Baikonur, stored in
  `country_boundaries` (code `KZ`), checked with `ST_Covers(geom, point)`.
* Embi (`lat 48.8264, lon 58.1442`) is only the default map centre and the first city.
* City resolution for an order: the active city whose active service zone covers the pickup;
  otherwise the nearest active city centre. Dispatch then uses that city's policy.

## 4. Identity and authentication

### 4.1 Mini Apps
1. Mini App sends raw `initData` to `POST /api/v1/auth/telegram/{passenger|driver}`.
2. API validates the HMAC with the **matching** bot token
   (`secret = HMAC_SHA256(key="WebAppData", msg=bot_token)`; data-check-string = all fields
   except `hash` — the `signature` field stays **in** — URL-decoded, sorted by key, `key=value`
   joined by `\n`), constant-time compare, `auth_date` not older than
   `TELEGRAM_INITDATA_MAX_AGE` (default 24h) and not more than 60s in the future.
3. API upserts the user by `telegram_user_id` and returns an **app token**:
   `base64url(payload).base64url(HMAC_SHA256(SESSION_SECRET, payload))`, payload
   `{"v":1,"uid":"<uuid>","app":"passenger|driver","exp":<unix>}`, TTL `APP_TOKEN_TTL` (12h).
4. Every request: `Authorization: Bearer <token>`. Middleware checks signature, expiry,
   `app` claim against the route group, and `users.is_blocked = false`.
5. `initDataUnsafe` and any `telegram_id` in a request body are never trusted.

### 4.2 Admin
* Email + password (argon2id), `admin_sessions` (random 32-byte token, SHA-256 stored),
  cookie `jt_admin` (`HttpOnly; Secure; SameSite=Strict; Path=/api`), 12h idle / 7d absolute.
* CSRF: login response returns `csrf_token`; it is also derivable from session; every
  non-GET admin request must send `X-CSRF-Token` equal to `HMAC(SESSION_SECRET, session_id)`.
* Login rate limit per IP and per email; 5 failures → 15 min lock.
* Roles → permissions (enforced server-side on every admin route):

| Permission | super_admin | operator | driver_moderator | finance_manager | auditor |
|---|---|---|---|---|---|
| dashboard.view, orders.view, drivers.view | ✓ | ✓ | ✓ | ✓ | ✓ |
| orders.manage (cancel, assign, disputes) | ✓ | ✓ | | | |
| drivers.moderate (approve/reject/corrections) | ✓ | | ✓ | | |
| drivers.manage (suspend/restore/city) | ✓ | ✓ | ✓ | | |
| documents.view (ID photos) | ✓ | | ✓ | | |
| finance.view (wallets, ledger, top-ups) | ✓ | | | ✓ | ✓ |
| finance.manage (confirm/reject/refund top-ups, adjustments) | ✓ | | | ✓ | |
| config.manage (cities, zones, tariffs, settings, templates) | ✓ | | | | |
| admins.manage | ✓ | | | | |
| audit.view, reports.export | ✓ | ✓ | | ✓ | ✓ |

* Bootstrap: `api admin create --email X --role super_admin` reads the password from stdin;
  or on first start, if no admins exist and `ADMIN_BOOTSTRAP_EMAIL` + `ADMIN_BOOTSTRAP_PASSWORD`
  are set, that super admin is created once (and a warning tells the operator to unset them).

### 4.3 Bots → API
* Internal listener `:8081`, routes under `/internal/v1/`, never proxied by Caddy.
* `Authorization: Bearer $INTERNAL_BOT_SERVICE_SECRET` (constant-time) + `X-Jantaxi-Bot`.
  The Telegram user id in internal requests comes from the Telegram update the bot received;
  the authenticated principal is the bot service.

## 5. Order lifecycle

Categories selected in UI → normalised fields:

| category | service_type | cargo_type | required extras |
|---|---|---|---|
| `taxi` | `passenger` | `none` | — (comment optional) |
| `parcel` | `delivery` | `parcel` | `cargo_description` (≤120), `recipient_phone`, `prohibited_items_ack=true` |
| `other` | `delivery` | `other` | `cargo_description` (≤120), `prohibited_items_ack=true` |

Statuses and allowed transitions (anything else → `409 invalid_transition`):

```
searching ──accept(driver/admin assign)──▶ accepted ──arriving(driver)──▶ driver_arriving
    │                                          │  └──start(driver)──────────────┐   │
    │                                          ▼                               ▼   ▼
    ├─cancel(passenger/admin/system timeout)─▶ cancelled ◀─cancel(passenger/driver/admin)
                                                                      in_progress ──complete(driver)──▶ completed
                                         in_progress ──cancel(admin only)──▶ cancelled
                                         in_progress|completed ──dispute(admin/passenger)──▶ disputed
                                         disputed ──resolve(admin)──▶ completed | cancelled
```
`draft` exists in the enum for future server-side drafts; quotes are stored in `fare_quotes`.

* Passenger cancel: only `searching | accepted | driver_arriving`.
* Driver cancel (with reason): only `accepted | driver_arriving`; order returns to **cancelled**
  (not re-dispatched automatically in MVP; passenger is notified and may re-order with one tap).
* Stops: intermediate stops must be completed in `seq` order while `in_progress`;
  `complete` requires all intermediate stops completed and marks the destination completed.
* Every transition: `UPDATE orders SET status=$new, version=version+1 ... WHERE id=$id AND status=$expected`,
  insert `order_status_events`, enqueue notifications, publish realtime event — one transaction.

### 5.1 Atomic acceptance (exactly-one winner)
In one transaction:
1. Load driver; require `status='approved'`, `is_online`, same `city_id` as the order, not blocked.
2. `SELECT ... FROM wallets WHERE driver_id=$d FOR UPDATE`; require
   `balance - reserved >= commission_amount` else `402 insufficient_balance` (with top-up hint).
3. `UPDATE orders SET status='accepted', driver_id=$d, vehicle_id=$v, accepted_at=now(), version=version+1
    WHERE id=$o AND status='searching' AND driver_id IS NULL RETURNING ...`
   → 0 rows ⇒ `409 order_already_taken`.
4. Partial unique index `orders_one_active_per_driver` makes a second concurrent active order for the
   same driver fail with a unique violation ⇒ `409 driver_has_active_order`.
5. Insert `wallet_reservations` + ledger `commission_reserve`; mark the winner's offer `accepted`,
   others `lost`; status event; outbox; `pg_notify`.

## 6. Money

* All amounts are integer **KZT** (`bigint`/`int64`, no floats anywhere in money paths).
* Commission = `(fare * commission_bps + 5000) / 10000` (round half up), `commission_bps`
  default `1000` (10%), snapshotted onto the order at creation.
* Wallet: `balance` (money the platform holds for the driver) and `reserved` (part of balance
  earmarked for active orders); `available = balance - reserved`; DB checks keep
  `balance >= 0`, `reserved >= 0`, `reserved <= balance`.
* Journal `wallet_ledger` is append-only (trigger blocks UPDATE/DELETE); each row stores
  `balance_delta`, `reserved_delta`, `balance_after`, `reserved_after` and a globally unique
  `idempotency_key`. Wallet row and ledger row change in the same transaction; a reconciliation
  query (`sum(deltas) == wallet`) is tested and exposed to admins.

| Event | balance_delta | reserved_delta | idempotency_key |
|---|---|---|---|
| top-up confirmed | +amount | 0 | `topup:<topup_id>` |
| order accepted | 0 | +commission | `reserve:<order_id>:<driver_id>` |
| order cancelled/reassigned before completion | 0 | −commission | `release:<reservation_id>` |
| order completed | −commission | −commission | `charge:<order_id>` |
| dispute resolved as cancelled after completion | +commission | 0 | `commission_refund:<order_id>` |
| admin adjustment | ±amount | 0 | client-supplied `adjust:<uuid>` |
| top-up refunded | −amount | 0 | `topup_refund:<topup_id>` |

* A unique-key conflict on the ledger means "already applied" → the operation returns the
  existing result (idempotent), never a second charge.

### 6.1 Kaspi Pay top-ups
`created → pending_verification → confirmed | rejected`, `created → expired` (job),
`confirmed → refunded`. The driver's "I paid" (`submit`) only moves to `pending_verification`.
Credit happens **only** when a finance admin confirms with the verified amount and the Kaspi
transaction/receipt number from trusted merchant records; `payment_transactions` has
`UNIQUE(provider, provider_transaction_id)` so one real payment can never credit twice.
Reference format: `<DRIVERCODE>-TOPUP-<6-digit number>` e.g. `EMB001-TOPUP-000123`.
Provider abstraction `payments.Provider` has `kaspi_manual` now; a webhook route
`POST /api/v1/payments/webhooks/{provider}` returns 404 until a verified provider is configured.

## 7. Dispatch

Eligible driver = `driver_profiles.status='approved'` ∧ `is_online` ∧ user not blocked ∧
same `city_id` as order ∧ no active order ∧ `available >= order.commission_amount` ∧
(if city `require_driver_location`: last location fresher than `driver_location_max_age_s`
and within `dispatch_radius_m` of pickup). On order creation all eligible drivers get an
`order_offers` row, a Telegram notification (outbox) and a `order.available` WS event.
The available-orders list is computed live (searching orders in the driver's city, filtered
by the same rules), so drivers who come online later still see open orders.
Job `search_expiry`: `searching` orders older than `cities.search_timeout_s` →
`cancelled` by `system` with reason `no_driver_found`.

## 8. Notifications (transactional outbox)

`notifications.Enqueue(ctx, tx, Notification{Bot, UserID, ChatID, Template, Params, DedupeKey})`
inserts into `notification_outbox` inside the business transaction (`ON CONFLICT (dedupe_key)
DO NOTHING`). Worker: claims rows with `FOR UPDATE SKIP LOCKED` + lease (`locked_until`),
renders the template in the user's language, sends via Bot API, records
`notification_attempts`; on 429 waits `retry_after`; on 5xx/network exponential backoff with
jitter (max 8 attempts → `dead`); on 403 (bot blocked / never started) → `dead` and marks
`users.<bot>_bot_blocked_at`. Admins can see and retry outbox rows. Global send rate ≤ 25 msg/s
per bot, ≤ 1 msg/s per chat.

Templates (keys): `order_created_passenger`, `order_available_driver`, `order_accepted_passenger`,
`order_accepted_driver`, `order_driver_arriving`, `order_started`, `order_stop_completed`,
`order_completed_passenger`, `order_completed_driver`, `order_cancelled_passenger`,
`order_cancelled_driver`, `order_taken_driver`, `order_no_driver`, `moderation_submitted`,
`moderation_approved`, `moderation_changes_requested`, `moderation_rejected`,
`driver_suspended`, `driver_restored`, `topup_created`, `topup_confirmed`, `topup_rejected`,
`wallet_low_balance`, `dispute_opened`, `dispute_resolved`, `phone_verified`.
Each message carries an inline **`web_app`** button (signed initData is guaranteed for inline
web_app buttons) whose URL is the Mini App URL plus `?startapp=<param>` (see §18); bot menus may
additionally use direct links `https://t.me/<bot_username>?startapp=<param>&mode=fullscreen`.

## 9. Realtime

* Publisher: `realtime.Publish(ctx, tx, Event)` executes `SELECT pg_notify('jt_events', $json)`
  inside the business transaction ⇒ delivered only on commit, never on rollback.
* Every API instance `LISTEN jt_events` (dedicated connection, auto-reconnect) and fans out to
  local WebSocket clients.
* Event JSON: `{"type":"order.accepted","id":"<uuid>","ts":"RFC3339","scopes":["user:<uuid>","driver:<uuid>","admin"],"data":{...small ids/status...}}`.
  Clients treat events as hints and re-fetch authoritative REST state.
* Scopes: `user:<user_id>` (passenger), `driver:<driver_id>`, `admin` (all admin sessions).
* WS endpoint `GET /api/v1/ws`. First client frame within 5s: `{"type":"auth","token":"<app token>"}`
  (admins are authenticated by cookie and skip this). Server pings every 25s; client
  reconnects with backoff 1s→30s and re-syncs via REST on reconnect / `visibilitychange` /
  Telegram `activated`.
* Event types: `order.created`, `order.available`, `order.taken`, `order.accepted`, `order.cancelled`,
  `order.driver_arriving`, `order.started`, `order.stop_completed`, `order.completed`,
  `order.disputed`, `driver.location_updated`, `driver.status_changed`, `wallet.updated`,
  `topup.updated`, `topup.confirmed`, `moderation.submitted`, `moderation.changes_requested`,
  `moderation.approved`, `moderation.rejected`, `driver.phone_verified`.

## 10. HTTP API conventions

* Base path `/api/v1`. JSON, `snake_case`, UTF-8. Request bodies limited (64 KiB JSON,
  10 MiB uploads), unknown JSON fields rejected.
* IDs are UUID strings; human numbers: `order.number` (e.g. 1042), `driver.code` (EMB001),
  `topup.reference` (EMB001-TOPUP-000123).
* Coordinates: `{"lat": 48.8264, "lon": 58.1442}` (WGS84, 6 decimals).
* Money: integers in KZT, field names without suffix (`fare`, `amount`, `balance`), plus
  `"currency": "KZT"` on wallet/top-up objects.
* Time: RFC 3339 UTC strings.
* Error body: `{"error":{"code":"order_already_taken","message":"...","fields":{"comment":"too_long"},"details":{}}}`.
  Frontends translate by `code`; `message` is English fallback for logs.
  Common codes: `bad_request`, `validation_failed`, `unauthorized`, `forbidden`, `not_found`,
  `conflict`, `invalid_transition`, `order_already_taken`, `driver_has_active_order`,
  `insufficient_balance`, `driver_not_approved`, `driver_offline`, `outside_service_area`,
  `route_unavailable`, `quote_expired`, `fare_not_allowed`, `phone_taken`, `plate_taken`,
  `phone_not_verified`, `registration_incomplete`, `too_many_requests`, `payment_reference_used`,
  `internal`.
* Lists for Mini Apps: `?limit=20&before=<cursor>` → `{"items":[...],"next_cursor":"..."|null}`.
  Admin lists: `?page=1&page_size=25` → `{"items":[...],"page":1,"page_size":25,"total":123}`.
* Idempotency: `POST /orders`, `POST /driver/wallet/topups`, admin adjustments take an
  `idempotency_key` (UUID) and return the original resource on replay.
* The full contract is `admin/backend/api/openapi.yaml` (embedded, served at
  `/api/v1/openapi.yaml`); a test checks that every mounted route is documented.

## 11. Text and validation rules

* Free text (`comment`, `cargo_description`, rating comment): trim, NFC-normalise, replace
  newlines/tabs with spaces, strip other control/format characters (Cc, and bidi overrides),
  collapse runs of spaces, max **120 Unicode code points** after normalisation. The Mini Apps
  count with the same rule (`[...s.normalize('NFC')].length`). Output is always escaped
  (Vue text interpolation; Telegram HTML parse mode with `html.EscapeString`).
* Phone: KZ mobile only, E.164 `+77XXXXXXXXX`; accepts `8 7xx…`, `+7 7xx…`, `7xx…`.
* Plate: uppercase, strip spaces/dashes, map Cyrillic look-alikes to Latin, validate
  `^\d{3}[A-Z]{2,3}(0[1-9]|1\d|20)$` (current format) — see `platform/plate`.
* Driver registration requires a Telegram-verified phone: the Mini App calls
  `WebApp.requestContact()`, Telegram sends the contact to the driver bot, the bot verifies
  `contact.user_id == from.id` and posts it to the API.

## 12. Files

* Private volume `FILE_STORAGE_DIR` (default `/data/files`), never served statically.
* Uploads: max 10 MiB, magic-byte sniffing (JPEG/PNG/WebP), decoded and re-encoded to JPEG
  (strips EXIF incl. GPS), longest side ≤ 2000px, SHA-256 stored.
* Access: drivers → own files; passengers → assigned driver's avatar for their active/past order;
  admins with `documents.view` (ID docs) or `drivers.view` (avatar/vehicle photos); every
  admin ID-document view is audit-logged. Responses: `Cache-Control: private, no-store`.

## 13. Telegram bots

* Long polling `getUpdates` (timeout 30s, `allowed_updates` = message, edited_message,
  callback_query, my_chat_member), `deleteWebhook` at start.
* Single consumer per token: lease via `POST /internal/v1/bots/{bot}/lease` (TTL 30s, renewed
  every 10s); an instance without the lease idles. Telegram 409 also triggers backoff.
* Offset durability: last processed `update_id` stored via the internal API; every update is
  claimed via `POST /internal/v1/bots/{bot}/updates/{update_id}/claim` before side effects ⇒
  restarts never double-process.
* Backoff: exponential 1s→60s with full jitter; honour `retry_after`; tokens never logged.
* Health: `GET :8090/healthz` → 200 when the last successful poll was < 90s ago.
* Bot UI is Kazakh by default; language stored in PostgreSQL via the API.

## 14. Frontend contract

* Vue 3 + TypeScript (strict) + Vite + Pinia + Vue Router (`createWebHistory(base)`) +
  vue-i18n (composition, `legacy:false`, default/fallback `kk`) + `@tma.js/sdk` +
  `maplibre-gl`. Icons: `@lucide/vue` (successor of the deprecated `lucide-vue-next`). TypeScript is pinned
  to 6.0.x until vue-tsc supports the TypeScript 7 native compiler.
* `useTelegramFullscreen()` (see `src/telegram/`) runs before the first route renders.
* Map: MapLibre GL with the light style from public config (`map.style_url`, default
  OpenFreeMap Positron), single map instance per screen kept alive across sheet changes,
  `resize()` on viewport changes (debounced, keeps center/zoom), custom numbered markers
  A–E, route line black, driver approach line dashed gray, current location blue dot.
* Public runtime config: `GET /api/v1/public/config?app=passenger|driver|admin` (bot usernames,
  direct-link base, support contacts, map style, default city centre, max route points,
  fare rules summary, Kaspi public settings, prohibited items list).

## 15. Design system — "iOS-native, Uber-white"

The product owner's direction: it must feel like a **native iPhone app** — strict, minimal,
nothing decorative or distracting — with an **Uber-like white/black** look. Follow Apple's Human
Interface Guidelines for structure and controls, with a monochrome palette.

* **Font**: system only — `-apple-system, BlinkMacSystemFont, "SF Pro Text", "SF Pro Display",
  Roboto, "Segoe UI", "Helvetica Neue", Arial, sans-serif` (no web fonts; Kazakh glyphs
  ә ғ қ ң ө ұ ү һ і must render). `-webkit-font-smoothing: antialiased`.
* **Type scale (iOS Dynamic Type defaults)**: Large Title 34/41 bold (-0.4px), Title1 28/34 bold,
  Title2 22/28 bold, Title3 20/25 semibold, Headline 17/22 semibold, Body 17/22, Callout 16/21,
  Subheadline 15/20, Footnote 13/18, Caption 12/16.
* **Colors (iOS system palette, light)**: background `#FFFFFF`; grouped background `#F2F2F7`;
  secondary fill `#F2F2F7`; tertiary fill `#E5E5EA`; separator `rgba(60,60,67,.29)` as 0.5px
  hairlines; label `#000000`; secondary label `rgba(60,60,67,.6)`; tertiary label
  `rgba(60,60,67,.3)`. Accents used **only for meaning**: green `#34C759` (online switch, money in,
  success), red `#FF3B30` (destructive, errors), blue `#007AFF` (GPS dot, text links only).
* **Buttons**: primary = filled **black** `#000000`, white 17px semibold label, height 50px,
  radius 14px, full-width inside sheets; secondary = filled gray `#F2F2F7` with black label;
  plain = text-only (black, or red when destructive). Pressed state = opacity .55 (iOS highlight),
  disabled = opacity .3. No ripples, no gradients, no glow, no glassmorphism.
* **Controls**: iOS segmented control (Taxi / Parcel / Other), iOS switch (51×31, green when on),
  inset-grouped lists (white cells radius 10px on `#F2F2F7`, 44px min row height, hairline
  separators inset 16px, chevron `›` for navigation rows), iOS-style text fields (`#F2F2F7`
  fill, radius 10px, 44px), iOS sheet with 36×5 grabber, action sheets for destructive
  confirmations (instead of browser `confirm()`), activity indicator spinner.
* **Layout**: map-first screens — the map fills the viewport; one bottom sheet carries the flow;
  floating white circular buttons (44px, subtle shadow `0 2px 8px rgba(0,0,0,.12)`) respect
  safe areas. Non-map screens use iOS large-title + inset-grouped list layout. Spacing 4/8/12/16/20/24/32;
  16px side margins. One primary action per screen.
* **Feedback**: Telegram `HapticFeedback` — `selectionChanged` on segmented/picker changes,
  `impactOccurred('light')` on primary taps, `notificationOccurred('success'|'error')` on results.
* **Motion**: iOS-like 200–300ms `cubic-bezier(.2,.8,.2,1)` sheet/page transitions; no decorative
  animation; respect `prefers-reduced-motion`.
* **CSS hygiene**: `-webkit-tap-highlight-color: transparent`, `overscroll-behavior: none`,
  `user-select: none` on controls, `touch-action: manipulation`, inputs ≥16px font (prevents
  iOS zoom), never rely on `100vh` (use Telegram viewport CSS vars).
* The admin panel uses the same palette and controls in a desktop layout (sidebar + content,
  macOS-like density: 13–15px body, 32–36px controls).

### 15.1 iOS 26 "Liquid Glass" layer (product owner: "like the new iPhone design")

Content stays white and minimal; the new iOS material is applied **only to floating chrome**
that sits above the map or scrolling content:

* **Glass material** (`.jt-glass`): `background: rgba(255,255,255,.72)`;
  `backdrop-filter: blur(24px) saturate(180%)` (+ `-webkit-` prefix); hairline border
  `0.5px solid rgba(255,255,255,.65)` plus an inner top highlight
  (`box-shadow: inset 0 1px 0 rgba(255,255,255,.8), 0 8px 24px rgba(0,0,0,.10)`).
  Fallback to solid `#FFFFFF` when `backdrop-filter` is unsupported, under
  `prefers-reduced-transparency: reduce`, or when a low-end device flag is set (map + blur on
  weak Android WebViews). Never put body text on glass over busy imagery without the fallback.
* **Where glass is used**: floating map buttons (locate, back, menu — 48px circles), the bottom
  sheet surface, the floating tab bar, navigation bars once content scrolls under them, toasts,
  the segmented-control track over the map.
* **Shapes**: capsule primary/secondary buttons (`border-radius: 999px`, height 52px); bottom
  sheets with 32px top radius and, in the peek/half state, a **floating inset sheet** (8px from
  the screen edges and above the bottom safe area, all corners 32px) that docks full-width when
  expanded; cards and inset-grouped lists radius 22px; concentric radii (inner radius = outer
  radius − padding).
* **Floating tab bar** (driver app main sections: Home, Orders, Wallet, Profile): glass capsule
  floating 8px above the bottom safe area, icons 24px with labels 10–11px semibold, selected item
  in a darker glass pill; it hides during an active trip action sheet.
* **Segmented control**: capsule track, sliding white thumb with soft shadow (spring 300ms).
* **Motion**: spring-like `cubic-bezier(.2,.9,.25,1.05)` 280–360ms for sheet snapping and thumb
  movement; glass elements fade/scale 0.96→1 on appear. Still no decorative animation.
* **Colors and typography stay as above** (white content, black primary, SF system font).

## 16. Background jobs (API process)

Each job runs on an interval and takes a `pg_try_advisory_lock` so only one API replica runs it:
`notification_worker` (1s), `search_expiry` (15s), `topup_expiry` (5m), `quote_cleanup` (1h),
`stale_driver_offline` (5m, offline after `DRIVER_ONLINE_IDLE_TIMEOUT` without heartbeat/location),
`bot_update_cleanup` (1h, keep 7 days), `location_history_retention` (1d, keep 30 days),
`session_cleanup` (1h).

## 17. Configuration

All configuration is environment variables (see `.env.example`); DB `app_settings` override the
runtime-editable ones (support contacts, Kaspi public payment settings, top-up limits,
prohibited items, min online balance). Secrets never live in the repo or the database.

## 18. Mini App start parameters (deep links)

`https://t.me/<bot>?startapp=<param>&mode=fullscreen` — `<param>` matches `^[A-Za-z0-9_-]{1,64}$`.
Parameters are navigation hints only; every screen re-fetches data with full authorization.

| App | Param | Screen |
|---|---|---|
| passenger | `home` (default) | map home |
| passenger | `taxi` / `parcel` / `other` | booking flow with category preselected |
| passenger | `orders` | my orders |
| passenger | `order_<uuid>` | order detail / live trip |
| passenger | `support` | support |
| passenger | `driver` | "become a driver" screen (links to driver bot) |
| driver | `home` (default) | driver home |
| driver | `register` | registration / corrections |
| driver | `orders` | available orders |
| driver | `order_<uuid>` | order detail (available or own) |
| driver | `trip` | active trip |
| driver | `wallet` / `topup` / `topup_<uuid>` | wallet / new top-up / top-up detail |
| driver | `profile` / `support` | profile / support |

Launch surfaces: menu button (`setChatMenuButton` → `web_app` URL of the Mini App), inline
`web_app` buttons (private chat) whose URL is `<MINIAPP_URL>?startapp=<param>`, and inline `url`
buttons with the direct link above. **Never** reply-keyboard `web_app` buttons: Telegram sends no
initData for them. The Mini Apps read the start parameter from `initData.start_param` first and
fall back to the `startapp` query parameter of their own URL.

## 19. Endpoint catalogue

Auth columns: `P` passenger app token, `D` driver app token (+ driver profile loaded when the
driver has one), `A:<perm>` admin session with permission, `S` bot service secret (internal port),
`-` public. All paths below are under `/api/v1` unless they start with `/internal/v1`.

### Identity, config, realtime
| Method | Path | Auth | Owner pkg | Notes |
|---|---|---|---|---|
| POST | /auth/telegram/passenger | - | auth | `{init_data}` → `{token, expires_at}`; upserts user + default `kk` preferences |
| POST | /auth/telegram/driver | - | auth | same, validated with the driver bot token |
| GET | /me | P/D | users | user, language, phone (masked), `driver` summary when app=driver (`id, code, status, is_online, city`) |
| PATCH | /me/preferences | P/D | users | `{language}` |
| GET | /public/config?app= | - | cities | see §14 |
| GET | /ws | P/D/A | realtime | WebSocket |
| GET | /openapi.yaml | - | app | contract |
| GET | /healthz, /readyz | - | app | outside `/api/v1` too |

### Geo, routes, passenger orders
| Method | Path | Auth | Owner pkg | Notes |
|---|---|---|---|---|
| GET | /geo/search?q=&lat=&lon=&lang= | P/D | routing | places gazetteer first, then provider; only KZ results; `[{title, subtitle, lat, lon, source}]` |
| GET | /geo/reverse?lat=&lon=&lang= | P/D | routing | `{title, subtitle, lat, lon, in_service_area}`; coordinates-only title when nothing found |
| POST | /routes/preview | P | routing+pricing | `{category, stops:[{lat,lon,address,address_details?}]}` (2..max_points) → quote: `{quote_id, expires_at, distance_m, duration_s, legs[{distance_m,duration_s}], geometry (GeoJSON LineString), bbox, fare:{recommended,min,max,step,options[]}, commission_bps, payment_methods[]}` |
| POST | /orders | P | orders | `{quote_id, fare, payment_method, comment?, cargo_description?, recipient_phone?, prohibited_items_ack?, idempotency_key}` → Order (201, or 200 on idempotent replay) |
| GET | /orders?scope=active\|history&limit=&before= | P | orders | cursor list of OrderSummary |
| GET | /orders/{id} | P | orders | Order: stops, route snapshot, status events, driver+vehicle when assigned, `driver_location` while active |
| POST | /orders/{id}/cancel | P | orders | `{reason?}` |
| POST | /orders/{id}/rating | P | orders | `{rating 1..5, comment?}` completed orders only, once |
| POST | /orders/{id}/dispute | P | orders | `{reason}` completed (≤72h) or in_progress |
| GET | /orders/{id}/driver-photo | P | drivers | assigned driver avatar (JPEG) |

### Driver
| Method | Path | Auth | Owner pkg | Notes |
|---|---|---|---|---|
| GET | /driver/registration | D | drivers | draft/current data, files, status, `corrections[]`, `phone_verified` |
| PUT | /driver/registration | D | drivers | `{first_name,last_name,vehicle:{make,model,color,plate}}` creates the profile on first call (code assigned) |
| POST | /driver/registration/files | D | drivers | multipart `kind` ∈ avatar,id_front,id_back,vehicle_main,vehicle_back,vehicle_side,vehicle_plate,vehicle_extra + `file` |
| DELETE | /driver/registration/files/{fileId} | D | drivers | only extra vehicle photos while editable |
| POST | /driver/registration/check | D | drivers | `{plate?}` advisory availability |
| POST | /driver/register | D | moderation | submit / resubmit → `pending` |
| GET | /driver/files/{id} | D | drivers | own files only |
| GET | /driver/profile | D | drivers | profile, vehicle, rating, trips, status |
| PATCH | /driver/profile | D | drivers | non-moderated fields only (none in MVP besides language via /me) → 422 for moderated fields |
| PATCH | /driver/availability | D | drivers | `{online}`; checks approved, not suspended, available ≥ `driver.min_online_balance` |
| POST | /driver/location | D | drivers | `{lat,lon,accuracy_m?,heading?,speed_mps?,recorded_at?}`; must be inside KZ; rate-limited 1/2s |
| GET | /driver/orders | D | dispatch | available orders (cards) |
| GET | /driver/orders/active | D | orders | current order or 204 |
| GET | /driver/orders/history?limit=&before= | D | orders | |
| GET | /driver/orders/{id} | D | orders | available order detail (if eligible) or own order |
| POST | /driver/orders/{id}/accept | D | dispatch | atomic; 409 `order_already_taken`, 402 `insufficient_balance` |
| POST | /driver/orders/{id}/arriving | D | orders | |
| POST | /driver/orders/{id}/start | D | orders | |
| POST | /driver/orders/{id}/stops/{stopId}/complete | D | orders | in sequence |
| POST | /driver/orders/{id}/complete | D | orders | settles commission once |
| POST | /driver/orders/{id}/cancel | D | orders | `{reason}` before start |

### Wallet and top-ups (driver)
| Method | Path | Auth | Owner pkg | Notes |
|---|---|---|---|---|
| GET | /driver/wallet | D | payments | `{balance,reserved,available,currency,min_online_balance}` |
| GET | /driver/wallet/transactions?limit=&before= | D | payments | ledger entries |
| POST | /driver/wallet/topups | D | payments | `{amount, idempotency_key}` → TopUp + `payment{url,recipient_name,instructions,reference,amount}` |
| GET | /driver/wallet/topups | D | payments | list |
| GET | /driver/wallet/topups/{id} | D | payments | |
| POST | /driver/wallet/topups/{id}/receipt | D | payments | multipart image, optional evidence |
| POST | /driver/wallet/topups/{id}/submit | D | payments | `{payment_reference?, note?}` → pending_verification (never credits) |
| POST | /driver/wallet/topups/{id}/cancel | D | payments | only `created` |
| POST | /payments/webhooks/{provider} | - | payments | 404 unless a verified provider is configured |

### Admin
| Method | Path | Perm | Owner pkg |
|---|---|---|---|
| POST | /admin/auth/login, /admin/auth/logout | - / session | auth |
| GET | /admin/auth/me | session | auth |
| POST | /admin/auth/password | session | auth |
| GET/POST | /admin/admins ; PATCH /admin/admins/{id} | admins.manage | auth |
| GET | /admin/dashboard?from=&to=&city_id= | dashboard.view | reports |
| GET | /admin/reports/{orders,ledger,topups}.csv | reports.export | reports |
| GET | /admin/orders?status=&city_id=&category=&from=&to=&q=&page= | orders.view | orders |
| GET | /admin/orders/{id} | orders.view | orders |
| POST | /admin/orders/{id}/cancel | orders.manage | orders |
| POST | /admin/orders/{id}/assign `{driver_id}` | orders.manage | dispatch |
| POST | /admin/orders/{id}/dispute `{reason}` | orders.manage | orders |
| GET | /admin/disputes?status= | orders.view | orders |
| POST | /admin/disputes/{id}/resolve `{outcome, resolution, refund_commission}` | orders.manage | orders |
| GET | /admin/drivers?status=&city_id=&online=&q=&page= | drivers.view | moderation |
| GET | /admin/drivers/{id} | drivers.view | moderation |
| GET | /admin/drivers/{id}/orders | drivers.view | orders |
| GET | /admin/drivers/{id}/ledger | finance.view | payments |
| GET | /admin/drivers/locations?city_id= | drivers.view | drivers |
| GET | /admin/files/{id} | documents.view (ID docs) / drivers.view (others) | drivers |
| POST | /admin/drivers/{id}/approve `{comment?}` | drivers.moderate | moderation |
| POST | /admin/drivers/{id}/reject `{reason, comment?}` | drivers.moderate | moderation |
| POST | /admin/drivers/{id}/request-corrections `{items:[{field,reason,comment?}], comment?}` | drivers.moderate | moderation |
| POST | /admin/drivers/{id}/suspend `{reason}` | drivers.manage | moderation |
| POST | /admin/drivers/{id}/restore | drivers.manage | moderation |
| PATCH | /admin/drivers/{id} `{city_id}` | drivers.manage | moderation |
| POST | /admin/drivers/{id}/wallet/adjustments `{amount (signed), reason, idempotency_key}` | finance.manage | payments |
| GET | /admin/topups?status=&q=&page= | finance.view | payments |
| GET | /admin/topups/{id} | finance.view | payments |
| GET | /admin/topups/{id}/receipt | finance.view | payments |
| POST | /admin/topups/{id}/confirm `{verified_amount, provider_transaction_id, paid_at, note?}` | finance.manage | payments |
| POST | /admin/topups/{id}/reject `{reason}` | finance.manage | payments |
| POST | /admin/topups/{id}/refund `{reason}` | finance.manage | payments |
| GET | /admin/ledger?driver_id=&type=&from=&to=&page= | finance.view | payments |
| GET | /admin/wallets/reconciliation | finance.view | payments |
| GET/POST | /admin/cities ; PATCH /admin/cities/{id} | dashboard.view (GET) / config.manage | cities |
| GET/POST | /admin/cities/{id}/zones ; PATCH/DELETE /admin/zones/{id} | config.manage | cities |
| GET/POST | /admin/places ; PATCH/DELETE /admin/places/{id} | config.manage | cities |
| GET | /admin/tariffs?city_id= ; POST /admin/tariffs (new version) | dashboard.view / config.manage | pricing |
| GET/PATCH | /admin/settings | config.manage | settings |
| GET | /admin/notification-templates ; PUT /admin/notification-templates/{key}/{lang} ; DELETE (revert) | config.manage | notifications |
| GET | /admin/notifications?status=&page= ; POST /admin/notifications/{id}/retry | orders.manage | notifications |
| GET | /admin/audit-logs?admin_id=&entity_type=&entity_id=&page= | audit.view | audit |

### Internal (bots → API, port 8081, `S`)
| Method | Path | Owner pkg | Notes |
|---|---|---|---|
| POST | /internal/v1/bots/{bot}/lease | botapi | `{instance_id, ttl_s}` → `{acquired, last_update_id}` |
| POST | /internal/v1/bots/{bot}/offset | botapi | `{instance_id, last_update_id}` (monotonic, holder only) |
| POST | /internal/v1/bots/{bot}/updates/{update_id}/claim | botapi | `{claimed: bool}` first-time only |
| POST | /internal/v1/bots/{bot}/users/sync | botapi | Telegram user + `started`/`blocked` flags → `{user_id, language, is_new}` |
| PUT | /internal/v1/bots/{bot}/users/{tg_id}/language | botapi | `{language}` |
| GET | /internal/v1/passenger/{tg_id}/orders?limit= | botapi | recent orders summary |
| GET | /internal/v1/driver/{tg_id}/summary | botapi | status, code, online, wallet, active order, moderation corrections |
| POST | /internal/v1/driver/{tg_id}/availability | botapi | `{online}` same rules as the Mini App |
| GET | /internal/v1/driver/{tg_id}/orders?limit= | botapi | available orders |
| POST | /internal/v1/driver/{tg_id}/contact | botapi | `{phone, contact_user_id}`; verifies `contact_user_id == tg_id` |
| POST | /internal/v1/driver/{tg_id}/location | botapi | `{lat, lon, accuracy_m?, live}` |
| GET | /internal/v1/config | botapi | Mini App URLs, bot usernames, support contacts |

## 20. Package APIs used across packages (Go)

These signatures are the contract between packages built in parallel. Keep them.

```go
// platform/db
type Querier interface{ Exec; Query; QueryRow }      // *pgxpool.Pool and pgx.Tx satisfy it
func WithTx(ctx, pool *pgxpool.Pool, fn func(pgx.Tx) error) error
func IsUniqueViolation(err error, constraint string) bool
func RunExclusive(ctx, pool, name string, fn func(ctx) error) (ran bool, err error)

// platform/httpx — handlers are httpx.H(func(w, r) error); errors are *httpx.Error

// auth (context.go is fixed; middleware in auth/*.go)
auth.RequirePrincipal(r) (Principal, error); auth.RequireDriver(r) (DriverContext, error)
auth.RequireAdminPrincipal(r) (AdminPrincipal, error); auth.BotFrom(ctx) string
(*auth.Service).RequireApp(auth.App) func(http.Handler) http.Handler
(*auth.Service).LoadDriverProfile(optional bool) func(http.Handler) http.Handler // sets DriverContext when a profile exists
(*auth.Service).RequireAdmin(perms ...auth.Permission) func(http.Handler) http.Handler
auth.RequireService(secret string) func(http.Handler) http.Handler
(*auth.Service).VerifyAppToken(token string) (Principal, error)
(*auth.Service).AdminFromRequest(r *http.Request) (AdminPrincipal, error)

// realtime
realtime.Publish(ctx, q db.Querier, realtime.Event{Type, Scopes []string, Data any}) error // pg_notify in tx
realtime.UserScope(uuid) "user:<id>"; realtime.DriverScope(uuid) "driver:<id>"; realtime.AdminScope "admin"

// notifications
notifications.Enqueue(ctx, q db.Querier, notifications.Notification{
    Bot notifications.Bot /* PassengerBot|DriverBot */, UserID uuid.UUID, Template string,
    Params map[string]any, DedupeKey string}) error   // resolves chat id from users; no-op on duplicate key
// Template keys and their params are documented in notifications/templates.go.

// wallets (all tx-scoped, idempotent by key, never publish events themselves)
wallets.Ensure(ctx, q, driverID) (walletID uuid.UUID, err error)
wallets.Get(ctx, q, driverID) (wallets.Wallet, error)          // Balance, Reserved, Available()
wallets.Reserve(ctx, tx pgx.Tx, driverID, orderID uuid.UUID, amount int64) (wallets.Reservation, error) // ErrInsufficientBalance
wallets.ReleaseForOrder(ctx, tx, orderID uuid.UUID, reason string) (released int64, err error)
wallets.SettleForOrder(ctx, tx, orderID uuid.UUID) (charged int64, err error)
wallets.RefundCommission(ctx, tx, orderID uuid.UUID, adminID uuid.UUID) (refunded int64, err error)
wallets.CreditTopUp(ctx, tx, driverID, topupID uuid.UUID, amount int64, adminID uuid.UUID) error
wallets.DebitTopUpRefund(ctx, tx, driverID, topupID uuid.UUID, amount int64, adminID uuid.UUID) error
wallets.Adjust(ctx, tx, driverID uuid.UUID, amount int64, adminID uuid.UUID, key, reason string) error
wallets.ListLedger(ctx, q, wallets.LedgerFilter) ([]wallets.LedgerEntry, total int, err error)
wallets.Reconcile(ctx, q) ([]wallets.Mismatch, error)
var wallets.ErrInsufficientBalance error

// settings (DB app_settings with typed defaults)
settings.Int(ctx, q, key string, def int64) int64
settings.Bool(ctx, q, key string, def bool) bool
settings.Strings(ctx, q, key string, def []string) []string
settings.JSON(ctx, q, key string, dst any) (found bool, err error)

// platform/geo
geo.Point{Lat, Lon float64}
geo.InServiceArea(ctx, q, p geo.Point) (bool, error)                 // KZ geom_check
geo.RequireInServiceArea(ctx, q, field string, pts ...geo.Point) error // 422 outside_service_area {field, index}
geo.DistanceMeters(a, b geo.Point) float64

// cities
cities.Resolve(ctx, q, p geo.Point) (cities.City, error)  // zone covering p, else nearest active city

// platform/textutil
textutil.CleanText(s string, max int) (string, error)     // ErrTooLong; NFC, control chars stripped
textutil.Len(s string) int                                 // code points after NFC

// platform/phone, platform/plate
phone.Normalize(s string) (string, error)                  // "+77XXXXXXXXX"
plate.Normalize(s string) (string, error)

// platform/storage
(*storage.Store).SaveImage(ctx, q, owner uuid.UUID, purpose string, r io.Reader) (storage.File, error)
(*storage.Store).Open(ctx, q, id uuid.UUID) (storage.File, io.ReadCloser, error)
storage.ServeImage(w, r, f storage.File, rc io.ReadCloser)  // private, no-store

// audit
audit.Log(ctx, q db.Querier, r *http.Request, audit.Entry{Action, EntityType, EntityID string, Before, After any}) error
```

Wiring: `internal/app` owns construction. Each domain area has a file `internal/app/wire_<area>.go`
(created as stubs) that only that area's owner edits; `app.go` calls them.
