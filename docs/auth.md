# Specification: Multi-User & Authentication for hexarch

**Status:** Implemented (see `docs/auth-plan.md` for the delivery order and `README.md` for usage).

**Implementation deviations from this spec:**
- `DeleteUser` blocks only the *last-admin* delete at the service level; **self-delete** is enforced by the web adapter (which knows the signed-in identity) — the service cannot see the caller.
- `UpdateUser`/`ListUsers`/`UserByID`/`UserByAPIKey` were added beyond the original three user methods, as required by the User Management page and the API-key auth path (spec §3.5 as revised).
**Scope:** Add users, authentication, and per-user task ownership to the hexagonal task manager.
**Applies to:** `cmd/main.go`, `internal/domain`, `internal/application`, `internal/repository` (all backends), and the three inbound adapters (`cli`, `httpapi`, `httpweb`).

---

## 1. Overview

Today the hexagon is single-user: every task belongs to "everyone", no identity
exists, and all three inbound adapters (CLI, REST API, Web UI) talk to
`application.TaskService` unauthenticated.

This specification introduces:

1. A **User** domain entity (`id`, `email`, `password`, `apikey`, `isadmin`) with
   a 1-to-many relationship to `Task` (a task is owned by exactly one user).
2. **Authentication** with email + password, reusing the existing application
   service (no new application service is created).
3. **Seeded bootstrap admin** created as part of schema creation.
4. Authentication in every inbound adapter (CLI flags, HTTP Basic / API key,
   session cookie for the web UI).
5. A DaisyUI/HTMX **Login page** and an admin-only **User Management page**,
   plus an "Admin" link in the task page navbar for admins.

### 1.1 Guiding principles (inherited from the architecture)

- The **domain** stays pure: `User` knows nothing about bcrypt, cookies, or SQL.
- The **application layer** orchestrates: ID generation (`idgen.go`), password
  hashing, and the read/validate/persist sequences.
- The **repository port** (`task_repo.go`) is the only storage-facing contract;
  every backend (memory, mongo, sqlite, postgres, oracle) implements it.
- **No new application service.** The user use cases — `CreateUser`,
  `UpdateUser`, `DeleteUser`, `ListUsers`, `UserByID`, `UserByAPIKey`, and
  `AuthUser` — are added to the existing `TaskService` / `TaskServiceImpl`.
  (The full set is required: the User Management page needs list/read/update,
  and the API-key middleware needs lookup-by-apikey.)

---

## 2. Functional Specification

### 2.1 Personas

| Persona | Description |
|---|---|
| **End user** | Authenticates with email + password; manages **only their own** tasks. |
| **Admin** (`isadmin = true`) | Everything an end user can do, plus: sees the **Admin** link in the navbar and manages users (create, edit, delete) on the User Management page. |
| **CLI / API client** | Scripts and tools that authenticate per invocation (CLI flags) or per request (HTTP Basic auth / API key). |

### 2.2 Functional requirements

#### FR-U1 — User entity
The system SHALL support users with the following attributes:

| Field | Type | Rules |
|---|---|---|
| `id` | `UserID` | Opaque, unique, minted by the `idgen` service (UUIDv4-style). Immutable. |
| `email` | `string` | Non-empty, unique across users. Normalized to lowercase at creation; the UNIQUE index is on the lowercased value, and lookups are by lowercase email. |
| `password` | `string` | Never stored or returned in clear text; stored as a bcrypt hash (see FR-U6). |
| `apikey` | `string` | Unique, opaque token minted by `idgen` at user creation; used to authenticate REST API calls. |
| `isadmin` | `bool` | Grants access to the User Management page; `false` for regular users. |

#### FR-U2 — User lifecycle
- A user with the admin role can **create** a user (email + password + admin flag).
- A user with the admin role can **edit** a user (email, admin flag, optional password reset).
- A user with the admin role can **delete** a user.
- Email must be unique; duplicate creation **and duplicate email on edit** are
  rejected with a conflict error.
