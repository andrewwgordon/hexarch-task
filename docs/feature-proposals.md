# Feature Proposals: hexarch Task Manager

Status: proposed (Release 1 delivered — see [`release_1.md`](release_1.md))  
Scope: product features and enabling technical work after the multi-user/auth
milestone documented in [`auth.md`](auth.md) and [`auth-plan.md`](auth-plan.md).

This document analyses the current application and proposes a prioritized
feature roadmap. Each proposal includes the motivation, user value, likely
architectural impact, and suggested acceptance criteria. It is intended to be
used as a backlog input, not as an implementation specification; individual
features should receive their own design/specification document before work
starts.

---

## 1. Current state

### 1.1 Strengths

The project already has a solid foundation:

- **Clean hexagonal boundaries** — domain → application port → repository port
  → inbound/outbound adapters.
- **Five interchangeable repositories** (`sqlite`, `postgres`, `oracle`,
  `mongodb`, `memory`) governed by a shared provider registry and exercised by
  a backend-agnostic conformance suite.
- **Three complete inbound adapters** — CLI, REST API, and a server-rendered
  HTMX/DaisyUI web UI.
- **Authentication baseline** — bcrypt password hashing, per-user API keys,
  HMAC-signed sessions, CSRF protection, per-request privilege refresh, and
  admin user management.
- **Mature HTMX patterns** — fragment/full-page dual rendering, multi-region
  mutation responses, progressive-enhancement PRG fallback, URL-pushed
  filters, and two explicit empty states.

### 1.2 Current functional limits

| Area | Current state |
|---|---|
| Task model | title, description, status, priority, deadline, owner |
| Status flow | `todo → in_progress → done → archived`; `done` cannot be reopened |
| Filtering | status + substring search over title/description |
| Sorting | fixed: priority descending, then created date descending |
| Organization | no tags, projects, lists, or subtasks |
| Bulk actions | none |
| Deletion | immediate hard delete; no trash or undo |
| History | no comments, activity feed, or audit trail |
| Reminders | deadlines are displayed but never trigger notifications |
| Collaboration | strict single-owner tasks; no sharing, assignment, or mentions |
| Real-time | no SSE/WebSocket; open tabs can become stale |
| Security follow-ups | `auth.md` defers rate limiting, password reset, email verification, session revocation, and richer roles |
| API | no OpenAPI schema, no ETags/optimistic concurrency, no idempotency keys |
| Data portability | no export or import |

### 1.3 Observations

- The fixed list ordering hides urgent deadlines. A task due tomorrow with
  priority 1 can appear below an undated priority-5 task.
- Deadlines are stored but are not actionable: there are no overdue/today/soon
  filters, reminders, or deadline-oriented sorting.
- The state machine is intentionally strict, but `done` is effectively a
  one-way door. Reopening is a common expectation and a very small change.
- The edit modal is already close to its practical capacity. Tags, comments,
  history, and sharing will require a richer task detail surface.
- `TaskService` now carries both task and user/auth use cases. It is still
  manageable, but splitting the ports before adding RBAC, password reset, or
  sharing would reduce coupling.
- Any change to `TaskFilter` or the repository port must be reflected in every
  backend and in `internal/repository/conformance`; this suite is the main
  guard against backend drift.
- Documentation drift: `README.md` lists `GET/POST /app/logout`, while the
  router correctly registers only `POST /app/logout`. The README should be
  corrected to match the implementation.

---

## 2. Prioritization model

Features are grouped into three releases:

| Release | Theme | Goal |
|---|---|---|
| 1 | Daily-driver productivity | Make the existing task model faster and safer to use |
| 2 | Organization & collaboration | Add structure, context, and shared work |
| 3 | Automation, security & operations | Add proactive behavior, hardening, and integration quality |

Within each release, proposals are ordered by expected value-to-effort ratio.
Items that require schema changes are called out explicitly because this project
supports five storage backends.

---

## 3. Release 1 — Daily-driver productivity

### 3.1 Reopen completed tasks

