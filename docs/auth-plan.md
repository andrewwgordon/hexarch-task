# Implementation Plan: Multi-User & Authentication for hexarch

**Spec:** `docs/auth.md`
**Status:** ✅ Implemented — all phases complete; `go build ./...`, `go vet ./...` and `go test ./...` green.
**Method:** Each phase is a small, independently verifiable increment that
leaves the tree green (`go build ./...` and `go test ./...` pass). Phases
strictly follow the dependency order of the hexagon: domain → application →
port → backends → adapters. Every phase ends with its own acceptance criteria
and a concrete test checklist; a phase is only "done" when all its boxes tick.

**Global gates (re-checked at the end of every phase):**

- [ ] `go vet ./...` and `go build ./...` pass
- [ ] `go test ./...` passes (all pre-existing tests still green)
- [ ] No clear-text password, hash, or API key appears in logs or outputs
- [ ] New code follows the existing file-header doc-comment convention

---

## Phase 0 — Baseline & tooling

**Goal:** Establish a green baseline and add the only new dependency.

**Tasks**

1. Run `go build ./...` and `go test ./...`; record current results (all green).
2. `golang.org/x/crypto` is already in `go.mod` as an **indirect** dependency
   (v0.48.0, via existing deps). Promote it to direct:
   `go get golang.org/x/crypto/bcrypt` (pure Go; no cgo). It is the only new
   direct dependency of this feature.
3. Create the branch / working tree for this feature.
4. Skim `docs/auth.md` §3 with implementers; confirm the two resolved design
   decisions: seeding lives in `sqldb` (no import cycle), session cookie
   carries only `userid` (privileges re-read per request).

**Acceptance criteria**

- Dependency `golang.org/x/crypto` present in `go.mod`; `go.sum` updated.
- Baseline test results recorded (needed to attribute later breakage).

**Tests**

- None new; baseline suite green is itself the exit test.

---

## Phase 1 — Domain: `User` entity (pure core)

**Goal:** `internal/domain/user.go` with `UserID`, `User`, factories,
accessors. No dependencies beyond stdlib.

**Files**

| File | Action |
|---|---|
| `internal/domain/user.go` | **New** |
| `internal/domain/user_test.go` | **New** |
| `internal/domain/errors.go` | Reuse existing `Invalid`/`Conflict`/`NotFound` (no change expected) |

**Tasks**

1. `type UserID string` with `String()`.
2. `User` struct: unexported `id`, `email`, `password` (hash only), `apikey`,
   `isAdmin`.
3. Accessors: `ID()`, `Email()`, `Password()`, `APIKey()`, `IsAdmin()`.
4. `NewUser(id UserID, email, passwordHash, apiKey string, isAdmin bool)` —
   validate: id non-empty, email non-empty + contains `@` + stored lowercase
   (`strings.ToLower`), hash non-empty, apikey non-empty.
5. `HydrateUser(...)` with the same validation.

**Acceptance criteria**

- Package imports nothing outside stdlib (`go list -deps` check optional).
- Empty/invalid inputs return `domain.Invalid`; no panic paths.

**Tests (`user_test.go`)**

- Valid `NewUser` round-trips all accessors; email is lower-cased.
- Rejects: empty id, empty email, email without `@`, empty hash, empty apikey.
- `HydrateUser` ≡ `NewUser` for the same inputs.
- Upper-case email input is normalized to lowercase.

**Gate:** domain package still imports nothing internal; `go test ./internal/domain` green.

---

## Phase 2 — Domain: task ownership

**Goal:** `Task` gains `userid`; factories require it.

**Files**

| File | Action |
|---|---|
| `internal/domain/task.go` | Add `userid` field + `UserID()` accessor; `NewTask`/`HydrateTask` gain `userid` param |
| `internal/domain/domain_test.go` | **Update** — all call sites pass a userid |

**Tasks**

1. Add unexported `userid UserID`, accessor `UserID() UserID`.
2. Extend `NewTask` and `HydrateTask` signatures; reject empty `userid` with
   `Invalid("task must have an owner")`.
3. Update every `domain_test.go` fixture (helper `mustTask` style) with a
   constant test userid.

