# Jan Taxi — Handoff (status as of 2026-10-16, ~22:30 Asia/Aqtobe)

This file tells the next engineer exactly what is done, what is verified,
and what is left. Read `docs/ARCHITECTURE.md` first — it is the binding design contract
(module boundaries, order state machine, money model, auth, realtime, endpoint catalogue §19,
cross-package Go APIs §20, iOS 26 design system §15/§15.1).

## 0. Latest status (2026-10-17, work paused)

- All tests green at pause time: `go test ./...` → 39 packages ok; `pnpm -r test` → passenger 224, driver 261, admin 64; `go vet` and `pnpm -r typecheck` clean.
- Done since the first handoff: radius-based dispatch (5 km → +2.5 km/min → 10 km around pickup A, fresh driver GPS required, no city binding; settings `dispatch.*`); passenger map fixes from a real-phone test (route framing, pin reverse-geocoding loop, local address search within 60 km, no raw coordinates shown, single Telegram back button); iOS 26 floating tab bars; demo mode (`VITE_DEMO=1`, `dev:demo` / `build:demo`) that never ships in production builds.
- Paused mid-way (finish these first):
  1. **Kaspi PDF receipts** (backend mostly written, needs completion + review): `admin/backend/internal/payments/receipt/`, `internal/platform/pdfdoc`, `storage/pdf.go`, migration `00006_kaspi_receipts.sql`, driver bot PDF intake (`driver/bot/internal/driverbot/receipt.go`, `internal/botapi/receipt.go`). Rules: receipt key = `№ чека` value `^QR\d{8,}$` (e.g. QR18149336524), unique in the DB together with file sha256; seller BIN must be 000809500289 (ИП "JAN-TAXI"); amount must equal the top-up request; fresh date (Astana time, `dd.mm.yyyy HH:MM`); PDF producer `WeasyPrint*`, single revision; per-driver daily auto limits; admin kill switch `topup.auto_confirm_enabled`. When all checks pass the wallet is credited automatically through the same ledger path as admin confirmation; otherwise pending for admin with reasons; duplicates rejected. Real sample receipts are kept outside the repo — never commit them. Still to do: adversarial fraud review, driver Mini App top-up screen with PDF upload, admin receipt viewer.
  2. **End-to-end UI pass on a real phone**: driver Mini App showed red error banners for a fresh driver (likely onboarding/403 `driver_not_registered` or `location_required` treated as errors) — reproduce and fix; then walk every passenger and driver flow against the live API.
  3. **Bot runtime bug**: after a network drop the long-polling loop can stall (health shows `last_poll_age_s` > 90) and never recovers without a restart — add per-request deadlines/transport timeouts and a self-restart in `admin/backend/pkg/botapi/runtime`. Also the passenger bot did not refresh its default menu button on restart while the driver bot did.
- Local live testing used a temporary Cloudflare quick tunnel (URL changes on every restart); production needs a real domain + Kazakhstan-hosted server (see §4.8). Public OSRM/Photon servers are for development only (OSRM demo forbids commercial use; self-host it — free, ~4 GB RAM).

## 1. Repository map

