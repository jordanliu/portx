---
name: portx-reference
description: Explain the PortX architecture, runtime request flow, configuration and state model, daemon/RPC/proxy boundaries, Cloudflare integration, and code navigation. Use when planning or reviewing a PortX change, locating the right package, debugging behavior across subsystems, or answering how the project works internally.
---

# PortX reference

Use this skill to build a current mental model before changing code. Read
`README.md` for product behavior and `docs/reference.md` for command semantics,
then read [architecture.md](references/architecture.md) for the subsystem map.
Verify important details against the implementation because this reference is a
navigation aid, not a replacement for tests or source.

## Runtime model

Keep these boundaries distinct:

1. `cmd/portx` is a thin executable entrypoint that calls `internal/cli.Run`.
2. The CLI resolves the active profile, validates the origin and hostname, and
   either starts a quick `cloudflared` process or talks to the managed daemon.
3. The managed daemon owns the local proxy, route leases, the named
   `cloudflared` process, runtime files, and the RPC server.
4. The router selects a lease by hostname and path, then reverse-proxies to the
   validated local target.
5. Cloudflare sends managed traffic through the named tunnel to the local proxy;
   the proxy dispatches it to the route target.

Do not put Cloudflare API calls in the router or route-selection logic. Do not
make the CLI reach into daemon internals when an RPC operation already exists.

## Find the right code

- CLI commands and user-facing orchestration: `internal/cli/`
- Global config, profiles, project `portx.yaml`, and platform paths:
  `internal/config/`
- Setup provisioning and rollback: `internal/cli/setup.go`
- Daemon lifecycle, idle tunnel handling, and process identity checks:
  `internal/daemon/`
- Unix/Windows daemon communication: `internal/rpc/`
- Lease persistence, renewal, expiry, reuse, and replacement:
  `internal/leases/`
- Host/path matching and reverse proxy behavior: `internal/router/`
- Origin normalization, hostname inference, and SSRF checks:
  `internal/origin/`
- `cloudflared` discovery and process supervision: `internal/cloudflared/`
- Cloudflare REST operations: `internal/cloudflare/`
- Credentials and OS secret-store backends: `internal/credentials/`
- JSON setup progress and durable state: `internal/state/`
- Terminal output, prompts, and live dashboard: `internal/ui/`
- Exit codes and typed user errors: `internal/apperr/`

Use the matching `*_test.go` files as executable design examples. Platform-specific
files use suffixes such as `_darwin.go`, `_linux.go`, `_windows.go`, `_unix.go`, and
`_other.go`; preserve that split when changing platform behavior.

## Change and debug workflow

1. Reproduce through the CLI or the narrowest package test.
2. Trace the call from `internal/cli` to the owning subsystem instead of patching
   symptoms in the UI.
3. Preserve validation at both boundaries: CLI validation improves UX, while the
   daemon re-validates RPC inputs as a security boundary.
4. Keep project configuration shareable and credentials/state local.
5. Add or update focused tests, run `gofmt`, then run `go test ./...` and the
   relevant Makefile checks.

For operational failures, start with `portx doctor`; use `portx config show` and
`portx config validate` to distinguish path/config issues from Cloudflare or
origin issues. For request routing, inspect `portx list`, the daemon status, and
the lease manager before changing DNS or tunnel resources.

Avoid destructive diagnostics. `reset --yes` and `make wipe WIPE_REMOTE=1` can
delete PortX-owned Cloudflare resources; use them only when explicitly requested.
