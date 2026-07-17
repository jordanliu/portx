#!/usr/bin/env bash
# Reset local PortX state for a clean setup/login test.
#
# Usage:
#   make wipe
#   make wipe WIPE_REMOTE=1
#   make wipe PORTX_PROFILE=work
set -euo pipefail

PORTX_BIN="${PORTX_BIN:-./bin/portx}"
PORTX_PROFILES="${PORTX_PROFILES:-${PORTX_PROFILE:-personal}}"
WIPE_REMOTE="${WIPE_REMOTE:-0}"

if [[ "$WIPE_REMOTE" == "1" ]]; then
	cat >&2 <<EOF
This will delete PortX-owned Cloudflare resources for profile(s) "$PORTX_PROFILES":
- the PortX tunnel
- the PortX wildcard DNS record
- local PortX state, credentials, and caches
EOF
	read -r -p 'Continue? [y/N] ' answer
	if [[ ! "$answer" =~ ^[Yy]([Ee][Ss])?$ ]]; then
		echo "Wipe cancelled."
	exit 0
	fi
fi

if [[ -x "$PORTX_BIN" ]]; then
	first_profile="${PORTX_PROFILES%% *}"
	"$PORTX_BIN" --profile "$first_profile" daemon stop >/dev/null 2>&1 || true
	"$PORTX_BIN" cleanup --force >/dev/null 2>&1 || true

	if [[ "$WIPE_REMOTE" == "1" ]]; then
		for profile in $PORTX_PROFILES; do
			"$PORTX_BIN" reset --yes --profile "$profile"
		done
	fi
fi

case "$(uname -s)" in
	Darwin)
		config_dir="$HOME/Library/Application Support/portx"
		state_dir="$config_dir"
		cache_dir="$HOME/Library/Caches/portx"
		;;
	*)
		config_dir="${XDG_CONFIG_HOME:-$HOME/.config}/portx"
		state_dir="${XDG_STATE_HOME:-$HOME/.local/state}/portx"
		cache_dir="${XDG_CACHE_HOME:-$HOME/.cache}/portx"
		;;
esac

rm -rf "$config_dir" "$state_dir" "$cache_dir"
rm -f "$HOME/.cloudflared/cert.pem"
find "$HOME/.cloudflared" -maxdepth 1 -type f -name 'cert.pem.bak.*' -delete 2>/dev/null || true

if command -v security >/dev/null 2>&1; then
	for profile in $PORTX_PROFILES; do
		for kind in api-token tunnel-token; do
			security delete-generic-password \
				-a "portx/profile/$profile/$kind" \
				-s portx >/dev/null 2>&1 || true
		done
	done
fi

if command -v secret-tool >/dev/null 2>&1; then
	for profile in $PORTX_PROFILES; do
		for kind in api-token tunnel-token; do
			secret-tool clear \
				service portx \
				account "portx/profile/$profile/$kind" >/dev/null 2>&1 || true
		done
	done
fi

find ./bin -type f -delete 2>/dev/null || true
find ./bin -type d -empty -delete 2>/dev/null || true

if command -v go >/dev/null 2>&1; then
	go clean -cache -testcache
fi

echo "PortX wipe complete. Run 'make build' before the next setup test."
