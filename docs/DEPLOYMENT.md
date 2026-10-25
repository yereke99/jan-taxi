# Deployment

Jan Taxi runs as one Docker Compose project on a single Linux server. This
document covers choosing and preparing the server, the first deployment,
upgrades without data loss, and rollback. Day-to-day operation (health, logs,
backups, secrets) is in [OPERATIONS.md](OPERATIONS.md); Telegram configuration is
in [TELEGRAM_SETUP.md](TELEGRAM_SETUP.md).

## 1. Hosting in Kazakhstan

The service stores personal data of citizens of Kazakhstan (names, phone numbers,
trip addresses, driver ID card photos). The Law of the Republic of Kazakhstan
"On personal data and their protection" No. 94-V, Art. 12(2), requires that such
data is stored in databases located on the territory of Kazakhstan. Therefore:

- The server, its disks and the volumes `pgdata`, `files` and `./backups` must
  be in a data centre in Kazakhstan. Get the data-centre location in writing
  from the hosting provider (contract or service description).
- Off-site backup copies must also stay in Kazakhstan.
- See [DATA_PROTECTION.md](DATA_PROTECTION.md) for the remaining obligations.

## 2. Server sizing

Budget for the first city (Embi) with OSRM on the same host:

| Component | Memory limit in compose | Notes |
|---|---|---|
| PostgreSQL + PostGIS | 2 GB (`DB_MEM_LIMIT`) | `shared_buffers=512MB` by default |
| API | 1 GB (`API_MEM_LIMIT`) | image decoding of uploads is capped at 2 at a time |
| OSRM router (`osrm-routed`, MLD) | 3 GB (`OSRM_MEM_LIMIT`) | estimate for the Kazakhstan extract: 1–2 GB resident |
| Caddy, two bots, backup job | about 1.3 GB total | |
| Operating system, page cache | 1–2 GB | |

| | Minimum | Recommended |
|---|---|---|
| CPU | 4 vCPU | 8 vCPU |
| RAM | 8 GB | 16 GB |
| Disk | 80 GB SSD | 160 GB SSD |
| Network | public IPv4, ports 80/443 | same, plus IPv6 |

Building the OSRM dataset (`make osrm-prepare`) needs an additional 3–5 GB of RAM
for about half an hour (limit `OSRM_PREPARE_MEM_LIMIT=6g`). On an 8 GB server run
it before launch and later only at night, or build the volume on another machine
and copy it. The OSRM memory figures are estimates for an extract of about 215 MB;
measure them on the first run (`docker stats`).

Disk: the Kazakhstan extract is about 215 MB and the OSRM dataset a few GB;
uploaded photos are re-encoded to at most 2000 px JPEG (typically a few hundred KB
each); backups keep `BACKUP_RETENTION_DAYS` (14) daily database dumps and file
archives.

## 3. DNS, TLS and firewall

1. The production template uses `jan-taxi.kz` (Mini Apps and API) and
   `admin.jan-taxi.kz` (admin panel).
2. In Hoster.kz DNS management, create these records for server `45.129.197.71`:

   | Type | Name | Value |
   |---|---|---|
   | A | `@` | `45.129.197.71` |
   | A | `admin` | `45.129.197.71` |

   Add AAAA only if the server also has working public IPv6. Wait until both
   names resolve before the first start. A `SERVFAIL` or `REFUSED` DNS response
   means the DNS zone/delegation needs fixing with the provider first.
3. Optionally add a CAA record allowing `letsencrypt.org` and `sectigo.com`
   (ZeroSSL), the certificate authorities Caddy uses.
4. Certificates are issued and renewed automatically by Caddy and stored in the
   `caddy_data` volume. Keep this volume: deleting it forces re-issuance and can
   hit the CA's rate limits.

Firewall (example with ufw):

```sh
ufw default deny incoming
ufw default allow outgoing
ufw allow from <admin-ip-or-vpn>/32 to any port 22 proto tcp
ufw allow 80/tcp
ufw allow 443/tcp
ufw allow 443/udp        # HTTP/3
ufw enable
```

Only the `web` service publishes ports. PostgreSQL, the API's internal listener
(8081), OSRM and Photon are on the internal `backend` network and are not
reachable from outside. Note that Docker manages its own iptables rules for
published ports; do not publish additional ports in `compose.yaml` or an
override file on a production server.