**Acceptance criteria**

- A task cannot be constructed without an owner (compile-time + runtime).
- All existing domain tests green after mechanical signature updates.

**Tests**

- New: `NewTask` with empty userid → `Invalid`.
- New: `HydrateTask` preserves and returns the userid.

---

## Phase 3 — Application: ID generation

**Goal:** Reuse `idgen.go` for user identities and API keys.

**Files**

| File | Action |
|---|---|
| `internal/application/idgen.go` | Extract `randomUUID()`; add `RandomUserID`, `RandomAPIKey` |
| `internal/application/service_test.go` | Add coverage for new generators |

**Tasks**

1. Refactor `RandomTaskID` body into `randomUUID() (string, error)`.
2. `RandomUserID() (domain.UserID, error)` → same UUIDv4 format.
3. `RandomAPIKey() (string, error)` → 32 crypto-random bytes hex-encoded
   (64-char token; NFR-3).

**Acceptance criteria**

- `RandomTaskID` output unchanged (existing tests still pass untouched).
- `RandomAPIKey` is 64 hex chars and unique across calls.

**Tests**

- `RandomUserID` returns parseable UUID format (regex check).
- `RandomAPIKey` length 64, decodable hex, two calls differ.
- Injected-generator tests continue to compile (`NewTaskServiceWith`).

---

## Phase 4 — Repository port extension (compile-safe)

**Goal:** `TaskRepository` gains the seven user methods; `TaskFilter` gains
`UserID`; every backend gets a mechanical stub so the tree **stays green**.
The conformance additions are *written* here but wired in during Phase 5
(wiring them now would fail against the stubs).

**Files**

| File | Action |
|---|---|
| `internal/repository/task_repo.go` | Extend `TaskRepository`; `TaskFilter.UserID *domain.UserID` |
| `internal/repository/{memory,mongo,sqlite,postgres,oracle}` | Mechanical stub methods returning `domain.Invalid("not implemented")` |
| `internal/repository/conformance/conformance.go` | **Prepare** — user contract suite written but not yet registered |
| `internal/repository/conformance/conformance.go` | Update file-header Public API comment per repo convention |

**Tasks**

1. Add to `TaskRepository` (per spec §3.5): `CreateUser`, `UpdateUser`,
   `DeleteUser`, `ListUsers`, `UserByID`, `AuthUser` (lookup only — doc
   comment must say "lookup; hashing happens in the application layer"),
   `UserByAPIKey`.
2. `TaskFilter.UserID *domain.UserID` — nil = all users.
3. Add stub methods to all five backends so every package compiles and all
   existing tests pass. Stubs return `domain.Invalid("not implemented")` and
   carry a `// TODO(phase N)` marker pointing at their implementing phase
   (memory → 5, SQL → 6, mongo → 7).
4. Write the conformance additions **in this phase but do not call them from
   `conformance.Repository` yet** (Phase 5 wires them in once the memory
   backend is real):

   - create user → found by `UserByID`, listed by `ListUsers` in
     **email order** (spec §3.5)
   - duplicate email `CreateUser` → `Conflict`
   - `UpdateUser` (change email/password-hash/isadmin) persists; changing
     email to an existing one → `Conflict`
   - `DeleteUser` → NotFound on second call; tasks of that user removed
   - `AuthUser` lookup by exact + mixed-case email; unknown → `NotFound`
   - `UserByAPIKey` by key; unknown → `NotFound`
   - `TaskFilter.UserID` returns only that owner's tasks; nil returns all

**Acceptance criteria**

- Full tree compiles and all existing tests pass (global gate holds — this is
  the reason for the stubs).
- Every new port method has a doc comment stating its contract (lookup vs.
  verification, Conflict/NotFound semantics).

**Tests**

- None new; the green suite is the exit test. Conformance functions compile
  but are unreferenced (checked via `go vet`; dead-code linters may need a
  temporary build tag — remove it in Phase 5).

---

## Phase 5 — Application service + memory backend: user use cases