**Status:** delivered in Release 1.

**Priority:** highest / smallest change.

Allow `done → in_progress` and/or `done → todo`. The exact transition should be
decided in the feature spec; allowing `done → in_progress` preserves the idea
that work restarts, while `done → todo` supports a full reset.

**Why**

- Users inevitably complete a task prematurely.
- Currently the only escape from `done` is `archived`, which is terminal.
- The domain already owns the transition table, so this is a clean,
  well-contained change.

**Likely impact**

- Add transitions to `allowedTransitions` in `internal/domain/task.go`.
- Update the web `transitionTable` in
  `internal/adapters/httpweb/views.go`.
- Add/update domain and adapter tests.
- No schema or repository changes.

**Acceptance criteria**

- A completed task can return to active work through every adapter.
- The UI renders the new action only where legal.
- Illegal transitions still return `CONFLICT`.
- Existing archived behavior remains terminal.
- Conformance tests remain green (no repository contract change expected).

---

### 3.2 Due-date intelligence

**Status:** delivered in Release 1.

**Priority:** high / low-to-medium effort.

Turn stored deadlines into actionable information:

- Filters for **Overdue**, **Due today**, and **Due this week**.
- A “Due soon” counter in the stats strip.
- Visual emphasis on overdue and imminent deadlines.
- Optional default sort by deadline.

**Why**

- Deadlines already exist but currently provide almost no product value.
- Overdue work is the most important signal in a task manager.
- This is additive and does not require a schema migration.

**Likely impact**

- Extend `repository.TaskFilter` with a due-date range or semantic due bucket.
- Implement in the shared SQL core, MongoDB adapter, and memory adapter.
- Extend `TaskStats` and `toStatsView` for due-soon/overdue counts.
- Add filter controls and badges in the web templates.
- Expose equivalent filters in the REST API and CLI.

**Acceptance criteria**

- “Overdue” includes tasks with a deadline before today and a non-terminal
  status; the handling of completed/archived tasks is explicit.
- “Due today” and “Due this week” use a documented timezone policy.
- All backends return identical results for the same filter.
- The stats strip reflects the new counts and remains consistent after
  mutations.
- REST, CLI, and web UI use the same filter semantics.

---

### 3.3 User-selectable sorting

**Status:** delivered in Release 1.

**Priority:** high / low effort.

Allow sorting by:

- priority,
- created date,
- updated date,
- deadline,
- title,

with ascending/descending direction. Keep the current default ordering for
backward compatibility.

**Why**

- Fixed ordering is the main reason urgent deadlines can be hidden.
- Sorting is a baseline expectation for a task list.
- It pairs naturally with due-date intelligence.

**Likely impact**

- Extend `TaskFilter` with sort field and direction.
- Update the shared SQL `ORDER BY` builder, MongoDB sort document, and memory
  comparator.
- Add a sort control to the web filter bar and preserve it in URL state.
- Add `?sort=` and `?order=` to the REST API and CLI.

**Acceptance criteria**

- Sorting is stable and deterministic across backends.
- Invalid sort fields/directions return `INVALID_ARGUMENT`, not storage errors.
- The selected sort survives filtering, paging, and mutation re-renders.
- The default remains priority desc, created_at desc.

---

### 3.4 Tags / labels

**Priority:** high / medium-high effort.

Add user-defined labels with:

- create/edit tag selection on tasks,
- tag filter chips in the list toolbar,
- optional tag color or a deterministic color assignment,
- tag display on task rows and detail views.

**Why**

- Tags are the lightest-weight way to organize work.
- They avoid committing to a rigid project hierarchy too early.
- They enable cross-cutting views such as `bug`, `customer`, `errand`, or
  `waiting`.

**Likely impact**

- New domain concept (`Tag` or a normalized label value on `Task`).
- Schema migration: either a task-tag join table (normalized) or embedded tag
  arrays (simpler for MongoDB, less relational). A join table is recommended
  for the SQL backends.
