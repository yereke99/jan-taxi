# Telegram setup

Jan Taxi uses **two separate bots**: one for passengers and one for drivers. Each
bot has its own Mini App (passenger: `https://APP_DOMAIN/passenger/`, driver:
`https://APP_DOMAIN/driver/`). Separate bots keep the audiences apart and are a
security requirement: Mini App sign-in data is validated with the token of the bot
that opened the app, so a passenger session can never authenticate as a driver.

Telegram opens Mini Apps only over HTTPS, so finish DNS and the first deployment
([DEPLOYMENT.md](DEPLOYMENT.md)) before configuring the Mini Apps.

## 1. Create the bots

In Telegram, open **@BotFather**:

1. `/newbot` → display name (for example "Jan Taxi") → username ending in `bot`.
   BotFather replies with the token.
2. Repeat for the driver bot (for example "Jan Taxi Driver").
3. Put the values into `.env`:

   ```
   PASSENGER_BOT_TOKEN=<token of the passenger bot>
   PASSENGER_BOT_USERNAME=<passenger bot username, without @>
   DRIVER_BOT_TOKEN=<token of the driver bot>
   DRIVER_BOT_USERNAME=<driver bot username, without @>
   ```

4. Recreate the services that use them: `docker compose up -d api passenger-bot driver-bot`.

