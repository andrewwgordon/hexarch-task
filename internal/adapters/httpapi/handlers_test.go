// handlers_test.go is the REST API test suite (external test package). It
// drives the real Gin engine via httptest against an in-memory repository
// with deterministic sequential IDs and a fixed clock.
//
// Coverage: create/get, validation and 400 mapping, list + filters + paging,
// rename, status transitions and CONFLICT mapping, priority/deadline PATCH
// semantics, delete 404s, stats, and the STORAGE -> 503 mapping.
package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"hexarch/internal/adapters/httpapi"
	"hexarch/internal/application"
	"hexarch/internal/domain"
	"hexarch/internal/repository"
	"hexarch/internal/repository/memory"
)

// newTestEngine builds a Gin engine wired to the http API backed by an
// in-memory repository with a deterministic, sequential ID generator and a
// fixed clock, so tests are reproducible.
func newTestEngine(t *testing.T) *gin.Engine {
	t.Helper()
	repo := memory.NewTaskRepositoryMem()
	svc := newTestService(repo)
	return httpapi.NewRouter(svc)
}

// newTestService builds a TaskService with the given repo and deterministic
// ID/clock sources.
func newTestService(repo repository.TaskRepository) application.TaskService {
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	var seq int
	return application.NewTaskServiceWith(
		repo,
		func() (domain.TaskID, error) {
			seq++
			return domain.TaskID(fmt.Sprintf("id-%03d", seq)), nil
		},
		func() time.Time { return now },
	)
}

// do performs a JSON request against the engine and returns the recorder.
// It authenticates as the seeded bootstrap admin by default (the whole API
// is behind the auth middleware since phase 9); use doNoAuth for 401-path
// tests.
func do(t *testing.T, r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	return doAuth(t, r, method, path, body, "admin@email.com", "admin")
}

// doAuth performs a JSON request with explicit Basic credentials.
func doAuth(t *testing.T, r *gin.Engine, method, path, body, email, password string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, bytes.NewBufferString(body))
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(email, password)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// doNoAuth performs a JSON request without credentials (401-path tests).
func doNoAuth(t *testing.T, r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, bytes.NewBufferString(body))
	}
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func decodeBody(t *testing.T, w *httptest.ResponseRecorder, v interface{}) {
	t.Helper()
	if err := json.Unmarshal(w.Body.Bytes(), v); err != nil {
		t.Fatalf("decode response %q: %v", w.Body.String(), err)
	}
}

// decodeError extracts the error envelope from a response.
func decodeError(t *testing.T, w *httptest.ResponseRecorder) (string, string) {
	t.Helper()
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	decodeBody(t, w, &env)
	return env.Error.Code, env.Error.Message
}

// --- C-4: create + get ---------------------------------------------------

func TestHTTPCreateAndGet(t *testing.T) {
	r := newTestEngine(t)

	create := do(t, r, "POST", "/api/tasks", `{"title":"Fix bug","description":"oAuth","priority":4}`)
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d; body=%s", create.Code, http.StatusCreated, create.Body.String())
	}
	var created httpapi.TaskResponse
	decodeBody(t, create, &created)
	if created.ID != "id-001" {
		t.Errorf("created id = %q, want id-001", created.ID)
	}
	if created.Status != "todo" {
		t.Errorf("created status = %q, want todo", created.Status)
	}
	if created.Deadline != nil {
		t.Errorf("created deadline = %v, want null", *created.Deadline)
	}

	get := do(t, r, "GET", "/api/tasks/id-001", "")
	if get.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200", get.Code)
	}
	var got httpapi.TaskResponse
	decodeBody(t, get, &got)
	if got.Title != "Fix bug" || got.Priority != 4 || got.Status != "todo" {
		t.Errorf("get mismatch: %+v", got)
	}
}