- Repository port additions for tag listing and filtering.
- Extend `TaskFilter` with tag filters.
- Update the REST DTOs, CLI output/options, and web forms/rows.
- Extend the conformance suite.

**Acceptance criteria**

- Tags are scoped per user (or explicitly shared, if that is decided later).
- Tag names are normalized consistently (trim/case policy documented).
- Filtering by one or more tags works identically on every backend.
- Deleting a task does not leave orphaned tag links.
- The task row/detail view clearly displays tags without crowding the list.
- Duplicate tag creation is prevented where appropriate.

---

### 3.5 Bulk actions

**Priority:** medium-high / medium effort.

Support multi-select with bulk:

- status changes,
- priority changes,
- tag assignment,
- delete/move-to-trash.

**Why**

- The current UI becomes tedious at 20+ tasks.
- Bulk actions are especially valuable after filtering or during weekly review.

**Likely impact**

- Primarily a web-adapter feature using the existing application methods.
- Add batch endpoints or loop server-side with per-item results.
- Add selection state and a bulk-action toolbar to the HTMX UI.
- Decide explicitly whether the operation is best-effort or transactional.
- Bulk delete should integrate with the trash feature if both are implemented.

**Acceptance criteria**

- Selection persists across paging only if explicitly designed to do so.
- “Select all filtered” has a documented scope and a clear count.
- Partial failures are reported without leaving the UI inconsistent.
- Bulk operations respect ownership and admin rules exactly like single-item
  operations.
- The list, stats, and toast regions refresh after the operation.

---

### 3.6 Trash / soft delete with restore

**Priority:** medium-high / medium effort, schema change.

Replace immediate hard delete with:

- soft-deleted tasks,
- a Trash view,
- restore,
- permanent delete,
- an optional undo window in the UI.

**Why**

- Deletion is currently destructive and irreversible.
- Users expect either undo or a trash bin.
- It provides a safer foundation for bulk delete.

**Likely impact**

- Add `deleted_at` (or equivalent) to the domain and schema.
- Every list/count/search query must exclude deleted tasks by default.
- Add restore and permanent-delete use cases.
- Extend `TaskFilter` with an explicit trash scope.
- Update REST, CLI, and web UI.
- Extend the conformance suite for filtering and restore semantics.
- Consider retention policy and cleanup for permanently deleted rows.

**Acceptance criteria**

- Deleted tasks disappear from normal lists, stats, and search.
- The Trash view lists only deleted tasks owned by the caller (or all, for
  admins, according to the ownership rules).
- Restore returns a task to normal listing with a sensible status.
- Permanent delete is clearly distinguished from soft delete.
- Ownership checks apply to restore and permanent delete.
- Admin cascade behavior when deleting a user is explicitly defined.

---

### 3.7 Quick-add and keyboard shortcuts

**Priority:** medium / low effort.

Add:

- a global quick-add input or shortcut,
- `/` to focus search,
- `n` for new task,
- `e` to edit the focused task,
- `Esc` to close modals,
- `?` to show a shortcut help dialog.

**Why**

- Cheap UX polish for a keyboard-driven task manager.
- Complements the existing modal and HTMX interactions.

**Likely impact**

- Web template/JavaScript only.
- Keep shortcuts out of text inputs unless explicitly intended.
- Ensure shortcuts do not conflict with browser or assistive-technology
  bindings.
- Document shortcuts in the UI and README.

**Acceptance criteria**

- Shortcuts work from the main task page and are disabled while typing in form
  fields.
- Dialogs are closable with `Esc` and remain accessible.
- The help dialog lists all shortcuts.
- No shortcut triggers a destructive action without confirmation.

---

## 4. Release 2 — Organization & collaboration

### 4.1 Projects / lists

**Priority:** high / high effort.

Introduce a `Project` entity with:

- name, optional description, color,
- owner and later optional members,
- exactly one project per task, or an explicit “Inbox” for unassigned tasks.

**Why**

- Tags answer “what kind of task”; projects answer “which body of work”.
- Projects are simpler than full workspaces and fit the existing owner model.
- They enable per-project stats, filtering, and navigation.

