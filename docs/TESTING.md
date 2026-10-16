# Testing

How to run every test suite of Jan Taxi and what the critical tests prove. All
commands run from the repository root; `make help` lists the targets.

## 1. Requirements

| Tool | Version | Used for |
|---|---|---|
| Go | 1.26 | backend, bots, Go tests |
| Docker | with Compose 2.24.4+ | test database, image builds, config checks |
| Node.js | 24 | frontend tests and builds |
| pnpm | 10 (`corepack enable`; the exact version is pinned in `package.json`) | frontend workspace |

## 2. Test database

Go tests that touch the database run against a real PostgreSQL 17 + PostGIS 3.5,
never mocks.

```sh
make test-db-up      # container jantaxi-devdb on localhost:55432 (user/password jantaxi)
make test-db-down    # stop and remove it (the data volume is kept)
```

- `testutil.NewDB(t)` builds one template database per test run with every
  migration and the seed data applied (city EMB with its zone and tariffs, the
  default settings, the Kazakhstan boundary), then clones a fresh database from it
  for each test and drops it afterwards. Tests are isolated and can run in
  parallel.
- `TEST_DATABASE_URL` selects another server (it must allow `CREATE DATABASE`);
  the default is `postgres://jantaxi:jantaxi@localhost:55432/postgres?sslmode=disable`.
- Without a reachable database, database tests **fail**. To run only the pure
  unit tests on a machine without Docker, set `JT_SKIP_DB_TESTS=1`; database
  tests are then skipped, and the run proves nothing about SQL, transactions or
  concurrency.

## 3. Running the suites

```sh
make test                  # all Go tests: go test -race -count=1 ./...
make lint                  # go vet ./..., gofmt check, vue-tsc type checks
make fe-install            # pnpm install --frozen-lockfile
make fe-test               # vitest, all three apps
make fe-typecheck          # vue-tsc, all three apps
make fe-lint               # project rule checks of each app (scripts/lint.mjs)
make fe-build              # production builds (type check + vite build)
make verify-shared         # shared Mini App core identical in both Mini Apps
make openapi-lint          # Redocly lint + every mounted route is in openapi.yaml
make config ENV_FILE=.env.example   # compose.yaml interpolates and validates
make caddy-validate        # Caddyfile parses with placeholder domains
make preflight             # checks a real .env before deployment
```

Narrower Go runs:

```sh
go test -race -count=1 ./admin/backend/internal/wallets/...
go test -race -count=1 -run TestConcurrentAcceptExactlyOneWinner -v ./admin/backend/internal/orders/
go test -race -count=1 ./passenger/bot/... ./admin/backend/pkg/...
```

Always keep `-race` for the concurrency tests; `-count=1` disables the result
cache so a database change is never hidden by a cached pass.

Frontend, one app at a time:

```sh
pnpm --filter @jantaxi/passenger-miniapp test
pnpm --filter @jantaxi/driver-miniapp typecheck
pnpm --filter @jantaxi/admin-frontend build
```

OpenAPI route coverage alone (opt-in because it fails while routes are being
added):

```sh
JT_CHECK_OPENAPI=1 go test -count=1 -run 'TestRoutesDocumented|TestSpecOperationsParser' ./admin/backend/internal/app/
```

## 4. What the critical tests prove

Test names are given so a reviewer can open the code; file paths are relative to
`admin/backend/internal/` unless stated otherwise.

### Exactly one driver wins an order

`orders/concurrency_test.go`, through the real HTTP handlers and database:

| Test | Proves |
|---|---|
| `TestConcurrentAcceptExactlyOneWinner` | 50 eligible drivers accept the same order at the same moment: exactly one gets 200, all others `409 order_already_taken`; one reservation and one `commission_reserve` ledger row exist; money moves once. |
| `TestConcurrentCompleteChargesOnce` | 20 simultaneous "complete" requests settle the commission exactly once. |
| `TestAcceptInsufficientBalance` | `402 insufficient_balance` with the amounts, and nothing in the order or wallet changes. |
| `TestDriverWithActiveOrderCannotAccept` | a driver who already holds an active order gets `409 driver_has_active_order`. |