| Service | Port / protocol | Published on the server |
|---|---|---|
| Caddy HTTP / certificate validation | `80/tcp` | Yes |
| Caddy HTTPS | `443/tcp` | Yes |
| Caddy HTTP/3 | `443/udp` | Yes |
| API / WebSocket | `8080/tcp` | No, Caddy proxies `/api/*` |
| Internal bot API | `8081/tcp` | No |
| PostgreSQL | `5432/tcp` | No |
| OSRM | `5000/tcp` | No |
| Photon | `2322/tcp` | No |
| Bot health (each bot container) | `8090/tcp` | No |
| Caddy administration | `2019/tcp` | No, container loopback only |

Caddy runs inside the `web` container; it serves the built frontends and
proxies the API directly. Ports 80/443 must be available for this container.
The development ports 5173/5174/5175 and local edge ports 8088/8089 are not
used by the production stack.

## 4. Preparing the server

- A current Linux distribution with automatic security updates, NTP time
  synchronisation and SSH key authentication only (password login disabled).
- Docker Engine with the Compose plugin (`docker compose version` must work;
  Compose 2.24.4 or newer).
- A dedicated deploy user in the `docker` group. Treat membership in that group
  as root access.
- `git`, `make` and `openssl`.

```sh
sudo mkdir -p /opt/jantaxi && sudo chown deploy: /opt/jantaxi
git clone <repository-url> /opt/jantaxi
cd /opt/jantaxi
```

## 5. First deployment

```sh
cd /opt/jantaxi
cp .env.example .env
chmod 600 .env
```

Use a fresh copy of this template on the server. The laptop's existing `.env`
is a development configuration with a temporary tunnel and is not suitable
for production. Do not upload `compose.override.yaml` or use
`compose.local.yaml` for this deployment.

Edit `.env`:

- `APP_DOMAIN`, `ADMIN_DOMAIN`, `ACME_EMAIL` and the four URLs
  (`PASSENGER_MINIAPP_URL=https://APP_DOMAIN/passenger/`,
  `DRIVER_MINIAPP_URL=https://APP_DOMAIN/driver/`,
  `ADMIN_FRONTEND_URL=https://ADMIN_DOMAIN/`, `API_PUBLIC_URL=https://APP_DOMAIN`).
  The template already sets the domains and URLs for `jan-taxi.kz`; fill in a
  real `ACME_EMAIL` and keep `CADDY_GLOBAL_OPTIONS` / `CADDY_SERVER_OPTIONS` empty,
  `APP_ENV=production` and `ADMIN_COOKIE_SECURE=true`.
- `POSTGRES_PASSWORD=$(openssl rand -hex 24)`.
- `SESSION_SECRET=$(openssl rand -hex 32)` and
  `INTERNAL_BOT_SERVICE_SECRET=$(openssl rand -hex 32)` (two different values).