**Goal:** All seven user methods on `TaskServiceImpl`; ownership flows through
`CreateTaskInput`; bcrypt helpers live here; the memory backend implements the
port for real and the conformance suite goes live against it. **This is the
phase that makes the user contract runnable.**

**Files**

| File | Action |
|---|---|
| `internal/application/task_service.go` | `CreateUserInput`, `UpdateUserInput`, `CreateTaskInput.UserID`, 7 new interface methods |
| `internal/application/task_service_impl.go` | Implement; add `HashPassword`/`CheckPassword`; guards |
| `internal/application/password.go` | **New** — `HashPassword`, `CheckPassword` (bcrypt wrappers) |
| `internal/application/password.go` | **New** — `HashPassword`, `CheckPassword` (bcrypt wrappers; cost is a parameter so tests can use `bcrypt.MinCost`) |
| `internal/application/service_test.go` | **Extend** |
| `internal/repository/memory/task_repo_mem.go` | User storage + local seed (spec §3.6: memory replicates the bootstrap admin) |
| `internal/repository/memory/task_repo_mem_test.go` | Update for new signatures; wire user conformance suite |

**Tasks**

1. Memory backend: add user map + the seven methods + `TaskFilter.UserID`
   filtering + delete-user cascades tasks (reference semantics) + **local
   seed**: replicate the `sqldb/seed.go` policy — after create-schema the
   memory store contains exactly one admin (`admin@email.com`, bcrypt of
   `admin`, minted id/apikey) only when no users exist. Use `bcrypt.MinCost`
   in test construction paths.
2. Extend `NewTaskServiceWith` (or add a variant) to inject **user-id and
   apikey generators** in addition to the task id generator and clock —
   same injection pattern the codebase already uses; default to
   `RandomUserID`/`RandomAPIKey` in `NewTaskService`. Deterministic tests
   need this.
3. `CreateUser`: mint id+apikey (via the injectable generators), normalize
   email, hash via `HashPassword`,
   `domain.NewUser`, `repo.CreateUser`; map unique-violation → `Conflict`.
3. `UpdateUser`: load, apply non-nil fields (rehash when password set),
   enforce **last-admin** rule (cannot demote or delete the only admin),
   `repo.UpdateUser`.
4. `DeleteUser`: block self-delete and last-admin delete with `Conflict`;
   else delegate (cascade per backend contract).
5. `AuthUser`: `repo.AuthUser(email)` → `CheckPassword`; identical generic
   error (`Invalid("invalid email or password")`) for unknown email, missing
   user, wrong password. Consider constant-time-ish behavior: on unknown email,
   compare against a fixed dummy hash to avoid a timing oracle.
6. `ListUsers` / `UserByID` / `UserByAPIKey`: thin delegation (ordering by
   email is the repo's contract — asserted in conformance).
7. `CreateTask`: require `input.UserID` non-empty; resolve via
   `repo.UserByID` (NotFound → invalid owner) → `domain.NewTask(..., userid...)`.
8. Wire the Phase 4 conformance user suite into `conformance.Repository` and
   through the memory backend's test; run it (memory is the reference
   implementation).

**Acceptance criteria**

- `TaskService` interface compiles with 7 new methods; all backends build
  (memory real; others may need stub compile fixes — acceptable only if the
  full backend implementation lands in its own later phase).
- Service enforces: duplicate email → `Conflict`; self-delete → `Conflict`;
  last-admin delete/demote → `Conflict`.

**Tests (`service_test.go`, `conformance` run via memory)**

- CreateUser returns user with id/apikey set (deterministic with injected
  generators), hash ≠ input password, `CheckPassword(hash, clear)` true.
- Duplicate email → `Conflict`; email normalized lowercase.
- Duplicate apikey is impossible in practice (256-bit), but the unique-index
  behavior is asserted at the repo level in the conformance suite.
- UpdateUser partial updates: each of email/password/isadmin independently;
  password change rehashes and old password no longer verifies.
- DeleteUser: NotFound; cascade removes owned tasks (memory).
- AuthUser: success / wrong password / unknown email → **same** error value.
- CreateTask with empty or unknown `UserID` → `Invalid`/`NotFound`.
- TaskFilter scoping: user A does not see user B's tasks; nil sees all.
- Last-admin demotion and deletion blocked; works once a second admin exists.
- Memory seed: fresh repo has exactly one admin; `AuthUser("admin@email.com",
  "admin")` succeeds; re-seeding is a no-op.

