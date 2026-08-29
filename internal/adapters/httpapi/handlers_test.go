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
func do(t *testing.T, r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
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
