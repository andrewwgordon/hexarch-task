[![CI](https://github.com/andrewwgordon/hexarch-task/actions/workflows/ci.yml/badge.svg?event=push)](https://github.com/andrewwgordon/hexarch-task/actions/workflows/ci.yml)
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

- **Domain** (`internal/domain`) – Pure business logic. `Task` and `User` are
  immutable value objects — `Task` has a guarded status **state machine** and a
  required owner, `User` carries only a bcrypt password hash — plus validation
  rules. The domain knows nothing about databases, terminals, or networks.
- **Application** (`internal/application`) – Use cases and orchestration. The
  `TaskService` interface is the **inbound port** (task use cases plus the
  user use cases: create/update/delete/list/get/auth users); it depends only on
  the `repository.TaskRepository` interface (the **outbound port**), never on a
  concrete storage engine. Password hashing and the anti-enumeration
  authentication checks live here.
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
│   │   ├── task.go                   #   Task entity + state machine + factories (owner-aware)
│   │   ├── user.go                   #   User entity + UserID + validation
│   │   ├── errors.go                 #   Typed domain errors
│   │   ├── domain_test.go            #   Domain state machine & validation tests (domain_test pkg)
│   │   └── user_test.go              #   User entity validation tests (domain_test pkg)
│   ├── application/                  # Use-case layer
│   │   ├── task_service.go           #   Inbound port (TaskService interface) + input/DTO types
│   │   ├── task_service_impl.go      #   Concrete use cases (tasks + users), DI for IDs/clock
│   │   ├── password.go               #   bcrypt hashing/verification (single place)
│   │   ├── idgen.go                  #   UUIDv4-style IDs + 256-bit API keys
│   │   └── service_test.go           #   Application use-case tests (application_test pkg)
│   ├── adapters/                     # Inbound adapters + shared HTTP helpers
│   │   ├── adapter.go                #   Shared Adapter interface + AppBase struct
│   │   ├── httpconv/                 #   Shared parsing & error mapping for HTTP adapters
│   │   │   └── httpconv.go           #     Deadline/status parsing, domain.Kind → HTTP status
│   │   ├── cli/                      #   CLI adapter
│   │   │   ├── cli.go                #     Cli struct + subcommands (auth pre-flight)
│   │   │   ├── args.go               #     Minimal `--flag value` parser
│   │   │   └── cli_test.go           #     CLI tests incl. authentication (cli_test pkg)
│   │   ├── httpapi/                  #   REST API adapter (Gin JSON)
│   │   │   ├── auth.go               #     Basic auth + X-API-Key middleware
│   │   │   ├── router.go             #     NewRouter(svc) → *gin.Engine (/api group)
│   │   │   ├── handlers.go           #     /api route handlers
│   │   │   ├── handlers_test.go      #     REST API handler tests (httpapi_test pkg)
│   │   │   ├── dto.go                #     JSON DTOs + parse helpers + response builders
│   │   │   ├── errors.go             #     domain.Kind → HTTP status mapping
│   │   │   └── server.go             #     Api struct: implements Adapter
│   │   └── httpweb/                  #   Web UI adapter (Gin + html/template + HTMX)
│   │       ├── router.go             #     NewRouter(svc) → *gin.Engine (/app group)
│   │       ├── session.go            #     HMAC-signed session cookie (userid only)
│   │       ├── csrf.go               #     CSRF token cookie + verification middleware
│   │       ├── middleware.go         #     requireAuth / requireAdmin (per-request user load)
│   │       ├── handlers.go           #     Web route handlers + login/logout
│   │       ├── user_handlers.go      #     Admin user management handlers (/app/users)
│   │       ├── handlers_test.go      #     Web UI handler tests (httpweb_test pkg)
│   │       ├── views.go              #     Template view structs & presentation helpers
│   │       ├── parse.go              #     Query-string filter parser
│   │       ├── errors.go             #     HTML error fragment rendering
│   │       ├── server.go             #     Web struct: implements Adapter
│   │       └── templates/            #     HTML templates (DaisyUI + Tailwind CDN + HTMX)
│   │           ├── index.html        #     Task page (Admin/Logout navbar links, CSRF header)
│   │           ├── login.html        #     Login page
│   │           ├── users.html        #     User Management page (admin only)
│   │           └── partials/         #     HTMX fragments
│   │               ├── error_alert.html
│   │               ├── modal_create.html
│   │               ├── modal_delete.html
│   │               ├── modal_edit.html
│   │               ├── modal_user_create.html
│   │               ├── modal_user_delete.html
│   │               ├── modal_user_edit.html
│   │               ├── mutation.html
│   │               ├── stats.html
│   │               ├── task_list.html
│   │               ├── task_row.html
│   │               ├── theme_toggle.html
│   │               ├── user_mutation.html
│   │               ├── user_row.html
│   │               └── user_table.html
│   └── repository/                   # Outbound port + Provider registry + factory + adapters
│       ├── task_repo.go              #   TaskRepository interface + TaskFilter
│       ├── config.go                 #   DBType, Config, ConfigFromEnv()
│       ├── factory.go                #   New(ctx, cfg) + backend registry
│       ├── conformance/              #   Backend-agnostic contract suite
│       │   ├── conformance.go        #     Task + owner-scoping contract
│       │   └── users.go              #     User contract (CRUD, auth lookup, cascade)
│       ├── sqldb/                    #   Shared SQL adapter + Dialect abstraction
│       │   ├── dialect.go            #     SQLite / Postgres / Oracle dialects + DDL
│       │   ├── repo.go               #     TaskRepositorySQL(db, Dialect)
│       │   ├── user_repo.go          #     User methods on the shared SQL core
│       │   └── seed.go               #     Bootstrap-admin seeding (bcrypt, idempotent)
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
| `internal/repository/conformance/{conformance,users}.go` | `sqlite_test` / `memory_test` | Backend-agnostic `TaskRepository` contract suite (tasks, owner scoping, users) |
| `internal/domain/{domain,user}_test.go` | `domain_test` | Entity creation, status transitions, validation, user rules |
| `internal/application/service_test.go` | `application_test` | Use cases with in-memory repo + deterministic ID/clock |
| `internal/adapters/cli/cli_test.go` | `cli_test` | CLI auth pre-flight, subcommand parsing, output |
| `internal/adapters/httpapi/handlers_test.go` | `httpapi_test` | REST API endpoints (incl. Basic/API-key auth), error mapping, parity with CLI |
| `internal/adapters/httpweb/handlers_test.go` | `httpweb_test` | Web UI endpoints, sessions/CSRF, HTMX fragments, error mapping |
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
- **Multi-user & authentication** (see [Authentication](#authentication) for
  the full architecture):
  - Email + password sign-in; passwords stored **only** as bcrypt hashes.
  - Per-user **task ownership** — every task belongs to exactly one user;
    users see and manage only their own tasks.
  - **Admin role** (`isadmin`) — sees all tasks and manages users (create,
    edit, delete) on a dedicated Web UI page.
  - **API keys** — one 256-bit key per user for `X-API-Key` REST auth.
  - **Bootstrap admin** (`admin@email.com` / `admin`) seeded automatically on
    an empty database.
  - Hardened web sessions (HMAC-signed cookies, per-request privilege
    refresh) and CSRF protection on every state-changing form.

All core features are available through the **CLI** (`hexarch cli`), the
**REST API** (`hexarch httpapi`), and the **interactive Web UI** (`hexarch httpweb`).

---

## Requirements

- [Go](https://go.dev/dl/) **1.27+** (per `go.mod`).

No `CGO` compiler is required: the app uses the pure-Go driver
[`modernc.org/sqlite`](https://pkg.go.dev/modernc.org/sqlite). Password
hashing uses the pure-Go [`golang.org/x/crypto/bcrypt`](https://pkg.go.dev/golang.org/x/crypto/bcrypt)
package — the only direct dependency added for authentication.

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
| `HEXARCH_EMAIL`     | —                   | CLI fallback for `--email` when authenticating. |
| `HEXARCH_PASSWORD`  | —                   | CLI fallback for `--password` when authenticating. |
| `HEXARCH_SESSION_SECRET` | random per boot | HMAC key for web session cookies. Unset = sessions invalidated on restart. |

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

> **Authentication is required for every subcommand except `help`:** pass
> `--email` / `--password` (or set `HEXARCH_EMAIL` / `HEXARCH_PASSWORD`).
> The CLI always operates on the authenticated user's own tasks; see
> [Authentication](#authentication).

#### Subcommands

```
hexarch cli help                                        # show usage

hexarch cli create --email admin@email.com --password admin \
        --title "Fix login bug" --desc "oAuth redirect" --priority 4 --deadline 2026-09-01
hexarch cli list --email admin@email.com --password admin          # all own tasks
hexarch cli list --email admin@email.com --password admin --status in_progress
hexarch cli list --email admin@email.com --password admin --search "login"
hexarch cli list --email admin@email.com --password admin --limit 20 --offset 40

hexarch cli get <id>
hexarch cli rename <id> "New title"
hexarch cli priority <id> 5
hexarch cli start <id>       # todo -> in_progress
hexarch cli done  <id>       # in_progress -> done
hexarch cli deadline <id> 2026-10-15
hexarch cli deadline <id> done      # clear the deadline
hexarch cli remove <id>
hexarch cli stats                # per-status counts
hexarch cli whoami               # print the authenticated user
```

#### Worked example

```bash
$ hexarch cli create --email admin@email.com --password admin --title "Ship v1" --priority 5
created <uuid>: Ship v1 (todo)

$ hexarch cli list --email admin@email.com --password admin
<uuid>                 Ship v1                p5  todo

$ hexarch cli whoami --email admin@email.com --password admin
admin@email.com (admin)

$ hexarch cli start <uuid>

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

All endpoints except `POST /api/login` require authentication — HTTP Basic
auth (`email:password`) or the `X-API-Key` header (see
[Authentication](#authentication)). Regular users see only their own tasks;
admins may pass `?user_id=<userid>` (or `?user_id=all`) on list endpoints.

| Method | Path             | Description                                                         | CLI equivalent |
|--------|------------------|---------------------------------------------------------------------|----------------|
| POST   | `/api/login`     | Exchange email + password for the user record (incl. apikey)        | `whoami`       |
| POST   | `/api/tasks`     | Create a task (body: `title`, `description`, `priority`, `deadline`)| `create`       |
| GET    | `/api/tasks`     | List tasks (`?status=&search=&limit=&offset=&user_id=`)             | `list`         |
| GET    | `/api/tasks/:id` | Get a task by id                                                    | `get`          |
| PATCH  | `/api/tasks/:id` | Update `title` / `status` / `priority` / `deadline`                 | `rename`, `priority`, `start`, `done`, `deadline` |
| DELETE | `/api/tasks/:id` | Delete a task                                                       | `remove`       |
| GET    | `/api/stats`     | Per-status counts                                                   | `stats`        |

#### Examples (`curl`)

```bash
# every subcommand except `help` requires authentication:
#   --email E --password P   (or HEXARCH_EMAIL / HEXARCH_PASSWORD)
# create (auth required on every subcommand)
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
- **Login & logout** — dedicated login page with inline error alerts; session
  cookies signed and expired server-side; conditional **Admin** navbar link.
- **User Management (admin)** — table of users with create/edit/delete modals,
  admin-role toggles, self-delete prevention, and success/error toasts.

#### Web routes

All routes except login/logout require an authenticated session; the user
management routes additionally require the admin role (see
[Authentication](#authentication)).

| Method | Path | Description |
|--------|------|-------------|
| GET | `/app/login` | Login page (redirects to `/app/` when already signed in) |
| POST | `/app/login` | Verify credentials, issue session cookie (CSRF-protected) |
| GET/POST | `/app/logout` | Clear the session, return to the login page |
| GET | `/app/users` | User Management page (admin only) |
| POST | `/app/users` | Create a user (admin only) |
| GET | `/app/users/:id/edit` | Edit-user modal fragment (admin only) |
| PATCH | `/app/users/:id` | Apply user edits (admin only) |
| GET | `/app/users/:id/delete` | Delete-confirmation modal fragment (admin only) |
| DELETE | `/app/users/:id` | Delete a user and their tasks (admin only) |
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

## Authentication

Since the multi-user update, **every task operation requires authentication**.
Users own their tasks; an admin (`isadmin = true`) sees and manages all tasks
and can create, edit, and delete users. This section documents the feature
set, the technical architecture, and the usage of each adapter. The design
specification lives in [`docs/auth.md`](docs/auth.md), the delivery plan in
[`docs/auth-plan.md`](docs/auth-plan.md).

### Features at a glance

| Capability | Where |
|---|---|
| Email + password sign-in (bcrypt-hashed, never stored in clear) | all adapters |
| Per-user task ownership (1-to-many User → Task) | all adapters |
| Admin role: manage users, see all tasks | Web UI + REST (`?user_id=`) |
| API keys (`X-API-Key`) for scripted REST access | REST API |
| Self-service "who am I" | CLI (`whoami`), REST (`POST /api/login`) |
| Login/logout pages, admin-only User Management page | Web UI |
| CSRF protection on every state-changing form | Web UI |
| Bootstrap admin seeded on first start | all backends |

### Technical architecture

Authentication follows the same hexagonal rules as the rest of the codebase —
no layer shortcuts, no new application service. The user use cases live on the
existing `TaskService` inbound port; user persistence lives on the existing
`TaskRepository` outbound port.

```
        ┌───────────────────────────────────────────────────────────────┐
        │                     Adapters (inbound)                        │
        │ cli: --email/--password pre-flight → AuthUser                 │
        │ httpapi: Basic auth + X-API-Key middleware → AuthUser /       │
        │          UserByAPIKey (per request)                           │
        │ httpweb: session cookie middleware → UserByID (per request)   │
        └──────────────────────────────┬────────────────────────────────┘
                                       ▼
        ┌───────────────────────────────────────────────────────────────┐
        │              Application — TaskService (inbound port)         │
        │  CreateUser / UpdateUser / DeleteUser / ListUsers /           │
        │  UserByID / AuthUser / UserByAPIKey                           │
        │  • bcrypt hashing & verification (password.go)                │
        │  • id minting: RandomUserID + RandomAPIKey (idgen.go)         │
        │  • guards: last-admin delete/demote, owner existence          │
        └──────────────────────────────┬────────────────────────────────┘
                                       ▼
        ┌───────────────────────────────────────────────────────────────┐
        │   Domain (pure): User + UserID; Task gains a required owner   │
        └──────────────────────────────┬────────────────────────────────┘
                                       ▼
        ┌───────────────────────────────────────────────────────────────┐
        │  TaskRepository (outbound port) — user methods are LOOKUPS    │
        │  only; no backend ever hashes or verifies passwords           │
        │  sqlite / postgres / oracle (shared sqldb core + seed.go)     │
        │  mongodb (users collection, unique email/apikey indexes)      │
        │  memory (reference implementation for the conformance suite)  │
        └───────────────────────────────────────────────────────────────┘
```

#### Data model

```
users (one) ──────< tasks (many)          FK: tasks.user_id → users.id
 id        PK                              ON DELETE CASCADE — deleting a
 email     UNIQUE                          user destroys their tasks
 password  bcrypt hash only
 apikey    UNIQUE (256-bit random hex)
 isadmin   bool
```

Every task is stamped with its owner's `userid` at creation
(`CreateTaskInput.UserID` — required and validated against the users table),
and task list/stats queries are scoped through `TaskFilter.UserID`
(`nil` = all users, admin scope only).

#### Password storage

- Hashing happens **exclusively** in the application layer
  (`internal/application/password.go`): `bcrypt` with a per-user random salt,
  cost 10 in production (`DefaultPasswordCost`), cost 4 in test suites
  (`TestPasswordCost`).
- Clear passwords never reach the domain, the repositories, the logs, or any
  API/HTML response. The REST user DTO exposes only `id`, `email`, `apikey`,
  `isadmin`.
- `AuthUser` verifies with `bcrypt.CompareHashAndPassword`; unknown emails and
  wrong passwords return the **same** generic error (`invalid email or
  password`), and unknown emails burn one dummy bcrypt comparison so response
  timing does not reveal whether an account exists.

#### Bootstrap admin seeding

Schema creation (`Dialect.CreateSchema`) inserts the bootstrap admin **only
when the users table is empty** (idempotent, every backend):

| Field    | Value             |
|----------|-------------------|
| Email    | `admin@email.com` |
| Password | `admin`           |
| Role     | admin             |

The hash is computed by the shared SQL seed (`internal/repository/sqldb/seed.go`);
the memory and mongo backends replicate the same policy. The clear password
`admin` exists only as bcrypt's input — never in storage, logs, or output.

> **Change this password first** — create your own admin in the User
> Management page (`/app/users`), then demote or delete the bootstrap account.
> The last remaining admin can never be deleted or demoted.

#### Web session design (`httpweb`)

- `session.go` — a **stateless HMAC-SHA256 signed cookie** (`hexarch_session`)
  carrying only `userid` + expiry (≤ 24 h). Signing secret:
  `HEXARCH_SESSION_SECRET`, or a random per-boot secret when unset
  (documented trade-off: sessions are invalidated on restart).
- **No privileges in the cookie.** The `requireAuth` middleware re-loads the
  user from the database on *every* request (`UserByID`, an indexed PK read)
  and derives `isadmin` and task ownership from that record. Demoted or
  deleted users lose access on their **next** request — no stale-cookie admin
  window and no server-side session store.
- `requireAdmin` guards the `/app/users` pages with the same DB-loaded flag.
- `csrf.go` — a random token cookie (`hexarch_csrf`) that must be echoed in a
  hidden form field or the `X-CSRF-Token` header (propagated to every HTMX
  request via `hx-headers:inherited` on `<body>` — htmx 4 requires the
  `:inherited` modifier) on every POST/PATCH/DELETE; mismatches get `403`.
- Unauthenticated page requests redirect to `/app/login?next=…` (same-origin
  paths only — open-redirect guard).

#### REST auth design (`httpapi`)

A middleware wraps the whole `/api` group (except the public
`POST /api/login`):

1. `X-API-Key: <key>` header → `UserByAPIKey` (indexed lookup), **or**
2. `Authorization: Basic base64(email:password)` → `AuthUser` (bcrypt verify).

Anything else → `401` with a `WWW-Authenticate: Basic` challenge and a single
uniform message (no account enumeration). Regular users are always scoped to
their own tasks; admins may pass `?user_id=<userid>` to inspect another user
or `?user_id=all` to span every user.

#### Repository contract

`AuthUser` on the port is deliberately a **lookup by lower-cased email only**
— bcrypt verification never happens in a backend. The conformance suite
(`internal/repository/conformance/users.go`) enforces the user contract —
CRUD, duplicate email/apikey → `Conflict`, ordered-by-email `ListUsers`,
auth lookup, `UserByAPIKey`, delete-user task cascade, and owner-scoped
filtering — against every backend.

### Usage

#### Web UI (`hexarch httpweb`)

- Visiting any page redirects to `/app/login` when unauthenticated.
- Sign in with email + password; you land on the task management page.
- Admins see an **Admin** link in the navbar leading to the User Management
  page (`/app/users`): create users, edit email/password/admin role, delete
  users (with their tasks). You cannot delete your own account, and the last
  admin cannot be demoted or deleted.
- Non-admins see no Admin link and get `403` on `/app/users`.

#### REST API (`hexarch httpapi`)

```bash
# Basic auth
curl -u admin@email.com:admin http://localhost:8080/api/tasks

# fetch your API key (hash-free response: id, email, apikey, isadmin)
curl -s -X POST http://localhost:8080/api/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"admin@email.com","password":"admin"}'
# → {"id":"…","email":"admin@email.com","apikey":"…","isadmin":true}

# use the API key
curl -H "X-API-Key: <apikey>" http://localhost:8080/api/tasks

# tasks carry their owner
curl -u admin@email.com:admin -X POST http://localhost:8080/api/tasks \
  -H 'Content-Type: application/json' -d '{"title":"Ship v1","priority":5}'
# → {"id":"…","userid":"<your user id>","title":"Ship v1",…}

# admins may inspect another user's tasks (or ?user_id=all for everyone's)
curl -u admin@email.com:admin 'http://localhost:8080/api/tasks?user_id=<userid>'
```

Missing or invalid credentials return `401`; wrong `POST /api/login`
credentials return `401` with the generic message.

#### CLI (`hexarch cli`)

Every subcommand except `help` requires credentials; the CLI always scopes to
the authenticated user's own tasks:

```bash
hexarch cli --email admin@email.com --password admin create --title "Ship it"
hexarch cli --email admin@email.com --password admin list
hexarch cli whoami --email admin@email.com --password admin
# or via environment:
HEXARCH_EMAIL=admin@email.com HEXARCH_PASSWORD=admin hexarch cli list
```

> **Breaking change:** CLI invocations that worked before the multi-user
> update now require `--email`/`--password` (or the env fallbacks
> `HEXARCH_EMAIL` / `HEXARCH_PASSWORD`).

#### Security properties

| ID | Property |
|---|---|
| NFR-1 | Passwords stored only as bcrypt hashes; never logged or rendered. |
| NFR-2 | One generic auth-failure message; no account enumeration; timing-equalized unknown-email path. |
| NFR-3 | API keys are 256-bit random hex, unique-indexed in every backend. |
| NFR-4 | Session cookies: `HttpOnly`, `SameSite=Lax`, `Secure` on TLS, ≤ 24 h; userid only — privileges re-read per request. |
| NFR-5 | Delete-user cascades to tasks everywhere; self-delete and last-admin delete/demote blocked. |
| NFR-7 | CSRF tokens on all state-changing web endpoints. |
| NFR-8 | No password hash or API key material in logs, views, or error messages; API DTOs expose at most `id/email/apikey/isadmin`. |

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