**Likely impact**

- New domain entity and repository methods.
- Schema migration and foreign-key/cascade decisions.
- Extend `TaskFilter` with a project scope.
- Add project navigation/sidebar to the web UI and project fields to REST/CLI.
- Update conformance tests.
- Decide how deleting a project affects its tasks (reassign to Inbox, block, or
  cascade).

**Acceptance criteria**

- Every task belongs to zero or one project according to the chosen model.
- Users can only see and select their own projects unless sharing is enabled.
- Project filters combine correctly with status, search, tags, and sorting.
- Project deletion has documented, tested task semantics.
- The UI makes the current project obvious.

---

### 4.2 Subtasks / checklists

**Priority:** medium-high / medium effort.

Start with lightweight checklist items inside a task:

- text,
- done/not done,
- ordering,
- optional progress indicator on the parent row.

Full parent-child tasks should be a later step because hierarchy introduces
cycle prevention, ownership inheritance, and recursive queries.

**Why**

- Reduces task fragmentation without introducing full project management.
- A checklist is often enough for “definition of done” details.

**Likely impact**

- Add a checklist value object or child entity to the domain.
- Persist as embedded JSON in relational stores or as a child table; choose
  deliberately because both approaches affect querying and conformance.
- Add to task detail UI and REST DTOs.
- Decide whether checklist completion affects task status automatically.

**Acceptance criteria**

- Checklist items can be added, edited, reordered, toggled, and deleted.
- Progress is visible on the task row/detail view.
- Checklist updates respect the same ownership and terminal-state rules as the
  task, or those rules are explicitly documented differently.
- All backends round-trip checklist data identically.

---

### 4.3 Comments and activity history

**Priority:** medium-high / medium effort.

Add:

- per-task comments,
- an append-only activity log for creation, status, priority, deadline, tags,
  assignment, and deletion changes.

**Why**

- Provides context and an audit trail.
- Prerequisite for meaningful collaboration and notifications.
- Makes the task detail surface useful.

**Likely impact**

- New `Comment` and `Activity` entities plus repository methods.
- Render as a DaisyUI timeline/drawer on a task detail page or drawer.
- Activity entries should be written by the application layer, not by
  repositories, so the source of truth stays explicit.
- Consider pagination and retention for long activity streams.

**Acceptance criteria**

- Comments are ordered chronologically and scoped to the task.
- Activity entries record actor, action, timestamp, and relevant before/after
  values without leaking password or API-key material.
- Users can read activity only for tasks they can access.
- Deleting a task defines whether comments/activity are deleted, retained, or
  archived.
- The activity log does not allow client-side forgery of actor or timestamp.

---

### 4.4 Task detail page or drawer

**Priority:** medium / enabling feature.

Add a dedicated task view at `/app/tasks/:id` or a large drawer containing:

- full description,
- status, priority, deadline, tags, project,
- checklist,
- comments,
- activity,
- actions.

**Why**

- The current edit modal cannot comfortably host tags, comments, history, and
  sharing.
- It provides a natural HTMX target for fragments and real-time updates.

**Likely impact**

- New full-page route plus fragment route.
- Reuse existing mutation and error-rendering patterns.
- Ensure direct-link authorization and 404 behavior are correct.
- Add navigation from task rows.

**Acceptance criteria**

- Direct navigation works without JavaScript and degrades to full-page loads.
- HTMX navigation updates the drawer without duplicating page state.
- Ownership/admin rules are enforced on every fragment endpoint.
- The page remains usable on mobile widths.

---

### 4.5 Shared tasks / assignment

**Priority:** medium / high effort and high authorization impact.

Start with **assignee only**: the task remains owned by its creator but can be
assigned to another user. Later, add explicit sharing with read/write roles.

**Why**

- Current ownership is strict 1-to-1; teams need delegation.
- Assignee-only avoids immediately redesigning the full ownership model.

**Likely impact**

