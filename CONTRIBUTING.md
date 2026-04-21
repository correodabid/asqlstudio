# Contributing to ASQL Studio

Thanks for considering a contribution. ASQL Studio is open-source under
[Apache 2.0](LICENSE) and welcomes bug reports, bug fixes, documentation
improvements, and well-scoped feature proposals.

## Before your first PR

1. Sign the [Contributor License Agreement](CLA.md). The CLA bot posts
   a comment on your first PR with a one-click link, or comment:
   `I have read the CLA Document and I hereby sign the CLA`.
2. Follow the commit format described below.
3. Run tests and linting before opening a PR.

## Ways to contribute

### Report a bug

Open an [issue](https://github.com/correodabid/asqlstudio/issues) with:

- What you expected to happen and what actually happened.
- Steps to reproduce: ASQL engine version, connection config, OS, and
  the exact action or query that triggered the problem.
- Screenshots or screen recordings are especially helpful for UI bugs.

For **security issues**, do not open a public issue — see
[SECURITY.md](SECURITY.md).

### Propose a feature

Open an issue labelled `proposal` describing:

- The use case: who hits this, when, and what makes the current UI
  inadequate.
- The smallest change that would unblock the use case.
- Whether it belongs in the desktop shell, the Go backend, or the
  React frontend.

### Fix a bug or ship a small improvement

Open a PR directly. For anything larger than ~300 lines of
non-mechanical code, open an issue first to align on the shape.

## Development setup

### Prerequisites

| Tool | Version | Install |
|---|---|---|
| Go | 1.25.9+ | https://go.dev/dl |
| Node.js | 18+ | https://nodejs.org |
| Wails CLI | v2 | `go install github.com/wailsapp/wails/v2/cmd/wails@latest` |

ASQL Studio also requires a running [ASQL](https://github.com/correodabid/asql)
engine to connect to. Follow the ASQL README to start a local instance.

### Clone and run

```bash
git clone https://github.com/correodabid/asqlstudio
cd asqlstudio

# Install frontend dependencies
cd webapp && npm install && cd ..

# Run in development mode (hot-reload frontend + Go backend)
wails dev

# Or build and run the desktop binary directly
wails build
./build/bin/asqlstudio -pgwire-endpoint 127.0.0.1:5433 -data-dir .asql
```

### Build frontend assets only

```bash
cd webapp && npm run build
```

The compiled assets land in `app/web/` and are embedded into the Go
binary at compile time. Do not edit files under `app/web/` manually.

### Environment variables

All CLI flags have environment-variable equivalents:

| Flag | Env var |
|---|---|
| `-pgwire-endpoint` | `ASQL_PGWIRE_ENDPOINT` |
| `-follower-endpoint` | `ASQL_FOLLOWER_ENDPOINT` |
| `-peer-endpoints` | `ASQL_PEER_ENDPOINTS` |
| `-admin-endpoints` | `ASQL_ADMIN_ENDPOINTS` |
| `-auth-token` | `ASQL_AUTH_TOKEN` |
| `-admin-auth-token` | `ASQL_ADMIN_AUTH_TOKEN` |
| `-data-dir` | `ASQL_DATA_DIR` |

## Tests

```bash
# Go backend tests
go test ./...

# Frontend type-check
cd webapp && npm run typecheck   # if configured

# Frontend lint
cd webapp && npm run lint
```

## Commit conventions

```
<type>(<scope>): <imperative summary>

<why, not what — the diff shows what>
```

- **types**: `feat`, `fix`, `chore`, `docs`, `refactor`, `style`, `test`
- **scopes**: `ui`, `backend`, `connection`, `assistant`, `security`,
  `schema`, `explain`, `deps`, `ci`
- Subject line under 72 chars, imperative ("add" not "added").
- Body explains *why* the change is correct, not what it does.

## What NOT to do

- Don't commit generated files under `app/web/` — they are
  rebuilt from `webapp/src/`.
- Don't commit `webapp/node_modules/`, `.asql/`, or built binaries.
- Don't add emojis to source files, commit messages, or documentation
  unless a maintainer asks for them.
- Don't `--force-push` without an explicit request from a maintainer.
- Don't skip pre-commit hooks (`--no-verify`).

## Code of Conduct

Participation in ASQL Studio is governed by the
[Contributor Covenant Code of Conduct](CODE_OF_CONDUCT.md). By
contributing you agree to abide by its terms.

## Licensing of your contribution

Contributions are accepted under the Apache License, Version 2.0.
See [CLA.md](CLA.md) for details.
