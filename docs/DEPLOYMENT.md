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

1. Choose two host names, for example `app.<your-domain>` (Mini Apps and API)
   and `admin.<your-domain>` (admin panel).
2. Create A (and AAAA, if the server has IPv6) records for both names pointing at
   the server. Wait until they resolve before the first start.
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

Edit `.env`:

- `APP_DOMAIN`, `ADMIN_DOMAIN`, `ACME_EMAIL` and the four URLs
  (`PASSENGER_MINIAPP_URL=https://APP_DOMAIN/passenger/`,
  `DRIVER_MINIAPP_URL=https://APP_DOMAIN/driver/`,
  `ADMIN_FRONTEND_URL=https://ADMIN_DOMAIN/`, `API_PUBLIC_URL=https://APP_DOMAIN`).
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
