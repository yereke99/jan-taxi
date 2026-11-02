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
  platform/iin                 IIN (ЖСН) normalisation, check digit and masking
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
  drivers                      driver profiles, registration drafts, IIN, availability, locations
  vehicles                     vehicles, plate uniqueness, photos uploaded before 2026-10-31
  moderation                   applications, automatic approval at submission, approve/reject/corrections
                               of older pending applications, suspend/restore
  wallets                      ledger: reserve/settle/release/credit/adjust, balances
  marketing                    promotions (§23): campaigns, coupons, passenger bonus balance, referrals,
                               promotions on orders, segments, broadcasts, reconciliation
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
* Tariff region of an order (`cities.Resolve`): the active city owning the **smallest** active
  service zone that covers pickup A; otherwise the nationwide default region **KAZ** («Қазақстан» /
  «Казахстан» / «Kazakhstan», `cities.is_default_region`, migration 00009) whose single zone is the
  whole country and whose tariffs are the base tariffs. There is **no** nearest-city fallback. The
  resolved region only selects the **tariff** (and the search timeout `cities.search_timeout_s`).
  A class or category the zone city has no active tariff for is priced by KAZ, as everywhere outside
  the zone, so drawing a city zone never switches a service off; each preview option names the
  region that priced it (`tariff_region`), and the order inherits that region from its quote.
  KAZ can never be deactivated, and its zone cannot be created, edited or deleted through the admin
  API (`409 default_region_locked`).
* Locality: the preview reverse-geocodes pickup A once (≤ 1.5 s): the settlement (city, town or
  village) whose outline covers it, by its own official name («Ембі», even where nearby addresses
  say «Эмба»), else the town or district of the nearest address; `null` on failure, never
  coordinates. It is stored on every quote; the order
  copies it to `orders.locality`. Every order DTO carries `locality` (where the trip happens) and
  `tariff_region` `{code, name:{kk,ru,en}}` (whose tariff priced it); UIs and the driver bot show the
  locality, the admin panel shows the tariff region separately.
* A driver's city only prefixes the driver code and groups drivers in the admin panel. It is decided
  once, when the first registration save creates the profile, from the device `location` sent with
  it: the city whose zone covers it, KAZ elsewhere in Kazakhstan, the configured default city when no
  (or a foreign) position is sent.
* **Dispatch never depends on a city.** A passenger anywhere in Kazakhstan (e.g. Almaty) creates an
  order and it is offered to free drivers whose fresh GPS location is within the order's search radius
  of pickup point A (§7), wherever those drivers are registered.
* **Pickup A always has coordinates** — the passenger's GPS position or the map point under the black
  centre pin — and only they decide the Kazakhstan check, the tariff region, the locality and dispatch.
  When the map search does not know the place (folk names, micro-districts, a yard inside a village),
  the passenger may describe A in their own words with the same «Мекенжай табылмады ма? Қолмен
  енгізіңіз» button as the other stops (e.g. «Мешіттің жанында, көк қақпа»). The text replaces only A's
  address (same cleaning and 200-code-point limit as every stop address); A stays an ordinary stop at
  its coordinates (`mode` `geocoded`; a `manual` pickup is refused with `pickup_coordinates_required`),
  and the driver bot offer, the driver Mini App and the admin panel show the text as the pickup. The
  text stays until the passenger replaces it: moving the home screen's pin, confirming «Картадан
  таңдау» or dragging marker A moves only A's point (coordinates, locality line, Kazakhstan check);
  editing the text or choosing a search result, a recent place or «Менің орнымды пайдалану» for A
  replaces it. The typed pickup needs a real point — the GPS position, a pin the passenger placed or a
  place they chose, never the default map centre the home screen names after a failed GPS attempt;
  without one the place search offers no typed pickup and asks to mark A on the map or turn on
  location first. Stops after A may instead be `manual`, without coordinates (§5).

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
* The admin API (`/api/v1/admin*`, login included) is served only on the admin host. The public Mini
  App host answers 404: Caddy before its API proxy, and the API itself for any admin request whose
  `Host` is the host of `PASSENGER_MINIAPP_URL` / `DRIVER_MINIAPP_URL` (unless `ADMIN_FRONTEND_URL`
  shares it), so the Mini App URL (and the local quick tunnel) cannot be used to lock out or guess
  admin accounts.
* Roles → permissions (enforced server-side on every admin route):

| Permission | super_admin | operator | driver_moderator | finance_manager | auditor | marketing_manager |
|---|---|---|---|---|---|---|
| dashboard.view | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| orders.view, drivers.view | ✓ | ✓ | ✓ | ✓ | ✓ | |
| orders.manage (cancel, assign, disputes) | ✓ | ✓ | | | | |
| drivers.moderate (approve/reject/corrections) | ✓ | | ✓ | | | |
| drivers.manage (suspend/restore/city) | ✓ | ✓ | ✓ | | | |
| documents.view (ID photos) | ✓ | | ✓ | | | |
| finance.view (wallets, ledger, top-ups) | ✓ | | | ✓ | ✓ | |
| finance.manage (confirm/reject/refund top-ups, adjustments; passenger bonus corrections and promotion reversals, §23.8) | ✓ | | | ✓ | | |
| config.manage (cities, zones, tariffs, settings, templates) | ✓ | | | | | |
| admins.manage | ✓ | | | | | |
| audit.view, reports.export | ✓ | ✓ | | ✓ | ✓ | |
| marketing.view (campaigns, coupons, bonus ledger, referrals, broadcasts, reports, reconciliation) | ✓ | ✓ | | ✓ | ✓ | ✓ |
| marketing.edit (draft campaigns and their versions, pause, segments) | ✓ | | | | | ✓ |
| marketing.approve (activate/resume/schedule/end, budgets, switches, coupon voids, referral decisions, large broadcasts together with marketing.broadcast) | ✓ | | | ✓ | | |
| marketing.broadcast (create, test, send, pause, cancel broadcasts) | ✓ | | | | | ✓ |

The marketing permissions and the role `marketing_manager` are described in §23.10: a marketing
manager prepares campaigns, segments and broadcasts; money starts flowing only with
`marketing.approve`. A broadcast above the approval threshold needs both `marketing.broadcast` and
`marketing.approve`, which with these roles only `super_admin` holds.

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

Route points: pickup A first (always with coordinates; its address may be the passenger's own text,
§3), up to three intermediate stops, the destination last. A stop after A may be `manual` — the
passenger's words with `NULL` coordinates — while `orders.manual_destination_enabled` is on and
`orders.manual_destination_pricing` is `passenger_tariff_bounds`; such a route gets no OSRM route or
metrics, and each class's quote offers the administrator's tariff range (`fare.recommended: null`,
`pricing_mode: passenger_tariff_bounds`).

Passenger-written addresses (owner, 2026-11-01): A and B may both be the passenger's own words, e.g.
an order for another person at a place known only by a local name. The order is still offered by
radius from A's coordinates — the passenger's GPS position or the pin they placed (§7). A typed A is
stored with `order_stops.address_typed = true` (`StopInput.address_typed`, echoed on every `Stop`,
`StopSummary` and `QuoteStop`); a manual stop is the passenger's words by definition and keeps
`false`. Every channel shows such text verbatim (never as «Point on the map»; only coordinate labels
with decimals are hidden) and marks it: the driver Mini App, the passenger app and the admin panel
with «Место указано пассажиром», the driver bot offer and trip card with the lines «✍️ Адрес написан
пассажиром» under A and, for stops without a map point, a ✍️ after their address plus «✍️ — место
указано пассажиром, точки на карте нет; маршрут не рассчитан»; `/orders` and `/trip` the same. The
bot `via` line holds up to three 200-character stops uncut; `/orders` leaves cards that would pass
Telegram's 4096-character limit to the Mini App list.