### The order state machine holds

| Test | Proves |
|---|---|
| `orders/transitions_test.go` `TestInvalidTransitions` | every action not allowed from the current state returns `409 invalid_transition` with `{from, action}` and changes nothing. |
| `TestCancellationReleasesReservation` | passenger and driver cancels after acceptance release the reserved commission. |
| `TestPassengerIDOR` | another passenger gets 404 for every order endpoint; a driver sees only orders offered to or held by them. |
| `orders/lifecycle_test.go` `TestFullLifecycle` | one order from quote to rating through the HTTP API, checking money, stops, notifications and history at each step. |
| `TestStopsOrderPreserved` | five stops keep their order end to end; intermediate stops can only be completed in sequence. |

### Money is never created, lost or charged twice

`wallets/commission_test.go` and `wallets/funds_test.go`, directly against the
ledger:

| Test | Proves |
|---|---|
| `TestReserveSettleReleaseFlow` | reserve, settle and release move `balance` and `reserved` exactly as in ARCHITECTURE §6. |
| `TestReserveReplayIsIdempotent`, `TestSettleReplayReturnsPreviousCharge` | repeating an operation with the same idempotency key returns the first result and writes nothing new. |
| `TestConcurrentReserveExactlySevenWin` | a driver with 700 ₸ and 50 orders reserving 100 ₸ each at the same time: exactly 7 succeed and 43 get `ErrInsufficientBalance`; `reserved` never exceeds `balance`. |
| `TestConcurrentSettleChargesOnce` | concurrent settlements of one order charge once. |
| `TestZeroCommissionWritesNoLedgerRows`, `TestReReserveSameDriverAfterRelease`, `TestRefundCommission` | edge cases of the commission lifecycle, including the dispute refund. |
| `TestCreditTopUpOnce`, `TestDebitTopUpRefund`, `TestAdjust` | top-up credits, refunds and admin adjustments are applied once per key. |
| `TestAdjustKeyReusedAcrossWalletsConcurrently` | one adjustment key cannot be applied to two wallets, even concurrently. |
| `TestLedgerIsAppendOnly` | the database trigger refuses `UPDATE` and `DELETE` on `wallet_ledger`. |
| `TestReconcileDetectsCorruption` | the reconciliation shown to admins finds a wallet whose balance differs from the sum of its ledger rows. |

### Top-ups credit only once and only after verification

`payments/*_test.go`:

| Test | Proves |
|---|---|
| `TestTopUpLifecycle` | a receipt and "I paid" leave the balance at 0; the finance confirmation credits it once (one ledger row, one notification), and a second confirmation changes nothing. |
| `TestConcurrentConfirmCreditsOnce` | 8 concurrent confirmations by two finance admins credit the wallet once. |
| `TestSameTransactionOnTwoTopUps` | one Kaspi transaction number cannot confirm two top-ups (`payment_reference_used`). |
| `TestRejectTopUp`, `TestRefundTopUp`, `TestExpiryJob` | rejection never credits, a refund debits once, unpaid top-ups expire. |
| `TestAdminPermissionMatrix` | every admin role gets exactly the finance permissions of ARCHITECTURE §4.2 on each finance route, and 403 otherwise. |
| `TestDriverCannotSeeOthersTopUp` | drivers see only their own top-ups and receipts. |
| `TestWebhook` | the payment webhook returns 404 while no verified provider is configured. |
| `payments/app_e2e_test.go` `TestFinanceThroughApp` | the real wiring end to end: driver sign-in, top-up, "I paid", finance admin confirmation, wallet balance. |

### Notifications are delivered once and fail safely

`notifications/worker_test.go` with a fake Telegram server:

