#!/bin/sh
# Set the architecture-specific digest from the independently trusted release manifest.
set -eu
umask 077
fail() { echo "$*" >&2; exit 1; }
[ "$(uname -s)" = Linux ] && [ -r /etc/os-release ] || fail "Linux with os-release required"
. /etc/os-release
case "$ID/${VERSION_ID:-}" in
    debian/12|debian/13|ubuntu/22.04|ubuntu/24.04|ubuntu/26.04) ;;
    *) fail "supported: Debian 12/13 or Ubuntu 22.04/24.04/26.04";;
esac
[ -d /run/systemd/system ] || fail "systemd required"
case "$(uname -m)" in
    x86_64) arch=amd64; digest=${NEXUS_SHA256_AMD64:-};;
    aarch64|arm64) arch=arm64; digest=${NEXUS_SHA256_ARM64:-};;
    *) fail "supported: x86_64 or arm64";;
esac
[ "${#digest}" -eq 64 ] || fail "a trusted 64-character SHA-256 is required for $arch"
case "$digest" in *[!0123456789abcdefABCDEF]*) fail "invalid digest";; esac
stage=$(mktemp -d) || exit 1
trap 'rm -f -- "$stage/nexus-node"; rmdir -- "$stage"' EXIT
trap 'exit 1' HUP INT TERM
if [ -n "${NEXUS_NODE_FILE:-}" ]; then
    cat -- "$NEXUS_NODE_FILE" > "$stage/nexus-node"
else
    case "${NEXUS_RELEASE_BASE:-}" in https://*) ;; *) fail "provide an HTTPS release base";; esac
    curl --proto '=https' --proto-redir '=https' --fail --silent --show-error --location \
        "${NEXUS_RELEASE_BASE%/}/nexus-node-linux-$arch" --output "$stage/nexus-node"
fi
(cd "$stage" && printf '%s  nexus-node\n' "$digest" | sha256sum --check --status) || fail "nexus-node SHA-256 mismatch"
chmod 0700 "$stage/nexus-node"
[ "$#" -gt 0 ] || set -- install
case "$1" in install|upgrade|status|uninstall) ;; *) fail "expected install, upgrade, status or uninstall";; esac
"$stage/nexus-node" "$@"