- Add `assignee_id` and authorization rules for visibility and mutation.
- Update `Caller` / ownership checks in the application layer.
- Extend filters for “assigned to me”.
- Update all adapters and conformance tests.
- Decide whether assignees can edit, only comment, or only update status.

**Acceptance criteria**

- A user can see tasks assigned to them according to the documented visibility
  rule.
- Mutating another user's task requires explicit permission, not merely being
  assigned.
- Admin behavior remains consistent.
- Assignment changes appear in the activity log.
- Deleting an assignee has defined task semantics.

---

## 5. Release 3 — Automation, security & operations

### 5.1 Calendar and Kanban views

**Priority:** medium-high / presentation-heavy.

- **Calendar:** month/week views grouped by deadline.
- **Kanban:** columns by status with HTMX drag-and-drop status changes.

**Why**

- The state machine maps cleanly to Kanban columns.
- Deadlines are inherently calendar-shaped.
- Offers two high-value views without changing core business rules.

**Likely impact**

- Add a view toggle and presentation templates.
- Kanban drag-and-drop must call the same status endpoint and handle illegal
  transitions with visible errors.
- Calendar needs a documented timezone and “no deadline” bucket.
- Keep list view as the canonical fallback.

**Acceptance criteria**

- Kanban only permits legal transitions and shows a clear error for illegal
  drops.
- Calendar and Kanban respect all active filters.
- Both views degrade to usable non-JavaScript alternatives or redirect to the
  list.
- URL state captures the active view.

---

### 5.2 Reminders and notifications

**Priority:** medium-high / higher effort.

Add:

- reminders before a deadline,
- a daily digest of overdue/today tasks,
- browser notifications and/or email delivery,
- per-user notification preferences.

**Why**

- Deadlines are currently passive.
- Reminders turn the app from a passive record into an assistant.

**Likely impact**

- Requires a scheduler/background worker and a notification outbound port.
- Add notification preference storage and delivery tracking.
- Keep the domain pure; delivery belongs to an adapter.
- Decide on idempotency so a reminder is not sent twice.
- Add email/provider abstraction if email is in scope.

**Acceptance criteria**

- Reminder timing uses a documented timezone and deadline semantics.
- Completed, archived, and deleted tasks do not generate reminders.
- Duplicate delivery is prevented across restarts.
- Users can disable or tune notifications.
- Delivery failures are logged without exposing sensitive data.

---

### 5.3 Recurring tasks

**Priority:** medium / medium-high effort.

Support simple recurrence (daily, weekly, monthly, weekdays) or an RRULE-style
subset.

**Why**

- Common for maintenance, routines, and recurring reviews.
- Complements deadlines and reminders.

**Likely impact**

- New domain concept plus a materialization strategy.
- Prefer creating the next occurrence on completion or via a scheduler rather
  than mutating historical instances.
- Define how editing a recurring series affects future occurrences.
- Extend REST/CLI/UI and conformance tests.

**Acceptance criteria**

- Recurrence rules are validated at creation.
- The next occurrence is created exactly once per cycle.
- Historical completed instances remain intact.
- Stopping or editing a series has documented behavior.
- Timezone and month-end behavior are explicit.

---

### 5.4 Time tracking / estimates

**Priority:** medium / medium effort.

Add estimated minutes and actual time entries, with a weekly summary.

**Why**

- Useful for personal planning and later billing.
- Pairs well with projects and stats.

**Likely impact**

- Add fields to the task plus a time-entry entity for accurate tracking.
- Add start/stop timer support or manual entry, not both initially.
- Extend stats aggregation and export formats.

**Acceptance criteria**

- Time entries are owned by the user who recorded them.
- Totals are consistent across backends and displayed per task/project.
- Starting a timer twice is either prevented or explicitly handled.
- Time data is included in export/import.

---

### 5.5 Real-time updates with SSE

**Priority:** medium / optional.

Push task mutations to other open tabs or collaborators using HTMX SSE.

**Why**

- Multiple tabs can silently diverge today.
- HTMX supports SSE cleanly and matches the existing architecture.

