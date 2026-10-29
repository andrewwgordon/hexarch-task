# Release 1 Implementation Plan: Daily-Driver Productivity

Status: implemented and tested  
Depends on: current `main` after the multi-user/auth milestone  
Parent roadmap: [`feature-proposals.md`](feature-proposals.md)  
Related specifications: [`auth.md`](auth.md), [`auth-plan.md`](auth-plan.md)

---

## 1. Purpose

Release 1 makes the existing task data more useful without introducing a new
major subsystem or a database migration. It covers three features:

1. **Reopen completed tasks** — allow `done → in_progress`.
2. **Due-date intelligence** — due filters, due counters, and overdue/today
   visual emphasis.
3. **User-selectable sorting** — priority, created, updated, deadline, or title,
   ascending or descending.

The release deliberately excludes tags, projects, trash, bulk actions,
reminders, and notifications. Those remain in later releases.

### 1.1 Goals

- Make overdue work visible and sortable.
- Remove the one-way door out of `done`.
- Preserve the existing default list ordering for compatibility.
- Keep search, status filtering, paging, ownership, and stats working exactly
  as before when the new options are absent.
- Implement the new query semantics identically across SQLite, Postgres,
  Oracle, MongoDB, and memory.
- Add no schema migration and no new third-party dependency.

### 1.2 Non-goals

- Custom user timezones or per-user date preferences.
- Recurring tasks, reminders, notifications, or calendar views.
- Tags, projects, subtasks, comments, or activity history.
- Trash/soft delete, bulk actions, or undo.
- Cursor pagination, saved filters, or full-text search improvements.
- Changes to authentication, sessions, CSRF, or user management.

---

## 2. Locked decisions

These decisions are part of the release contract. Any change requires updating
this document and the corresponding tests.

| Decision | Value |
|---|---|
| Reopen transition | `done → in_progress` only. `done → todo` is not added. |
| Archived behavior | Terminal; unchanged. |
| Deadline representation | Calendar date, stored as before. No schema change. |
| “Today” | Server-local calendar date, normalized to UTC midnight for comparison. |
| Due buckets | `overdue`, `today`, `soon`; mutually exclusive for counters. |
| Overdue range | deadline `< today` |
| Today range | `today ≤ deadline < tomorrow` |
| Soon range | `tomorrow ≤ deadline < today + 8 days` (next seven days) |
| Due filter default statuses | Active only (`todo`, `in_progress`) when no explicit `status` is supplied. |
| Due counters | Always active-only, overall for the caller’s scope; not affected by the current status/search filter. |
| Sort fields | `priority`, `created`, `updated`, `deadline`, `title` |
| Sort direction | `asc`, `desc`; default direction is `desc` except `title`, which defaults to `asc`. |
| Default list order | Unchanged: priority desc, created_at desc. |
| Missing deadline order | Always last, regardless of sort direction. |
| Query parameter names | `due`, `sort`, `order` |
| API compatibility | Additive; omitting new parameters preserves current behavior. |

### 2.1 Why `done → in_progress` only

`in_progress → todo` already exists, so a user who wants a full reset can reopen
to `in_progress` and then move back to `todo`. Adding both transitions at once
would create two similar UI actions and expand the API surface for little
benefit. Archived tasks remain terminal.

---

## 3. Behavioral specification

### 3.1 Reopen completed tasks

The domain transition table becomes:

```text
todo ────────▶ in_progress ────────▶ done ────────▶ archived
                     ▲                  │
                     └──────────────────┘
                        reopen (new)
```

Rules:

- A `done` task may move to `in_progress`.
- `done → todo` remains illegal.
- `archived` remains terminal.
- Reopening updates `updated_at`.
- Reopening does not clear the deadline, priority, title, or description.
- The web row for a done task renders a single **Reopen** action.
- The CLI gains `reopen <id>` as an explicit alias for the same transition.
  The existing `start <id>` command also works on a done task because it
  targets `in_progress`.

### 3.2 Due-date semantics

Deadlines are calendar dates, not wall-clock instants. For due filtering and
counters:

- Take the current application clock time.
- Extract its **server-local** year/month/day.
- Construct `today` as midnight UTC for those date components.
- Construct `tomorrow` as `today + 24h`.
- Compare stored deadlines as UTC midnights.

This makes the comparison date-only while avoiding timezone drift caused by
`time.Parse(time.DateOnly, …)` returning UTC.

| Bucket | Deadline expression | Included statuses |
|---|---|---|
| `overdue` | `deadline < today` | `todo`, `in_progress` by default |
| `today` | `deadline ≥ today && deadline < tomorrow` | `todo`, `in_progress` by default |
| `soon` | `deadline ≥ tomorrow && deadline < today + 8 days` | `todo`, `in_progress` by default |

Additional rules:

- If the caller supplies an explicit `status` filter, that status wins over the
  default active-only status set. Example: `?status=done&due=overdue` lists
  completed tasks whose deadline is before today.
- A task without a deadline never matches any due bucket.
- Due counters in the stats strip are always active-only and overall for the
  caller’s user scope. They are not narrowed by the current status, search, or
  due filter. This matches the existing “stats strip shows overall counts”
  behavior.
- The due buckets used by counters are mutually exclusive:
  - overdue: before today,
  - today: today,
  - soon: tomorrow through seven days ahead.
- “Soon” excludes today so the three counters can be added without double
  counting.

### 3.3 Sorting semantics

The query vocabulary is:

```text
sort=priority|created|updated|deadline|title
order=asc|desc
```

Sort definitions:

| `sort` | Primary ordering | Tie-breakers |
|---|---|---|
| `priority` | priority, in `order` direction | created_at desc, id asc |
| `created` | created_at, in `order` direction | id asc |
| `updated` | updated_at, in `order` direction | id asc |
| `deadline` | has-deadline first; then deadline in `order` direction; missing deadlines always last | created_at desc, id asc |
| `title` | case-insensitive title, in `order` direction | created_at desc, id asc |

Rules:

- With no `sort` and no `order`, behavior is unchanged:
  `priority DESC, created_at DESC`.
- If `sort` is supplied and `order` is omitted:
  - `priority`, `created`, `updated`, `deadline` default to `desc`;
  - `title` defaults to `asc`.
- If `order` is supplied with an empty `sort`, apply that order to the default
  `priority` sort.
- Sorting must be deterministic across backends. Every path includes `id ASC`
  as the final tie-breaker unless the primary sort already guarantees a unique
  order.
- Deadline sorting places tasks without deadlines last in both directions.
- Title sorting is case-insensitive. Use `lower(title)` in SQL, `$toLower` in a
  MongoDB aggregation, and `strings.ToLower` in memory.

### 3.4 Adapter contracts

#### REST API

New query parameters on `GET /api/tasks`:

| Parameter | Values | Default |
|---|---|---|
| `due` | `overdue`, `today`, `soon` | none |
| `sort` | `priority`, `created`, `updated`, `deadline`, `title` | `priority` |
| `order` | `asc`, `desc` | `desc`, except `title` → `asc` |

Examples:

```bash
GET /api/tasks?due=overdue
GET /api/tasks?due=today&sort=priority&order=asc
GET /api/tasks?sort=deadline&order=asc
GET /api/tasks?status=done&due=overdue&sort=updated&order=desc
```

`GET /api/stats` gains three additive fields:

```json
{
  "total": 12,
  "todo": 5,
  "in_progress": 3,
  "done": 4,
  "archived": 0,
  "overdue": 2,
  "due_today": 1,
  "due_soon": 3
}
```

Invalid `due`, `sort`, or `order` values return `400 INVALID_ARGUMENT` with a
clear message.

#### Web UI

The filter bar gains:

- a **Due** select: All / Overdue / Due today / Due soon,
- a **Sort** select: Priority / Created / Updated / Deadline / Title,
- an **Order** select: Descending / Ascending.

The stats strip gains three clickable counters: **Overdue**, **Due today**, and
**Due soon**. Clicking one applies the corresponding `due` filter while keeping
the current search, status, sort, and order where sensible.

Task rows gain deadline emphasis:

- overdue: red/error badge,
- due today: warning badge,
- due soon: info badge,
- no deadline: no deadline badge.

The done-row action set changes from `Archive` only to `Archive` and `Reopen`.

All new controls must:

- participate in the existing `#filter-form` and `hx-include`,
- use `hx-push-url="true"`,
- survive paging,
- survive the non-HTMX PRG fallback,
- be reset by “Clear filters”.

#### CLI

```text
hexarch cli list [--status …] [--search q] [--due overdue|today|soon]
                 [--sort priority|created|updated|deadline|title]
                 [--order asc|desc] [--limit n] [--offset n]

hexarch cli reopen <id>
```

`hexarch cli stats` gains:

```text
overdue:      n
due today:    n
due soon:     n
```

`reopen` calls `ChangeStatus(..., StatusInProgress)`. `start` continues to work
and can now reopen a completed task as a side effect of the domain change.

---

## 4. Architecture and data flow

### 4.1 Filter pipeline

```text
HTTP query / form / CLI flags
        │
        ▼
adapter parses strings into repository.TaskFilter
  Due, Sort, Order validated there
        │
        ▼
TaskServiceImpl.ListTasks / Stats
  • resolves Due into DeadlineFrom/DeadlineTo
  • applies default active statuses for due filters
  • validates sort/order defensively
        │
        ▼
TaskRepository.List / Count
  • shared SQL, MongoDB, and memory implementations
  • identical where/sort semantics
        │
        ▼
adapter renders the result
```

