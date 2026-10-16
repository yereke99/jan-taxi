# Jan Taxi — Handoff (status as of 2026-10-16, ~22:30 Asia/Aqtobe)

This file tells the next engineer (or coding agent) exactly what is done, what is verified,
and what is left. Read `docs/ARCHITECTURE.md` first — it is the binding design contract
(module boundaries, order state machine, money model, auth, realtime, endpoint catalogue §19,
cross-package Go APIs §20, iOS 26 design system §15/§15.1).

## 1. Repository map

| Path | What |
|---|---|
| `admin/backend/cmd/api` | Central Go API binary: `api serve`, `api migrate up\|status`, `api admin create`, `api healthcheck` |
| `admin/backend/cmd/devlink` | Dev tool: prints locally signed Telegram Mini App URLs (no real Telegram needed) |
| `admin/backend/internal/*` | Domain packages: auth, users, cities, settings, audit, realtime, notifications, wallets, routing, pricing, orders(+lifecycle), dispatch, stops, drivers, vehicles, moderation, payments, reports, botapi, app (wiring), platform/* |
| `admin/backend/migrations` | goose SQL migrations 00001–00004 (schema, Kazakhstan boundary, seed for Embi, driver consent) |
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

## 6. Key design decisions to keep

- Service area = all of Kazakhstan (PostGIS `country_boundaries.geom_check`, Natural Earth + 3 km buffer); Embi is only the default city/map centre.
- Money in integer KZT; commission `(fare*bps+5000)/10000`, default 1000 bps; reserve on accept, charge once on complete; append-only ledger with idempotency keys.
- Kaspi top-ups are credited ONLY by a finance admin entering a unique Kaspi transaction id (no public Kaspi API exists).
- Exactly-one acceptance: conditional UPDATE + partial unique index `orders_one_active_per_driver`.
- Telegram: initData HMAC with the matching bot token (`signature` stays in the check string); notifications via a transactional outbox; inline `web_app` buttons (never reply-keyboard web_app buttons — they carry no initData).
- UI: iOS 26 look (Liquid Glass only on floating chrome), white/black Uber-like palette, Kazakh default.