**Likely impact**

- Add an in-process event bus and an SSE endpoint.
- Scope events per user (and later per project/shared task).
- Keep the feature optional and leave normal request/response behavior intact.
- Avoid WebSockets unless bidirectional collaboration becomes necessary.

**Acceptance criteria**

- Changes in one tab are reflected in another without a manual refresh.
- Events are authorized and never leak another user's tasks.
- Reconnecting clients recover consistently.
- The app remains fully usable if the SSE stream is unavailable.

---

### 5.6 Import / export

**Priority:** medium / low-to-medium effort.

Add JSON and CSV export and a validated JSON/CSV import with dry-run preview.

**Why**

- Data portability, backup, migrations, and demos.
- Reuses domain hydration validation.

**Likely impact**

- New application use cases plus adapter endpoints and CLI commands.
- Define a stable export schema with versioning.
- Validate imported records through domain factories rather than raw inserts.
- Decide how conflicts, duplicate IDs, and user/task ownership are handled.

**Acceptance criteria**

- Export produces a documented, versioned format.
- Import can be previewed before commit.
- Invalid rows are reported with actionable line-level errors.
- Import never bypasses domain validation or ownership rules.
- Round-tripping export → import preserves task semantics.

---

### 5.7 Security hardening

The `auth.md` out-of-scope list is a good source of follow-up work. Prioritize:

1. **Login rate limiting / lockout** — protects bcrypt-backed authentication
   and reduces credential-stuffing risk.
2. **Password reset** — requires an email abstraction and time-limited,
   single-use reset tokens.
3. **Session revocation** — introduce a session ID or user auth-version check
   if immediate logout-everywhere is required.
4. **Email verification** — only necessary if self-registration is added.
5. **RBAC beyond `isadmin`** — only after shared tasks/projects exist.

**Why**

- The current implementation is intentionally minimal and documents these as
  deferred.
- Rate limiting is the highest-value, lowest-scope item.
- Password reset and session revocation become more important as the user base
  grows.

**Acceptance criteria**

- Rate limiting is per-account and per-source-IP, with documented thresholds
  and safe retry behavior.
- Reset tokens are single-use, expire quickly, and never reveal account
  existence.
- Revoking a session invalidates it on the next request.
- New roles are additive and do not silently broaden existing admin behavior.
- Security events are logged without secrets.

---

### 5.8 API quality improvements

**Priority:** medium / enabling.

Add:

- OpenAPI/Swagger schema and generated docs,
- ETag/`If-Match` optimistic concurrency,
- `Idempotency-Key` support for safe retries,
- cursor pagination for large datasets,
- consistent `?sort=` and `?tag=` filters across REST, CLI, and web.

**Why**

- The REST API is already a first-class adapter; clients need stable contracts.
- Concurrent edits are currently last-write-wins.
- Safe retries matter for mobile and integration clients.

**Likely impact**

- Version the API contract and document error codes.
- Add `updated_at`-based ETags; reject stale `If-Match` with `CONFLICT`.
- Store idempotency records with a TTL.
- Keep offset pagination for compatibility while adding cursor support.

**Acceptance criteria**

- OpenAPI output matches actual request/response shapes.
- Stale updates return `409 CONFLICT` instead of silently overwriting.
- Retried mutations with the same idempotency key produce one effect.
- Cursor and offset pagination return equivalent membership for the same query.
- REST, CLI, and web share filter/sort semantics.

---

## 6. Suggested implementation order

```text
1. Reopen tasks                  (tiny, immediate UX win)
2. Due-date filters + sort       (uses existing deadline data)
3. Trash / restore               (safety + schema migration practice)
4. Tags                          (organization foundation)
5. Bulk actions                  (web productivity)
6. Task detail drawer/page       (enables comments/activity)
7. Projects                      (major organizational step)
8. Comments + activity           (collaboration foundation)
9. Assignee/sharing              (authorization expansion)
10. Kanban/calendar              (presentation leverage)
11. Reminders/recurrence         (background infrastructure)
12. Auth hardening + API quality (operational maturity)
```