Adapters set the semantic `Due` field. The application resolves it into
absolute date bounds so repositories never call `time.Now()` and never depend
on a timezone.

### 4.2 Port additions

Extend `repository.TaskFilter`:

```go
type DueBucket string

const (
    DueOverdue DueBucket = "overdue"
    DueToday   DueBucket = "today"
    DueSoon    DueBucket = "soon"
)

type SortField string

const (
    SortPriority SortField = "priority"
    SortCreated  SortField = "created"
    SortUpdated  SortField = "updated"
    SortDeadline SortField = "deadline"
    SortTitle    SortField = "title"
)

type SortOrder string

const (
    SortAsc  SortOrder = "asc"
    SortDesc SortOrder = "desc"
)

type TaskFilter struct {
    UserID *domain.UserID
    Status *domain.Status
    Statuses []domain.Status // used when Status is nil (e.g. active-only due queries)
    Search string

    Due *DueBucket
    DeadlineFrom *time.Time // inclusive, resolved by application
    DeadlineTo   *time.Time // exclusive, resolved by application

    Sort  SortField
    Order SortOrder

    Limit  int
    Offset int
}
```

Add helpers in `repository`:

- `TaskFilter.Validate() error` — rejects unknown `Due`, `Sort`, and `Order`.
- `TaskFilter.EffectiveSort() (SortField, SortOrder)` — applies defaults.
- `DefaultTaskFilter()` remains unchanged in behavior.

`Statuses` semantics:

- If both `Status` and `Statuses` are set, `Status` takes precedence.
- If only `Statuses` is set, match any listed status.
- If neither is set, all statuses match.

### 4.3 Repository contract changes

`TaskRepository` method signatures do not change. Their documented semantics
become:

- `List` honors every filter field, applies the effective sort/order, then
  paging.
- `Count` honors `UserID`, `Status`, `Statuses`, `Search`, `DeadlineFrom`, and
  `DeadlineTo`. It ignores paging and sorting.
- `CountByStatus` honors the same filters plus the status argument.
- `DefaultTaskFilter` behavior remains priority desc / created_at desc.

This is a deliberate, explicit extension. Existing callers that only set
`UserID`, `Status`, `Search`, `Limit`, and `Offset` are unaffected.

### 4.4 Stats computation

`Stats` must preserve its current “overall counts” behavior. Implement it as:

1. Build a user-scoped base filter containing only `UserID`.
2. Compute total and per-status counts from that base filter, exactly as today.
3. Compute three additional counts using the base filter plus:
   - `Statuses: {todo, in_progress}`,
   - the relevant deadline range.
4. Return `TaskStats` with the new fields.

This means a `?status=done` or `?search=foo` filter still leaves the stats strip
showing overall counts, matching current behavior. Document this explicitly in
code comments and README.

---

## 5. Implementation phases

Each phase ends with a green test run for the packages it touches. Do not merge
a phase that leaves any backend uncompilable.

### Phase 0 — Baseline and decisions

**Tasks**

- Record the baseline: `go test ./...`, `go vet ./...`.
- Confirm the locked decisions in §2 with the team/product owner.
- Create the release checklist from §11.
- Fix the known README drift: `GET/POST /app/logout` should be documented as
  `POST /app/logout` only.

**Files**

- `README.md` (logout route correction)

**Exit criteria**

- Baseline suite is green.
- Decisions are recorded in this document.

---

### Phase 1 — Domain: reopen completed tasks

**Files**

- `internal/domain/task.go`
- `internal/domain/domain_test.go`
- `internal/adapters/httpweb/views.go` (transition table)
- `internal/adapters/httpweb/handlers_test.go` (action rendering)

**Changes**

- Add `StatusInProgress` to `allowedTransitions[StatusDone]`.
- Update the package/API comments describing the transition table.
- Add `Reopen` to `transitionTable[domain.StatusDone]` with target
  `in_progress`.
- Keep archived terminal.

**Tests**

- `done → in_progress` succeeds and updates `updated_at`.
- `done → todo` still returns `CONFLICT`.
- `archived → in_progress` still returns `CONFLICT`.
- Reopening preserves title, description, priority, and deadline.
- Existing lifecycle test still passes.
- Web test asserts a done row renders both `Archive` and `Reopen`.
- Web test asserts an archived row renders no status actions.

**Exit criteria**

- Domain and web tests pass.
- No repository or schema change is required.

---

### Phase 2 — Repository contract: filter types and helpers

**Files**

- `internal/repository/task_repo.go`
- `internal/repository/config_test.go` (only if helper tests fit here)
- new `internal/repository/task_repo_test.go` for filter helper tests

**Changes**

- Add `DueBucket`, `SortField`, `SortOrder`, and their constants.
- Extend `TaskFilter` with `Statuses`, `Due`, `DeadlineFrom`, `DeadlineTo`,
  `Sort`, and `Order`.
