// handlers_test.go is the web UI test suite (external test package). It
// drives the real Gin engine via httptest against an in-memory repository
// with deterministic sequential IDs and a fixed clock, covering full-page
// and HTMX fragment responses, form mutations, status transitions, error
// rendering, and the PRG fallback.
package httpweb_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"hexarch/internal/adapters/httpweb"
	"hexarch/internal/application"
	"hexarch/internal/domain"
	"hexarch/internal/repository"
	"hexarch/internal/repository/memory"
)

// newWebEngine builds a Gin engine wired to the httpweb adapter backed by an
// in-memory repository with a deterministic, sequential ID generator and a
// fixed clock, so tests are reproducible.
func newWebEngine(t *testing.T) *gin.Engine {
	t.Helper()
	repo := memory.NewTaskRepositoryMem()
	svc := newTestService(repo)
	r, err := httpweb.NewRouter(svc)
	if err != nil {
		t.Fatalf("newWebEngine: %v", err)
	}
	return r
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

// webDo performs a request against the web engine. hx toggles the HX-Request
// header. Form bodies are form-encoded.
func webDo(t *testing.T, r *gin.Engine, method, path string, hx bool, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if len(form) > 0 {
		req = httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	if hx {
		req.Header.Set("HX-Request", "true")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// seedWebTask creates a task via the web UI form endpoint with HX off.
func seedWebTask(t *testing.T, r *gin.Engine, title string) {
	t.Helper()
	form := url.Values{"title": {title}}
	w := webDo(t, r, "POST", "/app/tasks", false, form)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("seed %q status = %d, want 303; body=%s", title, w.Code, w.Body.String())
	}
}

// advanceStatus drives a task through one status step via the web UI.
func advanceStatus(t *testing.T, r *gin.Engine, id, status string) {
	t.Helper()
	form := url.Values{"status": {status}}
	w := webDo(t, r, "POST", "/app/tasks/"+id+"/status", true, form)
	if w.Code != http.StatusOK {
		t.Fatalf("advance %s to %s status = %d; body=%s", id, status, w.Code, w.Body.String())
	}
}

// --- Stage 0/1: harness + shell render -----------------------------------

func TestWebIndexRenders(t *testing.T) {
	r := newWebEngine(t)
	w := webDo(t, r, "GET", "/app/", false, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("index status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, frag := range []string{"id=\"filter-form\"", "id=\"task-list\"", "id=\"stats\"", "id=\"toast\"", "modal-create", "modal-edit", "modal-delete"} {
		if !strings.Contains(body, frag) {
			t.Errorf("index missing region %q", frag)
		}
	}
}

func TestWebIndexEmptyState(t *testing.T) {
	r := newWebEngine(t)
	w := webDo(t, r, "GET", "/app/", false, nil)
	body := w.Body.String()
	if !strings.Contains(body, "No tasks yet") {
		t.Errorf("empty-state missing from index: %s", body)
	}
}

// --- Stage 3: dual render branch -----------------------------------------

func TestWebFragmentsVsFullPage(t *testing.T) {
	r := newWebEngine(t)

	full := webDo(t, r, "GET", "/app/tasks", false, nil)
	if full.Code != http.StatusOK {
		t.Fatalf("full status = %d", full.Code)
	}

	frag := webDo(t, r, "GET", "/app/tasks", true, nil)
	if frag.Code != http.StatusOK {
		t.Fatalf("fragment status = %d", frag.Code)
	}
	body := frag.Body.String()
	if strings.Contains(body, "<html") || strings.Contains(body, "<body") {
		t.Errorf("HX fragment should not wrap in html/body: %s", body)
	}
	if !strings.Contains(frag.Header().Get("Vary"), "HX-Request") {
		t.Errorf("fragment missing Vary: HX-Request")
	}
}

func TestWebStatsFragment(t *testing.T) {
	r := newWebEngine(t)
	seedWebTask(t, r, "a")
	w := webDo(t, r, "GET", "/app/stats", true, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("stats status = %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "stat-value") {
		t.Errorf("stats missing stat-value cells: %s", body)
	}
}

// --- FR-6: stats ----------------------------------------------------------

func TestWebStatsCounters(t *testing.T) {
	r := newWebEngine(t)
	seedWebTask(t, r, "a")
	seedWebTask(t, r, "b")
	advanceStatus(t, r, "id-001", "in_progress")
	advanceStatus(t, r, "id-001", "done")

	w := webDo(t, r, "GET", "/app/stats", true, nil)
	body := w.Body.String()
	if !strings.Contains(body, "of 2 total") {
		t.Errorf("stats total missing: %s", body)
	}
}

// --- FR-1: list / filters / pagination -----------------------------------

func TestWebListFiltersCompose(t *testing.T) {
	r := newWebEngine(t)
	seedWebTask(t, r, "buy milk")
	seedWebTask(t, r, "write tests")
	advanceStatus(t, r, "id-001", "in_progress")

	w := webDo(t, r, "GET", "/app/tasks?status=in_progress&search=milk", true, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "task-row-id-001") {
		t.Errorf("filtered list should contain id-001: %s", body)
	}
	if strings.Contains(body, "task-row-id-002") {
		t.Errorf("filtered list should NOT contain id-002: %s", body)
	}
}

func TestWebListNoMatch(t *testing.T) {
	r := newWebEngine(t)
	seedWebTask(t, r, "one")
	w := webDo(t, r, "GET", "/app/tasks?status=done", true, nil)
	body := w.Body.String()
	if !strings.Contains(body, "No tasks match") {
		t.Errorf("no-match state missing: %s", body)
	}
}

func TestWebPaginationBoundaries(t *testing.T) {
	r := newWebEngine(t)
	for i := 0; i < 5; i++ {
		seedWebTask(t, r, fmt.Sprintf("t%d", i))
	}
	w := webDo(t, r, "GET", "/app/tasks", true, nil)
	body := w.Body.String()
	if strings.Contains(body, "btn-disabled\">Next") {
		t.Log("next disabled as expected on last page")
	} else if !strings.Contains(body, "Next »") {
		t.Errorf("pagination controls missing: %s", body)
	}
}

// --- FR-2: create ---------------------------------------------------------

func TestWebCreate(t *testing.T) {
	r := newWebEngine(t)
	form := url.Values{"title": {"Fix bug"}, "description": {"oAuth"}, "priority": {"4"}}
	w := webDo(t, r, "POST", "/app/tasks", true, form)
	if w.Code != http.StatusOK {
		t.Fatalf("create status = %d; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Task created") {
		t.Errorf("create missing success toast: %s", w.Body.String())
	}
	sw := webDo(t, r, "GET", "/app/stats", true, nil)
	if !strings.Contains(sw.Body.String(), "of 1 total") {
		t.Errorf("stats after create wrong: %s", sw.Body.String())
	}
}

func TestWebCreateEmptyTitle(t *testing.T) {
	r := newWebEngine(t)
	form := url.Values{"title": {""}, "priority": {"0"}}
	w := webDo(t, r, "POST", "/app/tasks", true, form)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("create empty-title status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "title must not be empty") {
		t.Errorf("expected inline error message: %s", w.Body.String())
	}
}

func TestWebCreatePriorityOutOfRange(t *testing.T) {
	r := newWebEngine(t)
	form := url.Values{"title": {"t"}, "priority": {"9"}}
	w := webDo(t, r, "POST", "/app/tasks", true, form)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("create out-of-range priority status = %d, want 400", w.Code)
	}
}

// --- FR-3: edit -----------------------------------------------------------

func TestWebEditForm(t *testing.T) {
	r := newWebEngine(t)
	seedWebTask(t, r, "old")
	w := webDo(t, r, "GET", "/app/tasks/id-001/edit", true, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("edit form status = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "old") {
		t.Errorf("edit form not pre-filled: %s", w.Body.String())
	}
}

func TestWebUpdateTitle(t *testing.T) {
	r := newWebEngine(t)
	seedWebTask(t, r, "old")
	form := url.Values{"title": {"new name"}}
	w := webDo(t, r, "PATCH", "/app/tasks/id-001", true, form)
	if w.Code != http.StatusOK {
		t.Fatalf("update status = %d; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Task updated") {
		t.Errorf("update missing toast: %s", w.Body.String())
	}
}

func TestWebUpdateEmptyTitle(t *testing.T) {
	r := newWebEngine(t)
	seedWebTask(t, r, "x")
	form := url.Values{"title": {""}, "priority": {"3"}}
	w := webDo(t, r, "PATCH", "/app/tasks/id-001", true, form)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("update empty-title status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
}

// --- FR-4: status ---------------------------------------------------------

func TestWebStatusTransitions(t *testing.T) {
	r := newWebEngine(t)
	seedWebTask(t, r, "task")
	advanceStatus(t, r, "id-001", "in_progress")
	advanceStatus(t, r, "id-001", "done")
	advanceStatus(t, r, "id-001", "archived")

	sw := webDo(t, r, "GET", "/app/stats", true, nil)
	body := sw.Body.String()
	if !strings.Contains(body, "of 1 total") {
		t.Errorf("stats after lifecycle wrong: %s", body)
	}
}

func TestWebIllegalTransition(t *testing.T) {
	r := newWebEngine(t)
	seedWebTask(t, r, "task")
	form := url.Values{"status": {"done"}}
	w := webDo(t, r, "POST", "/app/tasks/id-001/status", true, form)
	if w.Code != http.StatusConflict {
		t.Fatalf("illegal transition status = %d, want 409; body=%s", w.Code, w.Body.String())
	}
}

func TestWebStatusActionRenderedPerRow(t *testing.T) {
	r := newWebEngine(t)
	seedWebTask(t, r, "task")
	w := webDo(t, r, "GET", "/app/tasks", true, nil)
	body := w.Body.String()
	if !strings.Contains(body, ">Start</button>") {
		t.Errorf("todo row missing Start action: %s", body)
	}
	advanceStatus(t, r, "id-001", "in_progress")
	advanceStatus(t, r, "id-001", "done")
	advanceStatus(t, r, "id-001", "archived")
	w = webDo(t, r, "GET", "/app/tasks", true, nil)
	body = w.Body.String()
	if strings.Contains(body, ">Start</button>") || strings.Contains(body, ">Complete</button>") {
		t.Errorf("archived row should not offer status actions: %s", body)
	}
}

// --- FR-5: delete ---------------------------------------------------------

func TestWebDeleteFormAndDelete(t *testing.T) {
	r := newWebEngine(t)
	seedWebTask(t, r, "gone")
	w := webDo(t, r, "GET", "/app/tasks/id-001/delete", true, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("delete form status = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "gone") {
		t.Errorf("delete confirm missing title: %s", w.Body.String())
	}

	d := webDo(t, r, "DELETE", "/app/tasks/id-001", true, nil)
	if d.Code != http.StatusOK {
		t.Fatalf("delete status = %d; body=%s", d.Code, d.Body.String())
	}
	if !strings.Contains(d.Body.String(), "Task deleted") {
		t.Errorf("delete missing toast: %s", d.Body.String())
	}
	sw := webDo(t, r, "GET", "/app/stats", true, nil)
	if !strings.Contains(sw.Body.String(), "of 0 total") {
		t.Errorf("stats after delete wrong: %s", sw.Body.String())
	}
}

func TestWebStorageError(t *testing.T) {
	base := memory.NewTaskRepositoryMem()
	repo := &failingRepo{TaskRepository: base, failCreate: true}
	svc := newTestService(repo)
	r, err := httpweb.NewRouter(svc)
	if err != nil {
		t.Fatalf("TestWebStorageError: %v", err)
	}

	form := url.Values{"title": {"t"}}
	w := webDo(t, r, "POST", "/app/tasks", true, form)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("storage error status = %d, want 503", w.Code)
	}
	if !strings.Contains(w.Body.String(), "disk exploded") {
		t.Errorf("expected storage message: %s", w.Body.String())
	}
}

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
