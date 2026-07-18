---
name: portx-onboarding
description: Set up, build, test, configure, and troubleshoot the PortX Go project. Use when a user is new to this repository, asks how to install or run PortX, needs a quick or managed Cloudflare tunnel, wants to validate a local checkout, or needs help with project route files, profiles, cleanup, or setup failures.
---

# PortX onboarding

Help a developer get from a fresh checkout to a verified local build or a working
PortX route. Treat `README.md` and `docs/reference.md` as the user-facing source of
truth, and inspect the implementation when behavior is ambiguous.

## Choose the path

- For codebase orientation, subsystem ownership, or an explanation of request flow,
  use `$portx-reference` and read its architecture reference.
- For a source checkout, follow **Build and verify**.
- For a temporary public URL, follow **Quick tunnel**; no Cloudflare account is
  needed, but `cloudflared` must be installed and the local origin must be running.
- For a stable hostname, follow **Managed setup**. It changes Cloudflare resources
  and requires an active zone plus browser or API-token authentication.
- For repeatable project routes, follow **Project routes** after managed setup.

## Build and verify

Run commands from the repository root:

```bash
go version                 # requires Go 1.26.5 or newer
make build
./bin/portx version
make test
```

Use `make vet` for a focused static check. Use `make check` for the full repository
gate (`gofmt`, workflow lint, vet, tests, Staticcheck, and govulncheck); it may need
network access to download analysis tools. The Makefile deliberately sets
`CGO_ENABLED=0`, `GOTOOLCHAIN=local`, and read-only module flags.

When testing CLI changes, build first and invoke `./bin/portx` so the checkout is
tested instead of an unrelated installed binary. Keep unit tests adjacent to the
package they cover and run the narrowest relevant package test before `make test`.

## Quick tunnel

Start the local service, then run:

```bash
./bin/portx http 3000
# or: ./bin/portx http localhost:3000
```

PortX launches a temporary `cloudflared tunnel --url` process and prints a
`trycloudflare.com` URL. The URL is public only while the command runs. Use
`--json` for machine-readable status. Stop it with `Ctrl-C`.

If the binary is missing, check `./bin/portx cloudflared version`; install
`cloudflared` separately (Homebrew is the simplest option) and ensure it is on
`PATH`.

## Managed setup

Use this only when the user has selected a Cloudflare account and zone:

```bash
./bin/portx setup
./bin/portx doctor --verify
./bin/portx http 3000 --url=api
```

Setup authenticates, selects an account and zone, creates or reuses a PortX-owned
named tunnel, stores its token, configures the tunnel to reach PortX's local proxy,
creates or reuses the wildcard DNS record, saves the local profile, and verifies
public reachability. New DNS records can still be propagating after provisioning;
`doctor --verify` is the correct follow-up.

For non-interactive setup, prefer `CLOUDFLARE_API_TOKEN` or `setup --token`; never
put a token in command-line arguments or committed files. Use `setup --reauth` or
`login --force` when the cached browser certificate is stale. Managed mode uses
the selected profile and normally binds its local proxy to `127.0.0.1:4041`.

Profiles are local and may be selected explicitly:

```bash
./bin/portx --profile personal setup
./bin/portx --profile work http 3000 --url=api
```

The managed daemon serves one profile at a time; stop it before switching:
`./bin/portx daemon stop`.

## Project routes

Create `portx.yaml` in the repository (or use an existing one):

```yaml
version: 1
routes:
  api:
    target: http://127.0.0.1:3000
    hostname: api.example.dev
```

Then run:

```bash
./bin/portx start
./bin/portx start --only api
./bin/portx start --file ./ops/portx.yaml
```

PortX searches upward from the current directory for `portx.yaml`; pass `--file`
when running elsewhere. Project files contain route definitions only. Credentials,
tunnel IDs, and profile state remain in local user storage and should not be added
to the repository.

## Diagnose before changing state

Use this sequence for failures:

```bash
./bin/portx doctor
./bin/portx config show
./bin/portx config validate
./bin/portx --log-level debug http 3000
```

For active-route conflicts, inspect `./bin/portx list`, then use `stop`, `--reuse`,
or `--replace` deliberately. For stale local runtime records, use
`cleanup --force` only after inspecting `doctor` output. Do not run `make wipe`,
`reset --yes`, or `make wipe WIPE_REMOTE=1` during ordinary onboarding: they remove
local state, and remote cleanup can delete PortX-owned Cloudflare tunnels and DNS.

Read [operations.md](references/operations.md) for platform paths, credential
behavior, setup phases, and the troubleshooting matrix.