**Gate:** `go test ./internal/application ./internal/repository/conformance` green.

---

## Phase 6 — Shared SQL core: `sqldb` repo + dialects + seeding

**Goal:** One implementation of the user methods for SQLite/Postgres/Oracle;
users DDL + FK; `seed.go` bootstrap admin.

**Files**

| File | Action |
|---|---|
| `internal/repository/sqldb/repo.go` | 7 user methods; task queries honor `TaskFilter.UserID` |
| `internal/repository/sqldb/dialect.go` | Users DDL, FK `ON DELETE CASCADE`, per-dialect types |
| `internal/repository/sqldb/seed.go` | **New** — mint admin id/apikey, bcrypt("admin"), insert if empty |
| `internal/repository/sqldb/repo_test.go` | **New** — SQL-backed conformance run |

**Tasks**

1. Implement the user methods in `TaskRepositorySQL` with `Rebind` queries;
   email lookup uses the lowercased email (no `LIKE`); unique-violation →
   `Conflict` via `IsUniqueViolation`.
2. `SQLite.CreateSchema`: users table, tasks FK, `PRAGMA foreign_keys = ON`
   before DDL/DML, upgrade sequence (users → seed → conditional
   `ADD COLUMN user_id` via `pragma_table_info` check → backfill NULLs).
3. `Postgres.CreateSchema`: `BOOLEAN`, `BIGINT`, FK cascade.
4. `Oracle.CreateSchema`: `VARCHAR2(64)` id/password/apikey, `NUMBER(1)`
   isadmin, inline `ON DELETE CASCADE` FK, ORA-00955-tolerant PL/SQL block.
5. `seed.go`: run after table creation in all three dialects; insert
   (id, `admin@email.com`, bcrypt("admin"), apikey, 1) only when
   `SELECT COUNT(*) FROM users` = 0. The literal "admin" appears only as the
   bcrypt input. Share the mint-and-hash helper with the memory seed (Phase 5)
   to avoid divergence — e.g. a tiny internal `seeduser` helper in `sqldb`
   that `memory` mirrors by contract test, per spec §3.6.

**Acceptance criteria**

- Fresh SQLite DB: both tables exist with FK; exactly one seeded admin;
  hash verifies against "admin"; second run adds nothing (idempotent).
- Pre-existing SQLite DB (with tasks): upgraded in place; every task row ends
  up owned by the seeded admin.

**Tests**

- SQLite conformance: existing `sqlite_repo_test.go` extended to run the full
  user suite against a temp file DB.
- Migration test: create a v1 schema (tasks only) + rows, run `Open`, assert
  column added, rows backfilled, admin seeded, re-run is a no-op.
- Unique-violation mapping: duplicate email insert → `domain.Conflict` on all
  three dialects (postgres/oracle behind build tags / CI env as existing).

**Gate:** `go test ./internal/repository/...` green; manual smoke:
`hexarch` boots against a copy of `tasks.db` without error.

---

## Phase 7 — Mongo backend

**Goal:** Parity for the document store.

**Files**

| File | Action |
|---|---|
| `internal/repository/mongo/mongo_repo.go` | users collection, `userid` on task docs, seed |
| `internal/repository/mongo/mongo_repo_test.go` | **New/extend** (behind existing env-var guard) |

**Tasks**

1. `userDoc` BSON mapping; unique indexes on `email` and `apikey` in
   `ensureIndexes`.
2. Task docs gain `userid`; list/filter by it; delete-user removes tasks
   (single transaction where supported, else sequential).
3. Local seed (same policy as `seed.go`).

**Acceptance criteria / tests**

- Same user contract suite as SQL (shared test helper if feasible), guarded by
  the existing mongo env-var skip pattern.
- Duplicate email → `Conflict` via duplicate-key error mapping.

---

## Phase 8 — CLI adapter

**Goal:** Every task subcommand authenticates first.

**Files**

