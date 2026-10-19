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
| PostgreSQL | `pg_isready` | accepting connections |
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