Fare: `POST /orders` accepts any of the quote's `fare.options` (`min_fare..max_fare` on the
`fare_step` grid; otherwise `422 fare_not_allowed`). The passenger app (customer decision, 2026-10-31):
* **taxi** — no fare input: the order goes at the price of the class the passenger ticked, the price
  on its row and on the confirm button («Тапсырыс беру · Седан · 400 ₸»): the option's
  `fare.recommended`, or for a route with a manual stop, which has no recommendation, the first allowed
  fare (the tariff's `min_fare`);
* **parcel / other** — the fare input stays: a stepper over `fare.options`, or for a route with a
  manual stop an amount the passenger types within the tariff's min/max/step.

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
One operation, `dispatch.AcceptOrder(order, driver, options)`, serves both entry points — the driver
Mini App (`POST /driver/orders/{id}/accept`, the card's «Тапсырысты алу» and the order screen) and the
driver bot's «✅ Тапсырысты алу» (`POST /internal/v1/driver/{tg_id}/orders/{id}/accept`); it logs every
attempt (order, driver, channel, outcome code, duration, request id). In one transaction:
0. Lock the order row `FOR UPDATE`, then the driver profile (`FOR SHARE`), then the wallet: the lock
   order of every order transition, so concurrent acceptances of one order run one after another.
   A repeat of the winner (double tap, HTTP retry, redelivered callback, or the other channel) sees
   its own acceptance and is answered as a replay (`200`, `replayed`, nothing charged); a late tap
   on the driver's own finished order is `409 invalid_transition`, never "taken by another driver".
1. Load driver; require `status='approved'`, `is_online`, not blocked, no active order, and a fresh
   location (`driver_locations.recorded_at` within `dispatch.location_max_age_s`, else
   `422 location_required` with `details.reason` `location_required|location_stale` and `max_age_s`)
   within the order's effective radius of the pickup (else `404`: the order is outside the driver's
   reach). No city check.
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
| order with a passenger's promotion completed: the discount credited to the driver (`promo_compensation`, §23.6) | +discount | 0 | `promo:<application_id>` |
| that compensation reversed by finance (`promo_compensation_reversal`, §23.6) | −discount | 0 | `promo:<application_id>:reversal` |

* A unique-key conflict on the ledger means "already applied" → the operation returns the
  existing result (idempotent), never a second charge.
* The passenger bonus balance is a separate account with its own append-only ledger
  (`passenger_bonus_accounts`, `passenger_bonus_ledger`, §23.5); it never mixes with driver wallets.

### 6.1 Kaspi Pay top-ups
`created → pending_verification → confirmed | rejected`, `created → expired` (job),
`confirmed → refunded`. The driver's "I paid" (`submit`) only moves to `pending_verification`.
Reference format: `<DRIVERCODE>-TOPUP-<6-digit number>` e.g. `EMB001-TOPUP-000123`.
Provider abstraction `payments.Provider` has `kaspi_manual`; a webhook route
`POST /api/v1/payments/webhooks/{provider}` returns 404 until a verified provider is configured.

**Payee** (seeded by migration 00006 into `app_settings`, editable in `/admin/settings`):
ИП "JAN-TAXI" (`kaspi.recipient_name`), ИИН/БИН 000809500289 (`company.bin`, check digit
validated), IBAN KZ05722S000057176693 (`company.iban`, ISO 13616 check), АО "Kaspi Bank"
(`company.bank`), БИК CASPKZKA (`company.bic`), КБе 19 (`company.kbe`), address
`company.address`; Kaspi Pay link `kaspi.payment_url` = `https://pay.kaspi.kz/pay/xbp1imhd` (the payer
types the amount; the link carries neither amount nor reference). The seeded rows win over
`KASPI_PAY_*` environment defaults.

**Two ways to credit, one code path.** `payments.confirmTopUp` is the only place that credits a
top-up: `payment_transactions` row, `wallets.CreditTopUp` (`topup:<id>`, idempotent), status
`confirmed`, `top_up_events`, `admin_audit_logs`, `topup_confirmed` notification, realtime
events. Callers: (1) a finance admin confirming with the verified amount and Kaspi number
(`actor admin`, source `manual_admin`); (2) the system after a Kaspi PDF receipt passed every check
below (`actor system`, `confirmed_by_system`, audit action `topup.auto_confirm` with
`admin_id NULL`, provider `kaspi_pdf`, `provider_transaction_id` = receipt number, source
`receipt_auto`, `receipt_id`). Both are reversible with the existing refund.
Kaspi numbers are unique across `kaspi_manual` and `kaspi_pdf`
(`payment_transactions_kaspi_txn_key`), and an admin cannot confirm another top-up with a
number already attached to a receipt.

**Kaspi receipt PDF** (`POST /driver/wallet/topups/{id}/receipt` from the Mini App, or a PDF
sent to the driver bot → `POST /internal/v1/driver/{tg_id}/topup-receipt`, attached to the
driver's open top-up that already holds this receipt, else the newest open top-up with the
receipt's amount, else the newest open top-up). In one transaction (per-driver advisory lock): store the file →
reject reuse → insert `payment_receipts` → evaluate → credit or leave for review.
* Storage (`storage.SavePDF`): `%PDF-` magic, ≤ min(5 MiB, `MAX_UPLOAD_BYTES`), parsed by the
  defensive reader `platform/pdfdoc`; encrypted PDFs and active content (JavaScript, Launch,
  attachments, RichMedia, XFA, SubmitForm, …) are refused (`422 pdf_not_allowed`). Bytes are kept
  unchanged (the evidence) in private storage (`yyyy/mm/<uuid>.pdf`, mode 0600); served only to
  finance admins as `application/pdf`, `Content-Disposition: inline`, `Cache-Control: private,
  no-store`.
* Uniqueness is enforced by the database: `payment_receipts` has unique indexes on `file_sha256`,
  `(provider, receipt_serial)` and `qr_payload_hash` (NULLS DISTINCT), is append-only, and the
  insert uses `ON CONFLICT DO NOTHING`, so concurrent uploads of one receipt yield exactly one
  row. A receipt (same file, number or QR payload, or a number already recorded as a Kaspi
  payment) used before rejects the new top-up at once: `409 receipt_already_used`, status
  `rejected`, `reject_reason = duplicate_receipt`, system event with the evidence (matched keys,
  earlier receipt/top-up ids, admins only), audit `topup.reject_duplicate_receipt`, `topup_rejected`
  notification. Re-sending the receipt a top-up already has is a no-op.
* Parser (`payments/receipt`). **Calibration: VERIFIED** against a real Kaspi receipt
  (2026-10-17, kept outside the repository; `JT_KASPI_RECEIPT_SAMPLES=<dir> go test -run
  TestCalibrationSamples ./admin/backend/internal/payments/receipt`). Real receipts: PDF 1.7,
  single revision (one `%%EOF`, no `/Prev`, object stream + cross-reference stream), Producer
  `WeasyPrint 70.0`, no Creator/ModDate/XMP, text drawn with `TJ` through Roboto subsets
  (Type0 Identity-H, decoded via ToUnicode), one avatar image, **no QR code** (the Kaspi logo is
  vector art). Text order: «Оплата совершена» · amount · «₸» · seller (ИП "JAN-TAXI") ·
  «№ чека» QR… · «ИИН/БИН продавца» · «ФИО покупателя» · «Дата и время по Астане»
  dd.mm.yyyy HH:MM (UTC+5) · «Оплачено» с Kaspi Gold · «Адрес» · «Чек не является
  фискальным». The unique key is the number after «№ чека» (`^QR\d{8,}$`, upper case, no spaces);
  the payer is stored masked only («Е*** Б.»). Kazakh labels and the Kazakh status line
  («Төлем жасалды», …) are accepted but UNCERTAIN (no Kazakh sample yet; raises `status_kazakh`).
  Images are still scanned for QR codes (gozxing): a payload naming another number blocks
  crediting (`qr_mismatch`), its hash is unique; no QR raises the informational `qr_not_found`.
  Blocking flags: `incremental_update`, `object_redefined`, `trailing_data`,
  `structure_unexpected` (not exactly one `%%EOF`, `startxref` and cross-reference section),
  `xref_inconsistent` (`startxref` or an in-use cross-reference entry does not point at its
  object, an object-stream entry names no real stream, or an object in the file is not listed —
  `pdfdoc.Doc.XRefMismatches`; catches bytes edited in place in a single revision unless the edit
  keeps every offset, e.g. a content stream recompressed to exactly its old length),
  `header_unexpected` (the comment line after `%PDF-1.7` is not WeasyPrint's; qpdf, pikepdf,
  pypdf, editors and print-to-PDF write other bytes),
  `producer_unexpected` (not WeasyPrint*), `creator_present`, `moddate_present`,
  `xmp_metadata_present`, `page_count_unexpected`, `font_unexpected` (not Roboto*),
  `text_undecodable`, `annotations_present`, `form_present`, `serial_*`, `amount_*`,
  `status_missing`, `filename_serial_mismatch` (Kaspi shares `receipt_QR….pdf`). The parser never
  panics, bounds objects/streams/operators/pixels and is fuzzed (`FuzzParse`, `FuzzOpen`).
* Automatic credit only if **all** hold, else `pending_verification` with machine-readable
  reasons (driver sees reasons, never the file flags; admins see both):
  1. receipt not used before (above);
  2. seller BIN printed and = `company.bin` — else `recipient_mismatch`; a receipt without a BIN
     always goes to review (`receipt_incomplete`, informational flag `bin_missing`), plus
     `recipient_mismatch` when its seller name does not contain the distinctive part of
     `kaspi.recipient_name` (falls back to `KASPI_PAY_RECIPIENT_NAME`; normalised, Cyrillic
     look-alikes as Latin);
  3. amount = requested amount — `amount_mismatch`;
  4. `created_at − 10 min ≤ paid_at ≤ now + 5 min` and `now − paid_at ≤ topup.receipt_max_age_hours`
     (48) — `paid_before_topup` / `paid_in_future` / `receipt_too_old`;
  5. parsed completely with no blocking flag — `receipt_incomplete` / `receipt_inconsistent` /
     `tamper_suspected` / `receipt_unreadable`;
  6. per driver and business day (Asia/Aqtobe): count < `topup.auto_max_per_day` (3) and sum ≤
     `topup.auto_max_amount_per_day` (50 000 ₸) — `daily_count_limit` / `daily_amount_limit`;
  7. kill switch `topup.auto_confirm_enabled` (true) — `auto_confirm_disabled`.
* Images (JPEG/PNG/WebP) stay evidence only and never credit (`receipt.kind = image`).
* Residual risk: Kaspi offers no public receipt verification, so a file regenerated exactly like
  WeasyPrint with a new number and time, or another person's genuine receipt claimed first,
  passes the file checks. Limits: the per-driver daily caps, the kill switch, refunds, and finance
  admins reconciling receipt numbers with the Kaspi Pay sales history.
* Admin `GET /admin/topups/{id}` adds `receipt` (latest parsed receipt with flags, decision and
  reasons), `receipts`, `confirm_prefill` (amount, number, paid_at, masked payer) and
  `confirmed_automatically`; `GET /admin/topups/{id}/receipt` serves the latest file (PDF or
  JPEG), older ones via `GET /admin/files/{file_id}`. Driver views carry `receipt`
  `{kind, decision, reasons, receipt_serial, amount, paid_at}`.
* Driver bot: a PDF document in the chat → `getFile` (≤ 5 MiB, token never logged) → internal
  upload (never retried) → reply in the user's language: credited (amount, reference, balance),
  under review with the reasons, already used, or "create a top-up first" with an inline
  `web_app` button to `?startapp=topup`. Photos and non-PDF files get instructions to share the
  PDF from Kaspi. Research: Kaspi documents no public API, webhook or verification URL for Kaspi
  Pay receipts (its "проверка чека" service covers fiscal receipts of Kaspi ОФД — РНМ/ФП — and
  these receipts say «Чек не является фискальным»), so the receipt itself is the evidence.

## 7. Dispatch

Dispatch is **by geolocation radius around pickup point A**, across all of Kazakhstan. Neither the
driver's registered city nor the order's tariff city restricts it.

Eligible driver for a searching order = `driver_profiles.status='approved'` ∧ `is_online` ∧ user not
blocked ∧ no active order ∧ `available >= order.commission_amount` ∧ not the order's own passenger ∧
a **fresh location** (`driver_locations.recorded_at > now() - dispatch.location_max_age_s`) ∧
`ST_DWithin(driver location, order pickup, effective_radius)`.

Effective radius of an order (metres), growing while it searches:

```
effective_radius = min(radius_max_m, radius_base_m + radius_step_m * floor(order_age_s / radius_step_s))
order_age_s      = now() - orders.created_at
```

Global settings (`app_settings`, admin-editable via `GET/PATCH /admin/settings`, typed helper
`settings.Dispatch(ctx, q)`; the per-city columns `cities.dispatch_radius_m`,
`require_driver_location`, `driver_location_max_age_s` are legacy and no longer read):

| Key | Default | Range |
|---|---|---|
| `dispatch.radius_base_m` | 5000 | 500..50000 |
| `dispatch.radius_step_m` | 2500 | 0..20000 |
| `dispatch.radius_step_s` | 60 | 10..600 |
| `dispatch.radius_max_m` | 10000 | 500..100000 and ≥ base (`less_than_base`) |
| `dispatch.location_max_age_s` | 300 | 30..3600 |

With the defaults an order is offered within 5 km, after 1 min within 7.5 km, after 2 min within 10 km.

* **On creation** (same transaction) every eligible driver inside the base radius gets an
  `order_offers` row (with `distance_to_pickup_m`), an `order_available_driver` notification
  (outbox) and an `order.available` WS event.
* **Driver became available** (went online, or sent a fresh location after none or a stale one —
  `drivers.SetAvailability` / `drivers.RecordLocation`, Mini App and bot alike): right after that
  commit `dispatch.OfferNearby` runs the same per-order offer step as the job below for the searching
  orders around the driver, so the bot offer and `order.available` arrive at once.
* **Job `dispatch_expand`** (every 15 s, advisory lock): for each searching order whose search window
  is open, locks the order row (`FOR UPDATE SKIP LOCKED`, so an acceptance in progress is skipped
  until the next run) and offers it to drivers who are eligible inside the *current* effective radius
  and have no offer yet — drivers reached by the growing radius, drivers who came online, moved closer,
  topped up or finished a trip. `UNIQUE(order_id, driver_id)` prevents duplicates; notifications and
  `order.available` go only to the new offers.
* **Available-orders list** (`GET /driver/orders`, bot `/orders`, `GET /driver/orders/{id}`) is a live
  query with the same rules and the effective radius, sorted by distance to the pickup (nearest
  first), each card with `distance_to_pickup_m`. `eligibility.reason` is the first of `not_approved`,
  `offline`, `has_active_order`, `location_required` (no fix), `location_stale` (fix too old), or
  `insufficient_balance` (orders in reach exist but none is affordable). Orders outside the radius
  are simply not listed (`404` for detail and accept).
* **Going online** (`PATCH /driver/availability`, bot availability) requires a fresh location →
  otherwise `422 location_required` (`details.max_age_s`). Drivers send GPS from the Mini App while it
  is in the foreground (`POST /driver/location`) or as a Telegram live location in the driver bot.
* **Admin assignment** skips the online, location and radius checks but keeps approval, active-order
  and balance checks; the response `warnings` lists `offline` and one of `location_required`,
  `location_stale`, `out_of_radius` when the driver could not have taken the order themselves.
* Job `search_expiry`: `searching` orders past `search_expires_at` (created at + `cities.search_timeout_s`
  of the tariff city) → `cancelled` by `system` with reason `no_driver_found`.

## 8. Notifications (transactional outbox)

`notifications.Enqueue(ctx, tx, Notification{Bot, UserID, ChatID, Template, Params, DedupeKey})`
inserts into `notification_outbox` inside the business transaction (`ON CONFLICT (dedupe_key)
DO NOTHING`). Worker: claims rows with `FOR UPDATE SKIP LOCKED` + lease (`locked_until`),
renders the template in the user's language, sends via Bot API, records
`notification_attempts`; on 429 waits `retry_after`; on 5xx/network exponential backoff with
jitter (max 8 attempts → `dead`); on 403 (bot blocked / never started) → `dead` and marks
`users.<bot>_bot_blocked_at`. Admins can see and retry outbox rows. Global send rate ≤ 25 msg/s
per bot, ≤ 1 msg/s per chat.

Templates (keys; `notifications/templates.go` lists their params): `order_created_passenger`,
`order_available_driver`, `order_accepted_passenger`, `order_accepted_driver`,
`order_driver_arriving`, `order_started`, `order_stop_completed`, `order_completed_passenger`,
`order_completed_driver`, `order_cancelled_passenger`, `order_cancelled_driver`, `order_no_driver`,
`moderation_submitted`, `moderation_review_required`, `moderation_approved`,
`moderation_changes_requested`, `moderation_rejected`, `driver_suspended`, `driver_restored`,
`topup_created`, `topup_confirmed`, `topup_rejected`, `wallet_low_balance`, `dispute_opened`,
`dispute_resolved`, `phone_verified`, `driver_welcome_bonus` (§22.1), `driver_bonus_granted`
(§22.7); offer edits `order_offer_won`, `order_offer_taken`, `order_offer_cancelled`,
`order_offer_expired` (§22.4); marketing (§23) `promo_coupon_issued` (`amount`, `validity`; button
`coupons`), `promo_referral_reward` (`amount`; button `bonus`) and `promo_bonus_granted` (`amount`,
`comment?`; button `bonus`). A passenger's promotion (§23.6) adds optional params: `discount` and
`cash_due` on `order_created_passenger` and `order_completed_passenger`; `cash_due` and
`promo_compensation` on `order_available_driver`, `order_accepted_driver` and
`order_completed_driver`. A line that names an absent optional param is dropped, so orders without
a promotion read as before. Marketing broadcasts never use this outbox (§23.7).
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
* Scopes: `user:<user_id>` (passenger), `driver:<driver_id>`, `admin` (all admin sessions), and
  `drivers` (every approved driver's app; carries only `order.unavailable {order_id, status}`, sent
  when a searching order is taken, cancelled or expires, so its card leaves every open list — also
  lists of drivers who never held an offer for it). The driver app refreshes its list ~100–300 ms
  after `order.available` (jittered), drops cards on `order.taken` / `order.unavailable`, re-reads
  state on the first connection and every resync, and polls only as a fallback (20 s while the
  socket is down, 60 s while it is up).
* WS endpoint `GET /api/v1/ws`. First client frame within 5s: `{"type":"auth","token":"<app token>"}`
  (admins are authenticated by cookie and skip this). Server pings every 25s; client
  reconnects with backoff 1s→30s and re-syncs via REST on reconnect / `visibilitychange` /
  Telegram `activated`.
* Event types: `order.created`, `order.available`, `order.taken`, `order.unavailable`, `order.accepted`, `order.cancelled`,
  `order.driver_arriving`, `order.started`, `order.stop_completed`, `order.completed`,
  `order.disputed`, `driver.location_updated`, `driver.status_changed`, `wallet.updated`,
  `topup.updated`, `topup.confirmed`, `moderation.submitted`, `moderation.changes_requested`,
  `moderation.approved`, `moderation.rejected`, `driver.phone_verified`, `promotions.updated`.
* `promotions.updated` (scope `user:<user_id>`, data `{user_id}`) tells the passenger app that its
  coupons, bonus balance or referral state changed; the app re-reads `GET /promotions` (§23.11).

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
  `iin_taken`, `phone_not_verified`, `registration_incomplete`, `too_many_requests`,
  `payment_reference_used`, `internal`.
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
* Names (`first_name`, `last_name`, optional `patronymic`): cleaned like free text, at most 60 code
  points, letters of any script with spaces, `-`, `'` and `.` between them («Әлі-Ахмет», «O'Connor»).
* Phone: KZ mobile only, E.164 `+77XXXXXXXXX`; accepts `8 7xx…`, `+7 7xx…`, `7xx…`.
* Plate: uppercase, strip separators (spaces, dashes, dots, …), map Cyrillic look-alikes to Latin,
  drop a leading `KZ` country mark, validate `^\d{3}[A-Z]{2,3}(0[1-9]|1\d|20)$` (current format) or
  `^[A-Z]\d{3}[A-Z]{2,3}$` (1993–2012 format, still legal) — see `platform/plate`. The driver Mini
  App checks the same formats before saving.
* IIN (ЖСН, `platform/iin`): white space dropped, then exactly 12 digits, kept as a string (leading
  zeros matter). Check digit: the sum of the first 11 digits weighted 1…11, modulo 11; when that is
  10, the sum again with weights 3…11, 1, 2, modulo 11, and a second 10 means the number is never
  issued; the 12th digit must equal the result. Reasons `invalid` (not 12 digits) and `checksum`.
  The driver Mini App validates live with the same rule.
* Driver registration requires a Telegram-verified phone: the Mini App calls
  `WebApp.requestContact()`, Telegram sends the contact to the driver bot, the bot verifies
  `contact.user_id == from.id` and posts it to the API.

### 11.1 Driver registration

Customer assignment «ТЕХНИКАЛЫҚ ТАПСЫРМА» (30.10.2026) and the owner's decision of 31.10.2026: two
pages, no document scans, no manual approval.

* Driver Mini App, `/register?step=…` (the Telegram BackButton goes from page 2 back to page 1):
  1. `personal` — profile photo (camera or gallery, JPEG/PNG/WebP, downscaled on the device), Аты,
     Тегі, optional Әкесінің аты under the surname, ЖСН (digits only, numeric keyboard, live
     check-digit validation) and the phone shared through Telegram (above). «Келесі» opens page 2
     once everything required on page 1 is valid.
  2. `vehicle` — one body-type row preset to Sedan (icon and name; a tap lists the active classes,
     §21, and seats and cargo volume take the chosen class's defaults), make and model, colour,
     plate, and one personal-data consent checkbox above «Тіркелуді аяқтау» (consent text version
     `driver-pd-2026-10-v3`).

  Fields autosave (`PUT /driver/registration`); `iin` is sent only once all 12 digits are typed and
  the check digit is right (an invalid patronymic is held back the same way), so a half-typed value
  never fails a save and is never overwritten by the saved answer.
* Required at submission (`missing_fields`, in this order): `avatar`, `first_name`, `last_name`,
  `identity` (a valid IIN; the patronymic is optional), `phone` (always: it is how passengers call
  the driver; the setting `driver.require_phone_verification` is read-only and unused),
  `vehicle_class`, `vehicle_make_model`,
  `vehicle_color`, `vehicle_plate`. ID scans and vehicle photos are no longer collected (§12).
* `POST /driver/register` runs the automatic checks — everything required, the consent, the IIN,
  phone and plate not held by another live driver (`409 iin_taken` / `phone_taken` /
  `plate_taken`; the partial unique indexes decide simultaneous claims) — and approves in the same
  transaction with the system as the actor: the application round is recorded, then the same path
  as an administrator's approval runs (welcome bonus §22.1, `moderation_approved` message, audit,
  events). After success the app opens the Orders tab at once (the session takes the approved
  status from the answer, so the realtime `moderation.approved` hint cannot send the driver to Home
  first) and meanwhile reads a fresh GPS fix, sends it and goes online
  (`PATCH /driver/availability`); with a denied location or a refused "online" the driver stays on
  Orders, where the usual location and online prompts take over. A refused submission jumps to the
  page of the first missing field. Administrators can still suspend, restore and change the city.
* Applications from before: a `pending` one can still be approved, rejected or sent back for
  corrections by an administrator. In `changes_requested` the app shows only the pages with flagged
  fields (unflagged fields locked); the IIN and patronymic stay editable while no IIN is stored;
  finishing the corrected registration approves it automatically. `id_front`, `id_back` and
  `vehicle_photos` remarks are shown, but nothing can be uploaded for them any more.
* Approved drivers without an IIN keep working; the Profile page shows a non-blocking «ЖСН
  енгізіңіз» row that stores it once (`PUT /driver/profile/iin`). A stored IIN is shown masked.
* Data (migration 00013): `driver_profiles.iin` — plain `text`, `CHECK (iin ~ '^[0-9]{12}$')`,
  `NULL` until entered, unique among live profiles (partial unique index
  `driver_profiles_iin_unique` over `pending`, `approved`, `changes_requested`, `suspended`; drafts
  and rejected profiles never hold one); `driver_profiles.patronymic` — `text NOT NULL DEFAULT ''`,
  ≤ 60. Only the driver's own registration returns the IIN in full; the driver's profile, the admin
  panel and application snapshots carry `iin_masked` (`********1234`); it never goes into audit
  payloads, logs, notifications, realtime events, bot offers or passenger payloads
  ([DATA_PROTECTION.md](DATA_PROTECTION.md)).

## 12. Files

* Private volume `FILE_STORAGE_DIR` (default `/data/files`), never served statically.
* Uploads: max 10 MiB, magic-byte sniffing (JPEG/PNG/WebP), decoded and re-encoded to JPEG
  (strips EXIF incl. GPS), longest side ≤ 2000px, SHA-256 stored.
* Driver registration uploads only the profile photo (`kind=avatar`, ≤ 5 MiB; a new one replaces
  the current one, the old file stays as history). ID scans and vehicle photos are no longer
  accepted (§11.1); the ones uploaded before 31.10.2026 stay stored and appear read-only in the
  driver's registration and the admin panel, with the access rules below.
* Kaspi receipt PDFs (§6.1): `%PDF-` magic, ≤ 5 MiB, encrypted or active-content files refused,
  stored byte for byte (the evidence, mode 0600), served only to finance admins
  (`/admin/topups/{id}/receipt`, `/admin/files/{id}`) as `application/pdf` inline.
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
* Health: `GET :8090/healthz` → 200 for the lease holder while it is not stalled and made progress
  (a successful poll) within 90 s (waiting out Telegram's `retry_after` does not count); a standby
  is healthy only while its lease checks succeed. JSON adds `consecutive_failures`, `last_error`,
  `stalled`, `stalls`, `abandoned_handlers`. A bot unhealthy for 5 minutes exits with an error so
  the supervisor restarts it.
* Network resilience (`pkg/telegram`, `pkg/botapi`): HTTP/1.1-only transports with dial, TLS,
  response-header and idle timeouts, a separate pool for `getUpdates`, a hard deadline on every
  call (`getUpdates` timeout + 15 s, other calls 15 s; internal API 10 s, uploads 20 s). A call
  still running past its limit is abandoned and the connections are rebuilt
  (`ResetConnections`), as after 2 network failures in a row; a handler that ignores its
  context is abandoned 15 s after its 20 s timeout.
* Menu button: only the default menu button, published on every lease acquisition with the
  current Mini App URL; per-chat buttons are reset to the default on a chat's first update
  (`runtime.ChatMenus`).
* Bot UI is Kazakh by default; language stored in PostgreSQL via the API.
* Driver bot and dispatch (§7): a shared Telegram live location (and its edits) is the bot's GPS
  feed (`POST /internal/v1/driver/{tg_id}/location`). When going online or listing orders fails with
  `location_required` / `location_stale`, the bot explains both ways to send GPS — live location in
  the chat (📎 → Location → Share My Live Location) or opening the Mini App (inline `web_app` button)
  and allowing location access — in kk/ru/en.

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
`notification_worker` (1s), `search_expiry` (15s), `dispatch_expand` (15s, §7), `topup_expiry` (5m),
`quote_cleanup` (1h), `stale_driver_offline` (5m, offline after `DRIVER_ONLINE_IDLE_TIMEOUT` without heartbeat/location),
`bot_update_cleanup` (1h, keep 7 days), `location_history_retention` (1d, keep 30 days),
`session_cleanup` (1h); marketing (§23.12): `marketing_events` (2s, the award worker),
`marketing_coupon_expiry` (1m), `marketing_campaign_schedule` (30s), `marketing_grants` (5s),
`marketing_reconcile` (1h). The marketing `broadcast_worker` runs continuously next to the
notification worker (only with `NOTIFICATION_WORKER_ENABLED`), on one replica at a time (advisory
lock).

## 17. Configuration

All configuration is environment variables (see `.env.example`); DB `app_settings` override the
runtime-editable ones (support contacts, Kaspi public payment settings, top-up limits,
prohibited items, min online balance, dispatch radius and location freshness `dispatch.*`, the
marketing switches and broadcast limits `marketing.*`, §23.9).
Secrets never live in the repo or the database.

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
| passenger | `coupons` | promotions: the passenger's coupons (§23.11; button of `promo_coupon_issued`) |
| passenger | `bonus` | promotions: bonus balance and its history (button of `promo_referral_reward`, `promo_bonus_granted`) |
| passenger | `invite` | «Досыңды шақыр» — invite a friend (§23.5) |
| passenger | `ref_<CODE>` | a friend's invitation link `?startapp=ref_<CODE>`: the API captures the referral from the signed `initData.start_param` at sign-in (§23.5); the app opens like `home`. The bot link `?start=ref_<CODE>` is captured by the passenger bot |
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
| POST | /orders | P | orders | `{quote_id, fare, payment_method, comment?, cargo_description?, recipient_phone?, prohibited_items_ack?, promotion?: coupon\|bonus\|none, promotion_discount?, idempotency_key}` → Order (201, or 200 on idempotent replay); `409 promotion_changed {reason, discount}` when the server would apply another discount (§23.6) |
| GET | /orders?scope=active\|history&limit=&before= | P | orders | cursor list of OrderSummary |
| GET | /orders/{id} | P | orders | Order: stops, route snapshot, status events, driver+vehicle when assigned, `driver_location` while active |
| POST | /orders/{id}/cancel | P | orders | `{reason?}` |
| POST | /orders/{id}/rating | P | orders | `{rating 1..5, comment?}` completed orders only, once |
| POST | /orders/{id}/dispute | P | orders | `{reason}` completed (≤72h) or in_progress |
| GET | /orders/{id}/driver-photo | P | drivers | assigned driver avatar (JPEG) |

### Promotions (passenger, §23.11)
| Method | Path | Auth | Owner pkg | Notes |
|---|---|---|---|---|
| GET | /promotions | P | marketing | `PassengerPromotions`: `server_time`, `redemptions_enabled`, coupons (status computed against the database clock), `bonus {balance, reserved, available}`, `bonus_categories`, `offers` (the first-ride offer while it can still be earned), `banners`, `referral {state, lock_reason?, reward_amount, rewarded}`, `marketing_subscribed` |
| GET | /promotions/bonus/history?limit=&before= | P | marketing | cursor list `{id, type: grant\|spend\|adjustment_credit\|adjustment_debit, amount (signed), order_number, created_at}`; reservations are not shown |
| GET | /promotions/referral | P | marketing | `ReferralView`: state and copy; while unlocked the code (created on first view) and the links `https://t.me/<bot>?start=ref_<CODE>` / `?startapp=ref_<CODE>`; `earned`; up to 50 friends (first name + last initial, `attributed\|rewarded\|capped\|not_counted`) |
| PUT | /promotions/subscription | P | marketing | `{subscribed}` → `{marketing_subscribed}`; marketing broadcasts only, order messages unaffected |

### Driver
| Method | Path | Auth | Owner pkg | Notes |
|---|---|---|---|---|
| GET | /driver/registration | D | drivers | draft/current data (names, patronymic, own IIN unmasked, avatar, vehicle and class), status, `corrections[]`, `missing_fields`, `can_submit`, `phone_verified`, consent; ID photos and vehicle photos uploaded before 31.10.2026 read-only (§11.1) |
| PUT | /driver/registration | D | drivers | `{first_name?, last_name?, patronymic?, iin?, vehicle?:{make?, model?, color?, plate?, vehicle_class?, seats?, cargo_volume_l?}, location?}`: a key not sent keeps its value (the legacy `identity` object is still accepted); the first call creates the profile (code assigned, vehicle preset to `sedan`); `409 iin_taken` / `plate_taken`, `422 fields.iin = invalid\|checksum` |
| POST | /driver/registration/files | D | drivers | multipart `kind=avatar` + `file` (the only registration upload, ≤ 5 MiB) |
| POST | /driver/registration/check | D | drivers | `{plate?}` advisory availability |
| POST | /driver/register | D | moderation | automatic checks, then approval in the same transaction → `approved` (§11.1); `409 iin_taken\|phone_taken\|plate_taken`, `422 registration_incomplete` (`details.missing_fields`), `phone_not_verified`, `validation_failed` (`pd_consent*`) |
| GET | /driver/files/{id} | D | drivers | own files only |
| GET | /driver/profile | D | drivers | profile (patronymic, `iin_masked`), vehicle, rating, trips, status |
| PATCH | /driver/profile | D | drivers | non-moderated fields only (none in MVP besides language via /me) → 422 for moderated fields |
| PUT | /driver/profile/iin | D | drivers | `{iin}` once for a driver without one (Profile «ЖСН енгізіңіз»); the same value again is a no-op; `409 conflict` (`details.reason = iin_already_set`), `409 iin_taken`; → profile with `iin_masked` |
| PATCH | /driver/availability | D | drivers | `{online}`; checks approved, not suspended, available ≥ `driver.min_online_balance`, fresh location (`422 location_required`, `details.max_age_s`) |
| POST | /driver/location | D | drivers | `{lat,lon,accuracy_m?,heading?,speed_mps?,recorded_at?}`; must be inside KZ; rate-limited 1/2s |
| GET | /driver/orders | D | dispatch | available orders within the effective radius, nearest first (cards with `distance_to_pickup_m`) + `eligibility` |
| GET | /driver/orders/active | D | orders | current order or 204 |
| GET | /driver/orders/history?limit=&before= | D | orders | |
| GET | /driver/orders/{id} | D | orders | available order detail (if eligible, or offered to the driver and still open — the accept then says what to fix) or own order |
| POST | /driver/orders/{id}/accept | D | dispatch | atomic; 409 `order_already_taken`, 402 `insufficient_balance`, 422 `location_required`, 404 outside the radius |
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
| POST | /driver/wallet/topups/{id}/receipt | D | payments | multipart `file`: Kaspi PDF (parsed, may credit at once, §6.1) or image (evidence only); 409 `receipt_already_used` |
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
| POST | /admin/orders/{id}/assign `{driver_id}` (response `warnings`: `offline`, `location_required`, `location_stale`, `out_of_radius`) | orders.manage | dispatch |
| POST | /admin/orders/{id}/dispute `{reason}` | orders.manage | orders |
| GET | /admin/disputes?status= | orders.view | orders |
| POST | /admin/disputes/{id}/resolve `{outcome, resolution, refund_commission}` | orders.manage | orders |
| GET | /admin/drivers?status=&city_id=&online=&q=&page= | drivers.view | moderation |
| GET | /admin/drivers/{id} (patronymic, `iin_masked`; legacy ID photos only with documents.view) | drivers.view | moderation |
| GET | /admin/drivers/{id}/orders | drivers.view | orders |
| GET | /admin/drivers/{id}/ledger | finance.view | payments |
| GET | /admin/drivers/locations?city_id= | drivers.view | drivers |
| GET | /admin/files/{id} | documents.view (ID docs) / drivers.view (others) | drivers |
| POST | /admin/drivers/{id}/approve `{comment?}` (applications still pending from before 31.10.2026) | drivers.moderate | moderation |
| POST | /admin/drivers/{id}/reject `{reason, comment?}` (older `pending` / `changes_requested` applications) | drivers.moderate | moderation |
| POST | /admin/drivers/{id}/request-corrections `{items:[{field,reason,comment?}], comment?}` (older `pending` applications) | drivers.moderate | moderation |
| POST | /admin/drivers/{id}/suspend `{reason}` | drivers.manage | moderation |
| POST | /admin/drivers/{id}/restore | drivers.manage | moderation |
| PATCH | /admin/drivers/{id} `{city_id}` | drivers.manage | moderation |
| POST | /admin/drivers/{id}/wallet/adjustments `{amount (signed), reason, idempotency_key}` | finance.manage | payments |
| POST | /admin/drivers/{id}/bonus `{amount, comment?, idempotency_key}` (§22.7; ledger `bonus_credit`, bot message `driver_bonus_granted`) | finance.manage | payments |
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
| GET/POST | /admin/commission-areas ; PATCH/DELETE /admin/commission-areas/{id} ; POST …/check ; GET …/resolve, …/suggest, …/search (§22.9) | config.manage | commissionareas |
| GET | /admin/tariffs?city_id=&vehicle_class= ; POST /admin/tariffs (new version, `vehicle_class`) | dashboard.view / config.manage | pricing |
| GET/POST | /admin/vehicle-classes ; PATCH /admin/vehicle-classes/{code} (§21) | config.manage | vehicleclasses |
| GET/PATCH | /admin/settings | config.manage | settings |
| GET | /admin/notification-templates ; PUT /admin/notification-templates/{key}/{lang} ; DELETE (revert) | config.manage | notifications |
| GET | /admin/notifications?status=&page= ; POST /admin/notifications/{id}/retry | orders.manage | notifications |
| GET | /admin/audit-logs?admin_id=&entity_type=&entity_id=&page= | audit.view | audit |
| GET | /admin/marketing/overview (switches, campaigns with usage, counts, health; §23.10) | marketing.view | marketing |
| GET | /admin/marketing/campaigns?status=&template=&q=&page= ; GET /admin/marketing/campaigns/{id} (with versions) ; GET /admin/marketing/campaigns/{id}/preview (audience, liability, warnings) | marketing.view | marketing |
| POST | /admin/marketing/campaigns `{template, key?, title, description?, rules, starts_at?, ends_at?, budget_limit?, priority?, exclusivity_group?, change_note?}` ; POST /admin/marketing/campaigns/{id}/duplicate | marketing.edit | marketing |
| POST | /admin/marketing/campaigns/{id}/versions (drafts and ended: marketing.edit; scheduled, active, paused and the system campaigns: marketing.approve) ; POST /admin/marketing/campaigns/{id}/transition `{action, reason?, confirm, scheduled_at?, expected_version}` (`expected_version` with approver actions; system campaigns: marketing.approve for every action; §23.2) | marketing.edit or marketing.approve, per action | marketing |
| PATCH | /admin/marketing/campaigns/{id}/budget `{budget_limit \| unlimited: true, reason, confirm}` | marketing.approve | marketing |
| GET | /admin/marketing/coupons?campaign_id=&user_id=&status=&page= | marketing.view | marketing |
| POST | /admin/marketing/coupons/{id}/void `{reason}` (unused coupons only) | marketing.approve | marketing |
| GET | /admin/marketing/bonus-ledger?user_id=&type=&page= | marketing.view | marketing |
| GET | /admin/marketing/users?q= (Telegram id, phone, referral code; masked) ; GET /admin/marketing/users/{id} | marketing.view | marketing |
| POST | /admin/marketing/users/{id}/bonus-adjustments `{amount (signed), reason, idempotency_key, confirm}` | finance.manage | marketing |
| GET | /admin/marketing/referrals?status=&referrer_id=&page= | marketing.view | marketing |
| POST | /admin/marketing/referrals/{id}/review `{decision: approve\|reject, reason}` | marketing.approve | marketing |
| GET | /admin/marketing/applications?status=&review=true&page= (promotions on orders) | marketing.view | marketing |
| POST | /admin/marketing/applications/{id}/reverse `{reason, confirm}` | finance.manage | marketing |
| GET/POST | /admin/marketing/segments ; PATCH /admin/marketing/segments/{id} | marketing.view (GET) / marketing.edit | marketing |
| POST | /admin/marketing/segments/preview `{audience, filters}` (counts only) | marketing.view | marketing |
| GET | /admin/marketing/broadcasts?status=&page= ; GET /admin/marketing/broadcasts/{id} ; GET /admin/marketing/broadcasts/{id}/recipients?status=&page= | marketing.view | marketing |
| POST/PATCH | /admin/marketing/broadcasts ; PATCH /admin/marketing/broadcasts/{id} ; POST …/{id}/test `{telegram_user_id}` (ids in `marketing.test_recipients` only) ; POST …/{id}/transition `{action, scheduled_at?, reason?, confirm}` | marketing.broadcast (+ marketing.approve above `marketing.broadcast_approval_threshold`) | marketing |
| GET | /admin/marketing/reconciliation | marketing.view | marketing |
| GET | /admin/marketing/reports?from=&to= ; GET /admin/marketing/reports.csv | marketing.view (+ reports.export for the CSV) | marketing |
| GET/PATCH | /admin/marketing/settings (`marketing.*`, PATCH with a `reason`) | marketing.view / marketing.approve | marketing |

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
| POST | /internal/v1/driver/{tg_id}/orders/{id}/accept | botapi+dispatch | «✅ Тапсырысты алу» (§22.4): the same atomic accept as the Mini App (`dispatch.AcceptOrder`) → `{order, replayed, charged, wallet}`; 402/404/409/422 as the Mini App |
| POST | /internal/v1/passenger/{tg_id}/contact | botapi | `{phone, contact_user_id}` shared with the passenger bot (§22.5); verifies `contact_user_id == tg_id` |
| POST | /internal/v1/driver/{tg_id}/location | botapi | `{lat, lon, accuracy_m?, live}` |
| POST | /internal/v1/driver/{tg_id}/topup-receipt | botapi+payments | multipart `file` (Kaspi PDF) → `{outcome: credited\|pending_verification\|duplicate_rejected, reasons[], topup, balance}`; 409 `no_open_topup` / `receipt_already_used`, 413, 422, 429 (§6.1) |
| GET | /internal/v1/config | botapi | Mini App URLs, bot usernames, support contacts |
| POST | /internal/v1/passenger/{tg_id}/referral | marketing | `{payload: "ref_<CODE>"}` from `/start` in the passenger bot (passenger bot only, else 403) → `{attributed, outcome: attributed\|duplicate\|rejected, reason?}` (§23.5); 404 for an unknown user |
| POST | /internal/v1/bots/{bot}/users/{tg_id}/marketing | marketing | `{opt_out}` from a broadcast's unsubscribe button (`mkt_off`, both bots) → `{marketing_subscribed}` (§23.7) |

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
settings.Dispatch(ctx, q) settings.DispatchSettings  // RadiusBaseM, RadiusStepM, RadiusStepS, RadiusMaxM, LocationMaxAgeS; RadiusAt(age)
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
cities.Resolve(ctx, q, p geo.Point) (cities.City, error)  // smallest active zone covering p, else the default region KAZ

// platform/textutil
textutil.CleanText(s string, max int) (string, error)     // ErrTooLong; NFC, control chars stripped
textutil.Len(s string) int                                 // code points after NFC

// platform/phone, platform/plate
phone.Normalize(s string) (string, error)                  // "+77XXXXXXXXX"
plate.Normalize(s string) (string, error)

// platform/storage
(*storage.Store).SaveImage(ctx, q, owner uuid.UUID, purpose string, r io.Reader) (storage.File, error)
(*storage.Store).Open(ctx, q, id uuid.UUID) (storage.File, io.ReadCloser, error)
storage.ServeImage(w, r, f storage.File, rc io.ReadCloser)  // private, no-store; PDFs go to ServePDF
(*storage.Store).CheckPDF(data []byte) error                // magic, size, encryption, active content
(*storage.Store).SavePDF(ctx, q, owner uuid.UUID, purpose string, data []byte) (storage.File, error) // bytes kept as is, 0600
storage.ServePDF(w, r, f storage.File, rc io.ReadCloser)    // application/pdf, inline, private, no-store
storage.ServeFile(w, r, f storage.File, rc io.ReadCloser)   // image or PDF by stored type

// platform/pdfdoc (defensive reader, bounded by Limits)
pdfdoc.Open(data []byte, lim pdfdoc.Limits) (*pdfdoc.Doc, error)
(*pdfdoc.Doc).XRefMismatches() int                          // startxref/xref entries not matching the bytes
(*pdfdoc.Doc).ActiveContent() []string ; HasForm() bool ; HasXMPMetadata() bool
(*pdfdoc.Doc).Pages() []pdfdoc.Page ; ExtractText(p) (pdfdoc.PageText, error) ; PageImages(p, limit)

// pkg/telegram, pkg/botapi (bots and the notification worker)
(*telegram.Client).GetFile(ctx, fileID string) (*telegram.File, error)
(*telegram.Client).DownloadFile(ctx, filePath string, maxBytes int64) ([]byte, error) // token never in errors
(*telegram.Client).ResetChatMenuButton(ctx, chatID int64) error
(*telegram.Client).ResetConnections()                       // rebuild transports after a stall
(*botapi.Client).UploadTopUpReceipt(ctx, tgID int64, fileName string, data []byte) (botapi.TopUpReceiptResult, error) // never retried
(*botapi.Client).ResetConnections()
(*botapi.Client).CaptureReferral(ctx, tgID int64, payload string) (botapi.ReferralResult, error) // "/start ref_<code>", retried
(*botapi.Client).SetMarketingOptOut(ctx, tgID int64, optOut bool) error                         // broadcast button mkt_off

// marketing (§23) — called by orders/lifecycle inside their transactions
marketing.ParseRequest(kind *string, discount *int64, fields map[string]string) marketing.PromotionRequest
marketing.PlanOrder(ctx, tx, userID uuid.UUID, category string, fare int64, req PromotionRequest) (marketing.Plan, error) // 409 promotion_changed
marketing.Reserve(ctx, tx, plan marketing.Plan, orderID uuid.UUID) error            // order creation, before offers
marketing.SettleOrder(ctx, tx, orderID, driverID uuid.UUID) (compensation int64, err error) // completion, once
marketing.RecordRideCompleted(ctx, tx, orderID uuid.UUID, completedAt time.Time) error   // completion, award queue
marketing.ReleaseOrder(ctx, tx, orderID uuid.UUID, reason string) (released int64, err error) // every cancellation
marketing.LoadOrderPromotion(ctx, q, orderID uuid.UUID) (*marketing.OrderPromotion, error)
wallets.CreditPromoCompensation(ctx, tx, driverID, orderID uuid.UUID, amount int64, key string) error // promo:<application_id>
wallets.ReversePromoCompensation(ctx, tx, driverID, orderID uuid.UUID, amount int64, key string, adminID uuid.UUID, reason string) error
dispatch.PromoParams(params map[string]any, fare, discount int64)   // cash_due, promo_compensation in driver messages
settings.Marketing(ctx, q) settings.MarketingSwitches               // Awards, Redemptions, Broadcasts
(*auth.Service).OnPassengerStart(h auth.StartHook)                   // signed initData start_param of passenger sign-ins

// audit
audit.Log(ctx, q db.Querier, r *http.Request, audit.Entry{Action, EntityType, EntityID string, Before, After any}) error
```

Wiring: `internal/app` owns construction. Each domain area has a file `internal/app/wire_<area>.go`
(created as stubs) that only that area's owner edits; `app.go` calls them.

## 21. Vehicle classes (body types)

Product owner requirement (2026-10-19): drivers declare their vehicle body type at registration;
passengers choose the class they need (e.g. 7–8 people → minivan, not a sedan); parcels go only
to vehicles whose cargo space fits; admins manage classes, capacities and tariffs.

### 21.1 Classes (seeded, admin-editable — table `vehicle_classes`)

| code | kk / ru / en | passenger seats default / max | cargo volume default (L) | passengers | cargo | serves orders of |
|---|---|---|---|---|---|---|
| `sedan` | Седан / Седан / Sedan | 4 / 4 | 400 | ✓ | ✓ | sedan |
| `universal` | Универсал / Универсал / Estate | 4 / 4 | 600 | ✓ | ✓ | universal, sedan |
| `minivan` | Минивэн / Минивэн / Minivan (Alphard, Sienna) | 7 / 8 | 800 | ✓ | ✓ | minivan |
| `microbus` | Микроавтобус / Микроавтобус / Minibus | 15 / 20 | 1500 | ✓ | ✓ | microbus |
| `cargo_van` | Жүк фургоны / Грузовой фургон / Cargo van | 2 / 2 | 6000 | — | ✓ | cargo_van |
| `truck` | Жүк көлігі / Грузовик / Truck | 2 / 2 | 15000 | — | ✓ | truck |

Columns: `code` (PK, `^[a-z_]{2,20}$`), `name_kk/ru/en`, `examples` (short text, e.g. "Toyota Camry"),
`seats_default`, `seats_max`, `cargo_volume_l_default`, `passenger_capable`, `cargo_capable`,
`serves text[]` (class codes whose orders this class may take; always includes itself), `icon`
(`sedan|universal|minivan|microbus|cargo_van|truck`), `sort`, `is_active`.

### 21.2 Data model
- `vehicles`: `class_code` (FK, required at submit), `seats` (1..class.seats_max), `cargo_volume_l`
  (0..100000). These are moderated registration fields (correction field `vehicle_class`).
- `tariffs`: gain `vehicle_class`; one active tariff per (city, category, vehicle_class). Existing
  tariffs become `sedan`; defaults seeded for the other classes (step 50 ₸, 10% commission):
  universal 600–1800, minivan 1000–3000, microbus 2000–6000, cargo_van 2000–8000, truck 5000–20000.
- `fare_quotes` and `orders`: `vehicle_class` (required), `passenger_count` (taxi: 1..15),
  `cargo_size` (`s|m|l|xl` for parcel/other) and `cargo_volume_l` (derived: s=10, m=60, l=400, xl=1500).

### 21.3 API
- `POST /routes/preview` takes `{category, stops, passenger_count?, cargo_size?}` and returns the
  route once plus `options[]`: one per active class able to carry the request
  (`seats_max ≥ passenger_count` for taxi; `cargo_volume_l_default ≥ cargo volume` and cargo-capable
  for parcel/other), each `{vehicle_class, name{kk,ru,en}, examples, icon, seats_max,
  cargo_volume_l, quote_id, fare{recommended,min,max,step,options}, tariff_region, eta_s|null}`; classes
  that cannot carry the request are omitted. `POST /orders` uses the chosen option's `quote_id`.
  `eta_s` is deliberately coarse so that moving pickup A around cannot locate drivers: the pickup is
  snapped to a ~500 m grid, each driver's position is shifted by a secret per-driver offset (≤ 500 m,
  new every hour), only shifted positions inside `dispatch.radius_base_m` count, and the estimate
  (1 min + 2 min/km) is rounded up to 5-minute steps.
- `GET /public/config` exposes the active classes (for registration and the passenger UI).
- Admin: `GET/POST /admin/vehicle-classes`, `PATCH /admin/vehicle-classes/{code}` (config.manage, audited);
  tariffs endpoints gain `vehicle_class`.

### 21.4 Dispatch
Eligibility (§7) additionally requires the driver's current vehicle to serve the order's class
(`order.vehicle_class ∈ class(vehicle).serves`), `vehicle.seats ≥ order.passenger_count`, and
`vehicle.cargo_volume_l ≥ order.cargo_volume_l`.

### 21.5 UI
- Driver registration (§11.1, page 2): one «Көлік түрі» row preset to Sedan with the class icon and
  name; a tap lists the active classes (icon, name, examples, capacity) and seats and cargo volume
  take the chosen class's defaults.
- Passenger: after the route, a compact iOS list of class options (icon, name, "4 орын", price),
  a passenger-count stepper for taxi and a cargo-size segmented control for parcels; changing them
  re-quotes. The existing passenger flow stays as it is otherwise.
- Admin: Vehicle classes page, class column on tariffs, class/seats/volume on driver detail.

## 22. Business rules v2 (owner decisions, 2026-10-19)

These rules supersede §5 (driver cancellation), §6 (commission timing/amount) and §8 (driver
offers) where they conflict.

### 22.1 Welcome balance for new drivers
- Setting `driver.welcome_bonus` (KZT, default **500**; 0 disables).
- Granted automatically **once**, when a driver is approved for the first time — at submission,
  by the automatic approval (§11.1), or by an administrator approving an application still pending
  from before — and only if neither the driver's phone (E.164) nor the vehicle plate (normalised)
  has ever received it:
  table `welcome_bonus_grants` with `UNIQUE(driver_id)`, `UNIQUE(phone_e164)`, `UNIQUE(plate_normalized)`.
- Ledger entry type `bonus_credit`, idempotency key `welcome_bonus:<driver_id>`; audited;
  notification `driver_welcome_bonus`.

### 22.2 Commission is charged at acceptance
- Settings `commission.mode` = `fixed` (default) | `percent`; `commission.fixed_amount` (KZT, default **50**);
  `percent` uses the tariff's `commission_bps`. The amount is snapshotted on the order at creation.
- The full commission is charged in the **same transaction** as the atomic accept (ledger
  `commission_charge`, key `charge:<order_id>`); no reservation for new orders.
  Insufficient available balance → `402 insufficient_balance` and the order stays open for others.
- **Not refunded** when the passenger cancels — never automatically, at any moment after acceptance
  (owner decision 2026-10-19: prevents a driver and a passenger from colluding to cancel and get the
  commission back). Refund only through an explicit admin action with the refund option ticked
  (admin cancel, admin reassign `refund_previous`, dispute resolution with `refund_commission=true`;
  ledger `commission_refund`, audited). Do not add grace-period or automatic refunds.
- A driver whose available balance is below the commission stops receiving offers automatically
  and gets one `wallet_low_balance` notification with a top-up button.
- **Drivers never see the commission** (owner, 2026-10-31 and 2026-11-01): neither the word nor an
  amount that is the commission — not on the order cards, the order screen, the trip screen, the
  trip history, the completion screen (only «Сапар сәтті аяқталды»), the wallet (a neutral line
  «Тапсырыстар келуі үшін балансыңызды толтырып отырыңыз»; «orders paused» without an amount), the
  bot offers, `/orders`, the trip card, the completion message or the low-balance message (available
  balance and the top-up button only). A 402 at «Тапсырысты алу» says to top up, without the amount
  needed. The wallet history keeps the amounts so the balance adds up, labelled neutrally («Тапсырыс
  үшін есептен шығарылды», «Балансқа қайтарылды»). A template sent to the driver bot may not mention
  the commission: such an admin override is refused when saved and ignored when sending. Accounting
  is unchanged: the amount is snapshotted, charged at acceptance, kept in the ledger and shown to
  admins. The driver DTOs keep `commission_amount` / `commission_charged` for compatibility; the
  apps do not render them.

### 22.3 Only passengers cancel
- Drivers cannot cancel (no endpoint, no UI); they contact support; admins can cancel or reassign.
- Passenger cancel (`searching | accepted | driver_arriving`) requires `reason_code` ∈
  `long_wait | driver_issue | vehicle_issue | changed_plans | other` and `comment`
  (required for `other`, ≤ 120 code points). Stored as `orders.cancel_reason_code` + `cancel_reason`.
- Labels — kk / ru / en: `long_wait` Ұзақ күттім / Долго ждать / Waited too long;
  `driver_issue` Жүргізуші ұнамады / Не понравился водитель / Didn't like the driver;
  `vehicle_issue` Көлік ұнамады / Не понравилась машина / Didn't like the car;
  `changed_plans` Жоспарым өзгерді / Планы изменились / My plans changed;
  `other` Басқа себеп / Другое / Other.
- Admin dashboard shows cancellation counts by reason.
- The cancel request carries `expected_status` (the status the passenger saw). If the order moved on
  meanwhile — a driver accepted a search the passenger was cancelling — nothing changes and the API
  answers `409 conflict {reason: order_changed, status}`; the app shows the driver and the passenger
  decides again, so a driver is never charged for a cancellation meant for a search.
- Creating and cancelling accepted orders cannot drain drivers' wallets: after
  `orders.passenger_cancels_after_accept_per_day` (default 3, 0 = off) cancellations of
  driver-accepted orders within 24 hours, `POST /orders` answers `409 conflict
  {reason: too_many_cancellations, limit}`. Cancelling a search costs nobody and is not counted.
- The driver is told that the passenger cancelled, never why: driver order views return
  `cancel_reason: null` when `cancelled_by = passenger`. Driver order views and history still carry
  `commission_charged` (charges minus refunds for that order) for compatibility, but the driver apps
  no longer show it (§22.2): the kept commission appears only in the wallet history.

### 22.4 Offers in the driver bot (first tap wins)
- Each eligible driver receives a bot message (owner layout, 2026-10-31): «🚕 Жаңа тапсырыс №N · category»,
  💰 fare · payment method, 🏙 locality, 📍 pickup (A), 🛣 intermediate stops in route order, 🏁 destination
  (B) — a manually typed landmark is shown as typed —, 🧭 distance to pickup, 🗺 route distance/ETA when
  known, 🚗 vehicle class, 👤 passengers or 📦 cargo size/description, 📝 comment. No commission and no
  passenger phone. Inline buttons **«✅ Тапсырысты алу» / «✅ Забрать заказ» / «✅ Accept order»** (callback
  `take:<order_id>`) and **«📱 Қосымшаны ашу» / «📱 Открыть мини-приложение» / «📱 Open Mini App»**
  (web_app → the Mini App's order screen, fullscreen; opening never accepts).
- The accept button calls the internal API accept for that driver — `dispatch.AcceptOrder`, the same
  atomic operation as the Mini App. Success → the bot answers the callback at once and edits the
  message to «✅ Сіз алдыңыз»; the **trip card** with the passenger's contact (name with Telegram link,
  phone when shared, recipient phone for parcels, the whole route, class, load and comment) comes from
  the outbox for both channels — sent once (dedupe key per order, driver and version), retried, and
  withheld when the order was cancelled or reassigned before sending. In an `/orders` list message only
  the taken order's row is removed. Failure → callback alert; when the order is gone for good (taken,
  closed, out of reach) its button goes, when the driver can fix the problem (balance, going online,
  location, a running trip) the offer keeps its buttons.
- When an order is taken or cancelled, the other drivers' offer messages lose their keyboard and are
  marked taken/cancelled (edited via the stored `telegram_message_id`); old buttons stay safe because
  the server re-checks every tap.

### 22.5 Passenger phone
- Before the first order the passenger Mini App asks to share the phone through Telegram
  (`requestContact`); the passenger bot verifies `contact.user_id == from.id` and stores it.
  Skippable — then the driver only gets the Telegram link.
- Driver trip screen (owner, 2026-10-31): with a shared phone the passenger row shows the number
  (tap copies it), a WhatsApp button (`https://wa.me/<digits>` via `openLink`, no message text) and
  the call button; without one, the Telegram chat button takes the WhatsApp place. Call buttons in
  both apps are `tel:` links opened in a new window — Telegram for iOS ignores same-window `tel:`
  links (docs/TESTING.md §6).
- Passenger trip screen (owner, 2026-11-01): the driver's phone (shared through Telegram at
  registration, which always requires it) gives the call button while the order occupies the
  driver; a driver without a phone (approved before the rule) gets a «Жазу» button with their
  Telegram link (`OrderDriver.telegram_url`) instead, so the passenger can always reach the driver.

### 22.6 Passenger push when a driver takes the order (implement together with 22.4)
- The moment an accept succeeds (Mini App or the bot «Тапсырысты алу»), the passenger receives a Telegram
  message from the passenger bot (a push on the phone): the driver's **photo** (driver avatar sent with
  `sendPhoto`; upload the private file once per bot and cache the returned Telegram `file_id` in a new
  column), caption: driver name + rating, vehicle make/model/colour, **plate**, driver **phone**, and
  **«~N минутта келеді»**, plus a web_app button **«Картада қарау»** opening the live trip in the
  Mini App (fullscreen). Text in the passenger's language (kk/ru/en).
- ETA: route duration from the driver's **latest GPS fix** to pickup A via OSRM (the free
  OpenStreetMap router already used; timeout ≤ 2 s); fallback when OSRM fails or the fix is stale:
  straight-line distance / 30 km/h, rounded up to whole minutes, minimum 1. Computed right after the
  accept commits (never inside the accept transaction), stored on the order (`driver_eta_s`,
  `driver_eta_at`) and recomputed from new driver locations at most every 30 s while the order is
  `accepted`; exposed in the passenger Order DTO and the `order.accepted` / `driver.location_updated`
  realtime events.
- Passenger Mini App: the live trip sheet shows "~N мин" next to the existing driver card and the
  driver car moving on the map — no other design changes.

### 22.7 Admin controls for bonuses and commission (owner, 2026-10-19)
- `driver.welcome_bonus`: admin can switch it off (0) or change the amount at any time (e.g. 1000 KZT);
  the value at approval time is what a new driver receives. Admin UI: an on/off switch + amount.
- `commission.fixed_amount`: admin can change it at any time within **50–100 KZT** (validated range,
  setting `commission.fixed_min` = 50 / `commission.fixed_max` = 100 enforced by the API); applies to
  orders created after the change (amount is snapshotted on the order).
- Manual bonus to a specific driver: `POST /admin/drivers/{id}/bonus {amount, comment, idempotency_key}`
  (finance.manage; driver found in the UI by driver code like EMB001 or by phone in any format);
  ledger `bonus_credit` (key `bonus:<idempotency_key>`), audited, realtime `wallet.updated`, and a bot
  message to the driver: kk «Jan Taxi компаниясынан сізге {{amount}} бонус есептелді», ru «Компания
  Jan Taxi начислила вам бонус {{amount}}», en «Jan Taxi has credited you a bonus of {{amount}}»
  (+ comment if given, + button to the wallet). Bonus history visible on driver detail and in the ledger.

### 22.8 Passenger class choice and the nationwide region (owner, 2026-10-19)
- Every new booking starts on the first preview option (the cheapest class that fits the load). A
  class picked earlier (abandoned booking, other route, previous order) is never restored; only
  "order again" repeats the old class.
- A class row is selected only by a real tap (pointer down and up on the row, < 8 px movement, no
  scroll in between); scrolling or dragging the sheet never changes the selection.
- The confirm button names the class and price («Тапсырыс беру · Микроавтобус · 2 150 ₸»). A class
  with 7+ seats for a party of ≤ 4 asks first with an iOS action sheet. A taxi is ordered at exactly
  that price, with no fare field to fill in (§5).
- Tariff region and locality: see §3 (KAZ default region, `orders.locality`, driver code prefix).

### 22.9 Village commissions (owner, 2026-11-01)
- Each rural village can have its own driver commission, a **percentage of the fare** (e.g. 5 %, 7 %,
  10 %), set by an administrator (`config.manage`) in «Сёла и комиссия» (`/admin/commission-areas`,
  table `commission_areas`). Passenger prices are unchanged (tariffs per region, §3); drivers never
  see the commission (§22.2).
- **Which village:** the one whose area covers **pickup A** — A's coordinates, i.e. the passenger's
  GPS position or pin, also when A's address was typed (§5). An area is a circle (`radius_m`
  200–50 000 around the village `center`) or a drawn boundary (`polygon`, the service-zone geometry
  rules); where areas overlap, the village whose **centre is nearest** to A wins (equal distances: by
  name). Inactive areas are ignored. Outside every area the general policy applies (`commission.mode`,
  `commission.fixed_amount`, the tariff's `commission_bps`).
- **When:** resolved in the order-creation transaction and snapshotted on the order
  (`commission_mode = percent`, `commission_bps` = the village's rate, `commission_amount` = the
  rounded percentage of the fare, `commission_area_id`, `commission_area_name`); charged at
  acceptance exactly like any commission (§22.2). Changing or deleting an area never touches orders
  already created; a deleted area leaves its name on its orders (`commission_area.id` becomes
  `null`). The admin order view shows the village (`commission_area`), `orders.csv` the column
  `commission_area`.
- **Radius proposal** (`GET /admin/commission-areas/suggest`): from OpenStreetMap through the
  geocoder (Photon's city layer lists the settlements around the centre with their outlines):
  the settlement whose outline covers the centre → the half-diagonal of its outline box plus the
  centre's offset, × 1.2 for the outskirts (`osm_extent`); a settlement mapped as a point within 3 km
  → a typical reach of its kind (city 8 km, town 4 km, village 2 km, hamlet 1 km; `osm_type`);
  otherwise 2.5 km (`default`). The radius is then at most **half the distance to the nearest other
  village** (another active area or a settlement of the map data), so neighbouring circles meet
  halfway instead of overlapping, kept within 0.5–15 km (less only between villages closer than a
  kilometre) and rounded to 100 m. The administrator can change it (drag the edge, type km) or draw
  the exact boundary instead. `GET /admin/commission-areas/resolve` answers which village (if any) a
  point belongs to; `GET /admin/commission-areas/search` finds a village by name.
- **Villages the map search does not know** (owner, 2026-11-01): the administrator clicks the map to
  put the village point, types the name, takes a ready radius (1, 3, 5, 10 or 15 km), drags the edge
  or draws the boundary. While drawing, `POST /admin/commission-areas/check` checks the database: a
  village with the same name (any status; a save is refused), villages whose area already holds the
  point, villages the new area overlaps (allowed: the nearest centre decides) and the nearest other
  village; the editor shows these lines and opens the existing village on request.
- Audit: `commission_area.create`, `commission_area.update`, `commission_area.delete`.

## 23. Marketing and promotions

The customer's two launch promotions — «Алғашқы сапар» (first ride) and «Досыңды шақыр» (invite a
friend) — and the tools around them: versioned campaigns, the passenger bonus balance, promotions
applied to orders with the driver's compensation, segments, broadcasts, budgets and
reconciliation. Package `internal/marketing`, wiring `internal/app/wire_marketing.go`, migration
**00016**. The owner's guide (Russian) is [MARKETING.md](MARKETING.md); the runbook is
[OPERATIONS.md](OPERATIONS.md) «Marketing».

### 23.1 Principles, launch campaigns and data model

- The server decides eligibility and computes every discount; the apps only display what the API
  returns and send the promotion the passenger confirmed. Money is integer KZT.
- A promotion never changes the order's `fare`, its commission or the commission's basis: the
  commission is computed on the gross fare at creation and charged at acceptance as before (§22.2,
  §22.9). The passenger pays the driver `cash_due = fare − promo_discount` in cash; Jan Taxi credits
  the discount to the driver's wallet balance when the ride completes.
- Every money movement is an append-only ledger row under a unique idempotency key (passenger bonus
  ledger, driver wallet ledger); a replayed request, event or job run changes nothing.
- Nothing starts by itself: the seeded campaigns are drafts (off) until an administrator with
  `marketing.approve` activates them. The global switches (§23.9) are emergency stops, on by default.

Seeded by 00016 (titles and descriptions in kk/ru/en in version 1):

| key | template | rules (version 1) | budget | priority / group |
|---|---|---|---|---|
| `first_ride_coupon_400` «Алғашқы сапар» | `first_ride_coupon` | `amount 400, validity_hours 72, min_order_fare 400, categories [taxi]` | 100 000 | 10 / `launch_2026` |
| `referral_300_once` «Досыңды шақыр» | `referral_first_ride` | `reward_amount 300, categories [taxi], attribution_window_minutes 60, unlock coupon_redeemed, min_ride_seconds 60` | 100 000 | 20 / `launch_2026` |
| `manual_adjustments` (system) | `manual_grant` | `max_amount 50000` | none | 900 / — |

Data model (additive; the down migration refuses to drop marketing data):

| Table / column | Contents |
|---|---|
| `marketing_campaigns` | `key` (`^[a-z0-9_]{3,40}$`, unique), `template`, `status`, `is_system`, `current_version`, `budget_limit` (NULL = no cap), `redemptions_frozen`, `priority` (0..1000, lower first), `exclusivity_group` (stored, not enforced: stacking is impossible anyway, one promotion per order), `scheduled_at`, `active_since` (start of the current active period), `created_by`, `approved_by`, status timestamps. Partial unique index `marketing_campaigns_one_live_launch`: at most one `scheduled \| active \| paused` campaign per launch template (`first_ride_coupon`, `referral_first_ride`). |
| `marketing_campaign_versions` | immutable (trigger): `title`, `description` `{kk,ru,en}`, typed `rules`, `starts_at`/`ends_at` window, `change_note`, author. Coupons, grants and attributions keep the version that created them. |
| `marketing_coupons` | `UNIQUE (campaign_id, user_id)` (one per passenger and campaign, ever); `amount`, `min_order_fare` (CHECK `≥ amount`), `categories`, `status`, `source_order_id` (the qualifying ride), `issued_at`/`expires_at` (database clock), `reserved_order_id` and `redeemed_order_id` (each unique), `void_reason`. |
| `passenger_bonus_accounts`, `passenger_bonus_ledger` | the passenger bonus balance (CHECK `balance ≥ reserved ≥ 0`) and its append-only journal (trigger), §23.5. |
| `order_promotions` | the promotion applied to an order: `order_id` UNIQUE (one per order, no stacking), `kind`, `coupon_id`, campaign/version, `gross_fare`, `discount` (`0 < discount ≤ gross_fare`), `status`, `driver_id`, `compensation` (= discount once settled), `compensation_key` (unique), release/settle/reversal fields, `review_reason`. Partial unique index: a coupon is `reserved` or `settled` on at most one order. |
| `orders.promo_discount` | the discount snapshotted at creation (CHECK `0 ≤ promo_discount ≤ fare`). |
| `referral_codes`, `referral_attributions`, `referral_events` | a passenger's code (`disabled_at` turns it off); who invited whom (`invitee_user_id` UNIQUE, never rewritten, `invitee ≠ referrer`); every capture attempt (append-only). |
| `marketing_grants` | bonus awards `referral_reward \| passenger_credit \| driver_milestone \| manual`, status `pending → credited \| skipped`, `ledger_key` unique; unique per (campaign, passenger) for `referral_reward`/`passenger_credit`, per (campaign, driver) for `driver_milestone`; `referral_id` unique. |
| `marketing_events` | durable `ride_completed` events, `UNIQUE (kind, order_id)`, attempts, `result` (§23.3). |
| `marketing_segments`, `marketing_broadcasts`, `marketing_broadcast_recipients` | audiences, broadcasts and one delivery per recipient and broadcast (§23.7). |
| `user_preferences.marketing_opt_out_at` | the person's marketing opt-out (both bots). |
| other | `wallet_ledger` entry types `promo_compensation`, `promo_compensation_reversal`; `admin_users` role `marketing_manager`; `app_settings` `marketing.*` (§23.9). |

### 23.2 Campaigns, versions and statuses

Rules are typed per template and interpreted only by the server (no expression language); unknown
fields are refused, so a rule the server does not implement can never be stored. Validation
reasons are `fields["rules.<name>"]`.

| Template | Rules | Effect |
|---|---|---|
| `first_ride_coupon` | `amount` 1..100 000, `validity_hours` 1..720, `min_order_fare` ≥ `amount` (`below_amount`), `categories` ⊆ `taxi, parcel, other` | §23.4 |
| `referral_first_ride` | `reward_amount` 1..100 000, `categories`, `attribution_window_minutes` 5..10 080, `unlock` `coupon_redeemed \| coupon_redeemed_or_expired \| first_ride_completed`, `min_ride_seconds` 0..3600 | §23.5 |
| `passenger_credit` | `amount` 1..100 000, `segment_id` (a live passenger segment) | when an approver schedules the campaign (or activates it, if nothing was frozen before), every reachable passenger of the segment is frozen as a pending grant of `amount`; job `marketing_grants` credits them to the bonus balance while the campaign is active (`promo_bonus_granted`); the rules are locked once the campaign leaves draft |
| `driver_milestone_bonus` | `amount` 1..100 000, `rides` 1..1000, `categories`; the version needs `starts_at` and `ends_at` (`window_required`) | a driver whose completed orders in the categories inside the window reach `rides` gets `amount` on the wallet once (`bonus_credit`, key `bonus:<grant_id>`, message `driver_bonus_granted`); drivers read its title and description, which may not name the commission (`fields.title.<lang>` / `description.<lang>` = `mentions_commission`, §22.2) |
| `manual_grant` | `max_amount` 1..1 000 000 | system bucket of manual bonus credits (§23.8): cannot be created or duplicated through the API, has no lifecycle (it stays a draft and cannot be archived), and only `marketing.approve` changes its `max_amount` |
| `broadcast_only` | `{}` | copy only: shown as a banner in `GET /promotions` while active |

* Create (`marketing.edit`): `{template, key?, title, description?, rules, starts_at?, ends_at?,
  budget_limit?, priority?, exclusivity_group?, change_note?}` → a draft with version 1. Without a
  key the server makes `<template>_<6 hex>`; a taken key is `fields.key = taken`. The title is
  required in kk, ru and en (≤ 80 code points each), the description is optional (all three or
  none, ≤ 600). A budget sent by an admin without `marketing.approve` is ignored. `duplicate` copies
  the current version into a new draft.
* New version (`POST …/versions`, same body): the current version with the given changes. Template
  and key are immutable (`immutable`); budget, priority and group are refused
  (`fields.budget_limit = use_budget_endpoint`). Drafts and ended campaigns need `marketing.edit`;
  scheduled, active and paused ones, and the system campaigns whatever their status (`is_system`:
  the launch promotions' rules, and the `manual_adjustments` bucket whose `max_amount` is finance's
  per-credit limit), need `marketing.approve`; an archived campaign never changes.
* Status machine (`POST …/transition {action, reason?, confirm, scheduled_at?, expected_version}`;
  any other move is `409 invalid_transition {from, action}`):

```
draft ──schedule*──▶ scheduled ──(job marketing_campaign_schedule at scheduled_at)──▶ active
scheduled ──unschedule──▶ draft      scheduled ──(the job cannot activate it at its time)──▶ draft
draft | scheduled ──activate*──▶ active ──pause──▶ paused ──resume*──▶ active
active | paused | scheduled ──end*──▶ ended       (also the job once the version's ends_at passed)
draft | ended ──archive──▶ archived
freeze_redemptions* / unfreeze_redemptions*: any status but archived; the status stays

* marketing.approve, "reason" (3..300 code points), "confirm": true and "expected_version".
  The other actions need marketing.edit or marketing.approve; their reason is optional.
  Every action on a system campaign (the launch promotions, manual_adjustments) needs
  marketing.approve.
```

* `activate` and `resume` set `active_since = now()`: rides completed earlier never earn (§23.3).
  They refuse a version whose `ends_at` has passed (`fields.ends_at = in_past`) and a second live
  campaign of a launch template (`409 campaign_conflict`). `end` skips the campaign's pending grants
  (`campaign_ended`). A scheduled campaign that cannot be activated at its time returns to draft
  (logged).
* Reviewed version: an approver action carries `expected_version`, the version the approver looked
  at (`422 fields.expected_version = required` without it). If someone saved a newer version
  meanwhile, the action is refused with `409 version_conflict {current_version}`, so no one
  approves rules or copy they have not seen.
* `passenger_credit` freezes the audience the approver approves: `schedule` (or `activate`, when
  nothing was frozen at scheduling) turns every reachable passenger of the segment into a pending
  grant, paid by job `marketing_grants` only while the campaign is active. The segment is checked
  and the whole audience must fit the budget at that moment (`409 budget_insufficient {needed,
  remaining, recipients}`); people who join the segment later are not added. `unschedule`, and a
  scheduled campaign the job cannot activate, drop the pending grants; `end`, by an approver or by
  the job once `ends_at` passed, skips them.
* The `manual_adjustments` bucket has no lifecycle: it stays a draft, and every action that would
  change its status, `archive` included, is `409 campaign_conflict` (or `invalid_transition` when
  the status does not allow the action at all); `freeze_redemptions` and `unfreeze_redemptions` are
  accepted and change nothing, as the bucket has no coupons.
* `pause` stops new awards of the campaign; coupons already issued stay usable until they expire.
  `freeze_redemptions` makes the campaign's coupons unusable on new orders (reserved ones still
  settle or release) — the tool after a leak or abuse.