| File | Action |
|---|---|
| `internal/adapters/cli/cli.go` | `--email/--password`, `AuthUser` pre-flight, `whoami` |
| `internal/adapters/cli/args.go` | Global option parsing support (if needed) |

**Tasks**

1. Parse `--email`/`--password` with env fallbacks `HEXARCH_EMAIL` /
   `HEXARCH_PASSWORD` before dispatch; missing → usage error listing the
   options and env vars.
2. Call `AuthUser`; failure → generic message, non-zero exit; success → pass
   `user.ID()` into `CreateTaskInput` and `TaskFilter.UserID`.
3. `whoami` command: prints authenticated email + admin flag (optional, cheap).
4. Update `help()` text.
5. **Scope decision (final):** the CLI always scopes to the authenticated
   user's own tasks — unlike httpapi, no admin override flag is added. Admin
   scoping remains a web/API-only capability (spec §3.7); document this in
   `help()`/README so the asymmetry is explicit.

**Acceptance criteria / tests**

- `hexarch cli --email admin@email.com --password admin create --title X`
  succeeds against a seeded DB; task owned by admin.
- Wrong password → generic error, non-zero; no hash or key printed.
- `whoami` shows `admin@email.com (admin)`.
- Handler tests: arg parsing (flags, env fallback, missing), auth failure
  path. Admin-created tasks are still owned by the admin user; there is no
  `--user`/impersonation flag (scope decision above).

**Gate:** end-to-end CLI smoke against `tasks.db` copy.

---

## Phase 9 — HTTPAPI adapter

**Goal:** Authenticated REST; API-key path; hash-free login endpoint.

**Files**

| File | Action |
|---|---|
| `internal/adapters/httpapi/router.go` | Auth middleware on `/api` |
| `internal/adapters/httpapi/handlers.go` | Use authenticated user; scope lists |
| `internal/adapters/httpapi/dto.go` | `userid` on task DTOs; `userResponse` (id/email/apikey/isadmin only) |
| `internal/adapters/httpapi/errors.go` | 401/403 mapping |
| `internal/adapters/httpapi/handlers_test.go` | **Update + extend** |

**Tasks**

1. Middleware: `Authorization: Basic` → `AuthUser`; else `X-API-Key` →
   `UserByAPIKey`; neither → 401 + `WWW-Authenticate: Basic`.
2. Handlers use the authenticated user: `CreateTaskInput.UserID`;
   `TaskFilter.UserID` = caller (admin + `?user_id=` → that user or nil).
3. `POST /api/login`: JSON `{email,password}` → `AuthUser` →
   `{id,email,apikey,isadmin}` (no hash; NFR-8).
4. Update all existing handler tests to send credentials.

**Acceptance criteria / tests**

- No-credential request to any `/api` route → 401 with `WWW-Authenticate`.
- Bad Basic creds → 401 (same body as missing creds; no enumeration).
- `X-API-Key` path → 200; unknown key → 401.
- Create as user A, list as user B → B doesn't see A's task; admin sees all.
- `/api/login` success/failure; response JSON contains no `password` field
  (assert exact key set).
- Existing CRUD tests green with auth headers.

**Gate:** `go test ./internal/adapters/httpapi` green.

---

## Phase 10 — HTTPWEB: sessions, login, logout

**Goal:** Cookie session + `requireAuth`; login/logout pages; task pages
behind auth.

**Files**

| File | Action |
|---|---|
| `internal/adapters/httpweb/session.go` | **New** — HMAC cookie mint/verify |
| `internal/adapters/httpweb/csrf.go` | **New** — token issue/verify |
| `internal/adapters/httpweb/middleware.go` | **New** — `requireAuth`, `requireAdmin`, user load |
| `internal/adapters/httpweb/router.go` | Mount middleware; add `/login`, `/logout` |
| `internal/adapters/httpweb/handlers.go` | `loginForm`, `login`, `logout`; populate `User` |
| `internal/adapters/httpweb/views.go` | `userView` scaffolding (no password/apikey fields) |
| `internal/adapters/httpweb/templates/login.html` | **New** |
| `internal/adapters/httpweb/handlers_test.go` | **Update + extend** |