This order deliberately front-loads low-risk changes that improve daily use,
then introduces schema changes one at a time. It also defers background
infrastructure until the task model and detail surface are mature enough to
support notifications meaningfully.

---

## 7. Architectural guidance

These rules apply to every proposal above.

### 7.1 Keep business rules in the domain

- New state transitions, deletion semantics, recurrence rules, and permission
  rules belong in `internal/domain` or `internal/application`.
- Adapters must not decide what is legal based on UI convenience.
- Presentation knowledge (badges, labels, button styles) stays in
  `internal/adapters/httpweb/views.go`.

### 7.2 Treat the repository contract as a compatibility surface

- Any extension to `repository.TaskRepository` or `TaskFilter` must be
  implemented in **all** backends: `sqldb` (SQLite/Postgres/Oracle), `mongo`,
  and `memory`.
- Add corresponding cases to `internal/repository/conformance` in the same
  change.
- Prefer additive changes with sensible defaults so existing callers and
  databases keep working.
- Keep schema setup idempotent and migration-safe.

### 7.3 Split application ports before they grow further

`TaskService` currently carries task, user, and auth use cases. Before adding
sharing, RBAC, password reset, or notification preferences, consider splitting
into:

- `TaskService` — task CRUD, queries, stats,
- `UserService` — user management,
- `AuthService` — login, API-key lookup, password flows,
- `NotificationService` / `ProjectService` as needed.

This keeps adapters from depending on unrelated capabilities and makes
authorization easier to reason about.

### 7.4 Preserve the web adapter’s interaction contract

- Continue the fragment/full-page dual-render pattern.
- Mutations should refresh every region that can change: list, stats, and
  toast, or targeted regions with a documented reason.
- Keep PRG fallback for non-JavaScript clients.
- Keep CSRF protection on every state-changing route.
- Preserve URL state for filters, sorting, paging, and views.
- Ensure new HTMX fragments are authorized individually; a fragment endpoint is
  still an endpoint.

### 7.5 Define cross-cutting semantics once

For every feature that touches time, ownership, or deletion, document the
policy in one place and reuse it:

- timezone handling for deadlines and reminders,
- what “overdue” means for terminal statuses,
- whether archived tasks appear in stats/search/tags,
- ownership versus assignment versus sharing,
- soft-delete retention and cascade behavior,
- idempotency and concurrent-update behavior.

### 7.6 Keep documentation in sync

- Update `README.md` alongside adapter behavior changes.
- Record new environment variables, routes, flags, and API contracts.
- Fix known drift: the README lists `GET/POST /app/logout`, but only `POST` is
  implemented.
- Add a changelog entry for breaking changes, especially CLI flags and schema
  migrations.

---

## 8. Recommended first slice

The best initial implementation is a combined **“daily-driver” slice**:

1. Allow reopening completed tasks.
2. Add due-date filters and due-soon/overdue stats.
3. Add user-selectable sorting.

Why this slice first:

- It requires little or no schema change.
- It delivers immediate visible value.
- It exercises the full architecture safely: domain transition tables,
  application filters, all repository backends, conformance tests, REST/CLI
  parameters, and HTMX UI state.
- It establishes patterns for extending `TaskFilter` and URL state that later
  features (tags, projects, trash) can reuse.

A follow-up slice should add **trash/restore**, because it is the first
high-value feature that requires a careful schema migration and a new
cross-cutting query rule (“exclude deleted by default”), making it a good
rehearsal for tags and projects.

---

## 9. Summary

The project is already a capable, well-architected task manager. The highest
near-term value comes not from adding a large new subsystem, but from making
existing data actionable:

- deadlines should drive filters, sorting, and reminders;
- completed work should be reopenable;
- destructive actions should be reversible;
- organization should grow from tags into projects;
- collaboration should grow from comments/activity into assignment and sharing.

The roadmap above sequences those changes so each release remains shippable,
testable across all backends, and consistent with the hexagonal architecture.
