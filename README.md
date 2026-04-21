# ASQL Studio

ASQL Studio is the official desktop GUI for [ASQL](https://github.com/correodabid/asql),
a temporal relational database with a PostgreSQL-compatible wire protocol.
It is built with [Wails](https://wails.io) (Go backend + React/TypeScript frontend).

## Features

- **Query editor** — write and run ASQL queries with syntax highlighting,
  per-tab EXPLAIN toggle, and inline plan-tree annotation for indexed vs.
  residual predicate work.
- **Connection manager** — retarget pgwire and admin endpoints at runtime
  without relaunching, keep a recent-connection list, and save named
  connection profiles for local dev, demo clusters, and staging nodes.
- **Schema designer** — browse entities, design schemas, and apply diffs
  without writing DDL by hand.
- **Entity change stream** — build `TAIL ENTITY CHANGES` requests, inspect
  resume tokens and commit timestamps, and follow live entity transitions.
- **Security panel** — manage principals, roles, grants, password rotation,
  and safe deletion over the admin HTTP surface.
- **"Ask your data" assistant** — schema-aware query planner with optional
  LLM-guided planning (Ollama, OpenAI-compatible, or Anthropic Messages API).
  The final step is always deterministic: SQL is validated against the ASQL
  parser before the user can run it.

## Prerequisites

| Tool | Version | Install |
|---|---|---|
| Go | 1.25.9+ | https://go.dev/dl |
| Node.js | 18+ | https://nodejs.org |
| Wails CLI | v2 | `go install github.com/wailsapp/wails/v2/cmd/wails@latest` |
| ASQL engine | latest | https://github.com/correodabid/asql |

## Getting started

```bash
git clone https://github.com/correodabid/asqlstudio
cd asqlstudio
cd webapp && npm install && cd ..

# Development mode — hot-reload frontend + Go backend
wails dev

# Or build a native desktop binary
wails build
./build/bin/asqlstudio -pgwire-endpoint 127.0.0.1:5433 -data-dir .asql
```

## CLI flags

| Flag | Env var | Default | Description |
|---|---|---|---|
| `-pgwire-endpoint` | `ASQL_PGWIRE_ENDPOINT` | `127.0.0.1:5433` | ASQL pgwire address |
| `-auth-token` | `ASQL_AUTH_TOKEN` | — | Password for pgwire auth |
| `-follower-endpoint` | `ASQL_FOLLOWER_ENDPOINT` | — | Optional follower for lag view |
| `-peer-endpoints` | `ASQL_PEER_ENDPOINTS` | — | Comma-separated cluster nodes |
| `-admin-endpoints` | `ASQL_ADMIN_ENDPOINTS` | — | Comma-separated admin HTTP endpoints |
| `-admin-auth-token` | `ASQL_ADMIN_AUTH_TOKEN` | — | Bearer token for admin endpoints |
| `-data-dir` | `ASQL_DATA_DIR` | `.asql` | Local data directory for recovery |
| `-groups` | `ASQL_GROUPS` | — | Comma-separated domain groups for HA panel |

## LLM assistant configuration

Provider metadata lives in [`app/assistant_llm_catalog.json`](app/assistant_llm_catalog.json).
Out of the box it includes:

- **Ollama** (local, `http://127.0.0.1:11434`)
- **OpenAI-compatible** endpoints
- **Anthropic** Messages API

Edit the catalog to add or customize providers, model lists, and defaults.

## Repository layout

```
.
├── main.go            # Entry point
├── app/               # Go backend (Wails bindings, engine client, assistant)
│   ├── web/           # Generated — do not edit; rebuilt by `npm run build`
│   └── assistant_llm_catalog.json
├── webapp/            # React/TypeScript frontend source
│   └── src/           # Canonical UI source
└── wails.json         # Wails project config
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

Apache 2.0 — see [LICENSE](LICENSE).