* Budget: `PATCH …/budget {budget_limit | unlimited: true, reason, confirm}` (`marketing.approve`,
  0..10⁹), §23.8.
* Preview (`GET …/preview`): `audience` estimate, `per_person`, `max_liability` (audience × amount,
  capped by the budget), `usage`, and `warnings`: `awards_switch_off`, `redemptions_switch_off`,
  `fares_below_coupon_minimum` (an active tariff of the coupon's categories starts below
  `min_order_fare`), `legacy_passengers_locked` (the unlock policy excludes passengers who rode
  before), `coupon_campaign_not_active` (the default unlock needs a live coupon campaign),
  `no_budget_limit`.

### 23.3 Award pipeline

* The completion transaction (`orders.finishCompleted`, after `SettleOrder`) calls
  `marketing.RecordRideCompleted`: one statement that inserts `marketing_events (ride_completed,
  order_id, occurred_at = completed_at)` only while `marketing.awards_enabled` is not false and a
  `first_ride_coupon`, `referral_first_ride` or `driver_milestone_bonus` campaign is active
  (`ON CONFLICT (kind, order_id) DO NOTHING`). Nothing is queued while those are off, so switching a
  campaign on never awards rides completed before.
* Job `marketing_events` (2 s): up to 20 due events, each in its own transaction (`FOR UPDATE SKIP
  LOCKED`). It re-checks the awards switch, loads the ride (status `completed` or `disputed`, with
  `completed_at`), and applies every campaign live at `completed_at` (status `active`, version window
  covering it, `active_since ≤ completed_at`). The outcome per campaign is stored in
  `marketing_events.result`: `coupon_issued`, `already_issued`, `not_first_ride`,
  `category_not_eligible`, `budget_exhausted`, `referral_rewarded`, `referral_capped`,
  `referral_review:<reason>`, `referral_rejected:<reason>`, `milestone_credited`, …, or `skipped`
  (`not_completed`, `awards_disabled`, `too_old`).