- Add `TaskFilter.Validate()`.
- Add `TaskFilter.EffectiveSort()`.
- Update interface comments for `List`, `Count`, and `CountByStatus`.
- Keep `DefaultTaskFilter()` unchanged externally.

**Tests**

- Empty sort/order resolves to priority desc.
- `title` with empty order resolves to asc.
- Every valid sort/order/due token validates.
- Unknown tokens return `domain.Invalid`.
- `Status` takes precedence over `Statuses` (documented and tested in the
  repository implementations in Phase 3).

**Exit criteria**

- Repository package compiles.
- No backend behavior changes yet; existing tests remain green.

---

### Phase 3 — Repository implementations: due filtering, status sets, sorting

This is the largest phase. Implement the same semantics three times, then run
the conformance suite.

#### 3a. Shared SQL core

**Files**

- `internal/repository/sqldb/repo.go`

**Changes**

- Extract a reusable `whereClause(f repository.TaskFilter) (string, []any)` that
  handles:
  - `UserID`,
  - `Status`,
  - `Statuses` via `IN (?, …)`,
  - `Search` via the existing `CaseInsensitiveMatch`,
  - `DeadlineFrom` via `deadline > 0 AND deadline >= ?`,
  - `DeadlineTo` via `deadline > 0 AND deadline < ?`.
- Use the helper in `List`, `Count`, and `CountByStatus`.
- Extract an `orderClause(f)` helper using a whitelist:
  - priority: `priority <dir>, created_at DESC, id ASC`
  - created: `created_at <dir>, id ASC`
  - updated: `updated_at <dir>, id ASC`
  - deadline: `CASE WHEN deadline = 0 THEN 1 ELSE 0 END ASC, deadline <dir>, created_at DESC, id ASC`
  - title: `lower(title) <dir>, created_at DESC, id ASC`
- Keep the default as priority desc when sort is empty.
- Ensure `?` placeholders are still rebound by the dialect.

**Important**

- `deadline = 0` remains the no-deadline sentinel. Never include those rows in
  a due range.
- Do not use database-specific `NULLS LAST`; the `CASE` expression is portable
  across SQLite, Postgres, and Oracle.

#### 3b. Memory backend

**Files**

- `internal/repository/memory/task_repo_mem.go`

**Changes**

- Apply `Statuses`, `DeadlineFrom`, and `DeadlineTo` in `List`.
- Apply the same filters in `Count` and `CountByStatus`.
- Implement a shared `matchesFilter(t, f)` helper to avoid divergence.
- Implement the sort comparator exactly as specified in §3.3.
- Keep stable sorting and deterministic tie-breakers.

#### 3c. MongoDB backend

**Files**

- `internal/repository/mongo/mongo_repo.go`

**Changes**

- Add `Statuses` as `status: {$in: [...]}`.
- Add deadline range clauses using `deadline: {$gte, $lt}`. Missing/null
  deadline documents must not match.
- For filtering, `Count` and `CountByStatus` use the same filter builder.
- For sorting:
  - `priority`, `created`, `updated` can use a normal `Find` sort.
  - `deadline` and `title` need deterministic handling:
    - use an aggregation pipeline with `$addFields` for `has_deadline` and
      `lower_title`, then `$sort`,
    - or add equivalent stored fields; prefer the aggregation to avoid a
      migration.
  - Missing deadlines sort last in both directions.
- Add indexes to support the new filters/sorts where cheap:
  - `{userid: 1, status: 1, deadline: 1}`,
  - `{userid: 1, updated_at: -1}`.
- Keep the existing priority/created index.

#### 3d. Conformance suite

**Files**

- `internal/repository/conformance/conformance.go`
- `internal/repository/conformance/users.go` (only if shared fixtures change)

**Changes**

Add subtests covering:

- `Statuses` OR filtering.
- `Status` precedence over `Statuses`.
- Due ranges with resolved `DeadlineFrom`/`DeadlineTo`.
- No-deadline tasks excluded from ranges.
- Sort by priority ascending/descending.
- Sort by created ascending/descending.
- Sort by updated ascending/descending.
- Sort by deadline ascending/descending with missing deadlines last.
- Sort by title case-insensitive ascending/descending.
- Deterministic tie-breaking by id.
- Default ordering unchanged when sort/order are empty.
- `Count` honors due ranges and status sets.
- `CountByStatus` honors due ranges and status sets.
- Paging still applies after sorting.

Use fixed UTC times to avoid timezone flakiness.

**Exit criteria**

- `go test ./internal/repository/...` passes for SQLite and memory in CI.
- Postgres/Oracle/Mongo compile. If local instances are available, run their
  env-guarded suites; otherwise note them as unverified in the PR.
- The conformance suite is the source of truth for backend parity.

---

### Phase 4 — Application: resolve due filters and compute due stats

