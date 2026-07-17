#!/usr/bin/env bash
# Install PortX via Homebrew (best UX).
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/jordanliu/portx/main/scripts/install.sh | bash
set -euo pipefail

TAP="${PORTX_TAP:-jordanliu/portx}"
FORMULA="portx"

if ! command -v brew >/dev/null 2>&1; then
  echo "Homebrew is required: https://brew.sh" >&2
  echo "Then re-run this script, or: brew install ${TAP}/${FORMULA}" >&2
  exit 1
fi

echo "==> Tapping ${TAP}"
brew tap "${TAP}" 2>/dev/null || brew tap "${TAP}"

echo "==> Installing ${TAP}/${FORMULA} (includes cloudflared)"
brew install --formula "${TAP}/${FORMULA}"

echo
echo "Installed:"
portx version 2>/dev/null || true
cloudflared version 2>/dev/null || true
echo
echo "Next:"
echo "  portx http 3000"
echo "  portx setup"
