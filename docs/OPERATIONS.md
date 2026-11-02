# Operations

Day-to-day running of a Jan Taxi deployment. All commands run in the deployment
directory (for example `/opt/jantaxi`) as the deploy user. `make help` lists every
command.

## Health

```sh
make health      # API /readyz, both bots' health endpoints, container states
make ps          # containers with their Docker health status
```

| Check | Where | Healthy when |
|---|---|---|
| API liveness | `GET /healthz` on :8080 (Docker healthcheck `api healthcheck`) | the process serves HTTP |
| API readiness | `GET /readyz` on :8080 | database reachable, schema at the embedded migration version, realtime listener connected, not shutting down; returns 503 otherwise |
| Bots | `GET :8090/healthz` in each bot container (`<bot> healthcheck`) | the instance holding the Telegram lease is not stalled and polled successfully within 90 s (waiting out Telegram's `retry_after` does not count); a standby is healthy while its lease checks succeed. A bot unhealthy for 5 minutes exits and Docker restarts it |
| Caddy | admin endpoint `127.0.0.1:2019` inside the container | Caddy is running |
| PostgreSQL | `pg_isready` over TCP | accepting connections (the first-start setup listens on the Unix socket only and does not count) |
| OSRM | TCP port 5000 inside the container | router loaded |
| Photon (optional) | `GET :2322/status` inside the container | index loaded |

`/healthz` and `/readyz` are not exposed through Caddy. From the host:

```sh
docker compose exec web wget -q -O - http://api:8080/readyz
```

Docker restarts crashed containers (`restart: unless-stopped`). An `unhealthy`
status alone does not restart a container; look at its logs. The bots exit on
their own after 5 minutes unhealthy, so Docker restarts them.

## Logs

```sh
make logs                    # all services, follow
make logs SERVICE=api        # one service
docker compose logs --since 1h api | grep '"level":"ERROR"'
```

- Every service logs to stdout/stderr; Docker's json-file driver rotates the logs
  (`LOG_MAX_SIZE` × `LOG_MAX_FILE` per container, default 20 MB × 5).
- The API and bots log JSON (`LOG_FORMAT=json`). Each API request gets a
  `request_id`, returned in the `X-Request-ID` response header and attached to
  every log line of that request.
- Secrets are never logged: configuration values print redacted, bot tokens are
  scrubbed from errors and URLs, and Authorization headers and cookies are not
  logged. Caddy's access log removes `q`, `lat` and `lon` from query strings.
- Logs contain client IP addresses and user ids. Keep the rotation limits; do not
  ship logs to services outside Kazakhstan.

## Backups

The `db-backup` service runs `deploy/backup/backup.sh` every day at `BACKUP_TIME`
(default 03:30, `BACKUP_TZ=Asia/Aqtobe`), and immediately on start when no dump
from the last 24 hours exists. A failed run is retried after
`BACKUP_RETRY_MINUTES` (default 60).

Each run writes, under `BACKUP_HOST_DIR` (default `./backups`):

```
backups/db/<db>-<UTC timestamp>.dump            pg_dump custom format (compressed)
backups/db/<db>-<UTC timestamp>.dump.sha256
backups/files/files-<UTC timestamp>.tar.gz      private files volume (driver documents, photos, receipts)
backups/files/files-<UTC timestamp>.tar.gz.sha256
```

- Files are created with mode 600 in 700 directories; they contain personal data.
  They belong to root unless `BACKUP_OWNER=<uid>:<gid>` is set (use the ids of the
  user that copies them off-site, from `id -u` and `id -g`).
- Each dump is checked with `pg_restore --list` before it gets its final name; a
  failed run leaves only `.partial` files, which are removed a day later.
- Backups older than `BACKUP_RETENTION_DAYS` × 24 hours (default 14 days) are
  deleted only after a successful run, so the newest good backup is never pruned.
- Run a backup by hand (before every upgrade): `make backup`.
- Check the schedule and results: `make logs SERVICE=db-backup`.

### Off-site copies

A backup on the same disk does not survive the loss of the server. Copy
`./backups` daily to a second machine **in Kazakhstan** (localisation applies to
backups too), for example with a cron job on the backup host pulling over SSH:

```sh
# crontab on the backup host: copy new backups at 05:00 (never deletes on the
# target), keep 90 days there
0 5 * * * rsync -a --exclude 'restore/' --exclude '*.partial' deploy@app-server:/opt/jantaxi/backups/ /srv/jantaxi-backups/
30 5 * * * find /srv/jantaxi-backups/db /srv/jantaxi-backups/files -type f -mtime +90 -delete
```

The deploy user can read the files only when `BACKUP_OWNER` is set to its uid:gid
(otherwise run the copy as root). Restrict the SSH key to rsync, keep the target
encrypted at rest, and choose its retention together with the data-protection
rules ([DATA_PROTECTION.md](DATA_PROTECTION.md#4-retention-and-deletion)). Watch
the age of the newest dump (alert if older than 26 hours):

```sh
find /opt/jantaxi/backups/db -name '*.dump' -mmin -1560 | grep -q . || echo "no fresh database backup"
```

## Restore and restore drill

`make restore` never overwrites live data:

```sh
make restore FILE=backups/db/jantaxi-20261016T223000Z.dump
make restore FILE=backups/files/files-20261016T223000Z.tar.gz
```

- A database dump is restored into a **new** database
  (`<db>_restore_<timestamp>`, or `RESTORE_DB=<name>`); the script asks you to
  type that name, verifies the checksum, restores, and prints a summary (schema
  version, row counts, wallets whose balances differ from the ledger, newest
  order) followed by the exact commands to swap databases.
- A files archive is unpacked into `backups/restore/<name>/`, with the command to
  copy it into the live volume. Stored files have unique random names, so the copy
  only adds missing files.

**Monthly drill** (about 15 minutes): restore the newest dump into a new
database, check that the summary shows the expected order count and
`wallet_ledger_mismatches = 0`, unpack the matching files archive, then drop the
copy and delete the staging directory. Record the date and the result.

```sh
docker compose exec db psql -U jantaxi -d postgres -c "DROP DATABASE jantaxi_restore_<timestamp>;"
rm -rf backups/restore/files-<timestamp>
```

## Full data reset

Starts again with an empty database.

- **Deleted:** every user, driver, order, wallet, receipt and uploaded photo.
  Settings changed in the admin panel (tariffs, villages, texts, Kaspi details)
  go back to their defaults.
- **Kept:** `.env`, the TLS certificates (`caddy_data`), the routing and
  geocoding data (`osrm-data`, `photon-data`) and `./backups`.

```sh
make backup                                       # last copy of the old data
make down                                         # also removes containers of retired services
docker volume rm jantaxi_pgdata jantaxi_files     # database and uploaded files
make up && make health                            # migrations from an empty database
make admin-create EMAIL=... ROLE=super_admin NAME="..."
```

- The volume names start with `COMPOSE_PROJECT_NAME` (`jantaxi` by default);
  `docker volume ls` lists them.
- If `docker volume rm` answers "volume is in use", find the container that
  still mounts the volume with `docker ps -a --filter volume=jantaxi_files`,
  remove it with `docker rm -f <name>`, and repeat the step.
- Never add `-v` to `down` and never run `docker volume prune`: both would
  also delete the certificates and the routing data.
- The backup taken first is pruned after `BACKUP_RETENTION_DAYS`, like any
  other backup. Copy it elsewhere to keep it longer.
- After the reset:
  - Drivers register again and receive the welcome bonus again.
  - Open Mini Apps sign in again by themselves.
  - Kaspi details fall back to `KASPI_PAY_*` in `.env`.
  - If `ADMIN_BOOTSTRAP_EMAIL` is still set, the API creates that administrator
    at start; skip `make admin-create`.

## Notifications (Telegram outbox)

Every bot message is a row in `notification_outbox`, written in the same
transaction as the business change and sent by the worker in the API.

| Status | Meaning |
|---|---|
| `pending` | waiting for its first or next attempt (`next_attempt_at`) |
| `sending` | claimed by a worker (lease `locked_until`; expired leases are reclaimed) |
| `sent` | delivered (`telegram_message_id`) |
| `dead` | gave up: the user blocked the bot or never started it (403), a permanent Telegram error, or 8 failed attempts |
| `cancelled` | no longer relevant |

- Admin panel → Notifications (permission `orders.manage`) lists rows by status
  and retries a `dead` row once the cause is fixed.
- A 403 also sets `users.passenger_bot_blocked_at` / `driver_bot_blocked_at`; the
  user must open the bot and press Start again before messages can reach them.
- On 429 the worker waits `retry_after`; on 5xx and network errors it backs off
  exponentially. Sending is limited to about 25 messages/s per bot and 1/s per
  chat.

Quick checks:

```sh
docker compose exec db psql -U jantaxi -d jantaxi -c \
  "SELECT status, count(*) FROM notification_outbox WHERE created_at > now() - interval '1 day' GROUP BY 1;"
docker compose exec db psql -U jantaxi -d jantaxi -c \
  "SELECT template, last_error, count(*) FROM notification_outbox WHERE status = 'dead' AND created_at > now() - interval '7 days' GROUP BY 1, 2 ORDER BY 3 DESC LIMIT 20;"
```

A growing number of `pending` rows with old `next_attempt_at` means the worker is
not running: check `NOTIFICATION_WORKER_ENABLED=true` and the API logs.

## Money: top-ups and reconciliation

- Drivers create a top-up in the driver Mini App and pay through the Kaspi Pay
  link shown there, writing the top-up reference (`EMB001-TOPUP-000123`) in the
  payment message. "I paid" only moves the request to `pending_verification`.
- A finance admin opens Kaspi Pay → History → Paid, finds the payment (amount,
  payer, message with the reference, time), and confirms the top-up in the admin
  panel with the **verified amount** and the **Kaspi transaction number**. A
  transaction number can be used only once. Never confirm from a screenshot alone.
- Wrong or missing payments are rejected with a reason; confirmed top-ups paid
  back to the driver are marked refunded (the wallet is debited).
- Daily: admin panel → Wallets reconciliation must show no mismatches.
  Monthly: export Kaspi Pay sales (XLSX) and the admin top-up CSV for the same
  period and compare totals.

## Marketing

Promotions, coupons, the passenger bonus balance, referrals and broadcasts are
described in [ARCHITECTURE.md §23](ARCHITECTURE.md#23-marketing-and-promotions);
the owner's guide is [MARKETING.md](MARKETING.md) (Russian). Unless a command is
shown, everything below is done in the admin panel → Marketing, and every action
there is audit-logged.

Background work in the API: `marketing_events` (award worker, every 2 s),
`marketing_coupon_expiry` (1 min), `marketing_campaign_schedule` (30 s),
`marketing_grants` (5 s), `marketing_reconcile` (hourly) and the
`broadcast_worker`, which runs next to the notification worker
(`NOTIFICATION_WORKER_ENABLED=true`) on one replica at a time. Their log lines
carry `component=marketing` or `component=broadcast_worker`.

### Switching campaigns on and off

- After the upgrade to migration 00016 nothing runs: `first_ride_coupon_400`
  («Алғашқы сапар») and `referral_300_once` («Досыңды шақыр») are drafts with a
  100 000 ₸ pilot budget each.
- Activate, resume, schedule, end and freeze redemptions need `marketing.approve`
  (super admin, finance manager), a reason and a confirmation. Pause, unschedule
  and archive need `marketing.edit` or `marketing.approve`, except on the
  seeded campaigns (the two launch promotions and `manual_adjustments`): every
  switch of those, and any change of their rules, is an approver's (`403` for
  a marketing manager, whose switch in the campaign list stays locked).
- Approver actions carry the version the approver reviewed (`expected_version`,
  `422` without it). `409 version_conflict`: someone saved a new version of the
  campaign after it was opened. Reload, read the new version and repeat.
- Scheduling a passenger bonus campaign (`passenger_credit`) freezes its
  audience and checks the budget at that moment; people who join the segment
  later are not added. Unschedule to drop the frozen audience.
- `manual_adjustments` (the bucket of manual bonus credits) stays a draft and
  cannot be scheduled or archived (`409 campaign_conflict`); only an approver
  changes its per-credit cap (a new version). To stop manual credits, set its
  budget to 0.
- Read the campaign preview before activating (audience, maximum cost, warnings).
  Activate «Алғашқы сапар» before or together with «Досыңды шақыр»: by default a
  passenger may invite only after redeeming the first-ride coupon.
- Pause stops new awards; coupons already issued stay usable until they expire,
  and rides completed during the pause never earn later. Freeze redemptions stops
  the campaign's coupons on new orders; orders that already reserved one complete
  normally.
- `campaign_conflict` on activate: another campaign of the same kind is
  scheduled, active or paused. End it first.
- Which campaigns are on:

```sh
docker compose exec db psql -U jantaxi -d jantaxi -c \
  "SELECT key, status, budget_limit, active_since, redemptions_frozen FROM marketing_campaigns ORDER BY priority;"
```

### Emergency stops

| Setting | When switched off |
|---|---|
| `marketing.awards_enabled` | no new coupons, referral rewards or bonus credits; rides completed meanwhile never earn |
| `marketing.redemptions_enabled` | new orders cannot use a coupon or the bonus; orders that already reserved one settle or release normally |
| `marketing.broadcasts_enabled` | every broadcast stops sending, test sends included (queued tests wait until it is on again) |

- Change them on Marketing → Safety (`marketing.approve`, reason required) or,
  as a super admin, in Settings. One API container applies the change at once;
  other replicas within 5 seconds (settings cache).
- If the admin panel is unreachable, switch all three off in the database (this
  bypasses the audit log: record it in the incident notes):

```sh
docker compose exec db psql -U jantaxi -d jantaxi -c \
  "UPDATE app_settings SET value = 'false' WHERE key IN ('marketing.awards_enabled', 'marketing.redemptions_enabled', 'marketing.broadcasts_enabled');"
```

### Failed marketing events

- Overview → health: `events_pending`, `events_failed`, `oldest_pending_s`;
  reconciliation: `failed_events`; log line `marketing event failed`.
- Pending events normally clear within seconds. A growing `oldest_pending_s`
  means the job is not running or keeps failing: `make logs SERVICE=api | grep marketing`.
- An event is retried with backoff and marked `failed` after 10 attempts:

```sh
docker compose exec db psql -U jantaxi -d jantaxi -c \
  "SELECT id, order_id, attempts, last_error, created_at FROM marketing_events WHERE status = 'failed' ORDER BY id DESC LIMIT 20;"
```

- Once the cause is fixed, queue it again. Processing is idempotent (one coupon
  per passenger and campaign, one reward per referrer, unique ledger keys), so a
  retry never pays twice:

```sh
docker compose exec db psql -U jantaxi -d jantaxi -c \
  "UPDATE marketing_events SET status = 'pending', attempts = 0, next_attempt_at = now() WHERE id = <id> AND status = 'failed';"
```

- Rides older than 7 days are closed as `too_old` and never pay. Make good a
  missed award with a manual bonus credit (passenger) or a wallet bonus (driver),
  with a reason.
- What an event did: `SELECT order_id, result FROM marketing_events WHERE status = 'done' ORDER BY id DESC LIMIT 20;`
  (for example `{"first_ride_coupon_400": "coupon_issued"}`).

### Stuck reservations

- Overview `stuck_reservations` / reconciliation `stuck_reservation`: a
  promotion still `reserved` on an order that is completed or cancelled.
  Reservation, settlement and release run inside the order's own transactions,
  so this points to a bug or a manual database edit. Do not edit the rows by
  hand; collect the ids and escalate to engineering:

```sh
docker compose exec db psql -U jantaxi -d jantaxi -c \
  "SELECT op.id, o.number, o.status, op.kind, op.discount, op.reserved_at FROM order_promotions op JOIN orders o ON o.id = op.order_id WHERE op.status = 'reserved' AND o.status NOT IN ('searching', 'accepted', 'driver_arriving', 'in_progress', 'disputed');"
```

- Until it is fixed the passenger's coupon (or that part of the bonus) cannot be
  used; a manual bonus credit with a reason is the safe goodwill gesture.

### Referrals under review

- Marketing → Referrals, status `review`. Reasons: `short_ride` (the ride was
  shorter than the campaign's `min_ride_seconds`, 60 s) and `budget_exhausted`.
- Look at the ride in Orders (route, duration, who drove), then approve or
  reject with a reason (`marketing.approve`). Approve pays the inviter once; an
  inviter who was already rewarded ends `capped`. `409 budget_insufficient`:
  raise the campaign budget first, or reject.
- A dispute that cancels the friend's first ride after completion closes its
  referral with reason `qualifying_ride_cancelled`: one still in `review` becomes
  `rejected`; one already `rewarded` keeps its 300 ₸ and is only flagged (it is
  not counted in `review_referrals`). See «Reversing a promotion».
- Capture attempts are kept in `referral_events`. Many attempts on one code or
  many `invitee_not_new` / `invitee_has_orders` deserve a look:

```sh
docker compose exec db psql -U jantaxi -d jantaxi -c \
  "SELECT code, outcome, reason, count(*) FROM referral_events WHERE created_at > now() - interval '7 days' GROUP BY 1, 2, 3 ORDER BY 4 DESC LIMIT 20;"
```

- There is no admin button to disable a passenger's invitation code yet. In the
  database (not audited; affects only future captures):

```sh
docker compose exec db psql -U jantaxi -d jantaxi -c \
  "UPDATE referral_codes SET disabled_at = now() WHERE code = '<CODE>' AND disabled_at IS NULL;"
```

### Reversing a promotion

- Promotions flagged for finance (overview `review_promotions`; Marketing →
  Coupons & bonuses → Discounts on orders, "Under review only"); nothing moves
  automatically:
  - `order_cancelled_after_settlement`: a dispute resolved as cancelled on a
    completed order that used a coupon or the bonus; the promotion stays
    `settled`;
  - `coupon_source_cancelled`: the order completed with a first-ride coupon whose
    own qualifying ride a dispute had cancelled. The ride was taken at the
    discounted price, so the driver was compensated as usual.
- A dispute that cancels a first ride also takes back what that ride earned: its
  unused coupon is voided (`qualifying_ride_cancelled`), a coupon reserved on an
  open order is voided when that order is cancelled (and flagged as above if it
  completes), and the referral it qualified is rejected while under review or
  flagged once paid.
- Finance decides:
  - keep the compensation: nothing to do (the flag stays; there is no dismiss
    action);
  - reverse (`finance.manage`, reason and confirmation): the driver's
    compensation is debited from the available balance (`409
    insufficient_balance` when it is lower: wait for a top-up or agree a wallet
    adjustment), a spent bonus returns to the passenger, a redeemed coupon stays
    redeemed. The driver sees a `promo_compensation_reversal` row.
- A referral reward paid for a ride a dispute cancelled stays `rewarded` with
  reason `qualifying_ride_cancelled` (Marketing → Referrals). To take it back,
  debit the inviter's bonus with a manual adjustment (`finance.manage`, reason;
  only the available balance can be debited). Flagged rewards:

```sh
docker compose exec db psql -U jantaxi -d jantaxi -c \
  "SELECT id, referrer_user_id, qualifying_order_id, decided_at, updated_at FROM referral_attributions WHERE status = 'rewarded' AND reason = 'qualifying_ride_cancelled' ORDER BY updated_at DESC LIMIT 20;"
```

### Pausing or cancelling a broadcast

- Pause (`marketing.broadcast`) keeps the unsent deliveries; resume continues.
  Cancel drops the queued deliveries; messages Telegram already accepted cannot
  be recalled. All broadcasts at once: `marketing.broadcasts_enabled = false`
  (test sends stop too).
- An audience above `marketing.broadcast_approval_threshold` (100) is scheduled
  or sent by an admin who holds both `marketing.broadcast` and
  `marketing.approve` (by default only the super admin): `403
  approval_required` otherwise. A finance manager cannot send broadcasts; a
  marketing manager sends only smaller audiences.
- A scheduled broadcast that ends `failed` with the approval note
  `approval_required`: an admin without `marketing.approve` (a marketing
  manager) scheduled it and its audience grew above the threshold by the send
  time. Nothing was sent; create it again and have the super admin schedule or
  send it. A scheduled broadcast keeps the targeting it had when it was
  scheduled, whatever happens to its segment later.
- Test sends go only to the Telegram ids in `marketing.test_recipients` (empty
  after the upgrade). `422 not_test_recipient`: the id is not listed — an
  approver adds the staff ids on Marketing → Safety with a reason (the super
  admin also in Settings → Marketing); a person's Telegram id is shown as
  «TG …» in Marketing → Coupons & bonuses → person search.
  `bot_not_started` / `suppressed`: that person must start the audience's bot
  and not unsubscribe or block it. `409 broadcasts_disabled`: the switch is
  off. At most 10 tests per broadcast and 30 in total per hour.
- The worker yields while more than 20 order messages are due and pauses a bot
  for Telegram's `retry_after` on 429 (`broadcast flood control` in the log).
  `blocked` deliveries are people who blocked the bot; they are marked and skipped
  next time. Unsubscribed people are `suppressed`.
- Delivery counts:

```sh
docker compose exec db psql -U jantaxi -d jantaxi -c \
  "SELECT b.title, r.status, count(*) FROM marketing_broadcast_recipients r JOIN marketing_broadcasts b ON b.id = r.broadcast_id WHERE NOT r.is_test GROUP BY 1, 2 ORDER BY 1, 2;"
```

- A broadcast that stays `sending` with queued deliveries while the switch is on:
  check `NOTIFICATION_WORKER_ENABLED`, the order-message backlog (Notifications)
  and the API log (`broadcast batch failed`).

### Reconciliation issues

Marketing → Marketing reports → Promotions reconciliation must show no findings;
the hourly job logs `marketing reconciliation found issues` (ERROR) otherwise.

| Finding | Action |
|---|---|
| `failed_events` | see «Failed marketing events» |
| `stuck_reservation` | see «Stuck reservations» |
| `compensation_mismatch`, `orphan_compensation`, `reversal_without_status`, `totals_mismatch` | driver compensation and promotions disagree |
| `bonus_account_mismatch`, `bonus_reservation_mismatch` | a passenger bonus account disagrees with its ledger or its reserved orders |
| `coupon_promotion_mismatch`, `order_discount_mismatch` | a coupon or an order disagrees with its promotion |
| `grant_without_ledger`, `driver_grant_without_ledger`, `referral_cap_exceeded` | an award without its ledger row, or more than one referral reward |

The last four rows should never happen: every one of those writes is a single
transaction under unique keys. If one appears, switch awards and redemptions off,
run `make backup`, and escalate with the `ref` ids. Never fix balances with SQL:
corrections go through bonus adjustments or wallet adjustments, so the ledgers stay
complete.

### Rolling back a release

The previous release does not know promotions: an order with a discount would
complete without crediting the driver. Before switching images back
([DEPLOYMENT.md §7](DEPLOYMENT.md#7-rollback)), switch
`marketing.redemptions_enabled` off and wait until no active order carries a
reserved promotion:

```sh
docker compose exec db psql -U jantaxi -d jantaxi -c \
  "SELECT count(*) FROM order_promotions op JOIN orders o ON o.id = op.order_id WHERE op.status = 'reserved' AND o.status IN ('searching', 'accepted', 'driver_arriving', 'in_progress');"
```

Coupons and bonus balances stay in the database and work again after the next
upgrade.

## Rotating secrets

Edit `.env`, then recreate the affected containers (`docker compose up -d <services>`
re-reads the file; `restart` does not).

| Secret | Procedure | Effect |
|---|---|---|
| `SESSION_SECRET` | new value, `docker compose up -d api` | All Mini App tokens become invalid; the apps sign in again automatically with Telegram initData. Admin CSRF tokens change: admins reload the page or sign in again. |
| `INTERNAL_BOT_SERVICE_SECRET` | new value, `docker compose up -d api passenger-bot driver-bot` | Bots get 401 for a few seconds until all three are recreated. |
| Bot token | @BotFather → the bot → API Token → Revoke, put the new token in `.env`, `docker compose up -d api <bot>` | The old token stops working at once; plan a short window. |
| `POSTGRES_PASSWORD` | `docker compose exec db psql -U jantaxi -d postgres -c "ALTER ROLE jantaxi PASSWORD '<new>';"`, update `.env`, `docker compose up -d api db-backup` | The variable only initialises an empty data directory; the role password must be changed in the database. |
| Admin passwords | admin panel → change password; super admins manage other admins | — |
| `ADMIN_BOOTSTRAP_*` | remove both lines after the first admin exists | — |

Rotate secrets immediately after any suspected leak, and when someone with server
access leaves.

## Updating routing and geocoding data

- OSRM: `make osrm-prepare` (downloads the latest Geofabrik extract), then
  `docker compose --profile osrm up -d --force-recreate osrm`. Monthly is enough
  for a small town; roads change slowly.
- Photon (if self-hosted): `make geocoder-import`, then
  `docker compose --profile geocoder up -d --force-recreate photon`.

## Scaling notes

- One API container is the intended setup for the first cities. The database is
  the limit long before the API is.
- Several API replicas are possible because jobs take PostgreSQL advisory locks,
  realtime events fan out through `LISTEN/NOTIFY` to every replica, and the
  notification worker claims rows with `FOR UPDATE SKIP LOCKED`. Two changes are
  needed first: the `files` volume must be shared storage visible to every
  replica, and Caddy must list all upstreams.
- Bots: one active poller per bot token (database lease, renewed every 10 s).
  Extra bot containers wait as standby and take over within about 30 s.
- PostgreSQL scales vertically: raise `DB_MEM_LIMIT`, `PG_SHARED_BUFFERS` (about
  25% of its memory) and `PG_EFFECTIVE_CACHE_SIZE` together. Keep
  `DATABASE_MAX_CONNS` × API replicas below `PG_MAX_CONNECTIONS`.
- Slow queries (over `PG_LOG_MIN_DURATION_MS`, default 1 s) appear in
  `make logs SERVICE=db`.

## Incidents

- Personal-data breach or suspected unauthorised access: follow the procedure in
  [DATA_PROTECTION.md](DATA_PROTECTION.md#7-breach-response). The authorised body
  must be notified within one working day of discovery.
- Lost server: provision a new one ([DEPLOYMENT.md](DEPLOYMENT.md)), copy the
  newest off-site backup into `./backups`, `make up` with an empty database,
  stop `api` and the bots, restore the dump and the files archive with the swap
  steps, `make up`.
- Telegram outage or blocked API: orders still work in the Mini Apps already
  open; notifications queue in the outbox and are sent when Telegram is reachable
  (up to 8 attempts with backoff; retry `dead` rows from the admin panel).

## Local live stack (development machine)

The Docker stack behind a Cloudflare quick tunnel ([DEPLOYMENT.md §10](DEPLOYMENT.md#10-local-live-stack-docker--cloudflare-quick-tunnel))
is run with `deploy/local/stack.sh` and a state directory outside the repository
(`JT_STACK_DIR`). Day to day:

```sh
deploy/local/stack.sh status          # everything at a glance; exit 1 if something is off
deploy/local/stack.sh verify          # WebSocket and deep links through the tunnel, admin login
deploy/local/stack.sh rebuild         # deploy the current working tree (tunnel URL kept)
deploy/local/stack.sh logs api        # or passenger-bot, driver-bot, web, tunnel, db, watchdog
```

| Symptom | Check | Fix |
|---|---|---|
| Mini App does not open in Telegram | `status`: tunnel probes and menu buttons | `stack.sh up` (follows a new URL); after ~10 min of 530/1033 the watchdog runs `retunnel` itself |
| Mini App opens elsewhere but not on this Wi-Fi / Mac | `dig <host> @<router>` answers NXDOMAIN | DNS negative cache: flush it (macOS `sudo dscacheutil -flushcache; sudo killall -HUP mDNSResponder`) or `stack.sh retunnel` |
| Bot silent | `status`: bot health JSON (`role`, `last_poll_age_s`, `last_error`) | the watchdog restarts an unhealthy bot; `stack.sh logs driver-bot` |
| API errors after `rebuild` | `status` NOTE about edited migrations; `logs api` | `stack.sh rebuild` again after the migration is final |
| Nothing runs after a reboot | `stack.sh status` | `stack.sh up` (the watchdog is not a system service) |