**Files**

- `internal/application/task_service.go`
- `internal/application/task_service_impl.go`
- `internal/application/service_test.go`

**Changes**

- Extend `TaskStats`:

```go
type TaskStats struct {
    Total, Todo, InProgress, Done, Archived int
    Overdue, DueToday, DueSoon              int
}
```

- Add an unexported helper:

```go
func dueWindow(now time.Time, bucket repository.DueBucket) (from, to *time.Time, err error)
```

  Rules:
  - derive `today` from the clock’s local date components, normalized to UTC
    midnight;
  - overdue: `from=nil`, `to=today`;
  - today: `from=today`, `to=tomorrow`;
  - soon: `from=tomorrow`, `to=today+8d`.

- In `ListTasks`:
  - call `filter.Validate()`;
  - if `filter.Due != nil`, resolve `DeadlineFrom`/`DeadlineTo`;
  - if `filter.Due != nil && filter.Status == nil`, set
    `Statuses = {todo, in_progress}`;
  - leave `Due` set or clear it; repositories ignore it either way.
- In `Stats`:
  - build a user-scoped base filter with only `UserID`;
  - compute existing total/per-status counts from the base filter;
  - compute overdues/today/soon with `Count`, active statuses, and the
    appropriate date window;
  - return all fields.
- Update comments and the `ListTasks` contract description.

**Tests**

- With an injected clock at a fixed instant:
  - overdue range includes yesterday and excludes today;
  - today range includes today and excludes tomorrow;
  - soon range includes tomorrow and day+7, excludes day+8 and today;
  - a nil deadline never appears in any bucket;
  - due filter defaults to active statuses;
  - explicit `status=done` overrides active defaults;
  - archived tasks are excluded from due counters by default;
  - `Stats` due counters ignore status/search/due filters and remain overall;
  - `ListTasks` rejects an invalid sort/order.
- Use UTC fixtures to avoid local-time machine differences.

**Exit criteria**

- Application tests pass with deterministic clocks.
- No adapter changes are required for the application logic to work.

---

### Phase 5 — Shared HTTP parsing

**Files**

- `internal/adapters/httpconv/httpconv.go`
- new `internal/adapters/httpconv/httpconv_test.go`

**Changes**

- Add:
  - `ParseDue(string) (repository.DueBucket, error)`
  - `ParseSort(string) (repository.SortField, error)`
  - `ParseSortOrder(string) (repository.SortOrder, error)`
- Empty strings return the zero value and no error; adapters decide whether to
  apply defaults.
- Unknown tokens return `domain.Invalid` with the accepted values in the
  message.
- Avoid importing adapters; `httpconv` may import `repository`.

**Tests**

- Each valid token parses.
- Empty input succeeds with a zero value.
- Invalid tokens produce `INVALID_ARGUMENT`.
- Error messages list the accepted values.

**Exit criteria**

- Both HTTP adapters can share one parser.
- No behavioral change yet.

---

### Phase 6 — REST API adapter

**Files**

- `internal/adapters/httpapi/handlers.go`
- `internal/adapters/httpapi/dto.go`
- `internal/adapters/httpapi/handlers_test.go`

**Changes**

- `filterFromQuery`:
  - parse `due`, `sort`, and `order` via `httpconv`;
  - set the corresponding `TaskFilter` fields;
  - keep `status`, `search`, `limit`, and `offset` behavior unchanged.
- `StatsResponse`:
  - add `Overdue int json:"overdue"`,
  - add `DueToday int json:"due_today"`,
  - add `DueSoon int json:"due_soon"`.
- `toStatsResponse` maps the new fields.
- Ensure `scopeFilter` still applies admin `?user_id=` scoping before the
  service resolves due filters.

**Tests**

- `GET /api/tasks?due=today` returns only tasks due today.
- `?due=overdue&sort=deadline&order=asc` composes correctly.
- `?sort=title` is case-insensitive and deterministic.
- Invalid `due`, `sort`, or `order` returns 400.
- Existing list/filter/paging tests still pass.
- Reopen via `PATCH {"status":"in_progress"}` succeeds after `done`.
- `GET /api/stats` returns the new due fields with expected counts.
- Regular-user scoping still hides other users’ due tasks and counters.
- Admin `?user_id=` still works with due filters.

**Exit criteria**

- REST contract matches §3.4.
- OpenAPI is not part of this release; update README examples instead.

---

### Phase 7 — Web UI adapter

**Files**

- `internal/adapters/httpweb/parse.go`
- `internal/adapters/httpweb/views.go`
- `internal/adapters/httpweb/handlers.go`
- `internal/adapters/httpweb/templates/index.html`
- `internal/adapters/httpweb/templates/partials/stats.html`
- `internal/adapters/httpweb/templates/partials/task_row.html`
- `internal/adapters/httpweb/handlers_test.go`

**Changes**

