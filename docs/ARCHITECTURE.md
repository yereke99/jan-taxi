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
  and the driver bot offer, the driver Mini App and the admin panel show the text as the pickup. Moving
  the pin or choosing another place for A replaces the text. Without a pin or GPS position the app does
  not offer the typed pickup. Stops after A may instead be `manual`, without coordinates (§5).

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

Route points: pickup A first (always with coordinates; its address may be the passenger's own text,
§3), up to three intermediate stops, the destination last. A stop after A may be `manual` — the
passenger's words with `NULL` coordinates — while `orders.manual_destination_enabled` is on and
`orders.manual_destination_pricing` is `passenger_tariff_bounds`; such a route gets no OSRM route or
metrics, and each class's quote offers the administrator's tariff range (`fare.recommended: null`,
`pricing_mode: passenger_tariff_bounds`).

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

* A unique-key conflict on the ledger means "already applied" → the operation returns the
  existing result (idempotent), never a second charge.

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

Templates (keys): `order_created_passenger`, `order_available_driver`, `order_accepted_passenger`,
`order_accepted_driver`, `order_driver_arriving`, `order_started`, `order_stop_completed`,
`order_completed_passenger`, `order_completed_driver`, `order_cancelled_passenger`,
`order_cancelled_driver`, `order_taken_driver`, `order_no_driver`, `moderation_submitted`,
`moderation_approved`, `moderation_changes_requested`, `moderation_rejected`,
`driver_suspended`, `driver_restored`, `topup_created`, `topup_confirmed`, `topup_rejected`,
`wallet_low_balance`, `dispute_opened`, `dispute_resolved`, `phone_verified`; offer edits
`order_offer_won`, `order_offer_taken`, `order_offer_cancelled`, `order_offer_expired` (§22.4).
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
`session_cleanup` (1h).

## 17. Configuration

All configuration is environment variables (see `.env.example`); DB `app_settings` override the
runtime-editable ones (support contacts, Kaspi public payment settings, top-up limits,
prohibited items, min online balance, dispatch radius and location freshness `dispatch.*`).
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
| GET | /admin/tariffs?city_id=&vehicle_class= ; POST /admin/tariffs (new version, `vehicle_class`) | dashboard.view / config.manage | pricing |
| GET/POST | /admin/vehicle-classes ; PATCH /admin/vehicle-classes/{code} (§21) | config.manage | vehicleclasses |
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
| POST | /internal/v1/driver/{tg_id}/orders/{id}/accept | botapi+dispatch | «✅ Тапсырысты алу» (§22.4): the same atomic accept as the Mini App (`dispatch.AcceptOrder`) → `{order, replayed, charged, wallet}`; 402/404/409/422 as the Mini App |
| POST | /internal/v1/passenger/{tg_id}/contact | botapi | `{phone, contact_user_id}` shared with the passenger bot (§22.5); verifies `contact_user_id == tg_id` |
| POST | /internal/v1/driver/{tg_id}/location | botapi | `{lat, lon, accuracy_m?, live}` |
| POST | /internal/v1/driver/{tg_id}/topup-receipt | botapi+payments | multipart `file` (Kaspi PDF) → `{outcome: credited\|pending_verification\|duplicate_rejected, reasons[], topup, balance}`; 409 `no_open_topup` / `receipt_already_used`, 413, 422, 429 (§6.1) |
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
- Driver registration: a "Көлік түрі" step with class cards (icon, name, examples), seat stepper
  bounded by the class, cargo volume prefilled from the class default (editable).
- Passenger: after the route, a compact iOS list of class options (icon, name, "4 орын", price),
  a passenger-count stepper for taxi and a cargo-size segmented control for parcels; changing them
  re-quotes. The existing passenger flow stays as it is otherwise.
- Admin: Vehicle classes page, class column on tariffs, class/seats/volume on driver detail.

## 22. Business rules v2 (owner decisions, 2026-10-19)

These rules supersede §5 (driver cancellation), §6 (commission timing/amount) and §8 (driver
offers) where they conflict.

### 22.1 Welcome balance for new drivers
- Setting `driver.welcome_bonus` (KZT, default **500**; 0 disables).
- Granted automatically **once**, when an admin approves a driver for the first time, and only if
  neither the driver's phone (E.164) nor the vehicle plate (normalised) has ever received it:
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
- **Drivers do not see the commission on order surfaces** (owner, 2026-10-31): not on the order
  cards, the order screen, the trip screen, the trip history, the completion screen (only «Сапар
  сәтті аяқталды»), the bot offers, the bot `/orders` list, the trip card or the completion message;
  shortfall messages say how much the balance needs, not "commission". Accounting is unchanged: the
  amount is snapshotted, charged at acceptance, kept in the ledger (the wallet history still lists
  `commission_charge` entries) and shown to admins. The driver DTOs keep `commission_amount` /
  `commission_charged` for compatibility; the apps do not render them.

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
