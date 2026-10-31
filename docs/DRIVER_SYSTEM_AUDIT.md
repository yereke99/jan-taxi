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
| Kaspi | Server-side «Kaspi Платежи» intake keyed by the driver code, disabled until Kaspi grants access; PDF receipts keep working. |
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

## 5. Parallel work items

The registration overhaul, the Kaspi Платежи intake, the call/WhatsApp fix and the passenger
booking fixes were implemented and tested separately and merged afterwards; their findings,
migrations and results are recorded in §6 and §7.

## 6. Database migrations

To be completed with the merged registration and Kaspi changes.

## 7. Verification

To be completed with the final test run.
