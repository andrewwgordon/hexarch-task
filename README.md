[![CI](https://github.com/andrewwgordon/hexarch-task/actions/workflows/ci.yml/badge.svg)](https://github.com/andrewwgordon/hexarch-task/actions/workflows/ci.yml)
# Hexagonal Task Manager (`hexarch`)

A **task management application** written in Go, built around a
**hexagonal (ports & adapters) architecture** following a **domain-driven design
(DDD)** repository pattern. It ships with three interfaces on the same core:
- a **command-line** adapter (`cli`),
- a **REST API** adapter (`httpapi`) using Gin, and
- a **server-rendered web UI** adapter (`httpweb`) using Gin, Go `html/template`, HTMX, and DaisyUI.

The core business logic is completely independent of the storage engine and the
user-facing interface. Data is persisted to a **SQLite** database by default, and
an **in-memory** repository is provided so the storage backend can be swapped
without touching the application or domain layers.

Built with assistance from Pi.dev and DeepSeek v4 Flash 0731.

---

## Why a Hexagon?

The project is organised into the classic three rings of a hexagon:

```
        ┌──────────────────────────────────────────────────────────┐
        │                   Adapters (inbound)                     │
        │  cli  │  httpapi (REST JSON)  │  httpweb (HTMX Web UI)   │
        └────────────────────────────┬─────────────────────────────┘
                                     │ calls
                                     ▼
        ┌──────────────────────────────────────────────────────────┐
        │         Application (use cases / service)                │
        │         idgen, clock, orchestration                      │
        └────────────────────────────┬─────────────────────────────┘
                                     │ depends on port (interface)
                                     ▼
        ┌──────────────────────────────────────────────────────────┐
        │                  Domain (pure core)                      │
        │         business rules, invariants, errors               │
        └────────────────────────────┬─────────────────────────────┘
                                     │
                                     │ outbound port: TaskRepository
                                     ▼
        ┌──────────────────────────────────────────────────────────┐
        │        Repositories (outbound adapters)                  │
        │        implemented in separate packages                  │
        └──────────────────────────────────────────────────────────┘
```

- **Domain** (`internal/domain`) – Pure business logic. `Task` is an immutable
  value object with a guarded status **state machine** and validation rules. It
  knows nothing about databases, terminals, or networks.
- **Application** (`internal/application`) – Use cases and orchestration. The
  `TaskService` interface is the **inbound port**; it depends only on the
  `repository.TaskRepository` interface (the **outbound port**), never on a
  concrete storage engine.
- **Adapters** (`internal/adapters`) – inbound drivers implementing the shared
  `Adapter` interface and composing `AppBase`:
  - `cli` (`internal/adapters/cli`) parses arguments, invokes the service, and formats output.
  - `httpapi` (`internal/adapters/httpapi`) exposes a JSON REST API over Gin.
  - `httpweb` (`internal/adapters/httpweb`) serves HTML views styled with DaisyUI & Tailwind, powered by HTMX.
- **Repositories** (`internal/repository`) – outbound adapters governed by a
  **Provider registry**. Each backend package implements `repository.Provider`
  and **self-registers** via `repository.Register` in its `init()`. The
  **factory** (`repository.New`) simply looks up `HEXARCH_DB_TYPE` in the
  registry and delegates `Open` to the matching provider, which owns connection
  pooling, ping, and idempotent schema setup. Five built-in backends are
  available: `sqlite` (default), `postgres`, `oracle`, `mongodb`, and `memory`.
  The three relational stores share a single `TaskRepositorySQL` core in
  `repository/sqldb`, parameterized by a small `Dialect` interface so SQL
  variance stays behind one seam. MongoDB is a completely separate document-store
  adapter, proving the port works across storage paradigms.

### What this buys you

- **Testability** – the application can be driven against the in-memory repository
  with deterministic IDs and a fake clock (see `internal/application/task_service_impl.go`).
- **Swap-ability** – switching persistence layers means registering a new
  `repository.Provider`; the domain and application layers never change. The
  shared conformance suite guarantees every backend behaves identically.
- **Clear boundaries** – domain errors are typed (`NOT_FOUND`, `INVALID_ARGUMENT`,
  `CONFLICT`, `STORAGE`) so higher layers can react without reaching into details.

### Swappable Persistence Layer

The repository layer is designed as an **open registry** of storage backends:

1. **Provider interface** – each backend implements `repository.Provider` with a
   single method `Open(ctx, uri) (TaskRepository, io.Closer, error)`. The provider
   owns every resource needed by that backend (connection pool, client, file handle)
   and returns it as an `io.Closer` for the composition root to clean up.
2. **Self-registration** – backend packages register themselves via `init()`:
   ```go
   func init() { repository.Register(repository.TypeSQLite, provider{}) }
   ```
   Importing a backend with a blank identifier (`_ "hexarch/internal/repository/postgres"`)
   is all that is required to make it selectable at runtime.
3. **Factory dispatch** – `repository.New(ctx, cfg)` validates the configuration,
   looks up the requested `DBType` in the registry, and calls the provider's `Open`.
   There is no `switch` on database kinds inside the factory; new backends are added
   without modifying `factory.go`.
4. **Conformance contract** – `internal/repository/conformance` holds a
   backend-agnostic test suite that exercises every method of the
   `TaskRepository` port. Any new provider must pass this suite, ensuring that
   swapping backends never changes application behavior.

---

## Project Layout

```
hex-go/
├── cmd/
│   └── main.go                       # Composition root: wires db, repo, service, and adapters
├── internal/
│   ├── domain/                       # Core business logic (pure)
│   │   ├── task.go                   #   Task entity + state machine + factories
│   │   ├── errors.go                 #   Typed domain errors
│   │   └── domain_test.go            #   Domain state machine & validation tests (domain_test pkg)
│   ├── application/                  # Use-case layer
│   │   ├── task_service.go           #   Inbound port (TaskService interface) + input/DTO types
│   │   ├── task_service_impl.go      #   Concrete use cases, DI for ID/clock
│   │   ├── idgen.go                  #   UUIDv4-style ID generator
│   │   └── service_test.go           #   Application use-case tests (application_test pkg)
│   ├── adapters/                     # Inbound adapters + shared HTTP helpers
│   │   ├── adapter.go                #   Shared Adapter interface + AppBase struct
│   │   ├── httpconv/                 #   Shared parsing & error mapping for HTTP adapters
│   │   │   └── httpconv.go           #     Deadline/status parsing, domain.Kind → HTTP status
│   │   ├── cli/                      #   CLI adapter
│   │   │   ├── cli.go                #     Cli struct + subcommands
│   │   │   ├── args.go               #     Minimal `--flag value` parser
│   │   ├── httpapi/                  #   REST API adapter (Gin JSON)
│   │   │   ├── router.go             #     NewRouter(svc) → *gin.Engine (/api group)
│   │   │   ├── handlers.go           #     /api route handlers
│   │   │   ├── handlers_test.go      #     REST API handler tests (httpapi_test pkg)
│   │   │   ├── dto.go                #     JSON DTOs + parse helpers + response builders
│   │   │   ├── errors.go             #     domain.Kind → HTTP status mapping
│   │   │   └── server.go             #     Api struct: implements Adapter
│   │   └── httpweb/                  #   Web UI adapter (Gin + html/template + HTMX)
│   │       ├── router.go             #     NewRouter(svc) → *gin.Engine (/app group)
│   │       ├── handlers.go           #     Web route handlers
│   │       ├── handlers_test.go      #     Web UI handler tests (httpweb_test pkg)
│   │       ├── views.go              #     Template view structs & presentation helpers
│   │       ├── parse.go              #     Query-string filter parser
│   │       ├── errors.go             #     HTML error fragment rendering
│   │       ├── server.go             #     Web struct: implements Adapter
│   │       └── templates/            #     HTML templates (DaisyUI + Tailwind CDN + HTMX)
│   │           ├── index.html
│   │           └── partials/         #     HTMX fragments
│   │               ├── error_alert.html
│   │               ├── modal_create.html
│   │               ├── modal_delete.html
│   │               ├── modal_edit.html
│   │               ├── mutation.html
│   │               ├── stats.html
│   │               ├── task_list.html
│   │               └── task_row.html
│   └── repository/                   # Outbound port + Provider registry + factory + adapters
│       ├── task_repo.go              #   TaskRepository interface + TaskFilter
│       ├── config.go                 #   DBType, Config, ConfigFromEnv()
│       ├── factory.go                #   New(ctx, cfg) + backend registry
│       ├── conformance/              #   Backend-agnostic contract suite
│       │   └── conformance.go
│       ├── sqldb/                    #   Shared SQL adapter + Dialect abstraction
│       │   ├── dialect.go            #     SQLite / Postgres / Oracle dialects
│       │   └── repo.go               #     TaskRepositorySQL(db, Dialect)
│       ├── sqlite/                   #   SQLite backend (default, pure-Go)
│       ├── postgres/                 #   Postgres backend (pgx)
│       ├── oracle/                   #   Oracle 12c+ backend (go-ora)
│       ├── mongo/                    #   MongoDB document backend
│       └── memory/                   #   In-memory backend (tests / swap demo)
├── test/                             # End-to-end integration tests
│   └── integration_test.go           #   Real-socket server test (integration_test pkg)
├── go.mod / go.sum
```

### Dependency direction

Each layer only knows about the layers **inside** it. `cmd` composes the
repositories, service, and adapter; `adapters` and `repository` never know about
each other; `application` depends only on the `domain` and the `repository`
interface.

### Test strategy

Tests use **external test packages** (e.g., `domain_test`, `httpapi_test`) so they
exercise only exported APIs, preventing brittle coupling to internal details:

| File | Package | What it covers |
|------|---------|---------------|
| `internal/repository/conformance/conformance.go` | `sqlite_test` / `memory_test` | Backend-agnostic `TaskRepository` contract suite |
| `internal/domain/domain_test.go` | `domain_test` | Entity creation, status transitions, validation |
| `internal/application/service_test.go` | `application_test` | Use cases with in-memory repo + deterministic ID/clock |
| `internal/adapters/httpapi/handlers_test.go` | `httpapi_test` | REST API endpoints, error mapping, parity with CLI |
| `internal/adapters/httpweb/handlers_test.go` | `httpweb_test` | Web UI endpoints, HTMX fragments, error mapping |
| `test/integration_test.go` | `integration_test` | End-to-end over real TCP + SQLite |

---

## Features

- **Create** tasks with title, optional description, priority (0–5), and deadline.
- **List** tasks with filtering by status, full-text search, and paging
  (ordered by priority desc, then created date desc).
- **Inspect** a single task by ID.
- **Rename**, **re-prioritize**, and **set/clear deadlines**.
- **Lifecycle** management through a validated state machine:
  `todo → in_progress → done → archived`.
- **Delete** tasks.
- **Stats** — counts per status.

All core features are available through the **CLI** (`hexarch cli`), the
**REST API** (`hexarch httpapi`), and the **interactive Web UI** (`hexarch httpweb`).

---

## Requirements

- [Go](https://go.dev/dl/) **1.27+** (per `go.mod`).

No `CGO` compiler is required: the app uses the pure-Go driver
[`modernc.org/sqlite`](https://pkg.go.dev/modernc.org/sqlite).

The Web UI loads DaisyUI v5 and Tailwind CSS v4 directly from a CDN — no
Node.js or CSS build pipeline is needed.

---

## Installation

```bash
# build the binary (hexarch.exe on Windows, hexarch elsewhere)
go build -o hexarch ./cmd/main.go
```

To run without building:

```bash
go run ./cmd/main.go <command> ...
```

---

## Running the application

Invoke the binary with the adapter name as the first argument:

```
Usage:
  ./hexarch cli <subcommand> [options]
  ./hexarch httpapi [ip:port]
  ./hexarch httpweb [ip:port]
```

> On Windows this is `hexarch.exe cli ...`.

### Environment variables

| Variable            | Default             | Description                                     |
|---------------------|---------------------|-------------------------------------------------|
| `HEXARCH_DB_TYPE`   | `sqlite`            | Storage backend: `sqlite`, `postgres`, `oracle`, `mongodb`, or `memory`. |
| `HEXARCH_DB_URI`    | `tasks.db`          | Backend-specific connection string (see below). |
| `HEXARCH_DB_PATH`   | *(legacy)*          | Old SQLite-only variable; used as the URI when the backend is `sqlite` and `HEXARCH_DB_URI` is unset. |
| `HEXARCH_HTTP_ADDR` | `:8080` / `:8081`   | Listen address for `httpapi` (`:8080`) or `httpweb` (`:8081`). |

Backends are resolved by the **repository factory** (`repository.New`). The
factory looks up the registered `repository.Provider` for the requested type and
delegates connection, ping, and idempotent schema setup to that provider, which
returns a closer that the composition root owns:

| `HEXARCH_DB_TYPE` | `HEXARCH_DB_URI` example                                    |
|-------------------|---------------------------------------------------------------|
| `sqlite`          | `tasks.db` or `file:tasks.db?mode=rwc`                       |
| `postgres`        | `postgres://user:pass@host:5432/hexarch?sslmode=disable`     |
| `oracle`          | `oracle://user:pass@host:1521/service`                       |
| `mongodb`         | `mongodb://user:pass@host:27017/hexarch?authSource=admin`    |
| `memory`          | *(unused)*                                                  |

```bash
./hexarch httpapi                 # default: SQLite at ./tasks.db
HEXARCH_DB_TYPE=memory ./hexarch httpapi   # ephemeral, no persistence
HEXARCH_DB_TYPE=postgres HEXARCH_DB_URI=postgres://... ./hexarch httpapi
```

> **Note:** the conformance contract suite currently runs against `sqlite` and
> `memory` in CI. The `postgres`, `oracle`, and `mongodb` backends are fully
> implemented and registered with the factory, but are not yet exercised in
> automated CI until those databases are available.

---

### CLI adapter

#### Subcommands

```
hexarch cli help                                        # show usage

hexarch cli create --title "Fix login bug" --desc "oAuth redirect" --priority 4 --deadline 2026-09-01
hexarch cli list                                        # all tasks
hexarch cli list --status in_progress                   # filter by status
hexarch cli list --search "login"                       # full-text search
hexarch cli list --limit 20 --offset 40                 # paging

hexarch cli get <id>                                    # show a task
hexarch cli rename <id> "New title"
hexarch cli priority <id> 5
hexarch cli start <id>       # todo -> in_progress
hexarch cli done  <id>       # in_progress -> done
hexarch cli deadline <id> 2026-10-15
hexarch cli deadline <id> done      # clear the deadline
hexarch cli remove <id>
hexarch cli stats                # per-status counts
```

#### Worked example

```bash
$ hexarch cli create --title "Ship v1" --priority 5
created <uuid>: Ship v1 (todo)

$ hexarch cli list
<uuid>                 Ship v1                p5  todo

$ hexarch cli start <uuid>
<uuid> is now in_progress

$ hexarch cli done <uuid>
<uuid> is now done

$ hexarch cli stats
total:        1
todo:         0
in progress:  0
done:         1
archived:     0
```

#### Status lifecycle

Legal transitions are enforced by the domain:

```
todo ────────▶ in_progress ────────▶ done ────────▶ archived
                     ▲
                     └── (back to todo is allowed)
```

Attempting an illegal jump (e.g. `todo → done`) returns a `CONFLICT` error.

---

### HTTP REST API (`httpapi`)

Start the REST API server. Address resolution: CLI argument → `HEXARCH_HTTP_ADDR` env → `:8080`.

```bash
./hexarch httpapi                     # listens on :8080
HEXARCH_HTTP_ADDR=:9000 ./hexarch httpapi       # env override
./hexarch httpapi 127.0.0.1:9001      # argument override
```

#### Endpoints

| Method | Path             | Description                                                         | CLI equivalent |
|--------|------------------|---------------------------------------------------------------------|----------------|
| POST   | `/api/tasks`     | Create a task (body: `title`, `description`, `priority`, `deadline`)| `create`       |
| GET    | `/api/tasks`     | List tasks (`?status=&search=&limit=&offset=`)                      | `list`         |
| GET    | `/api/tasks/:id` | Get a task by id                                                    | `get`          |
| PATCH  | `/api/tasks/:id` | Update `title` / `status` / `priority` / `deadline`                 | `rename`, `priority`, `start`, `done`, `deadline` |
| DELETE | `/api/tasks/:id` | Delete a task                                                       | `remove`       |
| GET    | `/api/stats`     | Per-status counts                                                   | `stats`        |

#### Examples (`curl`)

```bash
# create
curl -s -X POST http://localhost:8080/api/tasks \
  -H 'Content-Type: application/json' \
  -d '{"title":"Ship v1","priority":5,"deadline":"2026-12-31"}'

# list all
curl -s 'http://localhost:8080/api/tasks'

# filter + paging
curl -s 'http://localhost:8080/api/tasks?status=done&limit=10&offset=0'

# full-text search
curl -s 'http://localhost:8080/api/tasks?search=ship'

# get one
curl -s http://localhost:8080/api/tasks/<id>

# rename
curl -s -X PATCH http://localhost:8080/api/tasks/<id> \
  -H 'Content-Type: application/json' -d '{"title":"Ship v2"}'

# status transitions (start / done)
curl -s -X PATCH http://localhost:8080/api/tasks/<id> \
  -H 'Content-Type: application/json' -d '{"status":"in_progress"}'
curl -s -X PATCH http://localhost:8080/api/tasks/<id> \
  -H 'Content-Type: application/json' -d '{"status":"done"}'

# set / clear a deadline
curl -s -X PATCH http://localhost:8080/api/tasks/<id> \
  -H 'Content-Type: application/json' -d '{"deadline":"2026-10-15"}'
curl -s -X PATCH http://localhost:8080/api/tasks/<id> \
  -H 'Content-Type: application/json' -d '{"deadline":null}'

# delete
curl -s -X DELETE http://localhost:8080/api/tasks/<id>

# stats
curl -s http://localhost:8080/api/stats
```

`PATCH /api/tasks/:id` may include any subset of `title`, `status`, `priority`,
and `deadline` in one request. A `deadline` of `null` clears it.

#### Response shapes

**Task:**
```json
{
  "id": "f9760f68-…",
  "title": "Ship v1",
  "description": "",
  "status": "todo",
  "priority": 5,
  "deadline": "2026-12-31",
  "created_at": "2026-08-26T21:41:34+01:00",
  "updated_at": "2026-08-26T21:41:34+01:00"
}
```

**List:** `{ "tasks": [ … ], "count": n }`

**Stats:** `{ "total": n, "todo": n, "in_progress": n, "done": n, "archived": n }`

**Error:**
```json
{ "error": { "code": "NOT_FOUND", "message": "task <id> not found" } }
```

| `code`             | HTTP status |
|--------------------|-------------|
| `INVALID_ARGUMENT` | `400`       |
| `NOT_FOUND`        | `404`       |
| `CONFLICT`         | `409`       |
| `STORAGE`          | `503`       |

---

### Web UI adapter (`httpweb`)

Start the server-rendered web application. Address resolution: CLI argument →
`HEXARCH_HTTP_ADDR` env → `:8081`.

```bash
./hexarch httpweb                     # listens on :8081
HEXARCH_HTTP_ADDR=:9002 ./hexarch httpweb       # env override
./hexarch httpweb 127.0.0.1:9002      # argument override
```

Open `http://localhost:8081/app/` in your browser. The UI renders via Go
`html/template` styled with DaisyUI v5 & Tailwind CSS v4 (loaded from CDN),
interacting dynamically via HTMX.

#### Web UI features

<img src="assets/web-ui.png" width="800" height="600">

- **Stats dashboard** — live radial-progress “Done” meter plus clickable counters
  per status that double as filters.
- **Search & filter** — real-time full-text search with 300 ms debounce,
  status dropdown filter, and a clear-filters action; all pushed to the URL
  for shareable links.
- **Task list** — paginated (20 per page, Prev / Next), sorted by priority then
  creation date, with two empty states: “No tasks yet” and “No tasks match
  the current filters.”
- **Inline status transitions** — each row shows only the legal next actions
  (e.g. **Start** for *todo*, **Complete** / **Back to to do** for
  *in_progress*, **Archive** for *done*). Illegal transitions are blocked by
  the domain and surface as inline error alerts.
- **Create task** — modal dialog with title, description, priority (0–5), and
  optional deadline.
- **Edit task** — server-prefetched modal with current values; supports renaming,
  re-prioritising, setting/clearing deadline.
- **Delete task** — confirmation modal prefilled with the task title.
- **Toast notifications** — success toasts auto-dismiss after 4 s; error toasts
  appear inline and replace the relevant region.
- **Dual-render architecture** — full-page shells for direct browser loads,
  HTML fragment swaps for HTMX requests, with `Vary: HX-Request` to prevent
  CDN/proxy collisions.
- **Graceful degradation** — every mutation falls back to a PRG redirect when
  JavaScript is absent.

#### Web routes

| Method | Path | Description |
|--------|------|-------------|
| GET | `/app/` | Full page shell (list + stats + filters) |
| GET | `/app/tasks` | Task list fragment (search, status, paging) |
| POST | `/app/tasks` | Create a task |
| GET | `/app/tasks/:id/edit` | Edit modal fragment |
| PATCH | `/app/tasks/:id` | Update title / priority / deadline |
| POST | `/app/tasks/:id/status` | Change status (transition) |
| GET | `/app/tasks/:id/delete` | Delete confirmation modal fragment |
| DELETE | `/app/tasks/:id` | Delete a task |
| GET | `/app/stats` | Stats fragment |
| GET | `/app/partials/empty` | Empty response (toast auto-dismiss backing endpoint) |

---

## Running the tests

```bash
go test ./...
```

The suite covers:

- **Domain** (`internal/domain/domain_test.go`) — state machine and validation.
- **Application** (`internal/application/service_test.go`) — use cases with
  in-memory repo, deterministic ID generator, and fake clock.
- **REST API** (`internal/adapters/httpapi/handlers_test.go`) — handler parity,
  error mapping, pagination, filters.
- **Web UI** (`internal/adapters/httpweb/handlers_test.go`) — handler parity,
  HTMX fragments, error mapping.
- **Integration** (`test/integration_test.go`) — real-socket server over SQLite.

All tests use **external test packages** so they exercise only the public API
(e.g. `domain_test`, `httpapi_test`).
