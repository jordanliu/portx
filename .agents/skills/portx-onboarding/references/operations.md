# PortX operations reference

## Local storage

PortX keeps project routes in `portx.yaml`, but stores credentials and runtime data
outside the checkout:

| Data | macOS | Linux | Windows |
| --- | --- | --- | --- |
| Config (`config.yaml`) | `~/Library/Application Support/portx` | `${XDG_CONFIG_HOME:-~/.config}/portx` | `${AppData:-~/AppData/Roaming}/portx` |
| State (`state.json`) | `~/Library/Application Support/portx` | `${XDG_STATE_HOME:-~/.local/state}/portx` | `${LocalAppData:-~/AppData/Local}/portx` |
| Cache/runtime | `~/Library/Caches/portx` | `${XDG_CACHE_HOME:-~/.cache}/portx` and `${XDG_RUNTIME_DIR}/portx` | `${LocalAppData}/portx/cache` |

The runtime directory contains the daemon socket, PID/lock records, lease files,
and `cloudflared.log`. Credentials use macOS Keychain, Linux Secret Service, or
Windows Credential Manager when available. `PORTX_CREDENTIALS_FILE=1` enables a
plaintext fallback and is unsuitable for shared machines or committed automation.

## Setup phases

Setup persists progress through these phases:

`none` → `authenticated` → `resources_selected` → `tunnel_ensured` →
`token_stored` → `config_applied` → `dns_ensured` → `verification_pending` →
`verified` → `ready`

Provisioning is saved before public DNS verification completes. A verification
pending result is therefore not proof that the profile must be reset; retry
`portx doctor --verify` first.

## Troubleshooting matrix

| Symptom | First response |
| --- | --- |
| `cloudflared not found` | Install it, put it on `PATH`, run `portx cloudflared version`. |
| Browser auth is stale | Run `portx login --force`, then `portx setup`. |
| DNS propagation is pending | Wait briefly and run `portx doctor --verify`; do not reset immediately. |
| Stale daemon/PID state | Run `portx doctor`, then `portx cleanup --force`. |
| Origin connection error | Confirm the app is listening; use `--no-origin-check` only when the preflight is inappropriate. |
| Hostname already active | Run `portx list`, then `stop`, `--reuse`, or `--replace`. |
| DNS record points elsewhere | Resolve the conflict in Cloudflare or choose another hostname. PortX does not replace it automatically. |
| Multi-level hostname has SSL errors | Prefer a first-level wildcard such as `*.example.dev`; check certificate coverage for deeper names. |
| Project routes are not found | Run from the project directory or pass `--file ./path/portx.yaml`. |

All running PortX URLs are internet-facing. PortX does not add application
authentication; use Cloudflare Access or application-level authentication for
sensitive services.