* An error rolls the event's transaction back; it is retried after `min(600, 5·2^attempts)` seconds
  and marked `failed` at the 10th attempt (overview health, reconciliation). Events whose ride is
  older than 7 days are closed as `too_old`: a worker restarted after a long stop never pays a
  backlog by surprise.
* Awards are idempotent by constraints (one coupon per passenger and campaign, one grant per
  beneficiary and campaign, unique ledger keys): a retried event never pays twice. Telegram
  messages go through the outbox in the same transaction and never roll an award back.
* First ride (`isFirstRide`): no other order of the passenger has an earlier `completed_at` (ties by
  id) — the first completed order of any category. Orders completed and later disputed or cancelled
  still count as taken.

### 23.4 First-ride coupon «Алғашқы сапар»

* Issued by the award worker for the passenger's first completed order when that order's category
  is in `rules.categories` (taxi) and the budget allows (else no coupon: `budget_exhausted`, a
  warning in the log). A passenger whose first completed order was a delivery, or who rode before
  the campaign's active period (a legacy passenger), never qualifies.
  `issued_at = now()` and `expires_at = now() + validity_hours` (72 h) from the database clock;
  message `promo_coupon_issued` (`amount`, `validity` e.g. «3 күн»), realtime `promotions.updated`.
* Validity is `expires_at > now()` everywhere: a coupon is unusable from `expires_at` on, even before
  job `marketing_coupon_expiry` (1 min, ≤ 1000 rows per run) marks it `expired`; views compute the
  status the same way.
