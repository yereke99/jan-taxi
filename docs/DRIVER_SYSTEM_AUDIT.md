# Jan Taxi — driver system audit and changes, 31 October 2026

Baseline: commit `3e12d4d` on `main`. Sources of the requirements: the owner's driver-system
specification and the customer's technical assignment «JAN TAXI — ТЕХНИКАЛЫҚ ТАПСЫРМА»
(30.10.2026, seven items). Where they differ, the customer's assignment wins; the owner confirmed
the open choices on 31.10.2026 (see "Decisions"). The design contract stays docs/ARCHITECTURE.md;
this file records what was found, why it happened and what changed.

## 1. Decisions (owner, 31.10.2026)

| Topic | Decision |
|---|---|
| Registration | Page 1: profile photo, first name, surname, optional patronymic, 12-digit IIN (ЖСН) with checksum, phone shared through Telegram. Page 2: make and model, colour, plate, one body-type row pre-set to Sedan. No ID/licence scans, no vehicle photos, no OCR. |
| Approval | No manual approval: automatic checks at submission, then the driver works at once (customer item 1.3). Admins can still suspend. |
| IIN storage | Plain text column with a unique partial index; masked in the admin panel; never in passenger payloads, bot offers, snapshots or logs. |
| Card button | Full-width green «Тапсырысты алу» at the bottom of every available-order card (customer's reference picture). |
| Passenger phone | Only the driver who took the order receives it (existing privacy rule); offers carry the route, fare and comment. |
| Kaspi | No Kaspi bank integration (owner, 31.10.2026, final): top-ups stay as they were — Kaspi Pay link plus the Kaspi PDF receipt sent to the bot or the Mini App. |
| Delivery | Commit and push to `origin/main`. |

## 2. Architecture touched

`api` (Go): `dispatch` (offers, radius, atomic accept, expansion), `orders` (+`lifecycle`),
`notifications` (outbox, templates, worker), `realtime` (pg_notify → LISTEN → WebSocket hub),
`drivers` (availability, locations, registration), `moderation`, `botapi` (internal API for the
bots), `wallets`. `driver-bot` (long polling, `driverbot`): offers, «Тапсырысты алу», `/orders`.
Driver Mini App (Vue 3): Orders tab (`OrdersPage`, `OrderCard`), order screen (`OrderDetailPage`),
trip (`TripPage`), history, runtime wiring (`app/runtime.ts`), shared realtime client.

Order flow, as it works now:

1. The passenger creates an order (`POST /orders`); in the same transaction every free driver whose
   fresh GPS fix is within the base radius of pickup A gets an `order_offers` row, an
   `order_available_driver` bot message (outbox) and `order.available` on the WebSocket.
2. `dispatch_expand` (15 s) offers the order to drivers reached later by the growing radius. A
   driver who goes online or sends a fresh fix after a stale one is offered the waiting orders
   around them at once (`dispatch.OfferNearby`).
3. The first «Тапсырысты алу» — card, order screen or bot — reaches `dispatch.AcceptOrder`; one
   transaction assigns the order, charges the commission, closes the other offers, queues the
   passenger's push and the winner's trip card, edits the other offers to "taken" and publishes
   `order.accepted`, `order.taken` and `order.unavailable`, all delivered only on commit.
4. Every open driver list drops the card within about a second (`order.unavailable` to the
   `drivers` scope); old bot buttons stay safe because every tap is re-checked.

## 3. Confirmed root causes and fixes

| # | Severity | Found (baseline) | Root cause | Change |
|---|---|---|---|---|
| A1 | High | A winner's duplicate tap arriving together through the bot and the Mini App (or a double tap, or a retry) got `402 insufficient_balance` or `409 order_already_taken`; the bot then told the winner "another driver took it" and removed the keyboard. Reproduced by `TestSameDriverDoubleAccept` (failed 3/3 before the fix). | The order row was read without a lock; the second transaction waited on the wallet lock and then re-checked balance and status as a stranger would. | `AcceptWith` locks the order row first (order → driver profile → wallet, the same order as the other transitions); the replay check runs under the lock. Admin assignment takes locks in the same order. |
| A2 | High | After «Алу» in the bot the passenger's phone reached the driver only through a best-effort send in the bot process: a lost response, a timeout or a bot restart lost it for good (a replay never re-sent it). | The bot channel bypassed the transactional outbox. | The trip card (route, class, load, comment, entrance details, passenger link and phone) is queued in the outbox for both channels: sent once, retried, withheld if the order was cancelled or reassigned before sending. |
| A3 | Medium | Two acceptance code paths wrapped the shared domain call differently and logged nothing. | — | One operation `dispatch.AcceptOrder` (transaction, error mapping, structured log with order, driver, channel, outcome, duration and request id); the bot sends `X-Request-ID: tg-cb-<callback id>`. |
| R1 | High | A card stayed in the Mini App list up to 20 s after another driver took the order when the viewing driver had no offer row (came online or moved after the order was created). | Close events were addressed to offer holders only, while the list is a live radius query. | New `drivers` WebSocket scope (approved drivers) carrying `order.unavailable {order_id, status}` on every accept, cancel and expiry of a searching order. |
| R2 | Medium | A driver who went online or got a fresh GPS fix received bot offers for orders already waiting only on the next 15 s expand run. | Dispatch ran only at creation and in the periodic sweep. | `dispatch.OfferNearby` right after going online and after a fresh fix that follows a stale one (same per-order lock and dedup as the job). |
| R3 | Medium | The Mini App missed hints in narrow windows: first connection, an event during an in-flight trip or wallet request, a half-open socket after the app returned from the background; a top-up did not bring orders back. | Client-side gaps. | Resync on the first connection; follow-up requests in the trip and wallet stores; a ping probe on foreground (reconnect after 5 s of silence); list refresh on wallet events and on going offline; 100–300 ms jittered refresh on `order.available`; polling only as a fallback (20 s offline, 60 s online). |
| B1 | Medium | Bot texts: commission in offers, `/orders`, trip card, completion and low-balance messages; «Маршрут: 0 м» for orders with a manually typed stop; tapping one order of an `/orders` list rewrote the whole list; a fixable failure (balance, offline, location) removed the accept button; an expired search was reported as "cancelled". | Texts and keyboard logic predated the owner's decisions and manual stops. | New offer layout and labels («✅ Тапсырысты алу», «📱 Қосымшаны ашу»), commission removed, list rows edited one at a time, buttons kept for fixable problems, «⌛ Іздеу уақыты бітті» for expired searches, unknown route length left out, bare coordinates never shown. |
| B2 | Medium | An offer already claimed by the outbox worker was still delivered after the order was taken or cancelled. | No send-time check. | The worker drops an `order_available_driver` row whose order no longer searches. |
| U1 | Medium | Accepting needed two taps (card → order screen → accept); the card was a `<button>`, so a nested button was impossible. | Card built as one tap target. | `<article>` with two sibling buttons; full-width green «Тапсырысты алу»; one shared `useAcceptOrder` for the card and the order screen (one acceptance at a time, location fixed in place, network loss checked against the active trip, distinct texts for taken / out of reach / closed). |
| U2 | Medium | Commission and net earnings shown on the order screen, trip screen, completion screen and history; the completion screen listed fare, commission and earnings (customer item 4). | §22.2 design predated the owner's decision. | Fare-only `FareBreakdown`; completion shows only «Сапар сәтті аяқталды»; history shows fares; shortfall texts say how much the balance needs. Accounting, ledger and admin views unchanged. |
| U3 | Low | Opening an order from a bot offer after going offline or with a stale fix showed "order unavailable". | The detail endpoint required full eligibility. | A driver holding an open offer can open it; the accept button explains what to fix. |

Registration, calls, passenger booking and Kaspi findings are in §5.

## 4. Concurrency and consistency

* Exactly one winner: order row `FOR UPDATE`, conditional `UPDATE … WHERE status='searching' AND
  driver_id IS NULL`, partial unique index `orders_one_active_per_driver`. Tests:
  `TestConcurrentAcceptExactlyOneWinner` (50 Mini App drivers), `TestConcurrentBotTaps` (20 bot
  taps), `TestAcceptAcrossChannels` (10 bot + 10 Mini App), `TestSameDriverDoubleAccept`
  (12 mixed repeats of one driver: one acceptance, one charge, the rest replays).
* Exactly-once money: commission ledger key `charge:<order_id>`; replays charge nothing.
* Events and messages leave only on commit (`pg_notify` and outbox rows inside the transaction);
  every API replica LISTENs, so WebSocket clients on any replica get them.
* Lock order order → driver profile → wallet for accept, assign, complete and cancel.
* Known limits: the bot handles updates one at a time (a burst of taps queues behind each other;
  the Mini App path is unaffected); `order.unavailable` fans out to every approved driver's
  socket (tiny payload, fine at the current scale).

## 5. Other items of the customer's assignment

| Item | Found (baseline) | Root cause | Change |
|---|---|---|---|
| Registration (customer 1) | Eight steps (personal data, phone, ID scans, class with seats and cargo, car, car photos, consent, review); every driver waited for a moderator or the document OCR worker. | Designed for manual document review. | Two pages: (1) profile photo, first name, surname, optional patronymic, IIN with its check digit, phone shared through Telegram → «Келесі»; (2) body type preset to Sedan, make and model, colour, plate, consent → «Тіркелуді аяқтау». Submission runs the automatic checks (IIN check digit, verified phone, IIN, phone and plate not held by another live driver) and approves in the same transaction; the app then takes a GPS fix, goes online and opens Orders. OCR worker, ID scans and car photos removed; drivers registered earlier add their IIN once from the profile. |
| Calls (customer 5) | Driver → passenger and passenger → driver call buttons did nothing on iPhones. | Telegram for iOS lets its WebView load a same-window `tel:` link, which the WebView cannot open; the passenger buttons used same-window links, and a call button in a sheet header could start a sheet drag that swallowed the tap. | Every call button is a `tel:` link opened in a new window (handed to the phone app by every Telegram client); contact controls never start a sheet drag; the numbers are shown and copy on tap as the fallback. Device checklist: docs/TESTING.md §6. |
| WhatsApp (owner) | The driver's passenger row had a chat-bubble button. | — | A WhatsApp button (`https://wa.me/<digits>`, opens the chat, sends nothing) when the passenger shared a phone; otherwise the Telegram chat button stays in that place. |
| Manual pickup A (customer 2) | Only stops after A could be typed. | A must keep coordinates for dispatch by radius. | A typed address for A keeps the pin/GPS coordinates; drivers see the typed text. No schema change. |
| Taxi price (customer 6) | After choosing the class the passenger still had to type the price on routes with a manual stop. | The fare was cleared for manual routes and a required input was shown. | Taxi orders always go at the chosen class price; the price input stays only for parcels and other deliveries. |
| Kaspi (customer 7) | — | — | Not changed by the owner's final decision: PDF receipts as before. |

## 6. Database migrations

One new migration, `00013_simplified_registration.sql` (no Kaspi migration: the integration was
cancelled):

* `driver_profiles.iin text` (NULL, twelve digits by a check constraint) and
  `driver_profiles.patronymic text NOT NULL DEFAULT ''` (at most 60 characters);
* unique partial index `driver_profiles_iin_unique` over the live statuses (pending, approved,
  changes_requested, suspended), so a draft or a rejected profile never blocks an IIN.

It only adds: no table rewrite (both columns are metadata-only on PostgreSQL 11+), no backfill, and
the index is built over a column that is still empty. The `migrate` service applies it during
`make up`, before the API starts. The down migration refuses to drop the columns once an IIN or a
patronymic is stored. The tables of migration 00011 (the retired document OCR) stay with their rows;
nothing writes to them any more. The previous release (`3e12d4d`) reads named columns only, so
going back to its images after 00013 needs no down migration.

## 7. Verification (31.10.2026)

| Check | Result |
|---|---|
| `gofmt -l`, `go vet ./...` | clean |
| `go test -race -count=1 ./...` (PostGIS 17 / 3.5) | 43 packages ok, 0 failures |
| `JT_CHECK_OPENAPI=1 … TestRoutesDocumented\|TestSpecOperationsParser` | pass |
| Driver Mini App: `vue-tsc`, vitest, lint, `vite build` | 516 tests passed, build ok |
| Passenger Mini App: the same | 374 tests passed, build ok |
| Admin panel: the same | 115 tests passed, build ok |
| `verify-shared` | 85 shared files identical |
| Upgrade through the Makefile, local Docker | see below |

Upgrade check, run the way the server is updated: the previous release (`3e12d4d`) started with
`make build` and `make up`, using an env file made from its `.env.example` with `VERIFICATION_*` /
`OCR_*` lines and `COMPOSE_PROFILES=ocr` left in. Drivers were added in every status, and an
`ocr-worker` container was created by the old compose file. Then the new tree was updated with
`make preflight`, `make config`, `make build`, `make up` and `make health`:

* The `migrate` service moved the schema from 12 to 13. The drivers kept their statuses, with an
  empty IIN and patronymic. The 00011 tables are still there.
* `/readyz` reported ready at schema 13, and both bots were healthy.
* The old `ocr-worker` container was removed by `--remove-orphans`.
* Against a stand-in Telegram API, `/start` worked as expected:
  * an approved driver without an IIN got the work menu;
  * a new user got the two-page registration text;
  * a driver with requested corrections got the new correction text;
  * the passenger bot sent its welcome.
* The driver and passenger Mini Apps and the API were served through Caddy.
* The bots reconnected on their own after the stand-in API was restarted.

Not covered here: real Telegram clients (iOS, Android, Desktop). For calls, see the device checklist
in docs/TESTING.md §6; registration checks on a phone are listed in the completion report.