| Path | What |
|---|---|
| `admin/backend/cmd/api` | Central Go API binary: `api serve`, `api migrate up\|status`, `api admin create`, `api healthcheck` |
| `admin/backend/cmd/devlink` | Dev tool: prints locally signed Telegram Mini App URLs (no real Telegram needed) |
| `admin/backend/internal/*` | Domain packages: auth, users, cities, settings, audit, realtime, notifications, wallets, routing, pricing, orders(+lifecycle), dispatch, stops, drivers, vehicles, moderation, payments, reports, botapi, app (wiring), platform/* |
| `admin/backend/migrations` | goose SQL migrations 00001–00006 (schema, Kazakhstan boundary, seed for Embi, driver consent, retention indexes, Kaspi receipts + payee seed) |
| `admin/backend/api/openapi.yaml` | OpenAPI 3.1 contract (served at `/api/v1/openapi.yaml`) |
| `admin/backend/pkg/telegram` | Bot API client (used by API notification worker and both bots) |
| `admin/backend/pkg/botapi` (+`/runtime`) | Internal-API client + shared long-polling bot runtime |
| `passenger/bot`, `driver/bot` | The two Telegram bots (long polling) |
| `passenger/miniapp`, `driver/miniapp` | Vue 3 + TS Mini Apps (shared core duplicated on purpose; `node passenger/miniapp/scripts/verify-shared.mjs`) |
| `admin/frontend` | Vue 3 admin panel |
| `compose.yaml`, `Dockerfile.*`, `Caddyfile`, `Makefile`, `deploy/`, `.env.example` | Deployment |
| `docs/` | ARCHITECTURE, DEPLOYMENT, OPERATIONS, TELEGRAM_SETUP, DATA_PROTECTION, DIAGRAMS, CUSTOMER_OVERVIEW_KK (Kazakh), TESTING, `design-preview/` PNG mockups |

## 2. Verified state (commands actually run, all green)

- Go: `go build ./...`, `go vet ./...`, `go test -race -count=1 ./...` → **37 packages ok, 0 failures**
  (needs a PostGIS on `localhost:55432`; see §5). Includes:
  - 50 drivers accepting one order concurrently over real HTTP → exactly one winner, 49 × `409 order_already_taken`,
    one reservation, one ledger reserve row (`admin/backend/internal/orders/concurrency_test.go`).
  - Commission charged exactly once under concurrent completes; wallet ledger reconciliation; idempotent top-up credit;
    duplicate Kaspi transaction id rejected; phone/plate uniqueness; geofence (Kazakhstan in, Orenburg/Tashkent/Bishkek out).
- Frontend: `pnpm -r typecheck` clean; `pnpm -r test` → passenger 206, driver 249, admin 61 tests passing.
- Migrations apply cleanly on PostGIS 3.5 / PostgreSQL 17.

## 3. In progress when this file was written

- **Frontend integration agent**: auditing every frontend HTTP/WebSocket call against the Go handlers and fixing
  mismatches; fixing `verify-shared` (`src/utils/twogis.ts` exists only in the driver app by design — exclude it
  from the shared set); i18n key parity; removing any placeholder.
- **Docker stack agent** (runs after the frontend agent): `docker compose build` + `up` with a local env in the
  scratchpad, smoke tests through Caddy (`http://localhost:8088/passenger/`, `/driver/`, admin on `:8089`).
If these did not finish, redo them (see §4.1 and §4.2).

## 4. Left to do (prioritised)

1. **Frontend ↔ backend contract audit** (if not completed): for every call in `passenger/miniapp/src/api`,
   `driver/miniapp/src` (api/features), `admin/frontend/src/api|services`, compare with the Go DTOs in
   `admin/backend/internal/<pkg>/` (json tags are the truth). Then `pnpm -r typecheck && pnpm -r test && pnpm -r build`.
2. **Docker bring-up** (if not completed): on Apple Silicon use `POSTGIS_IMAGE=imresamu/postgis:17-3.5-alpine`
   (official `postgis/postgis` has no arm64). `docker compose -p jantaxi-local --env-file <env> up -d --build`,
   check `/readyz`, SPA deep links, admin login, WebSocket through Caddy, backup service output.
3. **OpenAPI reconciliation**: the reconciliation agent was stopped mid-run to save budget. Make
   `admin/backend/api/openapi.yaml` match the Go handlers; verify with
   `JT_CHECK_OPENAPI=1 go test -count=1 -run TestRoutesDocumented ./admin/backend/internal/app` and
   `npx @redocly/cli@1 lint admin/backend/api/openapi.yaml`. First check the YAML is still valid.
4. **Cross-system end-to-end suite** in `admin/backend/tests/` (not written yet; package-level e2e tests exist,
   e.g. `orders/app_e2e_test.go`). Scenarios to cover: full golden path (passenger order → driver registration with
   uploads → contact verification via internal API → corrections → approval → Kaspi top-up confirmed by admin →
   online → accept → arriving → start → stops in order → complete → 10% commission charged once), notifications
   delivered to a fake Bot API with `web_app` buttons, WebSocket events, bots in-process against a fake Telegram.
5. **Real Telegram testing** (cannot be done without real bots): fullscreen/expand on iOS, Android, Desktop;
   safe areas; keyboard; `requestContact` → driver bot contact verification; 2GIS links (Android multi-stop,
   iOS next-stop fallback); returning from Kaspi/2GIS restores fullscreen; menu buttons and direct links
   `https://t.me/<bot>?startapp=<param>&mode=fullscreen`.
6. **Security / finance review** (planned adversarial review was not run): IDOR on every `{id}` route, admin
   RBAC matrix (ARCHITECTURE §4.2), CSRF on all non-GET admin routes, file access rules (§12), wallet invariants,
   rate limits, upload limits, log redaction.
7. **Kazakh copy review** by a native speaker (bots, Mini Apps, notifications, admin).
8. **Production setup** (needs the owner): two bot tokens from BotFather + Main Mini App config + domain;
   domain/DNS/TLS; Kazakhstan-hosted VPS (personal-data localisation law); Kaspi Pay payment link, recipient name
   and access to Kaspi Pay sales history for manual reconciliation; OSRM Kazakhstan data prep
   (`make osrm-prepare`, ~4 GB RAM); self-hosted Photon geocoder (public instances are dev-only);
   strong `SESSION_SECRET` / `INTERNAL_BOT_SERVICE_SECRET`; first admin via `api admin create`.
9. **Data retention**: purge policy/job for driver ID-document images after verification (Law 94-V) — business decision needed.

## 5. How to run locally

```bash
# test database (PostGIS on 55432)
docker run -d --name jantaxi-devdb -e POSTGRES_USER=jantaxi -e POSTGRES_PASSWORD=jantaxi -e POSTGRES_DB=jantaxi -p 55432:5432 imresamu/postgis:17-3.5-alpine
go test -race -count=1 ./...
pnpm install && pnpm -r typecheck && pnpm -r test && pnpm -r build
# API (development)
APP_ENV=development DATABASE_URL=postgres://jantaxi:jantaxi@localhost:55432/jantaxi?sslmode=disable go run ./admin/backend/cmd/api serve
# signed Mini App URL for a browser (dev only)
APP_ENV=development PASSENGER_BOT_TOKEN=<dev token> go run ./admin/backend/cmd/devlink --app passenger
```
See `README.md`, `Makefile` (`make help`) and `docs/DEPLOYMENT.md` for the full stack.

## 5.1 Change 2026-10-17 — radius-based dispatch (product owner correction)

- Dispatch no longer filters by city. A searching order is offered to free drivers (approved, online, not
  blocked, no active order, available ≥ commission) whose **fresh GPS fix** (≤ `dispatch.location_max_age_s`,
  default 300 s) is within the order's **effective radius** of pickup A:
  `min(radius_max_m, radius_base_m + radius_step_m * floor(age_s / radius_step_s))`, defaults 5000 / 2500 / 60 s /
  10000 m (global `app_settings` keys `dispatch.*`, editable in `/admin/settings`). See ARCHITECTURE §7.
- New job `dispatch_expand` (15 s) offers searching orders to newly eligible drivers inside the current radius.
- Going online requires a fresh location (`422 location_required`); accept returns `422 location_required`
  (`details.reason` `location_required|location_stale`) or `404` outside the radius; admin assign warns with
  `offline` / `location_required` / `location_stale` / `out_of_radius`.
- City now means only: tariff (`cities.Resolve`), search timeout, driver code prefix, admin grouping. The columns
  `cities.dispatch_radius_m`, `require_driver_location`, `driver_location_max_age_s` are unused (not dropped) —
  hide them in the admin UI.
- **Pending (other owners):** `admin/backend/api/openapi.yaml` (new settings keys, accept 422/404, assign warning
  values, availability 422 for every city), admin settings UI for `dispatch.*`, Mini App handling of accept
  `422 location_required`.

## 5.2 Change 2026-10-17 — Kaspi PDF receipts, automatic crediting (product owner decision)

- Payee seeded (migration `00006_kaspi_receipts.sql`, `app_settings`): ИП "JAN-TAXI", BIN 000809500289,
  IBAN KZ05722S000057176693, АО "Kaspi Bank", BIC CASPKZKA, КБе 19, address, Kaspi Pay link
  `https://pay.kaspi.kz/pay/xbp1imhd`; new settings `company.*` and `topup.auto_confirm_enabled` (kill switch,
  default on), `topup.auto_max_per_day` (3), `topup.auto_max_amount_per_day` (50 000), `topup.receipt_max_age_hours`
  (48). Seeded rows win over `KASPI_PAY_*` env values.
- Driver uploads (Mini App `POST /driver/wallet/topups/{id}/receipt`, now PDF or image) and PDFs sent to the driver
  bot (new internal `POST /internal/v1/driver/{tg_id}/topup-receipt`) are parsed by `internal/payments/receipt`
  (reader: `internal/platform/pdfdoc`, QR: `github.com/makiuchi-d/gozxing`, pure Go). Rules, flags, reasons and
  the calibration are in ARCHITECTURE §6.1. **Parser calibration: VERIFIED** on one real Kaspi receipt (Russian
  layout); Kazakh labels/status UNCERTAIN until a Kazakh-language receipt is seen.
- Real samples live outside the repo in `../jan-taxi-private/kaspi-receipt-samples`; run
  `JT_KASPI_RECEIPT_SAMPLES=<dir> go test -run TestCalibrationSamples ./admin/backend/internal/payments/receipt`
  with every new sample (it logs no personal data). Tests use synthetic PDFs (`receipt/receipttest`).
- Fuzzing: `go test -run '^$' -fuzz FuzzParse -fuzztime 60s ./admin/backend/internal/payments/receipt` and
  `-fuzz FuzzOpen ./admin/backend/internal/platform/pdfdoc` (90 s each run on 2026-10-17: no crashers).
- Storage: `storage.SavePDF/CheckPDF/ServePDF/ServeFile` (PDF kept byte for byte, 0600, ≤ 5 MiB; encrypted or
  active-content PDFs refused); `ServeImage` hands PDFs to `ServePDF`, so `/admin/files/{id}` serves receipts too.
- Telegram: `telegram.Client.GetFile` + `DownloadFile` (size cap, token never in errors/logs);
  `botapi.Client.UploadTopUpReceipt` (multipart, never retried); `bottest` fakes serve files and record raw bodies.
- **Pending (other owners):** driver Mini App — accept `application/pdf` in the receipt picker, show `receipt`
  (`decision`, `reasons`), handle `409 receipt_already_used` / `rejected_reason: duplicate_receipt`, tell drivers
  to share the Kaspi PDF; admin panel — show `receipt`, `receipts`, flags/reasons, the PDF (`receipt.file_url`),
  pre-fill the confirm form from `confirm_prefill`, mark `confirmed_automatically`, edit the new settings;
  ARCHITECTURE §12 (PDF receipts), §19 (internal route row) and §20 (new storage/telegram/botapi functions).
- `admin/backend/api/openapi.yaml` documents the new route, schemas and responses; the spec still has ~209
  dangling response `$ref`s from before (AdminUnauthorized, AppForbidden, …) — part of the OpenAPI reconciliation (§4.3).

## 6. Key design decisions to keep

- Service area = all of Kazakhstan (PostGIS `country_boundaries.geom_check`, Natural Earth + 3 km buffer); Embi is only the default city/map centre.
- Dispatch = geolocation radius around pickup A (growing with search age), never the driver's or order's city.
- Money in integer KZT; commission `(fare*bps+5000)/10000`, default 1000 bps; reserve on accept, charge once on complete; append-only ledger with idempotency keys.
- Kaspi top-ups are credited by a finance admin entering a unique Kaspi number, or automatically from a Kaspi PDF receipt that passes every check (§5.2); both go through `payments.confirmTopUp`. No public Kaspi API exists.
- Exactly-one acceptance: conditional UPDATE + partial unique index `orders_one_active_per_driver`.
- Telegram: initData HMAC with the matching bot token (`signature` stays in the check string); notifications via a transactional outbox; inline `web_app` buttons (never reply-keyboard web_app buttons — they carry no initData).
- UI: iOS 26 look (Liquid Glass only on floating chrome), white/black Uber-like palette, Kazakh default.