| Test | Proves |
|---|---|
| `TestTwoWorkersSendEachMessageOnce` | two workers on the same outbox never send a message twice (`FOR UPDATE SKIP LOCKED` + lease). |
| `TestWorker429RetryAfter`, `TestWorker5xxBackoffThenSuccess` | Telegram's `retry_after` is honoured; server errors back off and later succeed. |
| `TestWorker403MarksBlocked` | a user who blocked the bot is marked, and the row goes to `dead` without retries. |
| `TestWorkerMaxAttemptsDead`, `TestWorkerOther4xxAndRenderFailuresAreDead` | permanent failures stop after the attempt limit. |
| `TestWorkerReclaimsExpiredLease` | a row claimed by a crashed worker is picked up again. |
| `TestWorkerPerChatInterval` | one chat receives at most one message per second. |
| `TestWorkerSendsInUserLanguage`, `TestWorkerUsesAdminOverride` | kk/ru/en rendering and admin-edited templates. |

### Realtime events follow commits

`realtime/publish_test.go` `TestPublishDeliveredOnlyOnCommit`: an event published
inside a rolled-back transaction never reaches a client; a committed one does.
`TestListenerReconnectsAndAsksForResync`: after the LISTEN connection drops,
clients are told to re-sync.

### Authentication

| Test | Proves |
|---|---|
| `auth/initdata_test.go` | Telegram `initData` is accepted only with a correct HMAC from the matching bot (a passenger launch cannot sign in to the driver app), within the allowed age. |
| `auth/apptoken_test.go` | app tokens reject tampering, expiry and the wrong app; CSRF tokens are bound to the session and the secret. |
| `auth/admin_test.go` | admin login, session expiry, CSRF on writes, lockout after repeated failures, protection of the last super admin. |
| `app/app_test.go` | every protected router refuses unauthenticated requests; login rate limit; `/readyz` returns 503 while migrations are pending; the app refuses to start when an auth middleware is missing. |

### Telegram bots

`admin/backend/pkg/botapi/runtime/poller_test.go`: each update is processed once
and the offset stored; a restart does not process an update twice (claim);
only the lease holder polls; `409 Conflict` and `retry_after` back off; an
active webhook is removed; shutdown finishes the current update.
`internal/botapi/lease_test.go`: lease exclusivity under concurrent acquisition,
idempotent update claims, cleanup of old claims.

### Service area and input rules

`platform/geo/geo_test.go` checks the Kazakhstan boundary against the database:
Embi, Almaty, Saryagash, Baikonur and Astana are inside; Orenburg, Tashkent,
Bishkek, Moscow and Urumqi are outside. `platform/textutil`, `platform/phone`
and `platform/plate` cover the 120-code-point limit (Kazakh letters and emoji),
Kazakhstan mobile numbers and plate formats.

### Frontend

Vitest suites in each app (`src/**/*.test.ts`) cover the shared core: text
limits counted like the server, money/distance/time formatting in kk/ru/en, the
HTTP client's error envelope and single re-authentication on 401, the realtime
client's auth frame, backoff and re-sync, Telegram fullscreen and location
handling with a mocked SDK, start-parameter routing, map fitting; and in the
admin panel the CSRF header, login redirects and permission-based navigation.

## 5. What is not covered automatically

- Behaviour inside real Telegram clients (iOS, Android, Desktop, Web): fullscreen,
  safe areas, haptics, location and contact permissions, `t.me` links. Test on
  devices with staging bots ([TELEGRAM_SETUP.md](TELEGRAM_SETUP.md)).
- Real Kaspi Pay payments (verification is manual by design).
- The OSRM dataset build and the self-hosted Photon import (`make osrm-prepare`,
  `make geocoder-import`): run once on staging and check a few routes and searches.
- Backup restore: run the monthly restore drill
  ([OPERATIONS.md](OPERATIONS.md#restore-and-restore-drill)).
