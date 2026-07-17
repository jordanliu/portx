# PortX reference

This page collects the detailed command, profile, operations, security, and
troubleshooting reference. For the short product overview, see the
[README](../README.md).

## Commands and flags

Run `portx <command> --help` for the installed binary’s current help output.

### `portx http <target>`

Expose one local HTTP origin. Without `--url`, PortX starts a quick tunnel. With
`--url`, it starts a managed route under the configured profile.

| Flag                     | Purpose                                                                                                         |
| ------------------------ | --------------------------------------------------------------------------------------------------------------- |
| `--url`                  | Managed mode; empty value infers a hostname, a short value becomes a subdomain, and a full value is used as-is. |
| `--save`                 | Save a managed route to `portx.yaml`.                                                                           |
| `--name`                 | Name used when saving the route.                                                                                |
| `--json`                 | Print status as JSON and keep the session open until terminated.                                                |
| `--replace`              | Replace an existing active lease for the hostname.                                                              |
| `--reuse`                | Reuse an existing compatible route when possible.                                                               |
| `--host-header`          | Override the `Host` header sent to the local origin.                                                            |
| `--scheme`               | Use `http` or `https` for the local origin.                                                                     |
| `--no-origin-check`      | Skip the local listening-service preflight check.                                                               |
| `--insecure-skip-verify` | Allow an HTTPS origin with an invalid certificate.                                                              |

### `portx start`

Start routes defined in `portx.yaml`:

```bash
portx start
portx start --only api
portx start --file ./ops/portx.yaml
portx start --json
```

Additional flags include `--only`, `--no-origin-check`, and `--replace`.

### `portx setup` and `portx login`

```bash
portx setup
portx setup --token
portx setup --reauth
portx login
portx login --force
```

`--token` selects API-token authentication. `--reauth` ignores the cached
browser certificate and performs a fresh browser login. `login --force` does
the same without provisioning a tunnel or DNS record.

### Other commands

| Command                       | Purpose                                                                                  |
| ----------------------------- | ---------------------------------------------------------------------------------------- |
| `portx list`                  | List active routes.                                                                      |
| `portx stop <hostname-or-id>` | Stop a route by hostname or lease ID prefix.                                             |
| `portx doctor [--verify]`     | Check config, credentials, tunnel, DNS, daemon, origin, and optionally the public route. |
| `portx config show`           | Display resolved configuration paths and values.                                         |
| `portx config validate`       | Validate global and project configuration.                                               |
| `portx cleanup [--force]`     | Remove stale sockets, PID files, leases, and legacy runtime state.                       |
| `portx reset [--yes]`         | Delete PortX-owned Cloudflare resources and local profile data.                          |
| `portx daemon status`         | Show managed-daemon status.                                                              |
| `portx daemon stop`           | Stop the managed daemon.                                                                 |
| `portx cloudflared version`   | Show the resolved `cloudflared` binary and version.                                      |
| `portx cloudflared install`   | Show `cloudflared` installation guidance.                                                |
| `portx cloudflared update`    | Show `cloudflared` upgrade guidance.                                                     |
| `portx version`               | Print version and build information.                                                     |

Global options include `--profile`, `--log-level`, and the standard command
help/version flags.

## Profiles and multiple domains

A PortX profile connects one Cloudflare account and zone to one wildcard
namespace. Use as many hostnames as needed below it:

```text
api.example.dev
web.example.dev
hooks.example.dev
```

Create one profile per Cloudflare zone or account:

```bash
portx --profile personal setup   # *.example.dev
portx --profile work setup       # *.example.net
portx --profile work http 3000 --url=api
```

The managed daemon currently serves one profile at a time. Stop it before
switching profiles:

```bash
portx daemon stop
portx --profile work http 3000 --url=api
```

Profile configuration and credentials are local. They are not stored in
`portx.yaml`, so project route files can be committed safely.