#### Parse and state

- `filterFromQuery` parses `due`, `sort`, and `order` with `httpconv`.
- Add `DueFilter`, `Sort`, and `Order` to `listPage`.
- Add due/soon/today counts to `statsView`.
- Add `DueFilter` to `statsView` so the due counters can show an active state.

#### Presentation

- `taskView` gains:
  - `DeadlineClass` (error/warning/info/neutral),
  - `DeadlineLabel` (`Overdue`, `Today`, `Soon`, or the formatted date).
- Compute deadline state from the deadline and current server date. Prefer
  passing `time.Now()` into `toTaskView` from the handler so the function stays
  deterministic and testable.
- Add the **Reopen** action to `transitionTable[StatusDone]`.
- `listURL` must preserve `status`, `search`, `due`, `sort`, and `order`.
- Mutation responses already re-render the whole list + stats; keep that.

#### Templates

In `index.html` filter form:

- add a Due select with `name="due"` and the values `""`, `overdue`, `today`,
  `soon`;
- add a Sort select with `name="sort"` and the values `""`, `priority`,
  `created`, `updated`, `deadline`, `title`;
- add an Order select with `name="order"` and the values `""`, `desc`, `asc`;
- keep all existing `hx-get`, `hx-target`, `hx-include`, `hx-push-url`, and
  indicator attributes on the controls.

In `stats.html`:

- add three stat cards or a compact second stats row for Overdue, Due today,
  and Due soon;
- make each counter an HTMX link that sets `due=…` and preserves
  `search`/`status`/`sort`/`order` via `hx-vals` or the filter form;
- show an active visual state when the corresponding due filter is active.

In `task_row.html`:

- use `DeadlineClass`/`DeadlineLabel` for the deadline badge;
- render Reopen for done rows via the existing transition loop;
- keep the existing edit/delete behavior.

#### Tests

- Full page and fragment render the new due/sort/order controls.
- Due/sort/order selections are marked selected after a request.
- Filters compose with status, search, and paging.
- Paging preserves due/sort/order.
- Clear filters returns to the canonical list URL.
- Overdue/today/soon badges render with the expected classes/labels.
- Done rows offer Reopen; archived rows offer no status action.
- Reopen from the web succeeds and updates stats.
- Due counters render and are clickable.
- Non-HTMX mutations redirect to a URL preserving the new filters.
- Existing tests for pagination, empty states, CSRF, and ownership remain green.

**Exit criteria**

- Web UI is usable without JavaScript: the controls submit as normal query
  parameters and mutations PRG-redirect with the new filters.
- HTMX updates keep list and stats consistent.

---

### Phase 8 — CLI adapter

**Files**

- `internal/adapters/cli/cli.go`
- `internal/adapters/cli/cli_test.go`

**Changes**

- Add `reopen` to dispatch and help.
- `reopen` requires one ID and calls
  `ChangeStatus(ctx, id, domain.StatusInProgress, caller)`.
- Add `--due`, `--sort`, and `--order` parsing to `list`.
  - Use `repository` validation helpers or local parsing that matches the HTTP
    tokens.
  - Apply the same defaults as the API.
- Add the three due lines to `stats`.
- Update help text and comments.
- Keep the authentication pre-flight and option stripping unchanged.

**Tests**

- `reopen` works on a done task and fails on a missing/foreign task.
- `list --due overdue` filters correctly.
- `list --sort deadline --order asc` sorts correctly.
- Invalid `--due`, `--sort`, or `--order` returns usage-style errors.
- `stats` prints the new counters.
- Existing CLI auth/scoping tests remain green.

**Exit criteria**

- CLI, REST, and web use identical semantics for the new options.

---

### Phase 9 — Documentation, integration tests, and hardening

**Files**

- `README.md`
- `docs/release_1.md` (this document, status update)
- `docs/feature-proposals.md` (mark Release 1 items as delivered)
- `test/integration_test.go`
- optionally `assets/web-ui.png` (refresh screenshot if UI changes)

**Changes**

- README:
  - add due/sort/order to the REST query table and examples;
  - document the new stats fields;
  - add `reopen` and list options to the CLI section;
  - document the due-date definition, including the server-local “today” rule;
  - describe the new web controls and due counters;
  - correct the `POST /app/logout` route documentation.
- `feature-proposals.md`:
  - mark reopen, due-date intelligence, and sorting as delivered or move them
    to a completed section.
- Integration test:
  - create tasks with varied deadlines, priorities, and titles;
  - assert due filtering and sorting through the real REST socket;
  - reopen a task and assert the lifecycle through REST and web;
  - assert the stats JSON contains the new fields.
- Performance:
  - confirm list queries use the new deadline/status indexes where available;
  - optionally add SQL indexes in a follow-up if query plans show a need.
    Do not add a migration in this release unless measurements justify it.

**Exit criteria**