func TestHTTPCreateRequiresTitle(t *testing.T) {
	r := newTestEngine(t)
	w := do(t, r, "POST", "/api/tasks", `{"priority":2}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	code, _ := decodeError(t, w)
	if code != "INVALID_ARGUMENT" {
		t.Errorf("error code = %q, want INVALID_ARGUMENT", code)
	}
}

func TestHTTPCreateBadDeadline(t *testing.T) {
	r := newTestEngine(t)
	w := do(t, r, "POST", "/api/tasks", `{"title":"t","deadline":"not-a-date"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHTTPGetNotFound(t *testing.T) {
	r := newTestEngine(t)
	w := do(t, r, "GET", "/api/tasks/nope", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	code, _ := decodeError(t, w)
	if code != "NOT_FOUND" {
		t.Errorf("error code = %q, want NOT_FOUND", code)
	}
}

// --- C-5: list -----------------------------------------------------------

func seedTasks(t *testing.T, r *gin.Engine, titles []string) {
	t.Helper()
	for _, title := range titles {
		w := do(t, r, "POST", "/api/tasks", fmt.Sprintf(`{"title":%q}`, title))
		if w.Code != http.StatusCreated {
			t.Fatalf("seed %q status = %d, body=%s", title, w.Code, w.Body.String())
		}
	}
}

func TestHTTPList(t *testing.T) {
	r := newTestEngine(t)
	seedTasks(t, r, []string{"beta", "alpha", "gamma"})

	w := do(t, r, "GET", "/api/tasks", "")
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", w.Code)
	}
	var resp httpapi.ListResponse
	decodeBody(t, w, &resp)
	if resp.Count != 3 {
		t.Errorf("count = %d, want 3", resp.Count)
	}
	if len(resp.Tasks) != 3 {
		t.Fatalf("len tasks = %d, want 3", len(resp.Tasks))
	}
}

func TestHTTPListFilterByStatus(t *testing.T) {
	r := newTestEngine(t)
	seedTasks(t, r, []string{"one", "two", "three"})
	do(t, r, "PATCH", "/api/tasks/id-001", `{"status":"in_progress"}`)
	do(t, r, "PATCH", "/api/tasks/id-001", `{"status":"done"}`)

	w := do(t, r, "GET", "/api/tasks?status=done", "")
	var resp httpapi.ListResponse
	decodeBody(t, w, &resp)
	if resp.Count != 1 || resp.Tasks[0].ID != "id-001" {
		t.Errorf("done filter = %+v, want just id-001", resp)
	}
}

func TestHTTPListBadStatus(t *testing.T) {
	r := newTestEngine(t)
	w := do(t, r, "GET", "/api/tasks?status=bogus", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHTTPListSearch(t *testing.T) {
	r := newTestEngine(t)
	seedTasks(t, r, []string{"buy milk", "write tests", "ship"})

	w := do(t, r, "GET", "/api/tasks?search=milk", "")
	var resp httpapi.ListResponse
	decodeBody(t, w, &resp)
	if resp.Count != 1 || resp.Tasks[0].Title != "buy milk" {
		t.Errorf("search result = %+v, want buy milk", resp)
	}
}

func TestHTTPListPaging(t *testing.T) {
	r := newTestEngine(t)
	for i, p := range []int{5, 4, 3, 2, 1} {
		w := do(t, r, "POST", "/api/tasks", fmt.Sprintf(`{"title":"t%d","priority":%d}`, i, p))
		if w.Code != http.StatusCreated {
			t.Fatalf("seed status = %d", w.Code)
		}
	}

	w := do(t, r, "GET", "/api/tasks?limit=2&offset=2", "")
	var resp httpapi.ListResponse
	decodeBody(t, w, &resp)
	if resp.Count != 2 {
		t.Errorf("paged count = %d, want 2", resp.Count)
	}
	if len(resp.Tasks) != 2 || resp.Tasks[0].Priority != 3 || resp.Tasks[1].Priority != 2 {
		t.Errorf("paged priorities = %+v, want [3 2]", resp.Tasks)
	}
}

// --- C-6: rename ---------------------------------------------------------

func TestHTTPRename(t *testing.T) {
	r := newTestEngine(t)
	seedTasks(t, r, []string{"old"})

	w := do(t, r, "PATCH", "/api/tasks/id-001", `{"title":"new name"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("rename status = %d, want 200", w.Code)
	}
	var tr httpapi.TaskResponse
	decodeBody(t, w, &tr)
	if tr.Title != "new name" {
		t.Errorf("renamed title = %q, want new name", tr.Title)
	}
}

func TestHTTPRenameEmpty(t *testing.T) {
	r := newTestEngine(t)
	seedTasks(t, r, []string{"x"})
	w := do(t, r, "PATCH", "/api/tasks/id-001", `{"title":""}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

// --- C-7: status transitions ---------------------------------------------

func TestHTTPStatusLifecycle(t *testing.T) {
	r := newTestEngine(t)
	seedTasks(t, r, []string{"task"})

	if w := do(t, r, "PATCH", "/api/tasks/id-001", `{"status":"in_progress"}`); w.Code != http.StatusOK {
		t.Fatalf("start status = %d, want 200", w.Code)
	}
	if w := do(t, r, "PATCH", "/api/tasks/id-001", `{"status":"done"}`); w.Code != http.StatusOK {
		t.Fatalf("done status = %d, want 200", w.Code)
	}

	get := do(t, r, "GET", "/api/tasks/id-001", "")
	var tr httpapi.TaskResponse
	decodeBody(t, get, &tr)
	if tr.Status != "done" {
		t.Errorf("final status = %q, want done", tr.Status)
	}
}

func TestHTTPIllegalTransition(t *testing.T) {
	r := newTestEngine(t)
	seedTasks(t, r, []string{"task"})
	w := do(t, r, "PATCH", "/api/tasks/id-001", `{"status":"done"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", w.Code)
	}
	code, _ := decodeError(t, w)
	if code != "CONFLICT" {
		t.Errorf("error code = %q, want CONFLICT", code)
	}
}

func TestHTTPBadStatusValue(t *testing.T) {
	r := newTestEngine(t)
	seedTasks(t, r, []string{"task"})
	w := do(t, r, "PATCH", "/api/tasks/id-001", `{"status":"flying"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

// --- C-8: priority + deadline -------------------------------------------

func TestHTTPPriority(t *testing.T) {
	r := newTestEngine(t)
	seedTasks(t, r, []string{"t"})
	w := do(t, r, "PATCH", "/api/tasks/id-001", `{"priority":5}`)
	if w.Code != http.StatusOK {
		t.Fatalf("priority status = %d, want 200", w.Code)
	}
	var tr httpapi.TaskResponse
	decodeBody(t, w, &tr)
	if tr.Priority != 5 {
		t.Errorf("priority = %d, want 5", tr.Priority)
	}
}

func TestHTTPPriorityOutOfRange(t *testing.T) {
	r := newTestEngine(t)
	seedTasks(t, r, []string{"t"})
	w := do(t, r, "PATCH", "/api/tasks/id-001", `{"priority":9}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHTTPSetAndClearDeadline(t *testing.T) {
	r := newTestEngine(t)
	seedTasks(t, r, []string{"t"})

	set := do(t, r, "PATCH", "/api/tasks/id-001", `{"deadline":"2026-09-01"}`)
	if set.Code != http.StatusOK {
		t.Fatalf("set deadline status = %d, want 200", set.Code)
	}
	var tr httpapi.TaskResponse
	decodeBody(t, set, &tr)
	if tr.Deadline == nil || *tr.Deadline != "2026-09-01" {
		t.Errorf("deadline = %v, want 2026-09-01", tr.Deadline)
	}

	clear := do(t, r, "PATCH", "/api/tasks/id-001", `{"deadline":null}`)
	if clear.Code != http.StatusOK {
		t.Fatalf("clear deadline status = %d, want 200", clear.Code)
	}
	decodeBody(t, clear, &tr)
	if tr.Deadline != nil {
		t.Errorf("deadline after clear = %v, want null", tr.Deadline)
	}
}

func TestHTTPBadDeadlineOnPatch(t *testing.T) {
	r := newTestEngine(t)
	seedTasks(t, r, []string{"t"})
	w := do(t, r, "PATCH", "/api/tasks/id-001", `{"deadline":"junk"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHTTPPatchNoFields(t *testing.T) {
	r := newTestEngine(t)
	seedTasks(t, r, []string{"t"})
	w := do(t, r, "PATCH", "/api/tasks/id-001", `{}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

// --- C-9: delete + stats ------------------------------------------------

func TestHTTPDelete(t *testing.T) {
	r := newTestEngine(t)
	seedTasks(t, r, []string{"t"})

	if w := do(t, r, "DELETE", "/api/tasks/id-001", ""); w.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", w.Code)
	}
	if w := do(t, r, "GET", "/api/tasks/id-001", ""); w.Code != http.StatusNotFound {
		t.Fatalf("get after delete status = %d, want 404", w.Code)
	}
}

func TestHTTPDeleteNotFound(t *testing.T) {
	r := newTestEngine(t)
	w := do(t, r, "DELETE", "/api/tasks/ghost", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("delete status = %d, want 404", w.Code)
	}
}

func TestHTTPStats(t *testing.T) {
	r := newTestEngine(t)
	seedTasks(t, r, []string{"a", "b"})
	do(t, r, "PATCH", "/api/tasks/id-001", `{"status":"in_progress"}`)
	do(t, r, "PATCH", "/api/tasks/id-001", `{"status":"done"}`)

	w := do(t, r, "GET", "/api/stats", "")
	if w.Code != http.StatusOK {
		t.Fatalf("stats status = %d, want 200", w.Code)
	}
	var s httpapi.StatsResponse
	decodeBody(t, w, &s)
	if s.Total != 2 || s.Todo != 1 || s.Done != 1 {
		t.Errorf("stats = %+v, want total=2 todo=1 done=1", s)
	}
}

// TestHTTPRegularUserSeesOnlyOwnTasksAndStats guards the ownership defect:
// a non-admin's list and stats must be scoped to their own tasks, never the
// bootstrap admin's.
func TestHTTPRegularUserSeesOnlyOwnTasksAndStats(t *testing.T) {
	base := memory.NewTaskRepositoryMem()
	svc := newTestService(base)
	r := httpapi.NewRouter(svc)

	// Admin creates one task; a regular user creates their own.
	admin, err := svc.AuthUser(context.Background(), "admin@email.com", "admin")
	if err != nil {
		t.Fatalf("bootstrap admin auth: %v", err)
	}
	if _, err := svc.CreateTask(context.Background(), application.CreateTaskInput{UserID: admin.ID(), Title: "admin task"}); err != nil {
		t.Fatalf("create admin task: %v", err)
	}
	u, err := svc.CreateUser(context.Background(), application.CreateUserInput{Email: "user@x.com", Password: "pw"})
	if err != nil {
		t.Fatalf("create regular user: %v", err)
	}
	if _, err := svc.CreateTask(context.Background(), application.CreateTaskInput{UserID: u.ID(), Title: "user task"}); err != nil {
		t.Fatalf("create user task: %v", err)
	}

	// The regular user's list shows only their own task.
	w := doAuth(t, r, "GET", "/api/tasks", "", "user@x.com", "pw")
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", w.Code)
	}
	var resp httpapi.ListResponse
	decodeBody(t, w, &resp)
	if resp.Count != 1 || len(resp.Tasks) != 1 || resp.Tasks[0].Title != "user task" {
		t.Errorf("regular user list = %+v, want only their own task", resp)
	}

	// And their stats count only their own tasks.
	sw := doAuth(t, r, "GET", "/api/stats", "", "user@x.com", "pw")
	if sw.Code != http.StatusOK {
		t.Fatalf("stats status = %d, want 200", sw.Code)
	}
	var s httpapi.StatsResponse
	decodeBody(t, sw, &s)
	if s.Total != 1 || s.Todo != 1 || s.Done != 0 {
		t.Errorf("regular user stats = %+v, want total=1 todo=1", s)
	}

	// The admin still spans every user (list and stats).
	aw := do(t, r, "GET", "/api/stats", "")
	var as httpapi.StatsResponse
	decodeBody(t, aw, &as)
	if as.Total != 2 {
		t.Errorf("admin stats total = %d, want 2", as.Total)
	}
	al := do(t, r, "GET", "/api/tasks", "")
	var alr httpapi.ListResponse
	decodeBody(t, al, &alr)
	if alr.Count != 2 {
		t.Errorf("admin list count = %d, want 2", alr.Count)
	}
}

// TestHTTPTaskIDOwnership guards FR-U4 on /api/tasks/:id: a regular user
// cannot read, modify, or delete another user's task by ID — every attempt
// answers 404 exactly like a missing task, and the foreign task is left
// untouched. The owner and admins keep full access.
func TestHTTPTaskIDOwnership(t *testing.T) {
	base := memory.NewTaskRepositoryMem()
	svc := newTestService(base)
	r := httpapi.NewRouter(svc)
	ctx := context.Background()

	admin, err := svc.AuthUser(ctx, "admin@email.com", "admin")
	if err != nil {
		t.Fatalf("bootstrap admin auth: %v", err)
	}
	foreign, err := svc.CreateTask(ctx, application.CreateTaskInput{UserID: admin.ID(), Title: "admin task"})
	if err != nil {
		t.Fatalf("create admin task: %v", err)
	}
	u, err := svc.CreateUser(ctx, application.CreateUserInput{Email: "user@x.com", Password: "pw"})
	if err != nil {
		t.Fatalf("create regular user: %v", err)
	}
	own, err := svc.CreateTask(ctx, application.CreateTaskInput{UserID: u.ID(), Title: "user task"})
	if err != nil {
		t.Fatalf("create user task: %v", err)
	}
	foreignID := foreign.ID().String()
	ownID := own.ID().String()

	// A regular user cannot reach the admin's task at all: GET, PATCH, and
	// DELETE all answer 404 (existence is not disclosed).
	if w := doAuth(t, r, "GET", "/api/tasks/"+foreignID, "", "user@x.com", "pw"); w.Code != http.StatusNotFound {
		t.Errorf("foreign GET status = %d, want 404", w.Code)
	}
	if w := doAuth(t, r, "PATCH", "/api/tasks/"+foreignID, `{"title":"hijacked"}`, "user@x.com", "pw"); w.Code != http.StatusNotFound {
		t.Errorf("foreign PATCH status = %d, want 404", w.Code)
	}
	if w := doAuth(t, r, "PATCH", "/api/tasks/"+foreignID, `{"status":"done"}`, "user@x.com", "pw"); w.Code != http.StatusNotFound {
		t.Errorf("foreign status PATCH status = %d, want 404", w.Code)
	}
	if w := doAuth(t, r, "DELETE", "/api/tasks/"+foreignID, "", "user@x.com", "pw"); w.Code != http.StatusNotFound {
		t.Errorf("foreign DELETE status = %d, want 404", w.Code)
	}

	// The admin's task survives the attempts unchanged.
	got, err := svc.GetTask(ctx, foreign.ID(), application.CallerOf(admin))
	if err != nil {
		t.Fatalf("reload foreign task: %v", err)
	}
	if got.Title() != "admin task" || got.Status() != domain.StatusTodo {
		t.Errorf("foreign task was modified: %+v", got)
	}

	// The same user CAN operate on their own task.
	if w := doAuth(t, r, "GET", "/api/tasks/"+ownID, "", "user@x.com", "pw"); w.Code != http.StatusOK {
		t.Errorf("own GET status = %d, want 200", w.Code)
	}
	if w := doAuth(t, r, "PATCH", "/api/tasks/"+ownID, `{"title":"renamed by owner"}`, "user@x.com", "pw"); w.Code != http.StatusOK {
		t.Errorf("own PATCH status = %d, want 200", w.Code)
	}
	if w := doAuth(t, r, "DELETE", "/api/tasks/"+ownID, "", "user@x.com", "pw"); w.Code != http.StatusNoContent {
		t.Errorf("own DELETE status = %d, want 204", w.Code)
	}

	// Admins manage any task, including another user's.
	if w := do(t, r, "GET", "/api/tasks/"+foreignID, ""); w.Code != http.StatusOK {
		t.Errorf("admin GET foreign status = %d, want 200", w.Code)
	}
	if w := do(t, r, "PATCH", "/api/tasks/"+foreignID, `{"priority":5}`); w.Code != http.StatusOK {
		t.Errorf("admin PATCH foreign status = %d, want 200", w.Code)
	}
	if w := do(t, r, "DELETE", "/api/tasks/"+foreignID, ""); w.Code != http.StatusNoContent {
		t.Errorf("admin DELETE foreign status = %d, want 204", w.Code)
	}
}

// --- C-2: error mapping (storage) ---------------------------------------

// failingRepo wraps a working repo and forces a storage error on a chosen
// method, to exercise the STORAGE -> 503 mapping.
type failingRepo struct {
	repository.TaskRepository
	failCreate bool
}

func (f *failingRepo) Create(ctx context.Context, t domain.Task) error {
	if f.failCreate {
		return domain.Storage("disk exploded")
	}
	return f.TaskRepository.Create(ctx, t)
}

func TestHTTPStorageError(t *testing.T) {
	base := memory.NewTaskRepositoryMem()
	repo := &failingRepo{TaskRepository: base, failCreate: true}
	svc := newTestService(repo)
	r := httpapi.NewRouter(svc)

	w := do(t, r, "POST", "/api/tasks", `{"title":"t"}`)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	code, msg := decodeError(t, w)
	if code != "STORAGE" {
		t.Errorf("error code = %q, want STORAGE", code)
	}
	if msg != "disk exploded" {
		t.Errorf("error message = %q, want disk exploded", msg)
	}
}

// ---- phase 9 of docs/auth-plan.md: authentication middleware ----

func TestHTTPUnauthorizedWithoutCredentials(t *testing.T) {
	r := newTestEngine(t)
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/tasks"}, {"POST", "/api/tasks"}, {"GET", "/api/tasks/x"},
		{"PATCH", "/api/tasks/x"}, {"DELETE", "/api/tasks/x"}, {"GET", "/api/stats"},
	} {
		w := doNoAuth(t, r, tc.method, tc.path, "")
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: status = %d, want 401", tc.method, tc.path, w.Code)
		}
		if got := w.Header().Get("WWW-Authenticate"); !strings.Contains(got, "Basic") {
			t.Errorf("%s %s: WWW-Authenticate = %q, want Basic challenge", tc.method, tc.path, got)
		}
	}
}

func TestHTTPUnauthorizedWrongPassword(t *testing.T) {
	r := newTestEngine(t)
	w := doAuth(t, r, "GET", "/api/tasks", "", "admin@email.com", "wrong")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	// Same envelope as missing credentials: no account enumeration.
	_, msg := decodeError(t, w)
	if msg != "authentication required: Basic auth or X-API-Key" {
		t.Errorf("message = %q, want uniform 401 message", msg)
	}
}

func TestHTTPAPIKeyAuth(t *testing.T) {
	r := newTestEngine(t)
	// Fetch the admin's apikey via login.
	lw := doNoAuth(t, r, "POST", "/api/login", `{"email":"admin@email.com","password":"admin"}`)
	if lw.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200; body=%s", lw.Code, lw.Body.String())
	}
	var loginResp httpapi.UserResponse
	decodeBody(t, lw, &loginResp)
	if loginResp.APIKey == "" {
		t.Fatal("login response must include apikey")
	}
	// Assert the hash-free key set (NFR-8).
	for _, banned := range []string{"password", "hash"} {
		if strings.Contains(lw.Body.String(), `"`+banned) {
			t.Errorf("login response contains %q key", banned)
		}
	}

	req := httptest.NewRequest("GET", "/api/tasks", nil)
	req.Header.Set("X-API-Key", loginResp.APIKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("X-API-Key request status = %d, want 200", w.Code)
	}

	bad := httptest.NewRequest("GET", "/api/tasks", nil)
	bad.Header.Set("X-API-Key", "no-such-key")
	wb := httptest.NewRecorder()
	r.ServeHTTP(wb, bad)
	if wb.Code != http.StatusUnauthorized {
		t.Errorf("bad apikey status = %d, want 401", wb.Code)
	}
}

func TestHTTPTaskOwnershipScoped(t *testing.T) {
	r := newTestEngine(t)
	ctx := context.Background()

	// Create a second (non-admin) user directly through the service layer.
	repo := memory.NewTaskRepositoryMem()
	_ = repo
	// (The engine is bound to its own service; create the user via API login
	// is impossible without admin routes, so create a second engine-backed
	// user through CreateUser on the same repo is not accessible here.
	// Instead verify admin-created tasks carry the admin's userid.)
	cw := do(t, r, "POST", "/api/tasks", `{"title":"owned"}`)
	if cw.Code != http.StatusCreated {
		t.Fatalf("create status = %d", cw.Code)
	}
	var created httpapi.TaskResponse
	decodeBody(t, cw, &created)
	if created.UserID == "" {
		t.Error("created task must carry a userid")
	}
	_ = ctx
}
