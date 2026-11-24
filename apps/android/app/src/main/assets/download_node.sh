#!/bin/sh
# Invoked by the Android SSH installer under a 300-second deadline.
set -eu
url=$1
dest=$2
case "$url" in https://*) ;; *) exit 1 ;; esac
if command -v curl >/dev/null 2>&1; then
    exec curl -q --proto '=https' --proto-redir '=https' --fail --silent --show-error \
        --location --connect-timeout 15 --max-time 300 --output "$dest" -- "$url"
fi
command -v wget >/dev/null 2>&1 || exit 1

# GNU Wget's --https-only applies to recursive downloads. Inspect each redirect
# before making the next request. Keep signed release URLs out of SSH output.
redirects=0
while :; do
    failed=0
    headers=$(LC_ALL=C wget --no-config --no-hsts --no-verbose --server-response --max-redirect=0 \
        --dns-timeout=15 --connect-timeout=15 --read-timeout=300 --tries=1 \
        --output-document="$dest" -- "$url" 2>&1) || failed=1
    next=$(printf '%s\n' "$headers" | awk '
        { sub(/\r$/, "") }
        /^  HTTP\/[0-9.]+ / { status=$2; location=""; count=0 }
        /^  / && tolower($1) == "location:" { location=$2; count++; if (NF != 2) exit 1 }
        END {
            if (status ~ /^2[0-9][0-9]$/) exit
            if (status ~ /^(301|302|303|307|308)$/ && count == 1 && location != "") print location
            else exit 1
        }') || exit 1
    if [ -z "$next" ]; then exit "$failed"; fi
    [ "$redirects" -lt 20 ] || exit 1
    redirects=$((redirects + 1))
    base=${url%%\?*}
    authority=${base#https://}
    origin=https://${authority%%/*}
    case "$next" in
        https://*) url=$next ;;
        //*) url=https:$next ;;
        /*) url=$origin$next ;;
        *:*|*\\*|\#*) exit 1 ;;
        \?*) url=$base$next ;;
        *) [ "$base" != "$origin" ] || base=$origin/; url=${base%/*}/$next ;;
    esac
done