- Bot tokens and usernames ([TELEGRAM_SETUP.md](TELEGRAM_SETUP.md)).
- Kaspi Pay details (or set them later in the admin panel).
- Geocoder choice (`GEOCODING_*`), see section 8.
- `BACKUP_OWNER=$(id -u):$(id -g)` of the deploy user, so it can copy the backups
  off-site ([OPERATIONS.md](OPERATIONS.md#off-site-copies)).

`.env` is the single source of configuration: Compose reads it for interpolation
and passes the API's variables to the container explicitly (`x-api-env` in
`compose.yaml`). To use a file with another name, pass it to every command
(`make up ENV_FILE=/path/to/file`, `docker compose --env-file /path/to/file ...`).

Then:

```sh
make preflight                                   # refuses placeholders and inconsistent URLs
export IMAGE_TAG=$(git rev-parse --short HEAD)   # tag images with the release
make build
make osrm-prepare                                # once; downloads and builds routing data
make up                                          # db → migrate → api → bots, web
make health
make admin-create EMAIL=owner@<your-domain> ROLE=super_admin NAME="Owner Name"
```

Keep `IMAGE_TAG` in `.env` up to date (or export it in the deploy shell): Compose
uses it to name and select images, which is what makes rollback possible.

Then:

1. Configure both bots in @BotFather ([TELEGRAM_SETUP.md](TELEGRAM_SETUP.md)) and
   open each Mini App from Telegram.
2. Sign in to `https://ADMIN_DOMAIN/`, create the remaining admins with their own
   roles, check the city, service zone and tariffs, and fill in the support
   contacts and Kaspi Pay settings.
3. `make backup`, check `./backups`, and set up the off-site copy
   ([OPERATIONS.md](OPERATIONS.md#off-site-copies)).

Startup order is enforced by Compose: `db` healthy → `migrate` completes
successfully → `api` healthy → bots; `web` starts with the API. If `migrate`
fails, nothing else starts; read `make logs SERVICE=migrate`.

## 6. Upgrades without data loss

```sh
cd /opt/jantaxi
make backup                                       # fresh restore point
git fetch && git checkout <release>
export IMAGE_TAG=$(git rev-parse --short HEAD)    # new image tag; old images stay
make preflight
make build
make up                                           # runs migrate, then recreates changed services
make health
```

- Database migrations only move forward (`api migrate up`). They run in the
  `migrate` service before the new API starts and are applied under a database
  advisory lock.
- Write migrations so the previous release still works with the new schema
  (add columns and tables first, remove them in a later release). This keeps
  rollback a matter of switching images.
- `make up` recreates only containers whose image or configuration changed. The
  API drains HTTP and WebSocket connections for up to 20 seconds
  (`stop_grace_period: 30s`); clients reconnect and re-sync automatically.
- The bots hold a lease in the database; a restarted bot continues from the
  stored update offset, and every update is claimed once, so no message is
  processed twice.
- Refresh the routing data monthly: `make osrm-prepare`, then
  `docker compose --profile osrm up -d --force-recreate osrm`.

## 7. Rollback

If the new release misbehaves and its migrations were backward compatible:

```sh
export IMAGE_TAG=<previous tag>
make up
make health
```

The `api migrate` command has no "down" step. If a migration was not backward
compatible, restore the database from the backup taken before the upgrade:

```sh
make restore FILE=backups/db/<db>-<timestamp>.dump    # into a NEW database
# follow the printed steps to swap databases, then:
export IMAGE_TAG=<previous tag>
make up && make health
```

Data written between the backup and the rollback is then lost from the live
database (it remains in the renamed copy for manual recovery).

## 8. Routing and geocoding

- **OSRM** (required for route previews and fares): profile `osrm` in
  `COMPOSE_PROFILES`, data from `make osrm-prepare` (Geofabrik Kazakhstan extract,
  verified against Geofabrik's MD5, built with the car profile and the MLD
  algorithm). Image `ghcr.io/project-osrm/osrm-backend:v26.10.0-debian`.
- **Geocoder** (address search and reverse geocoding, always proxied by the API):
  - `GEOCODING_PROVIDER=photon` with empty `GEOCODING_BASE_URL` uses the public
    Photon instance (`photon.komoot.io`): fair use only, no service guarantee,
    and search text leaves Kazakhstan. Acceptable for a pilot only.
  - Self-hosted Photon: add `geocoder` to `COMPOSE_PROFILES`, run
    `make geocoder-import` (downloads the GraphHopper Central Asia dump, about
    190 MB, and imports Kazakhstan), set `GEOCODING_BASE_URL=http://photon:2322`,
    `make up`. The import tooling in `deploy/photon` has not been exercised by the
    project's automated checks; verify the first import and a few searches
    manually.
  - 2GIS: `GEOCODING_PROVIDER=twogis` with a commercial `GEOCODING_API_KEY`.
  - `none`: search falls back to admin-curated places only.

## 9. Local edge (plain HTTP)

To run the complete stack on a laptop without certificates, follow the comments
in `compose.override.example.yaml`: `APP_DOMAIN=http://localhost:8088`,
`ADMIN_DOMAIN=http://localhost:8089`, `CADDY_GLOBAL_OPTIONS="auto_https off"`,
`APP_ENV=development`, `ADMIN_COOKIE_SECURE=false` and the matching
`http://localhost` URLs. Caddy then serves `http://localhost:8088/passenger/`,
`http://localhost:8088/driver/` and `http://localhost:8089/`. On Apple Silicon
set `POSTGIS_IMAGE=imresamu/postgis:17-3.5-alpine`.

To publish the plain-HTTP edge to Telegram through a temporary HTTPS tunnel,
use the local live stack below instead.

## 10. Local live stack (Docker + Cloudflare quick tunnel)

A development setup that runs the complete platform on one machine with the real
test bots: every service in Docker, the Mini Apps reachable from Telegram over
HTTPS through a [Cloudflare quick tunnel](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/do-more-with-tunnels/trycloudflare/)
(`https://<random>.trycloudflare.com`). Quick tunnels have no uptime guarantee
and get a new host name every time `cloudflared` starts, so this is never a
production setup (production needs a domain and a server in Kazakhstan, §1–§5).

**Pieces.**

| File | Role |
|---|---|
| `compose.local.yaml` | overlay on `compose.yaml`: `web` publishes `127.0.0.1:8088` (Mini Apps + their `/api`, no `/api/v1/admin*`) and `127.0.0.1:8089` (admin panel + the whole `/api`); bot health on `127.0.0.1:18091` / `:18092`; `tunnel` service (`cloudflared --protocol http2 --url http://web:8088`, metrics on `127.0.0.1:20241`) |
| `deploy/local/stack.sh` | the only entry point: `env`, `up`, `rebuild`, `retunnel`, `status`, `verify`, `logs`, `down`, `menus`, `watchdog`, `import-devdb`, `psql` (`stack.sh help`) |
| `deploy/local/watchdog.sh` | background loop started by `stack.sh up` (see below) |
| `<state>/compose.env` | static settings written by `stack.sh env` from the repository `.env` (bot tokens, secrets, Kaspi, map and routing settings) plus local values: `APP_ENV=development`, `APP_DOMAIN=:8088`, `ADMIN_DOMAIN=:8089`, `CADDY_GLOBAL_OPTIONS="auto_https off"`, `CADDY_SERVER_OPTIONS="trusted_proxies static private_ranges"`, `ADMIN_FRONTEND_URL=http://localhost:8089/`, `ADMIN_COOKIE_SECURE=false`, a random `POSTGRES_PASSWORD`, `POSTGIS_IMAGE=imresamu/postgis:17-3.5-alpine` on Apple Silicon, laptop-sized memory limits, backups in `<state>/backups` |
| `<state>/tunnel.env` | the current tunnel: `TUNNEL_URL`, `PASSENGER_MINIAPP_URL=<url>/passenger/`, `DRIVER_MINIAPP_URL=<url>/driver/`, `API_PUBLIC_URL=<url>`, `CORS_ALLOWED_ORIGINS=<url>,http://localhost:8088`; rewritten by the script only |

`<state>` is a directory **outside the repository** (it holds secrets), given as
`JT_STACK_DIR` or `make local-* STACK_DIR=...`. The Compose project is
`jantaxi-local` (volumes `jantaxi-local_pgdata`, `jantaxi-local_files`); it does
not touch the test database container `jantaxi-devdb` on port 55432.

**Start.**

```sh
export JT_STACK_DIR=$HOME/jan-taxi-local          # any directory outside the repo
deploy/local/stack.sh env                         # compose.env from ./.env (real test bot tokens)
deploy/local/stack.sh up                          # or: make local-up STACK_DIR=$HOME/jan-taxi-local
deploy/local/stack.sh status
deploy/local/stack.sh verify
```

`up` builds missing images, starts the database, `web` and the tunnel, reads the
tunnel URL from cloudflared (`/quicktunnel` on the metrics port), waits until the
URL answers, writes `tunnel.env`, then starts migrations, the API, both bots and
the backup service. The bots publish their **default** menu buttons with the
Mini App URLs when they take the polling lease; the script checks them, sets
them itself if Telegram still reports the old value, and resets every known
user's per-chat button to `{"type":"default"}`. Finally it starts the watchdog
and prints the URLs:

- Mini Apps: `<tunnel>/passenger/` and `<tunnel>/driver/` (deep links such as
  `<tunnel>/driver/orders` serve the app; missing assets stay 404).
- Admin panel: `http://localhost:8089/` (bound to 127.0.0.1, never behind the
  tunnel). The admin API answers only there: on the Mini App site (`:8088`, the
  tunnel) `/api/v1/admin*` is 404 in Caddy, and the API itself refuses admin
  routes whose `Host` is the Mini App host. The first super admin comes from `ADMIN_BOOTSTRAP_EMAIL` /
  `ADMIN_BOOTSTRAP_PASSWORD` when the database has no admins.
- WebSocket: `wss://<tunnel>/api/v1/ws` (Mini Apps) and
  `ws://localhost:8089/api/v1/ws` (admin), proxied by Caddy.

**Commands.**

| Command | What it does |
|---|---|
| `rebuild` | rebuilds the api, bot and web images from the working tree, runs the migrations (`migrate` one-shot), recreates api, then bots and web. The database and the tunnel are not touched, so the URL stays. A failed build or migration leaves the running containers as they were; when the new api (or bots/web) does not become healthy, its log is printed and the previous image (`<image>-previous`) is started again. |
| `retunnel` | recreates the tunnel container (new URL), waits for it to answer, moves api, bots and menu buttons to it |
| `status` | container states and health, tunnel URL (and whether cloudflared still serves it), public probes of `/passenger/`, `/driver/`, deep links and `public/config` (200) and of `/api/v1/admin/auth/*` (404: no admin API through the tunnel), API `/readyz`, both bots' health JSON (`role` `leader`, `last_poll_age_s`), the bots' default menu buttons, the admin panel, the watchdog; exits 1 when anything is off |
| `verify` | SPA deep links and a WebSocket upgrade through the tunnel (101; 403 for a foreign `Origin`), the client IP seen by the API (must be the caller's public address), admin login → `me` → logout with the bootstrap credentials (never printed) |
| `logs <service>` / `logs watchdog` | follow a service's logs / the watchdog log |
| `down` | stops the watchdog and removes the containers; volumes, backups and `compose.env` stay. The next `up` gets a new tunnel URL. |
| `import-devdb [--replace]` | one-time copy of another PostgreSQL container's database (default `jantaxi-devdb`/`jantaxi`, read-only `pg_dump`, restored in one transaction) and of a files directory (`JT_IMPORT_FILES_DIR`) into the stack; refuses to overwrite a database that has a schema unless `--replace` |

**Watchdog** (`<state>/logs/watchdog.log`, every 30 s; `stack.sh watchdog
start|stop|status`): starts a stopped api/bot/web/db container again, restarts
an `unhealthy` one (at most once per 3 minutes), follows a changed tunnel URL
(cloudflared restarted, e.g. after Docker Desktop restarted) by moving api, bots
and menu buttons, and recreates the tunnel only after about 10 minutes
(`JT_TUNNEL_FAIL_LIMIT=20` probes) of Cloudflare answering 530 / error 1033.
Other failures, such as this machine being offline, neither count nor reset.
Each cycle runs a private copy of `stack.sh` (`<state>/run/`) that is refreshed
only from a version passing `sh -n`, so editing the script never runs half a
file. The watchdog does not survive a reboot of the machine: run `stack.sh up`
again (it keeps a running tunnel and its URL, or follows a new one).

**Migrations being written.** The script keeps a copy of every migration file it
has applied (`<state>/migrations-applied`); `up`, `rebuild` and `status` report an
applied migration that was edited in the tree since. The stack holds real users'
orders, so nothing is rolled back by default: put a schema change into a new
migration. Only with `JT_AUTO_REDO_MIGRATIONS=1` does `rebuild` redo edited
versions from `JT_REDO_MIN_VERSION` (default 7) on: it first writes a
`pg_dump -Fc` of the database to `<state>/backups/pre-redo/pre-redo-<time>.dump`
(and stops if that fails), then rolls the applied versions back with their own
`-- +goose Down` sections (newest first, one transaction each) and applies the
edited files. Versions 1–6 are never rolled back automatically. `make local-test`
(`deploy/local/stack_test.sh`) checks this without Docker.

**Details and pitfalls.**

- Every start of the tunnel container — including `docker restart` — gets a new
  URL. Never recreate it by hand; `stack.sh` and the watchdog keep `tunnel.env`,
  the API, the bots and the menu buttons in step.
- A new `trycloudflare.com` name does not resolve for its first seconds. A lookup
  through the local network at that moment is cached as "no such name" by macOS
  and by home routers, sometimes for many minutes, and then browsers, Telegram
  Desktop and phones on the same Wi-Fi cannot open the Mini Apps. The script's
  probes therefore resolve the name through public resolvers only (`dig @1.1.1.1`
  etc. + `curl --resolve`), and the bots get the URL only after it answers. If it
  happens anyway: on macOS `sudo dscacheutil -flushcache; sudo killall -HUP
  mDNSResponder`, or `stack.sh retunnel` for a fresh name.
- `getChatMenuButton` can report the previous default button for a few minutes
  after it was changed.
- Caddy trusts `X-Forwarded-For` from private networks only on this stack
  (`CADDY_SERVER_OPTIONS`), so rate limits see each Telegram user's own address
  instead of the tunnel container's.
- Development data only: public OSRM and Photon servers, `APP_ENV=development`.
  Telegram direct links `t.me/<bot>?startapp=…` need the Main Mini App URL in
  @BotFather, which a changing quick-tunnel URL cannot keep current; the menu
  buttons and the bots' inline `web_app` buttons always use the current URL.