* Use: the next taxi order the passenger creates while the coupon is valid and whose fare is at
  least `min_order_fare` (400). The passenger app applies it automatically — it sends
  `promotion: coupon` whenever `GET /promotions` lists a usable coupon for the order — and the
  server re-checks and computes it (§23.6). No partial redemption: a smaller fare leaves the coupon
  for a later order; as `min_order_fare ≥ amount`, the cash due never goes below zero (fare 400 →
  cash 0).
* States:

```
issued ──order created (reserve)──▶ reserved ──order completed (settle)──▶ redeemed
reserved ──order cancelled (release)──▶ issued (before expires_at) | expired (after)
reserved ──order cancelled, its qualifying ride cancelled meanwhile──▶ voided
issued ──expires_at passes──▶ expired
issued ──operator void (marketing.approve, reason) | its qualifying ride cancelled──▶ voided
```

* A cancelled order gives the coupon back until it expires; `expires_at` is never extended.
* When the qualifying ride itself is cancelled after completion (a dispute resolved as cancelled),
  the coupon it earned goes with it (`void_reason = qualifying_ride_cancelled`): an unused
  (`issued`) coupon is voided at once; a `reserved` one is voided, not returned, when the order
  holding it is cancelled. If that order completes instead, the ride was taken at the discounted
  price: the coupon is redeemed, the driver is compensated as usual and the promotion is flagged
  for finance (`review_reason = coupon_source_cancelled`, §23.6). A coupon already redeemed before
  the dispute is not touched.