- Documentation matches behavior.
- End-to-end tests pass.
- `go test ./...` and `go vet ./...` are green.
- CI passes for SQLite and memory conformance.

---

## 6. File-by-file change summary

| File | Change |
|---|---|
| `internal/domain/task.go` | Allow `done → in_progress`; update transition comments. |
| `internal/domain/domain_test.go` | Reopen success and illegal-transition tests. |
| `internal/repository/task_repo.go` | Add due/sort types, filter fields, validation, effective-sort helpers; update method contracts. |
| `internal/repository/task_repo_test.go` | **New** — filter validation and default-sort tests. |
| `internal/repository/sqldb/repo.go` | Shared where/order builders; due/status-set filtering; deterministic sorting. |
| `internal/repository/memory/task_repo_mem.go` | Matching filter and sort behavior. |
| `internal/repository/mongo/mongo_repo.go` | Due/status-set filters; aggregation sorting for deadline/title; indexes. |
| `internal/repository/conformance/conformance.go` | Contract tests for due filters, sorting, status sets, and counts. |
| `internal/application/task_service.go` | Extend `TaskStats` with overdue/today/soon. |
| `internal/application/task_service_impl.go` | Resolve due windows with the injected clock; compute due stats; validate filters. |
| `internal/application/service_test.go` | Deterministic due-window, sorting-validation, and stats tests. |
| `internal/adapters/httpconv/httpconv.go` | Parse due/sort/order tokens. |
| `internal/adapters/httpconv/httpconv_test.go` | **New** — parser tests. |
| `internal/adapters/httpapi/handlers.go` | Parse new query params; expose due stats. |
| `internal/adapters/httpapi/dto.go` | Add due fields to `StatsResponse`. |
| `internal/adapters/httpapi/handlers_test.go` | REST due/sort/order/reopen/stats tests. |
| `internal/adapters/httpweb/parse.go` | Parse due/sort/order into the filter. |
| `internal/adapters/httpweb/views.go` | List/stats/task view fields; deadline styling; Reopen transition. |
| `internal/adapters/httpweb/handlers.go` | Preserve new filters in `listURL`; pass current time to views. |
| `internal/adapters/httpweb/templates/index.html` | Due/sort/order controls. |
| `internal/adapters/httpweb/templates/partials/stats.html` | Due counters. |
| `internal/adapters/httpweb/templates/partials/task_row.html` | Deadline emphasis and Reopen action. |
| `internal/adapters/httpweb/handlers_test.go` | UI controls, filters, badges, reopen, stats tests. |
| `internal/adapters/cli/cli.go` | `reopen`; list due/sort/order options; stats lines; help text. |
| `internal/adapters/cli/cli_test.go` | CLI coverage for new options. |
| `test/integration_test.go` | End-to-end due/sort/reopen/stats parity. |
| `README.md` | Document new behavior, routes, filters, stats, CLI, and logout fix. |
| `docs/feature-proposals.md` | Mark Release 1 items delivered. |
| `assets/web-ui.png` | Optional refresh if the UI changes materially. |

No changes are expected in:

- `cmd/main.go`
- repository config/factory/provider registration
- authentication, session, CSRF, or user-management code
- SQL DDL/schema migration files
- `go.mod` / `go.sum`

---

## 7. Test plan

### 7.1 Unit and package tests

| Layer | Command | Focus |
|---|---|---|
| Domain | `go test ./internal/domain/...` | Reopen transition, terminal states |
| Repository | `go test ./internal/repository/...` | Conformance due/sort/count parity |
| Application | `go test ./internal/application/...` | Due-window resolution, stats, validation |
| HTTP conversion | `go test ./internal/adapters/httpconv/...` | Token parsing |
| REST API | `go test ./internal/adapters/httpapi/...` | Query params, stats, reopen |
| Web UI | `go test ./internal/adapters/httpweb/...` | Controls, filters, badges, PRG |
| CLI | `go test ./internal/adapters/cli/...` | New flags and reopen |
| Integration | `go test ./test/...` | Real-socket REST/web parity |

### 7.2 Backend matrix

| Backend | Required in this release |
|---|---|
| SQLite | Must pass conformance and integration tests in CI |
| Memory | Must pass conformance in CI |
| Postgres | Must compile; run env-guarded suite when available |
| Oracle | Must compile; run env-guarded suite when available |
| MongoDB | Must compile; run env-guarded suite when available |

Because sorting and deadline filtering are the first features with nontrivial
cross-backend ordering semantics, the conformance suite must exercise all sort
directions and both present/missing deadlines before this release is considered
complete.

### 7.3 Manual verification checklist

- Create tasks with no deadline, yesterday, today, tomorrow, and day+8.
- Verify each due filter returns exactly the expected set.
- Verify due counters are mutually exclusive and match the filtered lists.
- Sort by every field in both directions and page through results.
- Verify tasks without deadlines sort last for deadline sorting in both
  directions.