**Tasks**

1. `session.go`: HMAC-SHA256 over `userid|expiry`; env
   `HEXARCH_SESSION_SECRET`; `HttpOnly`, `SameSite=Lax`, `Secure` on TLS,
   ≤24 h. Cookie value = base64(payload).signature.
   **Secret resolution (final):** if `HEXARCH_SESSION_SECRET` is unset,
   generate a random secret **per boot** (crypto/rand) and accept the
   documented consequence that all sessions are invalidated on restart. Do
   **not** write the secret to disk by default; future persistence can be
   added via env, documented in README.
2. `requireAuth`: verify cookie → `UserByID` (deleted user → reject; this is
   the per-request privilege refresh) → stash user in context. Redirect
   `/app/login?next=…` when unauthenticated; validate `next` is same-origin
   path (open-redirect guard).
3. `requireAdmin`: DB-loaded `IsAdmin` false → 403 page.
4. CSRF: issue per-session token cookie; hidden field in all forms; verify on
   POST/PATCH/DELETE; HTMX requests send it via `hx-vals`/header; mismatch → 403.
5. Handlers: GET login (redirect if already authed), POST login
   (`AuthUser` → set cookie → PRG `/app/`), GET/POST logout (clear cookie →
   redirect `/app/login`).
6. Wrap the existing task routes with `requireAuth`; add **Login/Logout only**
   to the navbar (Phase 11 owns the Admin link and `index.html`'s final
   state; rendering an Admin link here would 404 since `/app/users` does not
   exist yet).

**Acceptance criteria / tests**

- Unauthenticated GET `/app/` → 302 to `/app/login`; after login → 200 with
  navbar showing Logout.
- Login with `admin@email.com`/`admin` → 303 to `/app/`; wrong password →
  re-rendered login page + error alert (no enumeration hint).
- Deleted user's still-valid cookie → treated as unauthenticated.
- POST without CSRF token → 403 (both HTML form and HTMX header path).
- `POST /logout` without a valid CSRF token → 403; with token → session
  cleared, redirect to `/app/login` (same behavior as GET logout).
- Cookie flags asserted in test (HttpOnly, SameSite).
- Existing httpweb tests updated to authenticate (helper that logs in).

---

## Phase 11 — HTTPWEB: User Management page

**Goal:** `/app/users` CRUD for admins; completes the UI.

**Files**

| File | Action |
|---|---|
| `internal/adapters/httpweb/router.go` | User routes with `requireAdmin` |
| `internal/adapters/httpweb/handlers.go` | `usersList`, `usersCreate`, `userEditForm`, `userUpdate`, `userDeleteForm`, `userDelete` |
| `internal/adapters/httpweb/parse.go` | User form parsing → `UpdateUserInput` (empty password field = unchanged) |
| `internal/adapters/httpweb/errors.go` | 403 admin-denied page; conflict rendering for user mutations |
| `internal/adapters/httpweb/views.go` | `userView`, `userListPage`, converters |
| `internal/adapters/httpweb/templates/users.html` | **New** |
| `templates/partials/{user_row,modal_user_create,modal_user_edit,modal_user_delete}.html` | **New** |
| `templates/index.html` | **Admin link only** (gated on `IsAdmin`; Login/Logout landed in Phase 10) |
| `internal/adapters/httpweb/handlers_test.go` | **Extend** |

**Tasks**

1. Routes with `requireAdmin`; `userUpdate` maps form fields to
   `UpdateUserInput` (nil = unchanged; empty password field = unchanged).
2. Self-delete and last-admin delete/demote surface `Conflict` as a toast /
   error alert (mirror `partials/error_alert.html` usage).
3. DaisyUI per the daisyui skill: `card`, `table`, `badge`, `modal`,
   `form-control`, `toggle`, `toast` — consistent with existing task modals.
4. HTMX: user mutations answer with user-list + toast fragment (mirror
   `mutation.html` pattern); non-HX fallback = PRG redirect.

**Acceptance criteria / tests**

- Admin sees table of users with edit/delete actions; non-admin direct GET →
  403 page; non-admin has no Admin navbar link.