- An admin cannot delete their own account.
- The last remaining admin account cannot be deleted or demoted (both rules
  guard against a user base with zero admins; see also NFR-5).
- All lifecycle operations are backed by the corresponding service methods:
  `CreateUser`, `UpdateUser` (edit email/admin flag/password), `DeleteUser`,
  `ListUsers`, and `UserByID` (§3.4).

#### FR-U3 — Authentication (AuthUser)
- `AuthUser` validates an email/password pair. On success it returns the
  authenticated user; on failure it returns a single generic error
  ("invalid email or password") for both unknown email and wrong password.
- Passwords are compared against the stored bcrypt hash.
- All three adapters (CLI, HTTPAPI, HTTPWEB) authenticate via `AuthUser`
  before performing any task operation.

#### FR-U4 — Task ownership
- Every task belongs to exactly one user (`userid` foreign key). Creating a
  task stamps it with the authenticated user's ID.
- Users see, filter, and modify **only their own** tasks. Admins see and manage
  **all** users' tasks (needed for administration; also the natural reading of
  a 1-to-many relationship with a shared stats view).
- Tasks referencing a deleted user are deleted with the user (FK cascade) —
  deleting a user destroys their tasks.

#### FR-U5 — Bootstrap admin
As part of database schema creation, an initial user is seeded exactly once:

| Field | Value |
|---|---|
| email | `admin@email.com` |
| password | `admin` (stored bcrypt-hashed) |
| isadmin | `true` |

Seeding is idempotent: it runs only when no users exist.

#### FR-U6 — Password storage
- Passwords are hashed with **bcrypt** (per-user random salt, cost ≥ 10) at
  creation and on password change.
- The clear password is never persisted, logged, or rendered in any view or API
  response.

#### FR-U7 — API keys
- Each user gets an `apikey` generated via `idgen` at creation.
- The REST API accepts `X-API-Key: <apikey>` as an alternative to HTTP Basic
  auth.

### 2.3 Adapter requirements

#### FR-A1 — CLI (`cli.go`)
- New global options `--email` and `--password` (environment fallbacks
  `HEXARCH_EMAIL` / `HEXARCH_PASSWORD`). Both are required for every
  task subcommand; the adapter calls `AuthUser` first and aborts with the
  generic failure message on bad credentials.

#### FR-A2 — HTTPAPI (`server.go` / `router.go` / `handlers.go`)
- Every `/api` route requires authentication via HTTP Basic auth
  (`Authorization: Basic base64(email:password)` → `AuthUser`)
  **or** the `X-API-Key` header (lookup by apikey).
- Missing/invalid credentials → `401 Unauthorized` JSON error.

#### FR-A3 — HTTPWEB (`server.go` / `router.go`)
New routes (all under the existing `/app` group):

| Route | Method | Behavior |
|---|---|---|
| `/app/login` | GET | Login page. Redirects to `/app/` if already authenticated. |
| `/app/login` | POST | Calls `AuthUser`; on success sets a session cookie and redirects to `/app/`; on failure re-renders the login page with an error alert. |
| `/app/logout` | GET/POST | Clears the session, redirects to `/app/login`. |
| `/app/users` | GET | User Management list (admin only). |
| `/app/users` | POST | Create a user (admin only). |
| `/app/users/:id/edit` | GET | Edit-user form/modal (admin only). |
| `/app/users/:id` | PATCH | Apply user edits (admin only). |
| `/app/users/:id/delete` | GET | Delete-confirmation modal (admin only). |
| `/app/users/:id` | DELETE | Delete the user (admin only; 403 for self-delete and last-admin). |

- Unauthenticated requests to any `/app` page are redirected to `/app/login`.
- On successful login the user lands on the existing task management page.

#### FR-A4 — Web UI components (DaisyUI + HTMX)
1. **Login page** (`templates/login.html`)
   - DaisyUI card with email + password fields, "Sign in" button.
   - Error alert (`partials/error_alert.html`) on failure.
   - On success: full redirect (PRG) to the task management page.
