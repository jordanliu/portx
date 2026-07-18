# PortX architecture map

## Request flows

### Quick tunnel

`portx http <target>` without `--url` normalizes the local origin and launches
`cloudflared tunnel --no-autoupdate --url <origin>`. The CLI watches the process,
extracts the temporary `trycloudflare.com` URL, and keeps the session alive until
termination. This path does not require a configured PortX profile.

### Managed route

`portx http <target> --url=<label>` resolves a full hostname under the active
profile's wildcard, ensures a daemon, and acquires a lease over RPC. The daemon
starts a named `cloudflared` tunnel when needed. That tunnel targets the daemon's
local proxy, whose default address is `127.0.0.1:4041`.

The route is registered in `internal/router.Registry` through
`internal/leases.Manager`. `internal/router.Proxy` matches the incoming hostname
and path, rewrites forwarding headers, and proxies to the normalized origin. The
lease records an owner PID/start time and an owner token so renew/release requests
are authorized and dead owners can be reconciled.

### Project start

`portx start` finds or receives a `portx.yaml`, validates its named routes, and
acquires each route through the same daemon/RPC path. `--only` filters names,
`--file` bypasses upward discovery, and `--json` uses a non-TUI reporting path.
Project routes define targets and hostnames, not credentials or tunnel IDs.

## Setup flow

`internal/cli/setup.go` coordinates:

1. Browser or API-token authentication.
2. Account and zone selection.
3. Reuse or creation of a PortX-owned named tunnel.
4. Tunnel-token retrieval and credential-store persistence.
5. Tunnel configuration pointing at the local proxy.
6. Wildcard CNAME reuse or creation, with conflict protection.
7. Local profile/state persistence.
8. End-to-end verification with a temporary origin and public DNS retries.

The durable phase markers live in `internal/state/store.go`. Setup uses rollback
snapshots for credentials, local config, state, tunnel configuration, and newly
created remote resources when provisioning fails.

## Configuration and state

- `internal/config.Load` reads the global `config.yaml`; `Config.Profile` resolves
  profile data such as account ID, zone, wildcard, and tunnel ID.
- `internal/config.FindProjectFile` walks upward for `portx.yaml` and retains a
  legacy `.portx.yaml` lookup for older checkouts.
- `internal/config.ResolveProfile` combines the CLI profile flag, project profile,
  and global default profile.
- `internal/config/paths.go` derives platform-specific config, state, cache, and
  runtime directories.
- `internal/credentials` abstracts Keychain, Secret Service, Credential Manager,
  and the explicit plaintext fallback.
- `internal/leases` persists one lease per route and expires leases using the
  configured TTL; `internal/state` persists setup progress and owned resources.

## Daemon and RPC

`internal/daemon.Daemon.Run` acquires the lock, recovers verified orphan process
records, starts the local proxy, writes a verified PID record, serves RPC, and
reconciles leases. It stops the tunnel when idle according to configuration.

`internal/rpc` defines protocol version 1, request/response methods, and request
event streaming used by the live dashboard. The daemon must validate every RPC
input again because callers can be replaced or bypassed.

## Tests and contracts

Tests are organized beside their implementation:

- `internal/cli/*_test.go`: command behavior and lifecycle contracts
- `internal/config/*_test.go`: YAML, profile, path, and validation behavior
- `internal/daemon/*_test.go`: startup, recovery, and lease lifecycle
- `internal/leases/*_test.go`: conflict, renewal, expiry, and persistence rules
- `internal/router/*_test.go`: matching, proxying, and request observation
- `internal/origin/*_test.go`: normalization, inference, and SSRF protection
- `internal/cloudflare/*_test.go`: API client behavior
- `internal/ui/*_test.go`: dashboard sizing and presentation contracts

Run `make test` for the full suite. Use `make vet`, `make format-check`, and
`make check` according to the risk of the change.