### 23.5 Referrals «Досыңды шақыр» and the passenger bonus balance

* `GET /promotions` → `referral.state`: `unavailable` (no active referral campaign), `locked` with
  `lock_reason` (`coupon_not_redeemed` under the default policy, `coupon_not_used` under
  `coupon_redeemed_or_expired`, `first_ride_required`, `paused` while awards are switched off,
  `disabled` when the passenger's code was disabled), or `unlocked`. Unlock policies
  (`rules.unlock`; changing it is a new version): `coupon_redeemed` (default, the assignment
  literally: only after the first-ride coupon was used), `coupon_redeemed_or_expired`,
  `first_ride_completed`.
* Code: created on the first `GET /promotions/referral` while unlocked — 8 random characters
  (crypto) from `ABCDEFGHJKLMNPQRSTUVWXYZ23456789` (no 0/O, 1/I), unique, with no Telegram id or
  phone in it. Links `https://t.me/<passenger bot>?start=ref_<CODE>` (`link`, the bot) and
  `?startapp=ref_<CODE>` (`app_link`, the Mini App); without `PASSENGER_BOT_USERNAME` only the code
  is returned. `referral_codes.disabled_at` turns a passenger's invitations off (database only; there
  is no admin endpoint yet).
* Capture (attribution), only through signed channels, never from a value an app sends: the
  passenger bot's `/start ref_<CODE>` → `POST /internal/v1/passenger/{tg_id}/referral {payload}`
  (source `bot_start`; the greeting never depends on it), or the Mini App's signed
  `initData.start_param` read at passenger sign-in (`auth.OnPassengerStart` hook, source `miniapp`;
  it never fails the sign-in). One transaction with the invitee's user row locked, so a bot start and
  a Mini App sign-in cannot both bind:
  1. the payload must be `ref_` + 8 code characters (else `rejected invalid_code`, not recorded);
  2. an existing attribution → `duplicate already_attributed` (the first code wins; nothing
     rewrites it);
  3. `rejected` with `campaign_inactive` (no active referral campaign now), `unknown_code` (or
     disabled), `self_referral`, `referrer_blocked`, `referrer_locked` (the code's owner is not
     unlocked), `invitee_has_orders`, `invitee_is_driver`, `invitee_not_new` (account older than
     `attribution_window_minutes`, 60);
  4. else `attributed` to the active campaign and version; the referrer's app gets
     `promotions.updated`.
  Every attempt except an invalid payload is kept in `referral_events` (fraud review).
* Reward, at the invitee's first completed ride (award worker; the referral campaign live at the
  completion):
  * not the invitee's first completed order → nothing (the attribution stays `attributed`);
  * category not in `rules.categories` → `rejected category_not_eligible`;
  * the referrer is blocked, has the invitee's phone, or drove the ride → `rejected
    referrer_blocked | same_phone | referrer_drove`;
  * the ride (started → completed) is shorter than `min_ride_seconds` (60) → `review short_ride`;
  * the referrer already has this campaign's reward → `capped already_rewarded`;
  * the budget does not allow it → `review budget_exhausted`;
  * else a `referral_reward` grant is credited at once (bonus ledger `grant`, key
    `grant:<grant_id>`), the attribution becomes `rewarded`, message `promo_referral_reward`.
  At most one reward per referrer and campaign (unique index), however many friends; later friends
  end `capped`. The invitee gets nothing from this campaign (they earn their own first-ride coupon).
* Review: `POST /admin/marketing/referrals/{id}/review {decision: approve | reject, reason}`
  (`marketing.approve`) decides a `review` attribution. Approve pays like the worker — still once per
  referrer, from the campaign that judged the ride (the attribution belongs to it from then on) and
  within its budget (`409 budget_insufficient` otherwise); reject closes it.
* Qualifying ride cancelled after completion (a dispute resolved as cancelled): "a ride that is
  stopped or not completed earns nothing". An attribution still in `review` becomes `rejected`; a
  `rewarded` one keeps its status and its reward but is flagged — both with `reason =
  qualifying_ride_cancelled`. No money moves automatically: finance decides whether to take the
  reward back with a manual bonus debit (§23.8).

```
attributed ──first ride qualifies──▶ rewarded
attributed ──referrer already rewarded──▶ capped
attributed ──category | referrer blocked | same phone | referrer drove──▶ rejected
attributed ──short ride | budget exhausted──▶ review ──approve──▶ rewarded | capped
                                                     └──reject──▶ rejected