2. **User Management page** (admin only, reachable from navbar "Admin" link)
   - Table of users: email, admin badge, created date, row actions (edit, delete).
   - "New User" modal (create) with email, password, `isadmin` toggle.
   - Edit modal (email, optional new password, `isadmin` toggle).
   - Delete confirmation modal (blocked for the current user's own account).
   - HTMX fragment behavior mirrors the task pages (modal forms, mutation
     responses with list + toast).
3. **Navbar** on the task page shows an **Admin** link (to `/app/users`) only
   when the signed-in user has `isadmin = true`; a **Logout** button is always
   shown after login.

### 2.4 Acceptance criteria

- `hexarch httpweb` with a fresh SQLite DB → navigating to `/app/` redirects to
  `/app/login`; `admin@email.com` / `admin` logs in and lands on the task page;
  the navbar shows **Admin** and **Logout**.
- A non-admin user logging in sees no Admin link; direct navigation to
  `/app/users` is denied (403).
- An admin can create, edit, and delete users from the UI; a duplicate email is
  rejected (on create **and** edit); the signed-in admin cannot delete
  themselves, and the last remaining admin cannot be deleted or demoted.
- Demoting an admin (or deleting any user) takes effect on their **next**
  request — no stale-cookie admin window.
- Submitting a state-changing web form without a valid CSRF token is rejected
  with 403.
- Each user's task list contains only their own tasks; admin sees all tasks.
- `hexarch cli --email admin@email.com --password admin create --title X`
  succeeds; wrong credentials fail with the generic auth error.
- `curl -u admin@email.com:admin http://localhost:8080/api/tasks` and
  `curl -H "X-API-Key: <key>" ...` both work; unauthenticated calls return 401.
- Passwords are stored only as bcrypt hashes in every backend schema.

---

## 3. Technical Specification

### 3.1 Domain layer — `internal/domain/user.go` (new)

```go
// UserID is the domain-level identity of a user (opaque, minted by idgen).
type UserID string

// User is the core domain entity. Password holds only the bcrypt hash.
type User struct {
    id       UserID
    email    string
    password string // bcrypt hash, never clear text
    apikey   string
    isAdmin  bool
}
```

- Accessors: `ID()`, `Email()`, `Password()` (hash), `APIKey()`, `IsAdmin()`.
- Factories:
  - `NewUser(id UserID, email, passwordHash, apiKey string, isAdmin bool) (User, error)`
    — validates email format (contains `@`, lower-cased) and rejects an empty
    hash. *The caller (application layer) hashes the password.*
  - `HydrateUser(...)` — rebuild from storage.
- No behavior beyond validation lives here; user is a value object like Task.

### 3.2 Task ownership — `internal/domain/task.go`

- Add an unexported `userid UserID` field plus accessor `UserID() UserID`.
- `NewTask` and `HydrateTask` gain a `userid UserID` parameter (empty userid is
  invalid — every task has an owner).

### 3.3 ID generation — `internal/application/idgen.go`

Extract the UUIDv4 body into an unexported `randomUUID()` helper and reuse it:

```go
func RandomTaskID() (domain.TaskID, error)   // existing behavior
func RandomUserID() (domain.UserID, error)   // new — same UUIDv4 format
func RandomAPIKey() (string, error)          // new — 32-byte hex token
```

### 3.4 Application layer — `task_service.go` / `task_service_impl.go` (extend, no new service)

Extend the `TaskService` interface:

```go
type CreateUserInput struct {
    Email    string
    Password string // clear text; hashed by the service before persistence
    IsAdmin  bool
}

type UpdateUserInput struct {
    Email    *string // nil = unchanged
    Password *string // nil = unchanged; if set, hashed by the service
    IsAdmin  *bool   // nil = unchanged
}

// Added to TaskService:
CreateUser(ctx context.Context, input CreateUserInput) (domain.User, error)
UpdateUser(ctx context.Context, id domain.UserID, input UpdateUserInput) (domain.User, error)
DeleteUser(ctx context.Context, id domain.UserID) error
ListUsers(ctx context.Context) ([]domain.User, error)
UserByID(ctx context.Context, id domain.UserID) (domain.User, error)
AuthUser(ctx context.Context, email, password string) (domain.User, error)
```

Task ownership reaches the service through the input struct. `CreateTaskInput`
gains a field, and the adapter (which knows the authenticated user from its
auth step) fills it in:

```go
type CreateTaskInput struct {
    UserID      domain.UserID // owner of the task; required, must be non-empty
    Title       string
    Description string
    Priority    int
    Deadline    *time.Time
}
```

List/stats scoping is expressed through the existing `repository.TaskFilter`
(`UserID *domain.UserID`): `ListTasks` and `Stats` take the filter/`userid`
scope from the adapter — regular users always pass their own ID, admins may
pass `nil` to span all users. No other `TaskService` signatures change.

`TaskServiceImpl` additions:

- **CreateUser** — mint `RandomUserID()` + `RandomAPIKey()`, validate input,
  `bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)`,
  build via `domain.NewUser`, persist with `repo.CreateUser`. Duplicate email →
  `domain.Conflict` (mapped from the repo's unique-violation detection).
- **UpdateUser** — load via `repo.UserByID`, apply only the non-nil fields of
  `UpdateUserInput` (hashing the new password if present), enforce the
  last-admin rule (cannot demote or delete the only remaining admin), persist
  with `repo.UpdateUser`. Duplicate email → `Conflict`.
- **DeleteUser** — refuse self-delete and last-admin-delete (`domain.Conflict`);
  otherwise `repo.DeleteUser(id)`; tasks are removed by FK cascade.
- **AuthUser** — `user, err := repo.AuthUser(ctx, email)`; then
  `bcrypt.CompareHashAndPassword`. Unknown email, missing user, and wrong
  password all return the **same** generic error to avoid account enumeration.
  Note: `AuthUser` performs the **verification** (hash compare); the
  repository method of the same name is a lookup only (§3.5).
- **CreateTask / other task methods** — the adapter supplies the authenticated
  `input.UserID`; the service validates it is non-empty and resolves it via
  `repo.UserByID` (NotFound → invalid owner) before calling
  `domain.NewTask(..., userid, ...)` and persisting. List/stats scope by the
  caller's `userid` via `TaskFilter` (admins may pass `nil` for all users).

Password hashing helper (`HashPassword`, `CheckPassword`) lives in the
application package so domain stays stdlib-only.

### 3.5 Repository port — `internal/repository/task_repo.go`

Extend `TaskRepository` (concrete backends already implement this interface):

```go
// Users
CreateUser(ctx context.Context, user domain.User) error          // Conflict on duplicate email
UpdateUser(ctx context.Context, user domain.User) error          // persist email/isadmin/password hash
DeleteUser(ctx context.Context, id domain.UserID) error          // NotFound if absent
ListUsers(ctx context.Context) ([]domain.User, error)            // ordered by email
UserByID(ctx context.Context, id domain.UserID) (domain.User, error)   // NotFound if absent
AuthUser(ctx context.Context, email string) (domain.User, error) // lookup by lower(email); NOT a password check
UserByAPIKey(ctx context.Context, key string) (domain.User, error)     // for the X-API-Key middleware
// Task ownership
// ownership is expressed via TaskFilter (below)
```

- **Semantics note:** the port's `AuthUser` is intentionally a *lookup by
  lowercase email only*. Bcrypt comparison never happens in a repository —
  `TaskServiceImpl.AuthUser` owns the hash comparison (§3.4). Implementers must
  not duplicate hashing inside backends.
- `TaskFilter` gains `UserID *domain.UserID` (nil = all users, used by admin).
- Conformance suite (`conformance.go`) gains explicit fixtures for **every**
  new port method: create user / duplicate email → Conflict / `UpdateUser`
  (including email-change conflict) / `DeleteUser` (NotFound) / `ListUsers` /
  `UserByID` / `AuthUser` lookup (known + unknown email) / `UserByAPIKey`, and
  task-owner filtering with the `TaskFilter.UserID` scope.

### 3.6 Backends

#### Shared SQL core — `internal/repository/sqldb/repo.go` & `dialect.go`
- `repo.go` implements the three user methods with `Rebind`-rewritten queries;
  email lookups use `CaseInsensitiveMatch`.
- `Dialect.CreateSchema` creates both tables and seeds the admin:

```sql
CREATE TABLE IF NOT EXISTS users (
    id       TEXT NOT NULL PRIMARY KEY,
    email    TEXT NOT NULL UNIQUE,
    password TEXT NOT NULL,   -- bcrypt hash only
    apikey   TEXT NOT NULL UNIQUE,
    isadmin  INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS tasks (
    id          TEXT NOT NULL PRIMARY KEY,
    user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title       TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL,
    priority    INTEGER NOT NULL,
    deadline    INTEGER NOT NULL DEFAULT 0,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);
```

Per-dialect adjustments:
- **SQLite** (`SQLite.CreateSchema`): as above; `isadmin INTEGER (0/1)`. Open
  issues `PRAGMA foreign_keys = ON` **before any DDL or DML** on the connection
  (SQLite defaults it to off per connection, and cascade/backfill depend on
  it). Existing DBs are upgraded in this order (each step idempotent):
  1. `CREATE TABLE IF NOT EXISTS users ...`;
  2. seed the admin (below);
  3. add `user_id` **only if absent** (check `pragma_table_info('tasks')`):
     `ALTER TABLE tasks ADD COLUMN user_id TEXT REFERENCES users(id)`. SQLite
     cannot add a NOT NULL column without a default, so the added column is
     nullable and is made NOT NULL-equivalent by immediately backfilling every
     NULL to the seeded admin's id (new code rejects empty userids anyway);
  4. backfill `UPDATE tasks SET user_id = (SELECT id FROM users LIMIT 1)`.
  Note: for the column to become truly `NOT NULL` at the storage level, a
  table rebuild (`CREATE new / INSERT SELECT / DROP / RENAME`) would be
  required — accepted as out of scope; the application-layer invariant plus
  the seeded owner covers existing rows.
- **Postgres** (`Postgres.CreateSchema`): `isadmin BOOLEAN NOT NULL DEFAULT false`,
  `deadline/created_at/updated_at BIGINT`; FK
  `REFERENCES users(id) ON DELETE CASCADE`.
- **Oracle** (`Oracle.CreateSchema`): `VARCHAR2(64)` ids, `VARCHAR2(64)` for
  `password` (a bcrypt hash is 60 chars), `NUMBER(1)` for `isadmin`; FK with
  `ON DELETE CASCADE` declared inline in the column clause (supported since
  Oracle 8i; the project's 12c floor is needed only for `OFFSET ... FETCH`
  pagination, not for FKs); still wrapped in the PL/SQL
  `EXCEPTION WHEN OTHERS` / ORA-00955 block.
- **Admin seeding** (all dialects, inside `CreateSchema`, after table
  creation): an `INSERT ... SELECT` guarded by "users table is empty".
  **Ownership decision:** the seeding — including the bcrypt hash of the
  default `admin` password — is computed **inside the `sqldb` package**
  (new file `internal/repository/sqldb/seed.go`): it mints the admin's
  `UserID`/`apikey` with the same UUIDv4-format helper, calls
  `bcrypt.GenerateFromPassword` once at startup, and inserts
  (id/admin@email.com/<hash>/<apikey>/1) only when `users` is empty. The
  password `admin` appears only as the input to bcrypt — it is never stored,
  logged, or emitted as a literal hash. This keeps `Dialect.CreateSchema`
  signature unchanged and avoids an import cycle (`repository` cannot import
  `application`; application imports repository), and `Provider.Open(ctx, uri)`
  needs no signature change. The `memory` and `mongo` providers replicate the
  same seed logic locally. If a future requirement moves seeding policy to the
  composition root, the injection point is a seed parameter on
  `repository.Config` — not a callback into the application layer.
- **Mongo** (`mongo_repo.go`): `users` collection with the same fields plus a
  unique index on `email` and `apikey`; task documents gain `userid`; task
  list/filter by `userid`; delete-user removes that user's tasks in a
  transaction or sequential delete.
- **Memory** (`task_repo_mem.go`): user map + per-user task slices; reference
  implementation for the extended conformance suite.

### 3.7 Inbound adapters

#### CLI — `internal/adapters/cli/cli.go`
- Parse `--email` / `--password` (fallback env `HEXARCH_EMAIL` /
  `HEXARCH_PASSWORD`) before dispatching any task subcommand.
- Call `svc.AuthUser(ctx, email, password)`; on failure print the generic
  error and exit non-zero. On success proceed with the subcommand, passing
  `user.ID()` into create/filter inputs. Add a `whoami` convenience command
  that prints the authenticated user (email + admin flag) — optional.

#### HTTPAPI — `internal/adapters/httpapi/`
- `router.go`: wrap `/api` group in a `BasicAuth`-style middleware that:
  1. `Authorization: Basic ...` → `AuthUser(email, password)`, or
  2. `X-API-Key: <key>` → `repo.UserByAPIKey(ctx, key)` (port method defined
     in §3.5).
- Invalid/missing credentials → `401` with `WWW-Authenticate: Basic`.
- `dto.go`/`handlers.go`: task responses gain `userid`; create attributes the
  task to the authenticated user; list/filter scope by the caller (admin may
  pass `?user_id=`).
- Optional: `POST /api/login` (JSON email+password → `AuthUser`) for clients
  that want to fetch their key. The response body is limited to
  `id`, `email`, `apikey`, `isadmin` — the password hash is **never** included
  in any API response (see NFR-1; the `userView` rule in the web adapter does
  not apply here, so the DTO must enforce it explicitly).

#### HTTPWEB — `internal/adapters/httpweb/`
- **Sessions:** add `session.go` with an HMAC-SHA256 signed cookie
  (`hexarch_session`) containing only `userid` + expiry; secret from env
  `HEXARCH_SESSION_SECRET` (generate-and-persist per process if unset). No
  server-side session store is required.
- **Privilege freshness:** the cookie deliberately does **not** embed
  `isadmin`. The `requireAuth` middleware loads the live user via
  `UserByID` on every request (a cheap PK lookup) and derives `isadmin`, the
  navbar state, and task ownership from that record. This means a demoted or
  deleted admin loses access **immediately**, not at cookie expiry, and a
  deleted user's cookie is simply rejected (session revocation without a
  server-side store).
- **Middleware:** `requireAuth` (redirect to `/app/login` when the cookie is
  absent/invalid) and `requireAdmin` (403 page when the DB-loaded user is not
  admin). Both use the per-request `UserByID` load described above.
- **CSRF:** a token-based guard (new `csrf.go`) protects every state-changing
  web form (`POST /login`, `POST /users`, `PATCH/DELETE /users/:id`, and the
  existing task mutations): a random token is stored in a cookie (or the
  session) and embedded as a hidden field in every form / `hx-vals` on
  HTMX requests; middleware rejects mismatches with 403. `SameSite=Lax` alone
  is not sufficient because HTMX `POST`/`DELETE` requests originate on the
  same site, and the API-key/Basic-auth REST paths are unaffected.
- **router.go** route table:

```go
web.GET("/login",  h.loginForm)     // full page
web.POST("/login", h.login)         // AuthUser; sets cookie; PRG to /app/
web.GET("/logout", h.logout)        // clears cookie; redirect /app/login
web.POST("/logout", h.logout)       // same handler (CSRF-protected form option)
web.GET("/users",          h.usersList)        // admin
web.POST("/users",         h.usersCreate)      // admin
web.GET("/users/:id/edit", h.userEditForm)     // admin
web.PATCH("/users/:id",    h.userUpdate)       // admin
web.GET("/users/:id/delete", h.userDeleteForm) // admin
web.DELETE("/users/:id",   h.userDelete)       // admin; 403 on self-delete / last-admin
```

- **views.go:** add `userView` / `userListPage` presentation types (never
  expose `password` or `apikey` in views).
- **Templates:** `templates/login.html`, `templates/users.html`,
  `partials/user_row.html`, `partials/modal_user_create.html`,
  `partials/modal_user_edit.html`, `partials/modal_user_delete.html`.
  DaisyUI components: `card`/`form-control`/`input`/`btn`/`badge`/
  `modal`/`table`/`toast`, consistent with the existing task pages.
- **index.html navbar:** conditional
  `{{ if .User.IsAdmin }}<a class="btn btn-ghost btn-sm" href="/app/users">Admin</a>{{ end }}`
  plus a Logout button; `handlers` populate `User` from the session.

### 3.8 Composition root — `cmd/main.go`
No structural change: the same `TaskService` (now with user methods) is passed
to every adapter. `NewApp` signatures are unchanged because `AppBase.Svc` is
already the shared `TaskService` port.

### 3.9 Security & non-functional requirements

| ID | Requirement |
|---|---|
| NFR-1 | Passwords stored **only** as bcrypt hashes; clear text never logged, rendered, or returned by the API. |
| NFR-2 | Auth failures return one generic message; no user enumeration. |
| NFR-3 | API keys are 256-bit random hex, generated by `RandomAPIKey`; unique index in all stores. |
| NFR-4 | Session cookie: `HttpOnly`, `SameSite=Lax`, `Secure` when served over TLS, ≤ 24 h expiry. Cookie carries only `userid` + expiry; privileges are re-read from the database per request (no stale-admin window). |
| NFR-5 | Delete-user cascades to tasks in every backend; self-delete and last-admin delete/demote are blocked (consistent with FR-U2, enforced in `TaskServiceImpl`). |
| NFR-6 | All new endpoints return within normal latency; auth adds one indexed lookup (`email` or `apikey` unique index) plus bcrypt compare (~50–100 ms at default cost, acceptable for these traffic levels); the web middleware's per-request `UserByID` is an indexed PK read. |
| NFR-7 | CSRF protection on all state-changing web endpoints via per-session token + hidden form field / `hx-vals` (see §3.7); REST endpoints are exempt (authenticated per request, no ambient cookie). |
| NFR-8 | No clear password, bcrypt hash, or API key is ever written to logs, error messages, or rendered views in any adapter; API DTOs expose at most `id/email/apikey/isadmin`. |

### 3.10 Testing plan

| Layer | Tests |
|---|---|
| Domain | `user_test.go`: validation of email (format, lowercase normalization)/hash/apikey, admin flag. `task_test.go`: task requires userid. |
| Application | Service tests with in-memory repo + deterministic idgen/clock: CreateUser (duplicate email → Conflict), UpdateUser (partial update, password rehash, duplicate email, last-admin demotion blocked), DeleteUser (NotFound, self-delete blocked, last-admin blocked, cascade), ListUsers/UserByID, AuthUser (success, wrong password, unknown email — same generic error). |
| Repository | Conformance suite extension runs against memory + sqlite (+ postgres/oracle in CI when configured) covering **every new port method**: `CreateUser`/duplicate → Conflict, `UpdateUser`, `DeleteUser` → NotFound, `ListUsers`, `UserByID`, `AuthUser` lookup (known/unknown email), `UserByAPIKey`, FK cascade, unique email/apikey, `TaskFilter.UserID` scoping. |
| Adapters | CLI arg parsing; httpapi middleware tests (401/200, X-API-Key path); httpweb tests: login success/failure, logout, admin-only access, user CRUD flows, admin cannot delete self, CSRF rejection, demoted-admin loses access immediately. |
| Existing tests | **Breaking signature changes** require updating existing suites: `domain_test.go` and `service_test.go` (new `NewTask`/`HydrateTask` userid param), `conformance.go` (user fixtures), `handlers_test.go` (httpapi & httpweb: auth now required on every route), `integration_test.go`. |
| Integration | `test/integration_test.go`: end-to-end — fresh DB seeds admin, login via web, create task as user, second user cannot see it. |

### 3.11 Migration & rollout

1. Schema creation is idempotent for all dialects. Existing SQLite DBs are
   upgraded in place in this order: create `users` → seed admin →
   `ALTER TABLE tasks ADD COLUMN user_id` (only if `pragma_table_info`
   reports it absent) → backfill NULLs to the seeded admin's id (full details
   in §3.6; a full NOT-NULL table rebuild is explicitly out of scope).
   Postgres and Oracle apply the same create-users → seed → add-FK sequence
   via their `CreateSchema` blocks.
