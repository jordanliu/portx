# PortX

Cloudflare Tunnel, without the configuration work.

PortX is a thin, developer-friendly wrapper around [`cloudflared`](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/). It handles tunnel setup, DNS, local routing, credentials, and project configuration so you can expose local services with simple, repeatable commands.

Create a temporary public URL with no account:

```bash
portx http 3000
# → https://random-name.trycloudflare.com
```

Or connect your Cloudflare zone and use stable custom hostnames:

```bash
portx setup
portx http --url=api 3000
# → https://api.example.dev
```

Run multiple local services through one tunnel, save routes in your repository, and stop managing `cloudflared` configuration files by hand.

## Contents

- [PortX](#portx)
  - [Contents](#contents)
  - [Install](#install)
  - [Quick tunnels](#quick-tunnels)
  - [Custom hostnames](#custom-hostnames)
    - [Multiple domains and profiles](#multiple-domains-and-profiles)
  - [PortX vs. cloudflared](#portx-vs-cloudflared)
  - [Project routes](#project-routes)
  - [Commands](#commands)
    - [`portx http <target>`](#portx-http-target)
    - [`portx start`](#portx-start)
    - [`portx setup` / `login`](#portx-setup--login)
    - [`portx list` / `stop`](#portx-list--stop)
    - [Other commands](#other-commands)
  - [Security and limitations](#security-and-limitations)
  - [Troubleshooting](#troubleshooting)
  - [License](#license)

## Install

```bash
brew tap jordanliu/portx
brew install portx
brew install cloudflared
```

Or run the installer script:

```bash
curl -fsSL https://raw.githubusercontent.com/jordanliu/portx/main/scripts/install.sh | bash
```

The script taps `jordanliu/portx` and installs PortX with its `cloudflared`
dependency. It requires [Homebrew](https://brew.sh).

PortX uses the [`cloudflared`](https://developers.cloudflare.com/cloudflare-one/connections/connect-apps/install-and-setup/installation/) binary on your `PATH`. It does not download or embed it.

Build from source with Go 1.26.5 or later:

```bash
git clone https://github.com/jordanliu/portx.git
cd portx
make build
# → ./bin/portx
```

PortX tests on Linux, macOS, and Windows in CI. PortX does not download or embed `cloudflared`; install a current Cloudflare-supported version separately and confirm it is available with:

```bash
portx cloudflared version
```

## Quick tunnels

Create a temporary public URL without a Cloudflare account:

```bash
portx http 3000
portx http localhost:3000
portx http 3000 --json
```

Quick tunnels use \*.trycloudflare.com and remain active until you terminate the process.

They are intended for temporary sharing. They have no SLA, do not support SSE, and are subject to Cloudflare concurrency limits.

## Custom hostnames

Managed mode gives you stable hostnames on your own Cloudflare zone, such as `api.example.dev`.

You need:

- An active Cloudflare zone
- `cloudflared` on your `PATH`
- Browser login or a custom user API token with:
  - Account → Cloudflare Tunnel → Edit
  - Account → Account Settings → Read
  - Zone → DNS → Edit
  - Zone → Zone → Read

When creating the token, scope **Account Resources** to the account that will own
the tunnel and **Zone Resources** to the zone PortX will configure. PortX first
lists the accounts and zones visible to the token, then creates the tunnel and
wildcard DNS record. An active token with no selected account resources will
verify successfully but will return no accounts.

Run the one-time setup:

```bash
portx setup
```

PortX authenticates with Cloudflare, creates a named tunnel, configures your namespace, adds wildcard DNS, and stores credentials in the OS keychain.

Start a managed route:

```bash
# hostname from the git repo / current folder name
portx http 3000 --url
# e.g. repo "payments" → https://payments.example.dev

# explicit short label
portx http 3000 --url=api
# → https://api.example.dev

# full hostname
portx http 3000 --url=api.example.dev
```

`portx http 3000` (no `--url`) is still a quick tunnel.

Multiple routes can share the same managed tunnel:

```bash
# Terminal 1
portx http 3000 --url=api

# Terminal 2
portx http 5173 --url=web
```

### Multiple domains and profiles

A profile is connected to one Cloudflare zone and one wildcard namespace. Use
multiple hostnames under that namespace freely, for example
`api.example.dev`, `web.example.dev`, and `hooks.example.dev`.

For separate domains or Cloudflare accounts, create a profile for each one:

```bash
portx --profile personal setup  # *.example.dev
portx --profile work setup      # *.example.net

portx --profile work http 3000 --url=api
# → https://api.example.net
```

The managed daemon currently serves one profile at a time. Stop it before
switching profiles:

```bash
portx daemon stop
portx --profile work http 3000 --url=api
```

If a hostname is already active, use `--replace`, `--reuse`, or stop the existing route:

```bash
portx list
portx stop api.example.dev
portx http --url=api 3000 --replace
```

Prefer `*.example.dev` over `*.proxy.example.dev`. On a standard full-zone setup, [Cloudflare Universal SSL](https://developers.cloudflare.com/ssl/edge-certificates/universal-ssl/limitations/) covers the zone apex and first-level subdomains, but not deeper hostnames such as `api.proxy.example.dev` without additional certificate configuration.

## PortX vs. cloudflared

PortX uses `cloudflared` under the hood. It does not replace Cloudflare Tunnel or implement its own tunneling protocol.

`cloudflared` provides the tunnel. PortX manages the local development workflow around it.

|                         | cloudflared                          | PortX                                          |
| ----------------------- | ------------------------------------ | ---------------------------------------------- |
| Quick temporary tunnel  | Yes                                  | Yes                                            |
| Stable custom hostname  | Manual tunnel and DNS setup          | One-time guided setup                          |
| Multiple local routes   | Configure ingress rules              | Start routes with separate commands            |
| Project configuration   | Maintain `cloudflared` configuration | Commit `portx.yaml` route definitions          |
| DNS provisioning        | Configure separately                 | Managed during setup                           |
| Credentials             | Manage directly                      | Stored in the OS keychain                      |
| Active route management | Manage processes and configuration   | `list`, `stop`, `replace`, and `reuse`         |
| Local diagnostics       | Cloudflare-focused tooling           | Checks config, DNS, tunnel, daemon, and origin |

Use `cloudflared` directly when you want full control over Cloudflare Tunnel configuration.

Use PortX when you want stable development URLs and repeatable project routes without managing the underlying tunnel and DNS configuration yourself.

## Project routes

Add a `portx.yaml` file to make a project's development hostnames repeatable and shareable.

The file contains only named route definitions. Tunnel provisioning, DNS, and credentials remain part of your local PortX profile.

Project routes are available only in managed mode.

```yaml
# portx.yaml
version: 1

routes:
  api:
    target: http://127.0.0.1:3000
    hostname: api.example.dev

  web:
    target: http://127.0.0.1:5173
    hostname: web.example.dev
```

Start a route and save it to `portx.yaml`:

```bash
portx http --url=api 3000 --save --name api
```

Start every route in the project:

```bash
portx start
```

Start one named route:

```bash
portx start --only api
```

Use a different project file:

```bash
portx start --file ./ops/portx.yaml
```

PortX walks up from the current directory until it finds `portx.yaml`.

## Commands

| Command         | Description                                                    |
| --------------- | -------------------------------------------------------------- |
| `portx http`    | Expose one local origin                                        |
| `portx start`   | Start routes from `portx.yaml`                                 |
| `portx setup`   | Authenticate and provision managed-mode resources              |
| `portx login`   | Authenticate with Cloudflare                                   |
| `portx list`    | List active routes                                             |
| `portx stop`    | Stop an active route                                           |
| `portx doctor`  | Diagnose configuration, tunnel, DNS, daemon, and origin state  |
| `portx cleanup` | Remove local runtime state                                     |
| `portx reset`   | Remove PortX-owned Cloudflare resources and local profile data |
| `portx version` | Print the PortX version and build information                  |

Run the built-in help for any command:

```bash
portx <command> --help
```

### `portx http <target>`

Expose one local HTTP origin.

Without `--url`, PortX starts a quick tunnel. With `--url`, it starts a managed route using your configured Cloudflare zone.

```bash
portx http 3000
portx http localhost:3000
portx http --url=api 3000
portx http --url=api 3000 --save --name api
portx http 3000 --json
```

| Flag                     | Description                                                          |
| ------------------------ | -------------------------------------------------------------------- |
| `--url`                  | Managed mode. No value → repo/folder name; `api` → `api.<namespace>` |
| `--save`                 | Save the managed route to `portx.yaml`                               |
| `--name`                 | Name used when saving the route                                      |
| `--json`                 | Print status as JSON and keep the session open until terminated      |
| `--replace`, `--reuse`   | Control what happens when the hostname is already leased             |
| `--host-header`          | Override the `Host` header sent to the local origin                  |
| `--scheme`               | Use `http` or `https` for the local origin                           |
| `--no-origin-check`      | Skip the check for a listening local service                         |
| `--insecure-skip-verify` | Allow an HTTPS origin with an invalid certificate                    |

Saving a route requires `--url`, `--save`, and `--name`.

### `portx start`

Start routes defined in `portx.yaml`.

Managed mode is required.

```bash
portx start
portx start --only api
portx start --file ./ops/portx.yaml
portx start --json
```

Additional flags include `--only`, `--no-origin-check`, and `--replace`.

### `portx setup` / `login`

`setup` runs the complete managed-mode provisioning flow:

```bash
portx setup
portx setup --token
```

`login` only authenticates with Cloudflare and stores the API token. Run `setup` before starting managed routes.

```bash
portx login
portx login --force  # ignore an existing certificate and log in again
```

Use profiles to maintain separate environments or Cloudflare accounts:

```bash
portx --profile work setup
```

### `portx list` / `stop`

List active routes:

```bash
portx list
```

Stop a route by hostname:

```bash
portx stop api.example.dev
```

You can also stop a route using a lease ID prefix:

```bash
portx stop <lease-id-prefix>
```

### Other commands

| Command                     | Description                                                                 |
| --------------------------- | --------------------------------------------------------------------------- |
| `portx doctor`              | Check configuration, credentials, tunnel, DNS, daemon, and origin state     |
| `portx config show`         | Display global and project configuration                                    |
| `portx config validate`     | Validate global and project configuration                                   |
| `portx cleanup [--force]`   | Remove local socket, PID, and lease files                                   |
| `portx reset [--yes]`       | Delete PortX-owned tunnel and DNS resources, then remove local profile data |
| `portx daemon status`       | Show the status of the local managed-mode daemon                            |
| `portx daemon stop`         | Stop the local daemon                                                       |
| `portx cloudflared version` | Show the resolved `cloudflared` binary and version                          |
| `portx cloudflared install` | Show installation guidance for `cloudflared`                                |
| `portx cloudflared update`  | Show upgrade guidance for `cloudflared`                                     |

The daemon starts automatically when managed mode needs it.

Reset options:

```bash
portx reset --yes                 # skip confirmation
portx reset --remote-only         # remove remote resources only
portx reset --keep-local          # remove remote resources, keep local data
```

## Security and limitations

- **Public access.** Anyone with the URL can reach your origin while the route is running. PortX does not add authentication. Use Cloudflare Access when access control is required.
- **Local dependency.** PortX uses the `cloudflared` binary installed on your system.
- **Credentials.** API and tunnel tokens are stored in the OS keychain, not in project configuration files.
- **Credential fallback.** `PORTX_CREDENTIALS_FILE=1` enables a local plaintext credential store only when the OS credential service is unavailable. Protect the resulting file and do not use this mode on shared machines or in committed automation.
- **Origin exposure.** Treat every configured target as internet-facing while its route is active. Target validation is defense-in-depth, not a replacement for host firewall rules or application authentication.
- **TLS override.** `--insecure-skip-verify` disables certificate verification between PortX and an HTTPS origin. Use it only for controlled local development.
- **Managed proxy.** Managed mode binds its local proxy to `127.0.0.1:4041` by default.
- **Streaming.** SSE and streaming require managed mode.
- **Quick tunnels.** Quick tunnels have no SLA and are subject to Cloudflare concurrency limits.
- **Platforms.** macOS and Linux are the best-tested platforms. Windows support is less mature.

Configuration and runtime files use the standard OS application-support or XDG paths.

Inspect the resolved paths and configuration:

```bash
portx config show
```

## Troubleshooting

Start with:

```bash
portx doctor
```

For additional detail:

```bash
portx --log-level debug http 3000
```

| Problem                                        | Try                                                                        |
| ---------------------------------------------- | -------------------------------------------------------------------------- |
| `cloudflared not found`                        | Install it with `brew install cloudflared` and verify it is on your `PATH` |
| Origin connection errors                       | Confirm the local app is listening, or use `--no-origin-check`             |
| Command says setup is required                 | Run `portx setup`                                                          |
| Hostname is already active                     | Run `portx list`, then use `--replace`, `--reuse`, or `portx stop …`       |
| SSL errors on a multi-level hostname           | Prefer `*.zone` instead of `*.sub.zone`                                    |
| Tunnel was deleted in the Cloudflare dashboard | Run `portx setup` again                                                    |
| Unexpected local state                         | Run `portx doctor`, then `portx cleanup --force`                           |

Use `portx cleanup --force` only when diagnostics indicate stale local runtime state.

## License

[MIT](LICENSE).

Cloudflare names and marks are trademarks of Cloudflare, Inc. This project is not affiliated with or endorsed by Cloudflare.