- Create with duplicate email → conflict error rendered, user not created.
- Edit: change email only / password only / admin flag only — each persists.
- Delete confirmation modal; actual DELETE removes user and (spot-check) their
  tasks disappear from all lists.
- Self-delete button hidden **and** server-side 403; last-admin delete → 403.
- Demoted admin's next request renders without Admin link (privilege
  freshness).
- CSRF token present in every modal form.

**Gate:** manual walk-through against a fresh `tasks.db`: login → create user →
login as new user → isolation check → admin deletes user.

---

## Phase 12 — Integration, docs, rollout

**Goal:** End-to-end confidence and documentation.

**Files**

| File | Action |
|---|---|
| `test/integration_test.go` | **Extend** — full auth story |
| `README.md` | Auth features, CLI flags, env vars, breaking change note |
| `docs/auth.md` | Mark spec "Implemented"; note any deviations |

**Tasks**

1. Integration scenarios (§3.10): fresh DB seeds admin; web login; task
   isolation between two users; API-key path; FK cascade on user delete;
   last-admin guard.
2. README: new env vars (`HEXARCH_EMAIL`, `HEXARCH_PASSWORD`,
   `HEXARCH_SESSION_SECRET`, incl. the per-boot-secret/session-invalidation
   note), CLI auth flags, CLI admin-scoping decision, `/api/login`, breaking
   change note, seeded credentials with a "change this password first"
   warning.
3. Doc-comment pass: every touched file's header comment (Public API /
   Private sections) updated to match the repo's convention — especially
   `conformance.go`, `task_repo.go`, `task_service*.go`, the `sqldb` files,
   and the new `session.go`/`csrf.go`/`seed.go`.
4. Final review pass against every acceptance criterion in `docs/auth.md` §2.4.

**Acceptance criteria / tests**

- `go test ./...` fully green, including new suites.
- `go vet ./...` clean; `gofmt -l` empty.
- Manual runbook passes: `hexarch httpweb` fresh DB → full acceptance list in
  spec §2.4 verified by hand.
- README documents the seeded default credentials and the breaking CLI change.

---

## Dependency graph (phase order)

```
0 → 1 → 2 → 3 → 4 → 5 ─┬→ 6 → 7 ─┐
                        │          ├→ 12
                        ├→ 8 ──────┤
                        └→ 9 → 10 → 11 ─┘
```

- Phases 1–2 (domain) must precede 3–5; Phase 4 (port + stubs) blocks 5–7 but
  leaves the tree green.
- Phase 5 (service + memory backend + live conformance) is the pivot; 6–7
  (SQL/Mongo backends), 8 (CLI), and 9 (HTTPAPI) all depend on 5 and are
  mutually independent — they can run in parallel.
- Phase 10 depends on 9's auth concepts but is a separate adapter; 10 → 11
  are sequential (navbar ownership split: 10 = Login/Logout, 11 = Admin link).
- Phase 12 is last. A phase may be merged with the next only if its own
  acceptance criteria are all green first.

## Risk register

| Risk | Mitigation |
|---|---|
| Signature change ripples (`NewTask`/`HydrateTask`) | Phase 2 updates all domain tests in the same commit |
| Port extension breaks backends / leaves tree red | Phase 4 adds mechanical stubs to all five backends; conformance wiring deferred to Phase 5 |
| bcrypt cost slows large test suites | `HashPassword` takes a cost parameter; tests use `bcrypt.MinCost` (Phases 5–6) |
| SQLite upgrade breaks existing `tasks.db` | Phase 6 migration test uses a copy of a real DB; upgrade is stepwise + idempotent |
| Import cycle in seeding | Resolved by design: seeding lives in `sqldb` (spec §3.6); memory/mongo replicate locally |
| Session secret handling surprising on restart | Documented: random per boot when env unset → all sessions invalidated (Phase 10, README) |
| HTMX + CSRF friction | Phase 10 wires `hx-vals` token into the shared mutation helpers once, not per-form |
| Mongo/Oracle untestable locally | Env-guarded suites mirror existing skip patterns; dialect SQL reviewed against §3.6 DDL |
