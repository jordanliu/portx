# PortX

Cloudflare Tunnel, without the configuration work.

PortX is a developer-friendly wrapper around
[`cloudflared`](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/).
It gives local services temporary public URLs or stable hostnames on your own
Cloudflare zone, while handling tunnel setup, DNS, credentials, and local route
lifecycle.

```bash
# Temporary URL; no Cloudflare account required
portx http 3000
# → https://random-name.trycloudflare.com

# Stable hostname on your Cloudflare zone
portx setup
portx http 3000 --url=api
# → https://api.example.dev
```

## Contents

- [PortX](#portx)
  - [Contents](#contents)
  - [Install](#install)
    - [Homebrew](#homebrew)
    - [Build from source](#build-from-source)
  - [Quick tunnels](#quick-tunnels)
  - [Custom hostnames](#custom-hostnames)
  - [Project routes](#project-routes)
  - [PortX vs. cloudflared](#portx-vs-cloudflared)
  - [Commands](#commands)
  - [Security](#security)
  - [Additional documentation](#additional-documentation)
  - [License](#license)

## Install

### Homebrew

```bash
brew tap jordanliu/portx
brew install portx
brew install cloudflared
```

Or use the installer script:

```bash
curl -fsSL https://raw.githubusercontent.com/jordanliu/portx/main/scripts/install.sh | bash
```

### Build from source

Requires Go 1.26.5 or later:

```bash
git clone https://github.com/jordanliu/portx.git
cd portx
make build
./bin/portx version
```

## Quick tunnels

Create a temporary public URL without a Cloudflare account:

```bash
portx http 3000
portx http localhost:3000
portx http 3000 --json
```

Quick tunnels use `*.trycloudflare.com` and stay active until the process is
terminated. They are intended for temporary sharing, have no SLA, do not
support SSE, and are subject to Cloudflare concurrency limits.

## Custom hostnames

Managed mode gives you stable hostnames such as `api.example.dev`.

Requirements:

- An active Cloudflare zone.
- `cloudflared` on your `PATH`.
- Browser login, or a user API token with Cloudflare Tunnel, account, and DNS
  permissions for the account and zone PortX will configure.

Run the one-time setup:

```bash
portx setup
```

PortX authenticates with Cloudflare, creates or reuses a named tunnel, applies
the tunnel configuration, creates a wildcard DNS record, and stores credentials
in the OS credential store. Newly-created DNS records can take time to
propagate; setup provisions the profile first and public verification can be
retried with:

```bash
portx doctor --verify
```

Start routes under the namespace:

```bash
# Infer a hostname from the repository or current folder name
portx http 3000 --url

# Use a short label
portx http 3000 --url=api

# Use a full hostname
portx http 3000 --url=api.example.dev
```

Multiple services can share the managed tunnel:

```bash
portx http 3000 --url=api
portx http 5173 --url=web
```

For profiles, multi-domain behavior, hostname conflicts, and certificate
limitations, see [Profiles and multiple domains](docs/reference.md#profiles-and-multiple-domains).

## Project routes

Commit route definitions to a repository with `portx.yaml`:

```yaml
version: 1

routes:
  api:
    target: http://127.0.0.1:3000
    hostname: api.example.dev
  web:
    target: http://127.0.0.1:5173
    hostname: web.example.dev
```

Start or save routes:

```bash
portx start
portx start --only api
portx http --url=api 3000 --save --name api
portx start --file ./ops/portx.yaml
```

PortX walks up from the current directory to find `portx.yaml`. The file
contains route definitions only; credentials and tunnel provisioning remain
local to each developer’s profile.

## PortX vs. cloudflared

`cloudflared` provides the tunnel. PortX manages the repeatable local workflow
around it.

| Capability             | cloudflared                 | PortX                                          |
| ---------------------- | --------------------------- | ---------------------------------------------- |
| Temporary tunnel       | Yes                         | Yes                                            |
| Stable custom hostname | Manual tunnel and DNS setup | Guided setup                                   |
| Multiple local routes  | Maintain ingress rules      | Start routes independently                     |
| Project configuration  | Maintain cloudflared config | Commit `portx.yaml`                            |
| DNS provisioning       | Configure separately        | Managed during setup                           |
| Route lifecycle        | Manage processes directly   | `list`, `stop`, `replace`, `reuse`             |
| Diagnostics            | Cloudflare-focused          | Config, DNS, tunnel, daemon, and origin checks |

Use cloudflared directly when you need full control over tunnel configuration.
Use PortX when you want stable development URLs without managing that
configuration yourself.

## Commands

| Command                   | Purpose                                             |
| ------------------------- | --------------------------------------------------- |
| `portx http <target>`     | Expose one local origin                             |
| `portx start`             | Start routes from `portx.yaml`                      |
| `portx setup`             | Authenticate and provision managed mode             |
| `portx login`             | Authenticate with Cloudflare                        |
| `portx list` / `stop`     | Inspect or stop active routes                       |
| `portx doctor [--verify]` | Diagnose local and public setup                     |
| `portx cleanup [--force]` | Remove stale local runtime state                    |
| `portx reset [--yes]`     | Remove PortX-owned resources and local profile data |
| `portx version`           | Print build information                             |

Run `portx <command> --help` for flags. The complete command reference is in
[Commands and flags](docs/reference.md#commands-and-flags).

## Security

PortX URLs are public while routes are running and PortX does not add
application authentication. Use Cloudflare Access or application-level auth
for sensitive services. Credentials are stored in the OS credential store when
available; `PORTX_CREDENTIALS_FILE=1` enables a plaintext fallback and should
not be used on shared machines or in committed automation.

Treat every configured origin as internet-facing. `--insecure-skip-verify`
disables TLS verification only between PortX and an HTTPS origin. Read the full
[security and limitations guide](docs/reference.md#security-and-limitations) before sharing a route.

## Additional documentation

- [Commands and flags](docs/reference.md#commands-and-flags)
- [Profiles and multiple domains](docs/reference.md#profiles-and-multiple-domains)
- [Reset, cleanup, and clean setup tests](docs/reference.md#reset-cleanup-and-clean-setup-tests)
- [Security and limitations](docs/reference.md#security-and-limitations)
- [Troubleshooting](docs/reference.md#troubleshooting)

## License

[MIT](LICENSE).

Cloudflare names and marks are trademarks of Cloudflare, Inc. This project is
not affiliated with or endorsed by Cloudflare.
