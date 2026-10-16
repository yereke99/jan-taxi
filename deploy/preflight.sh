#!/bin/sh
# Checks a Compose env file before `make up`: required values present,
# placeholders replaced, secrets long enough, URLs consistent with the domains.
# The file is parsed, never executed.
#
#   sh deploy/preflight.sh [.env]
set -eu

file=${1:-.env}
[ -f "$file" ] || { echo "preflight: $file not found"; exit 1; }

errors=0
warnings=0
err() { echo "  ERROR   $*"; errors=$((errors + 1)); }
warn() { echo "  WARNING $*"; warnings=$((warnings + 1)); }

# Last assignment of KEY, surrounding quotes removed.
get() {
	sed -n "s/^$1=//p" "$file" | tail -n 1 | sed -e 's/^"\(.*\)"$/\1/' -e "s/^'\(.*\)'\$/\1/"
}

require() {
	v=$(get "$1")
	if [ -z "$v" ]; then
		err "$1 is empty"
	elif echo "$v" | grep -qi 'change_me'; then
		err "$1 still holds the placeholder value"
	fi
}

echo "preflight: checking $file"

app_env=$(get APP_ENV)
[ -n "$app_env" ] || app_env=production
prod=0
[ "$app_env" = production ] && prod=1

for k in POSTGRES_PASSWORD SESSION_SECRET INTERNAL_BOT_SERVICE_SECRET \
	PASSENGER_BOT_TOKEN DRIVER_BOT_TOKEN PASSENGER_BOT_USERNAME DRIVER_BOT_USERNAME \
	APP_DOMAIN ADMIN_DOMAIN ACME_EMAIL \
	PASSENGER_MINIAPP_URL DRIVER_MINIAPP_URL ADMIN_FRONTEND_URL API_PUBLIC_URL; do
	require "$k"
done

pw=$(get POSTGRES_PASSWORD)
if [ -n "$pw" ] && ! echo "$pw" | grep -Eq '^[A-Za-z0-9._~-]+$'; then
	err "POSTGRES_PASSWORD must use URL-safe characters only (A-Z a-z 0-9 . _ ~ -); try: openssl rand -hex 24"
fi
if [ -n "$pw" ] && [ "${#pw}" -lt 16 ]; then
	err "POSTGRES_PASSWORD is shorter than 16 characters"
fi

s1=$(get SESSION_SECRET)
s2=$(get INTERNAL_BOT_SERVICE_SECRET)
[ -z "$s1" ] || [ "${#s1}" -ge 32 ] || err "SESSION_SECRET must be at least 32 characters"
[ -z "$s2" ] || [ "${#s2}" -ge 32 ] || err "INTERNAL_BOT_SERVICE_SECRET must be at least 32 characters"
[ -z "$s1" ] || [ "$s1" != "$s2" ] || err "SESSION_SECRET and INTERNAL_BOT_SERVICE_SECRET must differ"

t1=$(get PASSENGER_BOT_TOKEN)
t2=$(get DRIVER_BOT_TOKEN)
[ -z "$t1" ] || [ "$t1" != "$t2" ] || err "PASSENGER_BOT_TOKEN and DRIVER_BOT_TOKEN must belong to different bots"
# Same formats as the API's configuration check.
for k in PASSENGER_BOT_TOKEN DRIVER_BOT_TOKEN; do
	v=$(get "$k")
	[ -z "$v" ] || echo "$v" | grep -Eq '^[0-9]{5,}:[A-Za-z0-9_-]{30,}$' || err "$k is not a valid Telegram bot token"
done
for k in PASSENGER_BOT_USERNAME DRIVER_BOT_USERNAME; do
	v=$(get "$k")
	v=${v#@}
	if [ -n "$v" ] && ! { echo "$v" | grep -Eiq '^[a-z][a-z0-9_]{3,31}$' && echo "$v" | grep -Eiq 'bot$'; }; then
		err "$k must be a bot username (5-32 characters, ending in bot)"
	fi
done

app=$(get APP_DOMAIN)
admin=$(get ADMIN_DOMAIN)
if [ "$prod" = 1 ]; then
	for k in APP_DOMAIN ADMIN_DOMAIN ACME_EMAIL; do
		case "$(get "$k")" in
			*.example | *.example/* | *@*.example | http://*) err "$k still uses a placeholder (.example) or plain http: $(get "$k")" ;;
		esac
	done
	for k in PASSENGER_MINIAPP_URL DRIVER_MINIAPP_URL ADMIN_FRONTEND_URL API_PUBLIC_URL; do
		case "$(get "$k")" in https://*) ;; *) err "$k must use https in production" ;; esac
	done
	[ "$(get PASSENGER_MINIAPP_URL)" = "https://$app/passenger/" ] || err "PASSENGER_MINIAPP_URL should be https://$app/passenger/"
	[ "$(get DRIVER_MINIAPP_URL)" = "https://$app/driver/" ] || err "DRIVER_MINIAPP_URL should be https://$app/driver/"
	[ "$(get ADMIN_FRONTEND_URL)" = "https://$admin/" ] || err "ADMIN_FRONTEND_URL should be https://$admin/"
	[ "$(get API_PUBLIC_URL)" = "https://$app" ] || err "API_PUBLIC_URL should be https://$app"
	[ "$(get ADMIN_COOKIE_SECURE)" != false ] || err "ADMIN_COOKIE_SECURE must be true in production"
	[ -n "$(get ADMIN_BOOTSTRAP_PASSWORD)" ] && warn "ADMIN_BOOTSTRAP_PASSWORD is set; remove it after the first admin exists"
	[ -n "$(get KASPI_PAY_PAYMENT_URL)" ] || warn "KASPI_PAY_PAYMENT_URL is empty; set it here or in the admin panel before drivers top up"
	case ",$(get COMPOSE_PROFILES)," in
		*,osrm,*) ;;
		*) warn "COMPOSE_PROFILES does not include osrm; route previews need OSRM_BASE_URL to reach a router" ;;
	esac
fi

if [ "$errors" -gt 0 ]; then
	echo "preflight: $errors error(s), $warnings warning(s). Fix $file (see .env.example and docs/DEPLOYMENT.md)."
	exit 1
fi
echo "preflight: ok ($warnings warning(s))"