Treat the tokens like passwords: anyone with a token controls the bot and can
forge Mini App sign-ins. Never paste them into chats, tickets or screenshots. If
a token leaks, revoke it in @BotFather (the bot → API Token → Revoke) and update
`.env` ([OPERATIONS.md](OPERATIONS.md#rotating-secrets)).

The bots use long polling (`getUpdates`) and delete any webhook when they start.
Do not set a webhook for these bots, and do not run the same token anywhere else
(for example on a developer laptop): Telegram allows only one poller per token.
Use separate test bots for development.

## 2. Bot profile

Each bot publishes its own command list, description and short description
("About") in Kazakh (the default for every language), Russian and English through
the Bot API whenever it starts polling, and sets its menu button (section 4). Texts
typed into @BotFather for these fields are overwritten on the next start; change
them in the bot source (`passenger/bot`, `driver/bot`) instead.

Set in @BotFather → `/mybots` → the bot → **Edit Bot**:

- **Name**: for example "Jan Taxi" and "Jan Taxi Driver".
- **Botpic**: the service logo (square image).

Link the privacy policy ([DATA_PROTECTION.md](DATA_PROTECTION.md)) from a pinned
message or the support screen once it is published.

**Domain** (`/setdomain`) is only for the Telegram Login Widget on websites. Jan
Taxi does not use it: the Mini Apps sign in with `initData` and the admin panel
with email and password.

## 3. Main Mini App (one per bot)

@BotFather → `/mybots` → the bot → **Bot Settings** → **Configure Mini App** →
**Enable Mini App**, then set the URL:

| Bot | Mini App URL |
|---|---|
| passenger | `https://APP_DOMAIN/passenger/` |
| driver | `https://APP_DOMAIN/driver/` |

The URL must match `PASSENGER_MINIAPP_URL` / `DRIVER_MINIAPP_URL` in `.env`
exactly (including the trailing slash). In the same menu you can configure the
splash screen (icon and colours: white background, black text, matching the app).

With a Main Mini App, the bot's profile shows an **Open** button and these direct
links work:

```
https://t.me/<bot_username>?startapp
https://t.me/<bot_username>?startapp=<param>
https://t.me/<bot_username>?startapp=<param>&mode=fullscreen
```

`mode=fullscreen` opens the app in full-screen mode. Without it, a Main Mini App
opens at full height unless its launch mode was changed in @BotFather; the Mini
Apps also request full screen themselves on clients that support it (Bot API 8.0+). `<param>` must match
`^[A-Za-z0-9_-]{1,64}$`; it is a navigation hint only, every screen loads its data
with full authorisation:

| App | `startapp` | Opens |
|---|---|---|
| passenger | `home` (or none) | map home |
| passenger | `taxi`, `parcel`, `other` | booking with that category selected |
| passenger | `orders` | my orders |
| passenger | `order_<uuid>` | order detail / live trip |
| passenger | `support` | support |
| passenger | `driver` | "become a driver" (links to the driver bot) |
| driver | `home` (or none) | driver home |
| driver | `register` | registration / corrections |
| driver | `orders` | available orders |
| driver | `order_<uuid>` | order detail |
| driver | `trip` | active trip |
| driver | `wallet`, `topup`, `topup_<uuid>` | wallet / new top-up / top-up detail |
| driver | `profile`, `support` | profile / support |

Named Mini Apps (`/newapp`, links like `https://t.me/<bot>/<short_name>`) are
optional. Leave `PASSENGER_MINIAPP_SHORT_NAME` / `DRIVER_MINIAPP_SHORT_NAME` empty
to use the Main Mini App; set them only if you create named apps and want links in
that form.

## 4. Menu button

The menu button (left of the message field) opens the Mini App with signed user
data. Only the bot's **default** menu button is used. Each bot publishes it itself
every time it takes the polling lease (`setChatMenuButton` without `chat_id`,
title `Jan Taxi` / `Jan Taxi Driver`, URL = `PASSENGER_MINIAPP_URL` or
`DRIVER_MINIAPP_URL`, falling back to the URL from the API config), retrying
until Telegram accepts it, but only when that URL is https; the bot logs a
warning otherwise.

Per-chat menu buttons are not used: Telegram keeps a chat's own button, URL
included, until it is replaced, so it goes stale whenever the Mini App URL
changes. A chat that still carries one (from an older version or a manual
`setChatMenuButton` call with `chat_id`) is reset to the default the first time
it sends the bot an update after a restart. The button label is therefore the
same in every language. The default can also be set by hand in @BotFather →
`/mybots` → the bot → **Bot Settings** → **Menu Button**, for example before the
first deployment.

## 5. How the bots open the Mini Apps

- Menu button and inline `web_app` buttons in messages: Telegram passes signed
  `initData` (the app signs the user in with it).
- `t.me/<bot>?startapp=...` direct links and the profile's Open button: also
  signed.
- **Reply-keyboard `web_app` buttons are never used**: Telegram does not pass
  `initData` to apps opened from them, so the user could not sign in.

## 6. Driver phone verification and live location

- Registration requires a phone number verified by Telegram. The driver Mini App
  asks Telegram to share the contact; Telegram sends it to the driver bot as a
  contact message, and the bot accepts it only if the contact belongs to the
  sender. This works only in the private chat with the driver bot, so drivers must
  start the driver bot first.
- Drivers can share their **live location** with the driver bot (attachment →
  Location → Share my live location). The bot forwards updates to the API; points
  outside Kazakhstan are rejected.

## 7. Origin lock (Bot API 10.2)

Since 20 July 2026 Telegram blocks Mini App methods (location, buttons, haptics,
contact requests and so on) on pages from an origin other than the Mini App's own
domain. Consequences:

- Serve each Mini App only from `APP_DOMAIN`; never redirect the Mini App window to
  another domain.
- External pages (Kaspi Pay payment link, 2GIS navigation, support links) are
  opened with Telegram's `openLink` / `openTelegramLink`, i.e. outside the Mini App
  window. They keep working.
- @BotFather offers an opt-out of the origin lock; leave it enabled.

## 8. Testing

- **Without Telegram** (development): `make devlink APP=passenger START=taxi`
  prints a URL with `initData` signed by the token in `.env.dev`; open it in a
  desktop browser while `make dev-api` and `make fe-dev APP=passenger` run. Use
  `TG_ID=` to sign in as different users. `devlink` refuses to run with
  `APP_ENV=production`.
- **In Telegram**: create separate test bots, deploy a staging copy on its own
  domain (HTTPS), configure its Main Mini Apps as above, and test on iOS, Android
  and Telegram Desktop/Web. Check: full screen, map gestures, location permission,
  contact sharing for drivers, live location, Open buttons in notifications, and
  `t.me` links with `startapp`.
- Telegram Web (`web.telegram.org`) shows Mini Apps in an iframe; the
  Content-Security-Policy in the `Caddyfile` allows framing only by
  `https://web.telegram.org` and `https://*.telegram.org`.