review ──qualifying ride cancelled by a dispute──▶ rejected (qualifying_ride_cancelled)
rewarded ──qualifying ride cancelled by a dispute──▶ rewarded, reason qualifying_ride_cancelled (finance decides)
```

* The inviter's `GET /promotions/referral`: state, title and description, code and links while
  unlocked, `earned` (credited rewards), `rewarded`, and up to 50 friends as first name + last
  initial with `attributed | rewarded | capped | not_counted` (`review` and `rejected` read
  `not_counted`).

**Passenger bonus balance.** What the platform owes a passenger as a discount on later rides:
separate from driver wallets, never withdrawable, without expiry. `passenger_bonus_accounts
(balance, reserved)`, `available = balance − reserved`. Every change locks the account row, writes
one append-only `passenger_bonus_ledger` row under a unique key (the same key with the same effect
is a no-op, with another effect a conflict) and updates the account in the same transaction.

| Event | entry_type | balance | reserved | Key |
|---|---|---|---|---|
| reward or credit paid | `grant` | +amount | 0 | `grant:<grant_id>` |
| order created with the bonus | `reserve` | 0 | +discount | `bonus_reserve:<application_id>` |
| that order cancelled | `release` | 0 | −discount | `bonus_release:<application_id>` |
| that order completed | `spend` | −discount | −discount | `bonus_spend:<application_id>` |
| manual credit (§23.8) | `adjustment_credit` | +amount | 0 | `bonus_adjust:<uuid>` |
| manual debit (§23.8) | `adjustment_debit` | −amount | 0 | `bonus_adjust:<uuid>` |
| refund of a reversed promotion (§23.6) | `adjustment_credit` | +discount | 0 | `bonus_refund:<application_id>` |

The bonus pays `min(available, fare)` of a taxi order (`bonus_categories: [taxi]`); the rest stays
for later. The passenger's history (`GET /promotions/bonus/history`) lists grants, spends and
adjustments, not reservations.

### 23.6 Promotions on orders

* `POST /orders` takes `promotion` (`coupon | bonus | none`, default none) and
  `promotion_discount` (the discount the app showed). Inside the creation transaction, after the
  quote, fare, active-order and cancellation-limit checks, `marketing.PlanOrder` locks what the
  promotion would use and computes the discount:
  * `coupon`: the passenger's `issued` coupons, earliest expiry first (`FOR UPDATE`); the first
    one that is valid, not frozen (campaign `redemptions_frozen` or archived), in the order's
    category and with `fare ≥ min_order_fare`; discount = its amount;
  * `bonus`: taxi only; the account row locked; discount = `min(available, fare)`;
  * `409 promotion_changed` with `details {reason, discount}` when the server would apply something
    else: `redemptions_disabled`, `no_coupon`, `coupon_expired`, `coupon_frozen`,
    `category_not_eligible`, `fare_below_minimum`, `no_bonus`, `no_promotion` (a discount sent
    without a promotion), `discount_changed` (`details.discount` is the server's), `coupon_unavailable`
    (taken by a concurrent order). The app re-reads `GET /promotions` and shows the new price, so a
    passenger never pays more than the price they confirmed. Bad values: `fields.promotion` /
    `fields.promotion_discount` = `invalid`.
* `marketing.Reserve`, same transaction, before drivers are offered the order (offers already show
  the cash to collect): an `order_promotions` row (`reserved`, `gross_fare = fare`, `discount`), the
  coupon `reserved` for the order or a bonus `reserve` entry, and `orders.promo_discount`. One
  promotion per order — a coupon or the bonus, never both. An idempotent replay of `POST /orders`
  returns the original order without planning again.
* Completion (`marketing.SettleOrder`, in the completion transaction, once): the coupon becomes
  `redeemed` or the bonus is spent, the discount is credited to the completing driver
  (`promo_compensation`, key `promo:<application_id>`, `wallets.CreditPromoCompensation`), and the
  promotion becomes `settled` with `driver_id`, `compensation = discount` and `compensation_key`. The
  commission is untouched: it was charged at acceptance on the gross fare. A coupon whose own
  qualifying ride a dispute cancelled meanwhile is settled the same way — the ride was taken at the
  discounted price, so the driver is paid — and the promotion is flagged `review_reason =
  coupon_source_cancelled` for finance.
* Cancellation (`lifecycle.Cancel` → `marketing.ReleaseOrder`, on every path: passenger, admin,
  search timeout, dispute): a `reserved` promotion becomes `released` — the coupon is usable again
  until it expires (or voided when its qualifying ride was cancelled meanwhile, §23.4), a bonus
  reservation returns to `available` — and the driver gets nothing. A `settled` promotion whose
  order is cancelled after completion (a dispute resolved as cancelled) is flagged `review_reason =
  order_cancelled_after_settlement`; no money moves automatically. `orders.promo_discount` stays on
  the cancelled order as the snapshot. Cancelling a completed order (with or without a promotion of
  its own) also voids the unused coupon that ride earned and rejects or flags the referral it
  qualified (§23.4, §23.5).
* Reversal (`POST /admin/marketing/applications/{id}/reverse {reason, confirm}`, `finance.manage`,
  settled promotions only): the driver's compensation is debited (`promo_compensation_reversal`, key
  `promo:<application_id>:reversal`, only from the available balance — `409 insufficient_balance`),
  a spent bonus is refunded to the passenger (`bonus_refund:<application_id>`), a redeemed coupon
  stays redeemed; status `reversed`, `wallet.updated` to the driver, audited. There is no "dismiss"
  action: a flagged promotion that finance keeps stays flagged. Overview `review_promotions` and
  `GET /admin/marketing/applications?review=true` list both flags.

```
reserved ──order completed──▶ settled ──reverse (finance)──▶ reversed
reserved ──order completed, the coupon's qualifying ride cancelled meanwhile──▶ settled + coupon_source_cancelled
reserved ──order cancelled──▶ released
settled ──order cancelled by a dispute──▶ settled + order_cancelled_after_settlement (finance decides)
```

* Order DTOs: `promo_discount` and `cash_due` on `Order`, `OrderSummary`, `AdminOrder`, the
  available-order card (`GET /driver/orders`), `DriverOrderDetail` and `DriverOrderSummary`;
  `promotion` (`OrderPromotion {kind, title, discount, gross_fare, cash_due, status, compensation,
  settled_at}`, `null` without one) on `Order`, `AdminOrder` and `DriverOrderDetail`. Drivers get
  it without `kind` and `title`: how the passenger paid is not the driver's business.
* Messages: `order_created_passenger` and `order_completed_passenger` add `discount` and
  `cash_due`; `order_available_driver`, `order_accepted_driver` and `order_completed_driver` add
  `cash_due` («💵 Жолаушыдан алу») and `promo_compensation` («🎁 Jan Taxi акциясы: +400 ₸ сапардан
  кейін балансқа»). The driver's wallet history shows the `promo_compensation` row.
* Driver settlement follows the customer's document: the compensation goes to the driver's Jan Taxi
  wallet balance. It pays future commissions like any credit and is not withdrawable — the platform
  has no payouts (owner decision pending, §23.14).

### 23.7 Segments and broadcasts

* Audiences: `passengers` (users who started the passenger bot) and `drivers` (users with a driver
  profile who started the driver bot; `approved` drivers unless `driver_statuses` says otherwise).
* Segments (`marketing.edit`: `{name 1..120, audience, filters}`; the audience is immutable;
  `{archived: true}` archives): typed filters only, evaluated in SQL with bound parameters, unknown
  fields refused — `languages` (`kk | ru | en`), `registered_within_days`, `registered_before_days`,
  `completed_rides_min`, `completed_rides_max` (0..100 000, max ≥ min); passengers only:
  `last_ride_within_days`, `no_ride_for_days`, `has_active_coupon`; drivers only: `driver_statuses`
  (`approved | suspended | pending | changes_requested`), `online_within_days` (last heartbeat),
  `city_ids` (≤ 50). Days are 0..3650. A filter of the other audience is `driver_only_filter` /
  `passenger_only_filter`. Estimates (`POST …/segments/preview`, segment lists, broadcast detail)
  are counts only — `matching`, `reachable`, `suppressed` — never people.
* Suppressed: opted out (`user_preferences.marketing_opt_out_at`), blocked the bot
  (`users.<bot>_bot_blocked_at`) or blocked by an operator.
* Broadcasts (`marketing.broadcast`): `{title 1..120, audience, segment_id | filters, content
  {kk,ru,en}, button?, no_button?, campaign_id?}`; only drafts are edited (`409 invalid_transition`).
  The text is cleaned (NFC, CRLF → LF, control and format characters removed, line breaks kept, at
  most two blank lines), ≤ 3000 characters per language, and sent as HTML-escaped plain text in the
  recipient's language (kk fallback). An optional `button {label {kk,ru,en} ≤ 40, start_param}` opens
  the audience's Mini App at `?startapp=<start_param>` (§18; an HTTPS Mini App URL is required).
  Every message also carries the callback button «Жарнамадан бас тарту» / «Отписаться от
  рассылки» / «Unsubscribe» (`mkt_off`). Drivers never read about the commission (§22.2): a
  broadcast to drivers that names it is refused (`fields.content.<lang> = mentions_commission`).
* Status machine (`POST …/transition {action, scheduled_at?, reason?, confirm}`; `schedule` and
  `send_now` need `confirm: true`; the reason is kept as `approval_note`):

```
draft ──schedule──▶ scheduled ──(worker at scheduled_at: snapshot)──▶ sending ──(nothing left)──▶ completed
draft | scheduled ──send_now (snapshot now)──▶ sending ──pause──▶ paused ──resume──▶ sending
draft | scheduled | sending | paused ──cancel──▶ cancelled
scheduled ──(at scheduled_at: snapshot failed | above the threshold without an approver)──▶ failed
```

* Targeting: a draft follows its saved segment. `schedule` stores the resolved filters it checked on
  the broadcast (`send_now` those of its snapshot), so later segment edits never move a scheduled
  or running broadcast.
* Approval: an audience above `marketing.broadcast_approval_threshold` (100) needs an admin who holds
  `marketing.approve` besides `marketing.broadcast` — with the default roles only `super_admin`
  (`marketing.approve` alone opens no broadcast route: `403 forbidden`). A broadcaster without it
  gets `403 approval_required {recipients, threshold}`, checked against the estimate when
  scheduling and against the snapshot when sending now. `approved_by` records an approver who
  scheduled or sent the broadcast and stays empty for anyone else. At `scheduled_at` the worker
  takes the snapshot: a broadcast without an approver whose audience has grown above the threshold
  then in force is not sent — it becomes `failed` with `approval_note = approval_required` and its
  deliveries `cancelled`.
* Snapshot: one `queued` delivery per reachable person and a `suppressed` row per suppressed one
  (`UNIQUE (broadcast_id, user_id)` for non-test rows); `recipient_count` = queued. Pause stops the
  unsent deliveries; cancel marks the queued ones `cancelled` (the worker also closes any that a
  send in progress puts back into the queue), but messages Telegram already accepted cannot be
  recalled. "Sent" means accepted by Telegram.
* Test send (`POST …/test {telegram_user_id}`): one extra delivery (`is_test`, prefixed «🧪 TEST») of
  a `draft` or `scheduled` broadcast (else `409 invalid_transition {from, action: test}`), only to a
  Telegram id an approver listed in `marketing.test_recipients` (§23.9; empty by default, so staff
  list their own ids first) — `422 fields.telegram_user_id = not_test_recipient` otherwise — who
  started the audience's bot (`bot_not_started`) and neither opted out nor blocked it
  (`suppressed`). Tests stop with the broadcast switch: `409 broadcasts_disabled` while it is off,
  and a queued test is not sent until it is on again; suppression is checked again before sending.
  At most 10 tests per broadcast and 30 in total per hour (`429`).
* Worker `broadcast_worker` (its own tables, never the operational outbox; runs with the
  notification worker on one replica at a time — advisory lock — so the per-bot rate is global):
  * starts due scheduled broadcasts while broadcasts are on;
  * yields while more than 20 operational outbox messages are due (order messages first);
  * while broadcasts are on, claims `2 × rate` due deliveries (tests first) with a 2-minute lease
    (`locked_until`; a lease of a crashed worker expires and the delivery is claimed again);
  * re-checks suppression just before sending (`suppressed`);
  * one token bucket per bot at `marketing.broadcast_rate_per_s` (default 4/s, below Telegram's
    ~30/s); Telegram is never called inside a database transaction;
  * success → `sent` (`telegram_message_id`, `sent_at`); `429` → the whole bot pauses for
    `retry_after` and the delivery is released without spending an attempt; `403` → `blocked` and
    `users.<bot>_bot_blocked_at`; 5xx, network errors and timeouts → retried with exponential
    backoff (5 s doubling, ≤ 10 min, equal jitter) up to 5 attempts, then `failed`; other errors →
    `failed`. A `sending` broadcast with nothing queued becomes `completed`.
* Opt-out: the button in either bot → `POST /internal/v1/bots/{bot}/users/{tg_id}/marketing
  {opt_out: true}` (`marketing_opt_out_at` set; an alert «Жарнамалық хабарламалар өшірілді»); the
  passenger Mini App switch → `PUT /promotions/subscription {subscribed}`. The choice belongs to the
  person (one user row for both bots) and never affects order messages.
* Start parameters: a broadcast button only opens a Mini App screen (§18); a referral link
  `ref_<CODE>` is the one start parameter the API acts on, captured from the signed `initData` at
  passenger sign-in (§23.5).

### 23.8 Budgets, manual adjustments and reconciliation

* Usage of a campaign: `committed` = unexpired `issued` and `reserved` coupons + `pending` grants;
  `spent` = `redeemed` coupons + `credited` grants; `remaining = max(budget_limit − committed −
  spent, 0)` (`null` without a cap). Expired and voided coupons and skipped grants free their amount;
  the coupon of a reversed promotion stays spent.
* Every award checks `committed + spent + amount ≤ budget_limit` under the campaign row lock
  (`FOR NO KEY UPDATE`: it serialises budget checks and transitions without blocking the
  foreign-key checks of coupons, grants and ledger rows written meanwhile) in the transaction that
  writes the coupon or grant, so concurrent awards never overspend. Out of budget: no coupon
  (`budget_exhausted`), a referral goes to `review budget_exhausted`, a `passenger_credit` schedule
  or activation is refused, a manual credit is `409 budget_insufficient`. Pilot budgets: 100 000 ₸
  per launch campaign. A budget change is audited with its reason; lowering it below the usage only
  stops new awards.
* Manual corrections of a passenger's bonus (`POST /admin/marketing/users/{id}/bonus-adjustments
  {amount, reason, idempotency_key, confirm}`, `finance.manage`; `amount` signed, ≠ 0, within
  ±1 000 000; `idempotency_key` a UUID, ledger key `bonus_adjust:<uuid>`):
  * a credit goes through the `manual_adjustments` campaign: at most its `max_amount` (50 000) per
    operation (`422 fields.amount = above_max`, `details.max_amount`), within its budget, recorded as
    a `manual` grant; the passenger gets `promo_bonus_granted`;
  * a debit takes only available money (`409 insufficient_balance`);
  * the same key and amount is a no-op; the key with another effect is `409 idempotency_key_reused`;
  * the bucket can be neither scheduled nor archived (§23.2). Its budget (none by default) caps
    manual credits, and a budget of 0 stops them; its `max_amount` changes only through a new
    version by `marketing.approve`, so a marketing manager can raise neither.
* Reconciliation (`GET /admin/marketing/reconciliation`, `marketing.view`; job `marketing_reconcile`
  logs an error every hour while it is not OK): one read-only REPEATABLE READ snapshot, at most 100
  findings per check, ids only.

| Finding | Meaning |
|---|---|
| `compensation_mismatch` | a settled or reversed promotion without exactly its `promo_compensation` row (amount, order) |
| `orphan_compensation` | a `promo_compensation` row without its promotion |
| `reversal_without_status` | a `promo_compensation_reversal` row whose promotion is not `reversed` |
| `bonus_account_mismatch` | a bonus account differs from the sums of its ledger |
| `bonus_reservation_mismatch` | an account's `reserved` differs from its reserved bonus promotions |
| `stuck_reservation` | a promotion still `reserved` on an order that is no longer active |
| `coupon_promotion_mismatch` | a reserved or redeemed coupon without its matching promotion |
| `grant_without_ledger`, `driver_grant_without_ledger` | a credited grant without its ledger row |
| `referral_cap_exceeded` | more than one referral reward per referrer and campaign |
| `order_discount_mismatch` | `orders.promo_discount` differs from its promotion's discount |
| `totals_mismatch` | settled discounts differ from credited compensation |
| `failed_events` | marketing events marked `failed` |

Totals: `discounts_settled`, `compensation_credited`, `compensation_reversed`,
`bonus_outstanding`, `bonus_reserved`, `coupon_liability` (unexpired issued and reserved coupons),
`promotions_reserved`, `promotions_under_review`, `events_failed`, `events_pending`. The wallet
reconciliation (§6) covers the compensation rows as part of each wallet.

### 23.9 Switches and settings

| Key | Default | Effect when changed |
|---|---|---|
| `marketing.awards_enabled` | true | off: no ride is queued for awards and queued events close as `awards_disabled` (rides completed meanwhile never earn); no coupons, referral rewards, milestone bonuses or passenger credits; the first-ride offer disappears and invitations read `locked` / `paused` |
| `marketing.redemptions_enabled` | true | off: new orders cannot use a coupon or the bonus (`promotion_changed redemptions_disabled`); promotions already reserved still settle or release |
| `marketing.broadcasts_enabled` | true | off: no broadcast starts or sends, test sends included (`409 broadcasts_disabled`; queued tests wait) |
| `marketing.broadcast_rate_per_s` | 4 | 1..20 messages per second per bot |
| `marketing.broadcast_approval_threshold` | 100 | 1..10 000 000; larger audiences need `marketing.approve` besides `marketing.broadcast`, also when a scheduled broadcast starts (§23.7) |
| `marketing.test_recipients` | `[]` | `int_list`, up to 20 Telegram ids (1..9 999 999 999 999, no duplicates, stored sorted): the only people a test broadcast may reach; empty: no test can be sent |

The three switches are emergency stops and default to on; nothing is awarded or sent until a
campaign is activated or a broadcast is sent. All six keys are changed through `GET/PATCH
/admin/marketing/settings` (the panel's Marketing → Safety; `marketing.view` to read,
`marketing.approve` to change, the body's `reason` is required and audited as `settings.update`),
or by a super admin in Settings (§19, group Marketing). Other API replicas see a change within the
5-second settings cache.

### 23.10 Admin panel, permissions and audit

* Role `marketing_manager`: `dashboard.view`, `marketing.view`, `marketing.edit`,
  `marketing.broadcast` — prepares campaigns, segments and broadcasts. Money starts or resumes
  flowing only with `marketing.approve` (super_admin, finance_manager). Permissions (§4.2):
  * `marketing.view` — overview, campaigns and previews, coupons, bonus ledger, people, referrals,
    promotions on orders, segments, broadcasts and recipients, reconciliation, reports, settings;
  * `marketing.edit` — create and duplicate campaigns, versions of drafts and ended campaigns (not
    of the `manual_adjustments` bucket), `pause`, `unschedule`, `archive`, segments;
  * `marketing.approve` — `schedule`, `activate`, `resume`, `end`, `freeze_redemptions`,
    `unfreeze_redemptions`, versions of live campaigns and of `manual_adjustments`, budgets,
    settings (the test recipients included), coupon voids, referral decisions. It does not open the
    broadcast routes: a broadcast above the threshold is scheduled or sent by an admin who holds it
    together with `marketing.broadcast` — with the default roles only `super_admin`; a finance
    manager cannot send broadcasts and a marketing manager can send only audiences up to the
    threshold;
  * `marketing.broadcast` — create, edit, test (to the listed test recipients only), schedule, send,
    pause, resume and cancel broadcasts;
  * `finance.manage` — bonus adjustments and promotion reversals; `reports.export` together with
    `marketing.view` — `reports.csv`.
* High-risk actions carry a reason (3..300 code points) and `confirm: true`: approver campaign
  actions, budgets, bonus adjustments and reversals. Coupon voids, referral decisions and settings
  need the reason; scheduling or sending a broadcast needs `confirm`.
* Audit (`admin_audit_logs`, before/after): `marketing.campaign.{create, duplicate, version,
  schedule, unschedule, activate, pause, resume, end, archive, freeze_redemptions,
  unfreeze_redemptions, budget}` (entity `marketing_campaign`), `marketing.coupon.void`
  (`marketing_coupon`), `marketing.bonus.adjust` (`passenger_bonus`), `marketing.referral.{approve,
  reject}` (`referral_attribution`), `marketing.promotion.reverse` (`order_promotion`),
  `marketing.segment.{create, update}` (`marketing_segment`), `marketing.broadcast.{create, update,
  test, schedule, send_now, pause, resume, cancel}` (`marketing_broadcast`), `settings.update`
  (`app_setting`, with the reason).
* Personal data: people appear as `UserRef` — first name and last initial, the phone masked to its
  last four digits, the Telegram id; recipient lists show only user id, language and delivery state;
  the overview, reports, the CSV and the reconciliation are aggregates or ids. People are found by
  Telegram id, the phone's last 10 digits or a referral code (`GET /admin/marketing/users?q=`, at
  least 3 characters, 20 results).
* Overview (`GET /admin/marketing/overview`): switches; campaigns with usage; coupon, referral and
  broadcast counts by status; bonus `outstanding`, `reserved`, `accounts`; compensation `credited`
  and `pending`; health `events_pending`, `events_failed`, `oldest_pending_s`,
  `stuck_reservations`, `review_promotions`, `review_referrals`, `broadcast_queue`,
  `overdue_scheduled` (campaigns or broadcasts more than 5 minutes past their time).
* Reports (`GET /admin/marketing/reports?from=YYYY-MM-DD&to=YYYY-MM-DD`, business days in
  Asia/Aqtobe, default the last 30, at most 366): per day coupons issued, redeemed and expired,
  referrals, referral rewards, bonus granted, discounts settled, messages sent and failed; the
  referral and coupon funnel. `reports.csv` exports the same days.

### 23.11 Passenger app, driver app and bots

* Passenger API (`P`): `GET /promotions` — `server_time`, `redemptions_enabled`, the latest 10
  coupons (status computed against the database clock), `bonus`, `bonus_categories`, `offers` (the
  first-ride offer while it can still be earned: awards on, no completed order, no coupon yet),
  `banners` (active `broadcast_only` campaigns), the `referral` summary, `marketing_subscribed`;
  `GET /promotions/bonus/history?limit=&before=`; `GET /promotions/referral`;
  `PUT /promotions/subscription {subscribed}`. The app shows discounts and `cash_due` from these
  and from the order DTOs and never decides eligibility itself; `server_time` lets it count down
  `expires_at` without trusting the phone's clock.
* Realtime `promotions.updated` (scope `user:<id>`) when the passenger's coupons, bonus or
  referral state change: coupon issued, reserved, released, settled or voided by an operator;
  referral captured or rewarded; bonus credited, spent or adjusted. Like every event it is a hint:
  the app re-reads `GET /promotions` (§9).
* Start parameters (§18): `coupons`, `bonus`, `invite`; `ref_<CODE>` is the referral link.
* Driver: the available-order card, the order and trip screens and the history carry `cash_due` (what
  to collect) and `promo_discount` (what Jan Taxi credits after the ride); the commission stays
  hidden (§22.2).
* Bots: the passenger bot's `/start ref_<CODE>` captures the referral and greets as usual; both bots
  handle the unsubscribe button `mkt_off`; the passenger messages `promo_coupon_issued`,
  `promo_referral_reward` and `promo_bonus_granted` open the `coupons` / `bonus` screens. The
  internal order lists carry `promo_discount` and `cash_due` too (`BotOrderSummary`, the
  available-order cards): the driver bot's `/orders` and `/trip` show the cash to collect and the
  credit after the ride, the passenger bot's `/orders` what the passenger pays.

### 23.12 Jobs and workers

| Job | Interval | Work |
|---|---|---|
| `marketing_events` | 2 s | award worker: first-ride coupons, referral rewards, driver milestones (§23.3) |
| `marketing_coupon_expiry` | 1 min | marks expired coupons, ≤ 1000 per run (validity never depends on it) |
| `marketing_campaign_schedule` | 30 s | activates due scheduled campaigns (one that cannot be activated returns to draft and drops its frozen `passenger_credit` grants); ends active or paused campaigns whose version `ends_at` passed and skips their pending grants |
| `marketing_grants` | 5 s | credits pending `passenger_credit` grants of active campaigns, 100 per run (nothing while awards are off) |
| `marketing_reconcile` | 1 h | reconciliation; an error in the log when it is not OK |
| worker `broadcast_worker` | continuous, 1 s when idle | broadcasts (§23.7); only with `NOTIFICATION_WORKER_ENABLED`, one replica at a time |

### 23.13 Invariants, lock order and security

Invariants (database constraints unless noted):
- `0 ≤ orders.promo_discount ≤ fare`; `0 < order_promotions.discount ≤ gross_fare`; one promotion per
  order; a coupon live on at most one order; `min_order_fare ≥ amount`, so the cash due is never
  negative.
- A settled promotion has exactly one `promo_compensation` row of the same amount
  (`compensation = discount`, unique `compensation_key`; reconciliation).
- One coupon per passenger and campaign; one referral reward per referrer and campaign; one grant per
  referral; one attribution per invitee; one live campaign per launch template.
- A bonus account: `balance ≥ reserved ≥ 0`, equal to its ledger sums, and `reserved` equal to its
  reserved bonus promotions (reconciliation).
- Campaign versions, the bonus ledger and referral events are append-only (triggers).
- A promotion changes neither the fare nor the commission; the driver's compensation equals the
  discount.

Lock order (package `marketing`): order row → coupon row or bonus account → campaign row → driver
wallet. Order creation locks the coupon or the bonus account (the order row is new); completion
and cancellation lock order → promotion → coupon or account → wallet (the compensation). A wallet
locked earlier in those transactions belongs only to a commission reservation of the pre-v2 model,
and such orders carry no promotion. The award worker locks the event, reads the ride `FOR SHARE`
(a dispute resolution of that order waits for it), then locks the attribution, the campaign row
(budget), then the beneficiary's bonus account or wallet; a manual adjustment locks the account and
then only the `manual_adjustments` row, which no award locks, so the two never wait for each other.
Campaign rows are locked `FOR NO KEY UPDATE`, so orders that reserve a coupon never wait for them.
A reversal (a rare operator action) locks as settlement does: the promotion, the passenger's bonus
account (for a bonus), then the driver's wallet.

Security and abuse controls:
- Referral capture only through signed channels (the bot update behind the service secret, signed
  `initData`); codes are random, opaque and carry no personal data; the client's
  `promotion_discount` is only a confirmation, never an amount the server uses.
- Internal routes need the service secret: `/internal/v1/passenger/{tg_id}/referral` only from the
  passenger bot (`403`), `/internal/v1/bots/{bot}/users/{tg_id}/marketing` only for the calling bot.
- Admin: RBAC, CSRF, audit, masked people, aggregate exports; approvals for money and large
  broadcasts (a scheduled broadcast is checked again when it starts; an approver may pin the
  campaign version they reviewed); the audience an approver approves is frozen (broadcast
  targeting and `passenger_credit` grants at scheduling); test sends only to the staff ids in
  `marketing.test_recipients`, never to anyone who opted out or blocked the bot, only while
  broadcasts are on, 10 per broadcast and 30 in total per hour; driver copy never names the
  commission; broadcast text escaped and buttons limited to the Mini App with a validated start
  parameter; bot tokens redacted from delivery errors.
- Fraud controls: the first ride must be completed by a driver; new invitees only (window, no
  orders, not a driver); self-referral, same phone, the referrer driving and blocked users rejected;
  rides under 60 s held for review; a first ride a dispute cancels loses its coupon and flags its
  referral reward; once-only rules in the database; budgets under a lock; coupon void, redemption
  freeze, campaign pause, reversal; reconciliation. Cash itself cannot be verified (§23.14, item
  10).

### 23.14 Owner decisions

The owner's specification left ten questions open. The defaults below are implemented; each lists
what changes it (a new campaign version or a setting) or says that a code change is needed.

| # | Question | Implemented default | Why it is safe | Alternatives |
|---|---|---|---|---|
| 1 | Coupon expired unused: may the passenger invite friends? | No: `unlock: coupon_redeemed` | the assignment literally; the two promotions never overlap | `coupon_redeemed_or_expired`, `first_ride_completed` (new version) |
| 2 | Passengers who rode before the launch | No first-ride coupon (not their first ride); no invitations under the default unlock (preview warning `legacy_passengers_locked`) | no retroactive cost; budget only for new riders | `first_ride_completed` unlock; a `passenger_credit` campaign for a segment of earlier riders |
| 3 | What is the "second ride" | The next taxi order created while the coupon is valid with fare ≥ 400; a cancelled order returns the coupon; deliveries never consume it | one coupon, one redemption, database-enforced | `categories`, `validity_hours`, `min_order_fare` (new version) |
| 4 | Fare below 400 | The coupon is not applied and stays for a later order; fare 400 → cash 0; no partial redemption | the cash due never goes negative; no remainder to track | `min_order_fare` ≥ `amount` (new version); partial redemption is a code change |
| 5 | Driver settlement | The discount is credited to the driver's Jan Taxi balance at completion; it pays commissions; not withdrawable | exact, idempotent, reconciled; no cash leaves the company | **owner to decide** whether a cash payout process is needed (code and accounting change) |
| 6 | Commission basis | Gross fare, charged at acceptance (fixed 50 ₸ or the village percentage) | the driver's net equals an order without a promotion | net-of-discount basis is a code change |
| 7 | Referral attribution | The first valid code wins; the invitee must be new (account ≤ 60 min, no orders, not a driver); captured from the bot start or the signed Mini App start; never rewritten | no hijacking, no rebinding, every attempt logged | `attribution_window_minutes` 5..10 080 (new version) |
| 8 | Bonus partial spend and expiry | Pays `min(available, fare)` of a taxi order, the rest stays; no expiry | the passenger never loses earned money; ledger exact | expiry or other categories are code changes |
| 9 | Budgets and approval thresholds | 100 000 ₸ per launch campaign; activation by `marketing.approve`; broadcasts above 100 recipients need an admin with both `marketing.broadcast` and `marketing.approve` (the super admin by default); manual credit ≤ 50 000 per operation | spending is capped under a lock; every money switch is audited | budget endpoint; `marketing.broadcast_approval_threshold`; `max_amount` of `manual_adjustments` (new version by `marketing.approve`) |
| 10 | Proof of a cash ride | None is possible for cash; the driver's completion is the evidence | controls of §23.13; the money at risk is commission credit, not cash | raise `min_ride_seconds`; freeze redemptions; pause; void and reverse |