PortX leases managed hostnames locally. Inspect or stop an existing lease before
replacing it:

```bash
portx list
portx stop api.example.dev
portx http --url=api 3000 --replace
```

PortX does not silently replace a DNS record that points elsewhere. Resolve the
conflict in Cloudflare or choose another hostname.

Prefer a first-level wildcard such as `*.example.dev` over
`*.proxy.example.dev`. Deeper names may need additional certificate coverage;
see Cloudflare’s [Universal SSL limitations](https://developers.cloudflare.com/ssl/edge-certificates/universal-ssl/limitations/).

## Reset, cleanup, and clean setup tests

Clean stale local runtime state with:

```bash
portx doctor
portx cleanup --force
```

Current PortX can recover dead legacy PID-only files from older builds. It stops
only processes that identify as PortX or `cloudflared` and refuses unrelated
live processes.

Reset a profile and delete PortX-owned Cloudflare resources:

```bash
portx reset --yes --profile personal
portx reset --remote-only
portx reset --keep-local
```

Remote cleanup requires a working API credential. If remote cleanup cannot be
completed, PortX retains local recovery data.

Do not delete the entire `~/.cloudflared` directory if you use unrelated
Cloudflare tunnels. The wipe removes only PortX’s browser certificate and its
timestamped backups.

## Security and limitations

Anyone who knows a PortX URL can reach the exposed origin while the route is
running. PortX does not add application authentication; use Cloudflare Access
or application-level auth for sensitive services.

Credentials use the OS credential store when available:

- macOS Keychain
- Linux Secret Service
- Windows Credential Manager

`PORTX_CREDENTIALS_FILE=1` enables a local plaintext fallback. Protect that
directory and never commit it or use it on a shared machine. Do not share
`~/.cloudflared/cert.pem`; use `portx login --force` if it becomes stale.

Treat every configured origin as internet-facing. `--insecure-skip-verify`
disables TLS verification between PortX and an HTTPS origin. Managed mode binds
its local proxy to `127.0.0.1:4041` by default.

Quick tunnels are temporary, have no SLA, do not support SSE, and are subject
to Cloudflare concurrency limits. PortX relies on the `cloudflared` binary
installed on the machine.

Direct downloads should be checked against published checksums. PortX does not
currently publish independent signatures, an SBOM, or SLSA/in-toto provenance
attestations.

## Troubleshooting

Start with:

```bash
portx doctor
portx doctor --verify
portx --log-level debug http 3000
```

| Problem                                     | What to try                                                                                                          |
| ------------------------------------------- | -------------------------------------------------------------------------------------------------------------------- |
| `cloudflared not found`                     | Install it with `brew install cloudflared`, then run `portx cloudflared version`.                                    |
| Browser login reports an invalid token      | Run `portx login --force`, then `portx setup`.                                                                       |
| Setup says DNS propagation is pending       | Wait briefly and run `portx doctor --verify`; new wildcard records may not be visible to every resolver immediately. |
| Setup says stale or unverified daemon state | Run `portx doctor`, then `portx cleanup --force`.                                                                    |
| Origin connection errors                    | Confirm the app is listening on the target port, or use `--no-origin-check` when appropriate.                        |
| Hostname is already active                  | Run `portx list`, then `portx stop <hostname>`, `--replace`, or `--reuse`.                                           |
| DNS record already points elsewhere         | Resolve the record conflict in Cloudflare or choose another hostname.                                                |
| SSL errors on a multi-level hostname        | Prefer `*.zone` over `*.sub.zone`, or configure the required certificate coverage.                                   |
| Tunnel was deleted in Cloudflare            | Run `portx setup` again.                                                                                             |
| Project routes are not found                | Run from the project directory or pass `--file ./path/portx.yaml`.                                                   |

Setup saves provisioned resources before public verification. If DNS is still
propagating, the profile may already be usable even though verification is
pending. Run `portx doctor --verify` before resetting anything.