2. Existing CLI scripts/scripts using `hexarch cli ...` require the new
   `--email/--password` (or env vars) — documented as a **breaking change** in
   the README changelog.
3. No new dependencies beyond `golang.org/x/crypto/bcrypt` (pure Go).

### 3.12 Out of scope

- Password reset / "forgot password" flows, email verification.
- Refresh tokens, OAuth/OIDC, SSO.
- Session revocation list (stateless signed cookies).
- Fine-grained roles beyond the single `isadmin` flag.
- Rate limiting of login attempts (recommended follow-up).

---

## 4. File-by-file change summary

| File | Change |
|---|---|
| `internal/domain/user.go` | **New** — `UserID`, `User`, factories, accessors. |
| `internal/domain/task.go` | Add `userid` field, accessor, factory/hydrate params. |
| `internal/application/idgen.go` | Add `RandomUserID`, `RandomAPIKey`; extract shared UUID helper. |
| `internal/application/task_service.go` | Add `CreateUserInput`, `UpdateUserInput`; `CreateTaskInput.UserID`; extend `TaskService` with `CreateUser`, `UpdateUser`, `DeleteUser`, `ListUsers`, `UserByID`, `AuthUser`. |
| `internal/application/task_service_impl.go` | Implement the seven methods + bcrypt helpers (hash/compare) + last-admin and self-delete guards. |
| `internal/repository/task_repo.go` | Extend `TaskRepository` with `CreateUser`, `UpdateUser`, `DeleteUser`, `ListUsers`, `UserByID`, `AuthUser` (lookup), `UserByAPIKey`; `TaskFilter.UserID`. |
| `internal/repository/sqldb/dialect.go` | Users DDL + FK + admin seed for SQLite/Postgres/Oracle; FK on tasks. |
| `internal/repository/sqldb/seed.go` | **New** — bootstrap-admin seeding (id/apikey mint + bcrypt hash), shared by the SQL providers. |
| `internal/repository/sqldb/repo.go` | Implement the user methods; scope task queries by owner. |
| `internal/repository/{sqlite,postgres,oracle}` | Pass seed user into schema creation; FK/backfill handling. |
| `internal/repository/{memory,mongo}` | User storage + ownership semantics. |
| `internal/repository/conformance/conformance.go` | User contract tests. |
| `internal/adapters/cli/cli.go` | `--email/--password` auth before subcommands. |
| `internal/adapters/httpapi/{router,handlers,dto}.go` | Basic auth + API key middleware; `userid` in DTOs; `POST /api/login` (hash-free response). |
| `internal/adapters/httpweb/{server,router,handlers,views,session,csrf}.go` | Auth + CSRF middleware, session cookie, login/logout/user routes, per-request user load, navbar admin link. |
| `internal/adapters/httpweb/templates/…` | `login.html`, `users.html`, user modals/partials; navbar updates. |
| `internal/domain/{domain_test.go}`, `internal/application/service_test.go`, `internal/adapters/{httpapi,httpweb}/handlers_test.go`, `test/integration_test.go` | Update for new signatures and mandatory auth (see §3.10). |
| `README.md` | Document auth features, CLI flags, env vars, breaking change. |
