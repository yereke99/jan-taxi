# Diagrams

Mermaid diagrams of how Jan Taxi works. GitHub, GitLab and most Markdown
viewers render them directly. The binding rules behind every diagram are in
[ARCHITECTURE.md](ARCHITECTURE.md) (section numbers are given with each
diagram); the HTTP details are in
[admin/backend/api/openapi.yaml](../admin/backend/api/openapi.yaml). A Kazakh
overview for the customer is in [CUSTOMER_OVERVIEW_KK.md](CUSTOMER_OVERVIEW_KK.md).

Contents:

1. [System architecture](#1-system-architecture)
2. [Deployment topology](#2-deployment-topology)
3. [Passenger booking flow](#3-passenger-booking-flow)
4. [Dispatch and atomic acceptance](#4-dispatch-and-atomic-acceptance)
5. [Order state machine](#5-order-state-machine)
6. [Driver registration and moderation](#6-driver-registration-and-moderation)
7. [Driver navigation](#7-driver-navigation)
8. [Wallet and commission flow](#8-wallet-and-commission-flow)
9. [Kaspi Pay top-up](#9-kaspi-pay-top-up)
10. [Notification outbox](#10-notification-outbox)
11. [Realtime updates](#11-realtime-updates)

## 1. System architecture

ARCHITECTURE §1. Only the API talks to PostgreSQL. The bots reach the API's
internal listener with a shared service secret; the Mini Apps and the admin panel
use the public API through Caddy.

```mermaid
flowchart LR
  subgraph Users
    P["Passenger<br/>Telegram app"]
    D["Driver<br/>Telegram app"]
    A["Admin<br/>web browser"]
  end

  subgraph Telegram["Telegram platform"]
    TG["Bot API"]
    PBOT["Passenger bot chat"]
    DBOT["Driver bot chat"]
  end

  subgraph Server["Server in Kazakhstan"]
    WEB["web: Caddy<br/>TLS, static apps, /api proxy"]
    PMA["Passenger Mini App<br/>/passenger/"]
    DMA["Driver Mini App<br/>/driver/"]
    ADM["Admin panel<br/>ADMIN_DOMAIN"]
    API["api: Go<br/>REST + WebSocket :8080<br/>internal bot API :8081<br/>jobs, notification worker"]
    PB["passenger-bot<br/>long polling"]
    DB2["driver-bot<br/>long polling"]
    PG[("PostgreSQL 17 + PostGIS")]
    FS[("private files volume")]
    OSRM["OSRM router<br/>Kazakhstan roads"]
    BK["db-backup<br/>nightly"]
  end

  GEO["Geocoder<br/>Photon or 2GIS"]
  TILES["Map tiles<br/>OpenFreeMap"]
  TWOGIS["2GIS app<br/>navigation"]
  KASPI["Kaspi Pay"]

  P --> PBOT
  P -->|opens| PMA
  D --> DBOT
  D -->|opens| DMA
  A --> ADM
  PMA --- WEB
  DMA --- WEB
  ADM --- WEB
  WEB -->|"/api/v1 + WebSocket"| API
  PBOT -.-> TG
  DBOT -.-> TG
  PB -->|getUpdates| TG
  DB2 -->|getUpdates| TG
  PB -->|"service secret"| API
  DB2 -->|"service secret"| API
  API -->|sendMessage| TG
  API --> PG
  API --> FS
  API --> OSRM
  API -.->|search, reverse| GEO
  BK --> PG
  BK --> FS
  PMA -.->|tiles| TILES
  DMA -.->|tiles| TILES
  DMA -.->|deep link| TWOGIS
  DMA -.->|payment link| KASPI
```

## 2. Deployment topology

`compose.yaml`. Only the `web` service publishes ports. `backend` is an internal
Docker network without internet access; `edge` connects Caddy with the API and
gives the API outbound access (Telegram, geocoder); `egress` gives the bots and
the one-shot data downloads outbound access.

```mermaid
flowchart TB
  NET(["Internet<br/>ports 80, 443 tcp/udp"])

  subgraph netEdge["network: edge"]
    WEB["web<br/>caddy:2-alpine + SPA builds"]
  end

  subgraph netBackend["network: backend, internal"]
    API["api<br/>serve --migrate=false"]
    MIG["migrate<br/>one-shot: migrate up"]
    DB[("db<br/>postgis 17-3.5")]
    PB["passenger-bot"]
    DRB["driver-bot"]
    BK["db-backup<br/>daily pg_dump + files tar"]
    OSRM["osrm<br/>profile osrm"]
    PH["photon<br/>profile geocoder"]
  end

  subgraph netEgress["network: egress"]
    OUT(["Telegram Bot API,<br/>Geofabrik, GraphHopper"])
  end

  NET --> WEB
  WEB -->|"/api/* :8080"| API
  API -->|"outbound via edge"| NET
  MIG --> DB
  API --> DB
  API --> OSRM
  API -.-> PH
  PB -->|":8081"| API
  DRB -->|":8081"| API
  PB --> OUT
  DRB --> OUT
  BK --> DB

  V1[("volume pgdata")] --- DB
  V2[("volume files")] --- API
  V2 --- BK
  V3[("volume osrm-data")] --- OSRM
  V4[("caddy_data, caddy_config")] --- WEB
  V5[("./backups on the host")] --- BK
  V5 -.->|"off-site copy, in Kazakhstan"| OFF[("second server")]
```

Start order enforced by Compose: `db` healthy, then `migrate` completes
successfully, then `api` healthy, then both bots. `web` starts once `api` has
started.

## 3. Passenger booking flow

ARCHITECTURE §4.1, §5, §14, §19. The server computes the route and the allowed
fares; the client only chooses inside that range.

```mermaid
sequenceDiagram
  autonumber
  actor P as Passenger
  participant MA as Passenger Mini App
  participant API as API
  participant OSRM as OSRM
  participant DB as PostgreSQL
  participant W as Notification worker
  participant TG as Telegram

  P->>MA: open from the bot menu or a t.me link
  MA->>API: POST /auth/telegram/passenger with initData
  API->>API: check HMAC with the passenger bot token and auth_date
  API-->>MA: app token, 12 h
  MA->>API: GET /public/config?app=passenger
  P->>MA: choose Taxi, Parcel or Other and set 2 to 5 points
  MA->>API: GET /geo/search and /geo/reverse
  MA->>API: POST /routes/preview with category and stops
  API->>DB: every stop inside Kazakhstan?
  API->>OSRM: route through all stops
  API->>DB: store fare quote with tariff and commission rate
  API-->>MA: quote_id, distance, duration, geometry, fare range
  P->>MA: pick a fare and confirm
  MA->>API: POST /orders with quote_id, fare and idempotency_key
  API->>DB: one transaction: order, stops, route snapshot, offers to eligible drivers, outbox rows, pg_notify
  API-->>MA: order, status searching
  W->>DB: claim outbox rows
  W->>TG: sendMessage to each eligible driver with an Open button
  Note over API,DB: A driver accepts, see diagram 4
  API-->>MA: realtime order.accepted, the app re-fetches the order
  MA-->>P: driver name with last-name initial, car, plate, phone
  API-->>MA: order.driver_arriving, the driver is at the pickup
  API-->>MA: order.started, order.stop_completed
  API-->>MA: order.completed
  P->>MA: pay the driver in cash or by transfer, then rate the trip
  MA->>API: POST /orders/{id}/rating
```

If no driver accepts within the city's `search_timeout_s` (default 900 s), the
`search_expiry` job cancels the order with reason `no_driver_found` and the
passenger is notified.

## 4. Dispatch and atomic acceptance

ARCHITECTURE §5.1, §7. Many drivers may press Accept at the same moment; the
database guarantees exactly one winner and exactly one commission reservation.

```mermaid
sequenceDiagram
  autonumber
  actor D1 as Driver 1
  actor D2 as Driver 2
  participant API as API
  participant DB as PostgreSQL

  par both press Accept
    D1->>API: POST /driver/orders/{id}/accept
  and
    D2->>API: POST /driver/orders/{id}/accept
  end
  API->>DB: BEGIN for driver 1
  API->>DB: driver approved, online, same city, not blocked
  API->>DB: lock wallet row FOR UPDATE, available at least the commission
  API->>DB: UPDATE orders SET status accepted WHERE status searching AND driver_id IS NULL
  DB-->>API: 1 row for driver 1
  API->>DB: reservation and ledger commission_reserve, offers accepted or lost, status event, outbox, pg_notify
  API->>DB: COMMIT
  API-->>D1: 200 order accepted
  API->>DB: same UPDATE for driver 2
  DB-->>API: 0 rows
  API-->>D2: 409 order_already_taken
```

Other refusals: `402 insufficient_balance` when the wallet cannot cover the
commission (with a top-up hint), `409 driver_has_active_order` from the partial
unique index `orders_one_active_per_driver`.

Eligibility (computed live, so drivers who come online later still see open
orders): profile `approved`, online, user not blocked, same city as the order,
no active order, `available >= commission`, and, if the city requires it, a
fresh location within `dispatch_radius_m` of the pickup.

## 5. Order state machine

ARCHITECTURE §5. Every transition is a conditional `UPDATE ... WHERE status =
expected` plus a status event, notifications and a realtime event in one
transaction. Anything not drawn here is `409 invalid_transition`.

```mermaid
stateDiagram-v2
  [*] --> searching: passenger creates the order
  searching --> accepted: driver accepts or admin assigns
  searching --> cancelled: passenger, admin, or search timeout
  accepted --> driver_arriving: driver arrived at the pickup
  accepted --> in_progress: driver starts
  driver_arriving --> in_progress: driver starts
  accepted --> cancelled: passenger, driver with reason, admin
  driver_arriving --> cancelled: passenger, driver with reason, admin
  in_progress --> in_progress: driver completes the next stop
  in_progress --> completed: driver completes, all stops done
  in_progress --> cancelled: admin only
  in_progress --> disputed: passenger or admin
  completed --> disputed: passenger within 72 h, or admin
  disputed --> completed: admin resolves
  disputed --> cancelled: admin resolves
  completed --> [*]
  cancelled --> [*]
```

`driver_arriving` means the driver has arrived at the pickup and is waiting.
Money attached to the transitions:

| Transition | Wallet effect |
|---|---|
| `searching → accepted` | reserve the commission |
| `accepted / driver_arriving / in_progress → cancelled` | release the reservation |
| `in_progress → completed` | charge the commission once |
| `disputed → cancelled` after completion, with refund | refund the commission |

## 6. Driver registration and moderation

ARCHITECTURE §11, §19. The phone number is accepted only when Telegram itself
delivers the driver's own contact to the driver bot.

```mermaid
sequenceDiagram
  autonumber
  actor D as Driver
  participant BOT as Driver bot
  participant MA as Driver Mini App
  participant API as API
  participant FS as Private files
  actor M as Moderator

  D->>BOT: /start
  BOT->>API: POST /internal/v1/bots/driver/users/sync
  BOT-->>D: welcome with an Open button
  D->>MA: open
  MA->>API: POST /auth/telegram/driver with initData
  MA->>API: PUT /driver/registration with name and vehicle
  API-->>MA: profile created as draft, driver code assigned
  loop avatar, ID front and back, vehicle photos
    MA->>API: POST /driver/registration/files
    API->>FS: check type, re-encode JPEG, strip EXIF and GPS
  end
  MA->>D: Telegram asks to share the phone number
  D->>BOT: contact message from Telegram
  BOT->>BOT: contact.user_id equals the sender
  BOT->>API: POST /internal/v1/driver/{tg_id}/contact
  API-->>MA: realtime driver.phone_verified
  D->>MA: accept the personal-data consent and submit
  MA->>API: POST /driver/register
  API-->>M: realtime moderation.submitted in the admin panel
  M->>API: GET /admin/files/{id}, every ID view is audit-logged
  alt approve
    M->>API: POST /admin/drivers/{id}/approve
  else ask for corrections
    M->>API: POST /admin/drivers/{id}/request-corrections with items
  else reject
    M->>API: POST /admin/drivers/{id}/reject with a reason
  end
  API->>BOT: outbox message via the Bot API
  BOT-->>D: result with an Open button
```

Driver profile statuses:

```mermaid
stateDiagram-v2
  [*] --> draft: first PUT /driver/registration
  draft --> pending: submit
  pending --> approved: moderator approves
  pending --> changes_requested: moderator asks for corrections
  pending --> rejected: moderator rejects
  changes_requested --> pending: driver resubmits
  approved --> suspended: admin suspends, driver goes offline
  suspended --> approved: admin restores
```

Only `approved` drivers can go online (`PATCH /driver/availability`), and only
while their available balance is at least `driver.min_online_balance`.

## 7. Driver navigation

The driver Mini App opens 2GIS with https links through Telegram's `openLink`
(custom schemes are blocked inside Telegram):
`https://2gis.ru/directions/tab/car/points/<from>|<via...>|<to>` with points as
`lon,lat`; an empty `<from>` starts at the phone's current location. The full
route always stays visible on the map inside Jan Taxi.

```mermaid
flowchart TD
  A["Order accepted"] --> B["Navigate to the pickup"]
  B --> C{"Telegram platform"}
  C -->|"iOS"| N["Next-stop link<br/>from the current location<br/>to the next point"]
  C -->|"Android, desktop, web"| F{"GPS fix and at most<br/>8 intermediate points?"}
  F -->|yes| FR["Full-route link<br/>GPS position, every remaining stop,<br/>destination"]
  F -->|no| N
  N --> O["2GIS app, or the 2GIS web page"]
  FR --> O
  O --> R["Back in Jan Taxi"]
  R --> S{"Where is the driver?"}
  S -->|"at the pickup"| T["Mark arrived<br/>driver_arriving"]
  T --> U["Start trip<br/>in_progress"]
  U --> V{"Intermediate stop left?"}
  V -->|yes| W["Navigate to the next stop"] --> X["Complete the stop<br/>stops complete in order"] --> V
  V -->|no| Y["Navigate to the destination"] --> Z["Complete the order<br/>commission charged once"]
```

The 2GIS base URL comes from the setting `navigation.twogis_base_url` (only
`2gis.ru`, `2gis.kz` and `2gis.com` over https are accepted).

## 8. Wallet and commission flow

ARCHITECTURE §6. Money is integer tenge. Commission =
`(fare × commission_bps + 5000) / 10000`, rounded half up, `commission_bps`
default 1000 (10%), snapshotted on the order. Every change writes the wallet row
and an append-only ledger row with a unique idempotency key in the same
transaction.

```mermaid
flowchart LR
  subgraph Wallet["Driver wallet"]
    BAL["balance"]
    RES["reserved"]
    AV["available = balance - reserved"]
  end

  TU["Top-up confirmed by finance<br/>key topup:topup_id"] -->|"balance +amount"| BAL
  ACC["Driver accepts an order<br/>key reserve:order_id:driver_id"] -->|"reserved +commission"| RES
  CAN["Order cancelled before completion<br/>key release:reservation_id"] -->|"reserved -commission"| RES
  CMP["Order completed<br/>key charge:order_id"] -->|"balance -commission<br/>reserved -commission"| BAL
  REF["Dispute resolved as cancelled<br/>key commission_refund:order_id"] -->|"balance +commission"| BAL
  ADJ["Admin adjustment<br/>key adjust:uuid"] -->|"balance +/- amount"| BAL
  TR["Top-up refunded<br/>key topup_refund:topup_id"] -->|"balance -amount"| BAL
  BAL --- AV
  RES --- AV
```

Worked example with a fare of 1,000 ₸ and 10% commission:

| Step | balance | reserved | available |
|---|---|---|---|
| top-up of 2,000 ₸ confirmed | 2,000 | 0 | 2,000 |
| order accepted, 100 ₸ reserved | 2,000 | 100 | 1,900 |
| order completed, 100 ₸ charged | 1,900 | 0 | 1,900 |

The passenger pays the full 1,000 ₸ to the driver directly (cash or bank
transfer); the platform never receives passenger money. A repeated request with
the same idempotency key returns the first result and never charges twice. The
database keeps `balance >= 0`, `reserved >= 0` and `reserved <= balance`; admins
see a reconciliation that compares every wallet with the sum of its ledger rows.

## 9. Kaspi Pay top-up

ARCHITECTURE §6.1. There is no automatic payment confirmation: a finance admin
credits the wallet only after finding the payment in the merchant's Kaspi Pay
records.

```mermaid
sequenceDiagram
  autonumber
  actor D as Driver
  participant MA as Driver Mini App
  participant API as API
  participant K as Kaspi Pay
  actor F as Finance admin

  D->>MA: Wallet, Top up, amount
  MA->>API: POST /driver/wallet/topups with amount and idempotency_key
  API-->>MA: reference EMB001-TOPUP-000123, payment link, recipient, instructions
  MA->>K: openLink to the Kaspi Pay payment link
  D->>K: pay the amount, write the reference in the message
  D->>MA: I paid, optional receipt photo
  MA->>API: POST /driver/wallet/topups/{id}/submit
  API-->>F: realtime topup.updated, status pending_verification
  F->>K: find the payment in Kaspi Pay history
  alt payment found
    F->>API: POST /admin/topups/{id}/confirm with verified amount and Kaspi transaction number
    API->>API: one transaction: payment_transactions unique per provider and transaction, ledger topup credit
    API-->>MA: realtime topup.confirmed and wallet.updated
  else not found or wrong
    F->>API: POST /admin/topups/{id}/reject with a reason
  end
```

```mermaid
stateDiagram-v2
  [*] --> created
  created --> pending_verification: driver submits I paid
  created --> expired: driver cancels, or topup_expiry job
  pending_verification --> confirmed: finance confirms, wallet credited
  pending_verification --> rejected: finance rejects
  confirmed --> refunded: money returned, wallet debited
```

A Kaspi transaction number can be used only once (`UNIQUE(provider,
provider_transaction_id)`), so one real payment can never credit two top-ups.

## 10. Notification outbox

ARCHITECTURE §8. Telegram messages are rows written in the same transaction as
the business change, so a rolled-back change never sends a message and a
committed one is never lost.

```mermaid
sequenceDiagram
  autonumber
  participant S as Business code
  participant DB as PostgreSQL
  participant W as Worker in the API
  participant TG as Telegram Bot API

  S->>DB: BEGIN, change, INSERT notification_outbox ON CONFLICT dedupe_key DO NOTHING, COMMIT
  loop every second, one active worker holds an advisory lock
    W->>DB: claim due rows FOR UPDATE SKIP LOCKED with a lease
    W->>W: render the template in the user's language, kk, ru or en
    W->>TG: sendMessage with an inline web_app Open button
    alt 200 OK
      W->>DB: status sent, telegram_message_id
    else 429 Too Many Requests
      W->>DB: next attempt after retry_after
    else 5xx or network error
      W->>DB: exponential backoff with jitter
    else 403 bot blocked or never started
      W->>DB: status dead, mark users bot_blocked_at
    end
  end
```

```mermaid
stateDiagram-v2
  [*] --> pending
  pending --> sending: claimed with a lease
  sending --> sent: delivered
  sending --> pending: retry later, or lease expired
  sending --> dead: 403, permanent error, or 8 attempts
  dead --> pending: admin retries
  pending --> cancelled: no longer relevant
```

Sending is limited to 25 messages per second per bot and one message per second
per chat. Admins see and retry rows under Notifications in the admin panel.

## 11. Realtime updates

ARCHITECTURE §9. Events are published with `pg_notify` inside the business
transaction, so clients hear about a change only after it is committed. Events
are hints; clients re-fetch the authoritative state over REST.

```mermaid
sequenceDiagram
  autonumber
  participant C as Mini App or admin panel
  participant API1 as API replica
  participant DB as PostgreSQL
  participant S as Business code

  C->>API1: GET /api/v1/ws
  C->>API1: first frame type auth with the app token, admins use the cookie
  API1-->>C: type ready
  API1->>DB: LISTEN jt_events on a dedicated connection
  S->>DB: BEGIN, change, SELECT pg_notify, COMMIT
  DB-->>API1: notification after commit
  API1-->>C: event for the scopes user, driver or admin
  C->>API1: GET the changed resource over REST
  Note over C,API1: Server pings every 25 s, the client reconnects with backoff 1 s to 30 s and re-syncs on reconnect and when the app returns to the foreground
```