- Complete a task, reopen it, and confirm the timestamp/status.
- Verify an archived task cannot be reopened.
- Verify search + status + due + sort + paging compose.
- Verify the non-JavaScript flow by disabling JavaScript and submitting the
  filter form and a mutation.
- Verify a regular user cannot see another user’s due tasks or counters.
- Verify the admin `?user_id=` scope still works with due filters.
- Verify CLI output and exit behavior for invalid tokens.

---

## 8. Risks and mitigations

| Risk | Mitigation |
|---|---|
| Backend sorting drift, especially missing deadlines and case-insensitive titles | Add exhaustive conformance cases; define exact tie-breakers; use portable `CASE` in SQL and explicit aggregation fields in Mongo. |
| “Today” differs by machine timezone in tests | Inject the clock in application tests; use fixed UTC dates; document the server-local date rule. |
| Due filter changes existing stats behavior | Keep `Stats` per-status counts on a user-only base filter; add tests proving status/search/due filters do not alter the stats strip. |
| Extending `Count` breaks existing callers | Audit all callers; preserve default behavior; make `Stats` explicitly sanitize its filter; add regression tests. |
| Mongo aggregation for deadline/title sorting underperforms | Add indexes; keep aggregation pipeline minimal; measure with seeded data. |
| `Status` + `Statuses` ambiguity | Document precedence; test both; adapters only set `Due`, application sets `Statuses`. |
| UI state lost during paging/mutation | Extend `listURL` and `hx-include`; add tests for paging and PRG fallback. |
| Reopen creates unexpected API behavior | Additive transition only; document it; keep archived terminal; test illegal `done → todo`. |
| README/API drift | Update docs in the same PR; correct the logout route description. |
| Scope creep into tags/trash/bulk actions | Keep the non-goals list in this document; reject unrelated changes. |

---

## 9. Rollout and compatibility

### 9.1 Compatibility

- All new query parameters are optional.
- The default list order and default stats behavior are unchanged.
- The REST stats response only gains fields; existing clients can ignore them.
- The CLI only gains options and subcommands; existing commands remain valid.
- The domain gains one legal transition; no existing transition is removed.
- No database schema change is required, so deployments need no migration step.

### 9.2 Rollout

1. Merge phases behind no feature flag; the changes are additive.
2. Deploy to a staging environment with a copy of production data.
3. Run the manual checklist against SQLite and, where available, one external
   backend.
4. Deploy to production.
5. Monitor:
   - list latency for due/sort queries,
   - any 500/503 increase on list/stats,
   - client errors caused by new parameter validation.

### 9.3 Rollback

- Revert the release commit(s). No data rollback is required because there is
  no schema change.
- Reverting the domain transition removes the Reopen action; existing reopened
  tasks remain valid `in_progress` tasks.
- Reverting the query parameters restores the old API/UI behavior, but clients
  using the new parameters will receive 400s until they stop sending them.

---

## 10. Out of scope for Release 1

Explicitly deferred to later releases:

- tags/labels,
- projects/lists,
- subtasks/checklists,
- comments/activity history,
- trash/soft delete and restore,
- bulk actions,
- quick-add/keyboard shortcuts,
- per-user timezone settings,
- reminders/notifications,
- recurring tasks,
- calendar/Kanban views,
- import/export,
- real-time updates,
- OpenAPI/ETags/idempotency,
- auth hardening (rate limiting, password reset, session revocation).

---

## 11. Definition of done

Release 1 is complete when all of the following are true:

- [x] `done → in_progress` is legal in the domain and available in the web UI
      and CLI.
- [x] `done → todo` and all archived transitions remain illegal.
- [x] `GET /api/tasks` accepts `due`, `sort`, and `order` with the documented
      values and errors.
- [x] The web filter bar exposes due/sort/order and preserves them across
      paging and PRG fallback.
- [x] Task rows visibly distinguish overdue, due-today, and due-soon deadlines.
- [x] `GET /api/stats` and the web stats strip expose overdue, due-today, and
      due-soon counts.
- [x] Due filters use the documented active-status default and explicit-status
      override.
- [x] Sorting is deterministic and identical across SQLite, memory, and the
      other backends per the conformance suite.
- [x] Missing deadlines sort last for deadline sorting in both directions.
- [x] Default ordering remains priority desc, created_at desc.
- [x] Ownership and admin scoping apply to every new filter and counter.
- [x] Unit, conformance, adapter, CLI, and integration tests pass.
- [x] `go test ./...` and `go vet ./...` pass locally (CI pending).
- [x] README and `feature-proposals.md` are updated.
- [x] The logout route documentation is corrected.
- [x] No database migration is required.
- [ ] Manual verification checklist in §7.3 has been completed (automated coverage added; manual pass pending).
