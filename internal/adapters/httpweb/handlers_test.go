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

// newWebSession returns an engine plus an authenticated client (the seeded
// bootstrap admin).
func newWebSession(t *testing.T) (*gin.Engine, *webClient) {
	t.Helper()
	r := newWebEngine(t)
	return r, newLoggedInClient(t, r, "admin@email.com", "admin")
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

// webClient carries the cookies (session + CSRF) of one authenticated
// browser session across requests.
type webClient struct {
	r       *gin.Engine
	csrf    string
	email   string
	cookies map[string]string
}

// newLoggedInClient performs the full login dance against /app/login and
// returns a client holding the session + CSRF cookies.
func newLoggedInClient(t *testing.T, r *gin.Engine, email, password string) *webClient {
	t.Helper()
	// 1. GET the login page to obtain the CSRF cookie.
	jar := map[string]string{}
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/app/login", nil)
	r.ServeHTTP(w, req)
	for _, ck := range w.Result().Cookies() {
		jar[ck.Name] = ck.Value
	}
	csrf := jar["hexarch_csrf"]
	if csrf == "" {
		t.Fatal("login page did not issue a CSRF cookie")
	}
	// 2. POST the credentials with the token.
	form := url.Values{
		"email":      {email},
		"password":   {password},
		"csrf_token": {csrf},
		"next":       {"/app/"},
	}
	req = httptest.NewRequest("POST", "/app/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range jar {
		req.AddCookie(&http.Cookie{Name: k, Value: v})
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("login status = %d, want 303; body=%s", w.Code, w.Body.String())
	}
	for _, ck := range w.Result().Cookies() {
		if ck.Value != "" {
			jar[ck.Name] = ck.Value
		}
	}
	return &webClient{r: r, csrf: csrf, email: email, cookies: jar}
}

// webDo performs a request against the web engine. hx toggles the HX-Request
// header. Form bodies are form-encoded. Requests carry the client's cookies
// and CSRF token.
func (c *webClient) do(t *testing.T, method, path string, hx bool, form url.Values) *httptest.ResponseRecorder {
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
	req.Header.Set("X-CSRF-Token", c.csrf)
	for k, v := range c.cookies {
		req.AddCookie(&http.Cookie{Name: k, Value: v})
	}
	w := httptest.NewRecorder()
	c.r.ServeHTTP(w, req)
	return w
}

// seedWebTask creates a task via the web UI form endpoint with HX off.
func seedWebTask(t *testing.T, c *webClient, title string) {
	t.Helper()
	form := url.Values{"title": {title}}
	w := c.do(t, "POST", "/app/tasks", false, form)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("seed %q status = %d, want 303; body=%s", title, w.Code, w.Body.String())
	}
}

// advanceStatus drives a task through one status step via the web UI.
func advanceStatus(t *testing.T, c *webClient, id, status string) {
	t.Helper()
	form := url.Values{"status": {status}}
	w := c.do(t, "POST", "/app/tasks/"+id+"/status", true, form)
	if w.Code != http.StatusOK {
		t.Fatalf("advance %s to %s status = %d; body=%s", id, status, w.Code, w.Body.String())
	}
}

// --- Stage 0/1: harness + shell render -----------------------------------

func TestWebIndexRenders(t *testing.T) {
	_, c := newWebSession(t)
	w := c.do(t, "GET", "/app/", false, nil)
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

// TestWebSharedShellAndModalContract locks in the template refactor: the
// head/navbar partials are the single source of the version pins and shell
// data, and the modal lifecycle is centralized in partials/modal_js.html
// rather than per-element inline handlers.
func TestWebSharedShellAndModalContract(t *testing.T) {
	// Public login shell: shared head, but HTMX is deliberately not loaded.
	r := newWebEngine(t)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/app/login", nil))
	login := w.Body.String()
	if !strings.Contains(login, "<title>Sign in — To Do</title>") {
		t.Errorf("login missing shared head title")
	}
	if strings.Contains(login, "htmx.org@4.0.0") {
		t.Errorf("login should not load HTMX")
	}
	if strings.Contains(login, "data-modal") {
		t.Errorf("login navbar should not offer a modal action")
	}

	// Authenticated shell: version pins, navbar action, CSRF header and the
	// centralized modal script all come from the shared partials.
	_, c := newWebSession(t)
	idx := c.do(t, "GET", "/app/", false, nil)
	if idx.Code != http.StatusOK {
		t.Fatalf("index status = %d", idx.Code)
	}
	body := idx.Body.String()
	for _, want := range []string{
		"<title>To Do</title>",
		"htmx.org@4.0.0",            // head partial pins HTMX
		`data-modal="modal-create"`, // navbar action
		"hx-headers:inherited",      // CSRF header for DELETE requests
		"data-modal-error",          // create-form error region
		`hx-status:2xx="swap:none"`, // success swaps only <hx-partial>
		"htmx:after:swap",           // modal_js open handler
	} {
		if !strings.Contains(body, want) {
			t.Errorf("index missing %q", want)
		}
	}
	if strings.Contains(body, "onclick=") || strings.Contains(body, "hx-on:") {
		t.Errorf("index still contains per-element inline JS")
	}

	// Admin shell: shared navbar with the user-management action.
	uw := c.do(t, "GET", "/app/users", false, nil)
	if uw.Code != http.StatusOK {
		t.Fatalf("users status = %d", uw.Code)
	}
	users := uw.Body.String()
	if !strings.Contains(users, "<title>User Management — To Do</title>") {
		t.Errorf("users missing shared head title")
	}
	if !strings.Contains(users, `data-modal="modal-user-create"`) {
		t.Errorf("users missing shared navbar action")
	}
	if !strings.Contains(users, "admin@email.com") {
		t.Errorf("users navbar missing the signed-in email")
	}
}

func TestWebIndexEmptyState(t *testing.T) {
	_, c := newWebSession(t)
	w := c.do(t, "GET", "/app/", false, nil)
	body := w.Body.String()
	if !strings.Contains(body, "No tasks yet") {
		t.Errorf("empty-state missing from index: %s", body)
	}
}

// --- Stage 3: dual render branch -----------------------------------------

func TestWebFragmentsVsFullPage(t *testing.T) {
	_, c := newWebSession(t)

	full := c.do(t, "GET", "/app/tasks", false, nil)
	if full.Code != http.StatusOK {
		t.Fatalf("full status = %d", full.Code)
	}

	frag := c.do(t, "GET", "/app/tasks", true, nil)
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
	_, c := newWebSession(t)
	seedWebTask(t, c, "a")
	w := c.do(t, "GET", "/app/stats", true, nil)
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
	_, c := newWebSession(t)
	seedWebTask(t, c, "a")
	seedWebTask(t, c, "b")
	advanceStatus(t, c, "id-001", "in_progress")
	advanceStatus(t, c, "id-001", "done")

	w := c.do(t, "GET", "/app/stats", true, nil)
	body := w.Body.String()
	if !strings.Contains(body, "of 2 total") {
		t.Errorf("stats total missing: %s", body)
	}
}

// TestWebRegularUserSeesOnlyOwnTasksAndStats guards the ownership defect:
// a non-admin's list and stats must be scoped to their own tasks, never the
// bootstrap admin's.
func TestWebRegularUserSeesOnlyOwnTasksAndStats(t *testing.T) {
	repo := memory.NewTaskRepositoryMem()
	svc := newTestService(repo)

	// Register a regular user (CreateUser seeds the bootstrap admin first).
	hash, err := application.HashPassword("pw", application.TestPasswordCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	regular, err := domain.NewUser("u-regular", "regular@x.com", hash, "key-regular", false)
	if err != nil {
		t.Fatalf("NewUser(regular): %v", err)
	}
	if err := repo.CreateUser(context.Background(), regular); err != nil {
		t.Fatalf("CreateUser(regular): %v", err)
	}

	// Admin owns one task; the regular user owns another.
	admin, err := svc.AuthUser(context.Background(), "admin@email.com", "admin")
	if err != nil {
		t.Fatalf("bootstrap admin auth: %v", err)
	}
	if _, err := svc.CreateTask(context.Background(), application.CreateTaskInput{UserID: admin.ID(), Title: "admin task"}); err != nil {
		t.Fatalf("create admin task: %v", err)
	}
	if _, err := svc.CreateTask(context.Background(), application.CreateTaskInput{UserID: regular.ID(), Title: "user task"}); err != nil {
		t.Fatalf("create user task: %v", err)
	}

	r, err := httpweb.NewRouter(svc)
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	c := newLoggedInClient(t, r, "regular@x.com", "pw")

	// Full page: only the user's own task is listed, and the stats strip
	// counts only it — never the admin's task.
	w := c.do(t, "GET", "/app/", false, nil)
	body := w.Body.String()
	if !strings.Contains(body, "user task") {
		t.Errorf("regular user index missing their task: %s", body)
	}
	if strings.Contains(body, "admin task") {
		t.Errorf("regular user index leaks admin task: %s", body)
	}
	if !strings.Contains(body, "of 1 total") {
		t.Errorf("regular user stats total wrong: %s", body)
	}

	// Stats fragment is scoped the same way.
	sw := c.do(t, "GET", "/app/stats", true, nil)
	if !strings.Contains(sw.Body.String(), "of 1 total") {
		t.Errorf("regular user stats fragment wrong: %s", sw.Body.String())
	}

	// The admin still spans every user.
	ac := newLoggedInClient(t, r, "admin@email.com", "admin")
	aIndex := ac.do(t, "GET", "/app/", false, nil)
	aBody := aIndex.Body.String()
	if !strings.Contains(aBody, "admin task") || !strings.Contains(aBody, "user task") {
		t.Errorf("admin index should list both tasks: %s", aBody)
	}
	if !strings.Contains(aBody, "of 2 total") {
		t.Errorf("admin stats total wrong: %s", aBody)
	}
}

// TestWebTaskIDOwnership guards FR-U4 on the /app task routes: a regular
// user cannot open the edit/delete forms of another user's task nor mutate
// it by direct URL — every attempt answers 404 like a missing task.
func TestWebTaskIDOwnership(t *testing.T) {
	repo := memory.NewTaskRepositoryMem()
	svc := newTestService(repo)
	ctx := context.Background()

	admin, err := svc.AuthUser(ctx, "admin@email.com", "admin")
	if err != nil {
		t.Fatalf("bootstrap admin auth: %v", err)
	}
	foreign, err := svc.CreateTask(ctx, application.CreateTaskInput{UserID: admin.ID(), Title: "admin task"})
	if err != nil {
		t.Fatalf("create admin task: %v", err)
	}
	hash, err := application.HashPassword("pw", application.TestPasswordCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	regular, err := domain.NewUser("u-regular", "regular@x.com", hash, "key-regular", false)
	if err != nil {
		t.Fatalf("NewUser(regular): %v", err)
	}
	if err := repo.CreateUser(ctx, regular); err != nil {
		t.Fatalf("CreateUser(regular): %v", err)
	}
	own, err := svc.CreateTask(ctx, application.CreateTaskInput{UserID: regular.ID(), Title: "user task"})
	if err != nil {
		t.Fatalf("create user task: %v", err)
	}
	foreignID := foreign.ID().String()

	r, err := httpweb.NewRouter(svc)
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	c := newLoggedInClient(t, r, "regular@x.com", "pw")

	// Every /app task route aimed at the admin's task answers 404.
	if w := c.do(t, "GET", "/app/tasks/"+foreignID+"/edit", true, nil); w.Code != http.StatusNotFound {
		t.Errorf("foreign edit form status = %d, want 404", w.Code)
	}
	if w := c.do(t, "GET", "/app/tasks/"+foreignID+"/delete", true, nil); w.Code != http.StatusNotFound {
		t.Errorf("foreign delete form status = %d, want 404", w.Code)
	}
	if w := c.do(t, "PATCH", "/app/tasks/"+foreignID, true, url.Values{"title": {"hijacked"}}); w.Code != http.StatusNotFound {
		t.Errorf("foreign PATCH status = %d, want 404", w.Code)
	}
	if w := c.do(t, "POST", "/app/tasks/"+foreignID+"/status", true, url.Values{"status": {"done"}}); w.Code != http.StatusNotFound {
		t.Errorf("foreign status change status = %d, want 404", w.Code)
	}
	if w := c.do(t, "DELETE", "/app/tasks/"+foreignID, true, nil); w.Code != http.StatusNotFound {
		t.Errorf("foreign DELETE status = %d, want 404", w.Code)
	}

	// The admin's task was not modified.
	got, err := svc.GetTask(ctx, foreign.ID(), application.CallerOf(admin))
	if err != nil {
		t.Fatalf("reload foreign task: %v", err)
	}
	if got.Title() != "admin task" || got.Status() != domain.StatusTodo {
		t.Errorf("foreign task was modified: %+v", got)
	}

	// The owner still reaches their own task through the same routes.
	ownID := own.ID().String()
	if w := c.do(t, "GET", "/app/tasks/"+ownID+"/edit", true, nil); w.Code != http.StatusOK {
		t.Errorf("own edit form status = %d, want 200", w.Code)
	}
	if w := c.do(t, "POST", "/app/tasks/"+ownID+"/status", true, url.Values{"status": {"in_progress"}}); w.Code != http.StatusOK {
		t.Errorf("own status change status = %d, want 200", w.Code)
	}
	if w := c.do(t, "DELETE", "/app/tasks/"+ownID, true, nil); w.Code != http.StatusOK {
		t.Errorf("own DELETE status = %d, want 200", w.Code)
	}
}

// --- FR-1: list / filters / pagination -----------------------------------

func TestWebListFiltersCompose(t *testing.T) {
	_, c := newWebSession(t)
	seedWebTask(t, c, "buy milk")
	seedWebTask(t, c, "write tests")
	advanceStatus(t, c, "id-001", "in_progress")

	w := c.do(t, "GET", "/app/tasks?status=in_progress&search=milk", true, nil)
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
	_, c := newWebSession(t)
	seedWebTask(t, c, "one")
	w := c.do(t, "GET", "/app/tasks?status=done", true, nil)
	body := w.Body.String()
	if !strings.Contains(body, "No tasks match") {
		t.Errorf("no-match state missing: %s", body)
	}
}

func TestWebPaginationBoundaries(t *testing.T) {
	_, c := newWebSession(t)
	for i := 0; i < 5; i++ {
		seedWebTask(t, c, fmt.Sprintf("t%d", i))
	}
	w := c.do(t, "GET", "/app/tasks", true, nil)
	body := w.Body.String()
	if strings.Contains(body, "btn-disabled\">Next") {
		t.Log("next disabled as expected on last page")
	} else if !strings.Contains(body, "Next »") {
		t.Errorf("pagination controls missing: %s", body)
	}
}

// --- FR-2: create ---------------------------------------------------------

func TestWebCreate(t *testing.T) {
	_, c := newWebSession(t)
	form := url.Values{"title": {"Fix bug"}, "description": {"oAuth"}, "priority": {"4"}}
	w := c.do(t, "POST", "/app/tasks", true, form)
	if w.Code != http.StatusOK {
		t.Fatalf("create status = %d; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Task created") {
		t.Errorf("create missing success toast: %s", w.Body.String())
	}
	sw := c.do(t, "GET", "/app/stats", true, nil)
	if !strings.Contains(sw.Body.String(), "of 1 total") {
		t.Errorf("stats after create wrong: %s", sw.Body.String())
	}
}

func TestWebCreateEmptyTitle(t *testing.T) {
	_, c := newWebSession(t)
	form := url.Values{"title": {""}, "priority": {"0"}}
	w := c.do(t, "POST", "/app/tasks", true, form)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("create empty-title status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "title must not be empty") {
		t.Errorf("expected inline error message: %s", w.Body.String())
	}
}

func TestWebCreatePriorityOutOfRange(t *testing.T) {
	_, c := newWebSession(t)
	form := url.Values{"title": {"t"}, "priority": {"9"}}
	w := c.do(t, "POST", "/app/tasks", true, form)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("create out-of-range priority status = %d, want 400", w.Code)
	}
}

func TestWebCreateInvalidDeadline(t *testing.T) {
	_, c := newWebSession(t)
	form := url.Values{"title": {"t"}, "deadline": {"not-a-date"}}
	w := c.do(t, "POST", "/app/tasks", true, form)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("create invalid deadline status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "deadline must look like") {
		t.Errorf("expected deadline error message: %s", w.Body.String())
	}
}

func TestWebStatsInvalidOffset(t *testing.T) {
	_, c := newWebSession(t)
	w := c.do(t, "GET", "/app/stats?offset=abc", true, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("stats invalid offset status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
}

func TestWebLogoutRequiresPost(t *testing.T) {
	r := newWebEngine(t)
	c := newLoggedInClient(t, r, "admin@email.com", "admin")

	// Logout is a state change: the convenience GET route is gone.
	if w := c.do(t, "GET", "/app/logout", false, nil); w.Code != http.StatusNotFound {
		t.Fatalf("GET logout status = %d, want 404", w.Code)
	}

	// POST with the CSRF token succeeds and expires the session cookie.
	w := c.do(t, "POST", "/app/logout", false, url.Values{})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("POST logout status = %d, want 303; body=%s", w.Code, w.Body.String())
	}
	cleared := false
	for _, ck := range w.Result().Cookies() {
		if ck.Name == "hexarch_session" && ck.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Errorf("logout did not clear the session cookie: %v", w.Result().Cookies())
	}
}

// --- FR-3: edit -----------------------------------------------------------

func TestWebEditForm(t *testing.T) {
	_, c := newWebSession(t)
	seedWebTask(t, c, "old")
	w := c.do(t, "GET", "/app/tasks/id-001/edit", true, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("edit form status = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "old") {
		t.Errorf("edit form not pre-filled: %s", w.Body.String())
	}
}

func TestWebUpdateTitle(t *testing.T) {
	_, c := newWebSession(t)
	seedWebTask(t, c, "old")
	form := url.Values{"title": {"new name"}}
	w := c.do(t, "PATCH", "/app/tasks/id-001", true, form)
	if w.Code != http.StatusOK {
		t.Fatalf("update status = %d; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Task updated") {
		t.Errorf("update missing toast: %s", w.Body.String())
	}
}

func TestWebUpdateEmptyTitle(t *testing.T) {
	_, c := newWebSession(t)
	seedWebTask(t, c, "x")
	form := url.Values{"title": {""}, "priority": {"3"}}
	w := c.do(t, "PATCH", "/app/tasks/id-001", true, form)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("update empty-title status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
}

// --- FR-4: status ---------------------------------------------------------

func TestWebStatusTransitions(t *testing.T) {
	_, c := newWebSession(t)
	seedWebTask(t, c, "task")
	advanceStatus(t, c, "id-001", "in_progress")
	advanceStatus(t, c, "id-001", "done")
	advanceStatus(t, c, "id-001", "archived")

	sw := c.do(t, "GET", "/app/stats", true, nil)
	body := sw.Body.String()
	if !strings.Contains(body, "of 1 total") {
		t.Errorf("stats after lifecycle wrong: %s", body)
	}
}

func TestWebIllegalTransition(t *testing.T) {
	_, c := newWebSession(t)
	seedWebTask(t, c, "task")
	form := url.Values{"status": {"done"}}
	w := c.do(t, "POST", "/app/tasks/id-001/status", true, form)
	if w.Code != http.StatusConflict {
		t.Fatalf("illegal transition status = %d, want 409; body=%s", w.Code, w.Body.String())
	}
}

func TestWebStatusActionRenderedPerRow(t *testing.T) {
	_, c := newWebSession(t)
	seedWebTask(t, c, "task")
	w := c.do(t, "GET", "/app/tasks", true, nil)
	body := w.Body.String()
	if !strings.Contains(body, ">Start</button>") {
		t.Errorf("todo row missing Start action: %s", body)
	}
	advanceStatus(t, c, "id-001", "in_progress")
	advanceStatus(t, c, "id-001", "done")
	advanceStatus(t, c, "id-001", "archived")
	w = c.do(t, "GET", "/app/tasks", true, nil)
	body = w.Body.String()
	if strings.Contains(body, ">Start</button>") || strings.Contains(body, ">Complete</button>") {
		t.Errorf("archived row should not offer status actions: %s", body)
	}
}

// --- FR-5: delete ---------------------------------------------------------

func TestWebDeleteFormAndDelete(t *testing.T) {
	_, c := newWebSession(t)
	seedWebTask(t, c, "gone")
	w := c.do(t, "GET", "/app/tasks/id-001/delete", true, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("delete form status = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "gone") {
		t.Errorf("delete confirm missing title: %s", w.Body.String())
	}

	d := c.do(t, "DELETE", "/app/tasks/id-001", true, nil)
	if d.Code != http.StatusOK {
		t.Fatalf("delete status = %d; body=%s", d.Code, d.Body.String())
	}
	if !strings.Contains(d.Body.String(), "Task deleted") {
		t.Errorf("delete missing toast: %s", d.Body.String())
	}
	sw := c.do(t, "GET", "/app/stats", true, nil)
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
	c := newLoggedInClient(t, r, "admin@email.com", "admin")

	form := url.Values{"title": {"t"}}
	w := c.do(t, "POST", "/app/tasks", true, form)
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

// ---- phase 11 of docs/auth-plan.md: user management ----

// TestWebUsersAdminOnly: a non-admin is denied /app/users (403) and the
// index navbar has no Admin link.
func TestWebUsersAdminOnly(t *testing.T) {
	r := newWebEngine(t)
	// Log in as the seeded admin to create a regular user.
	admin := newLoggedInClient(t, r, "admin@email.com", "admin")

	// Non-admin login requires creating the user first — do it via the admin
	// on the users page.
	form := url.Values{
		"email": {"alice@example.com"}, "password": {"pw-alice"},
		"csrf_token": {admin.csrf},
	}
	w := admin.do(t, "POST", "/app/users", false, form)
	if w.Code != http.StatusSeeOther && w.Code != http.StatusOK {
		t.Fatalf("create user status = %d; body=%s", w.Code, w.Body.String())
	}

	alice := newLoggedInClient(t, r, "alice@example.com", "pw-alice")
	if w := alice.do(t, "GET", "/app/users", false, nil); w.Code != http.StatusForbidden {
		t.Errorf("non-admin GET /app/users status = %d, want 403", w.Code)
	}
	// Index page for non-admin must not contain the Admin link.
	w = alice.do(t, "GET", "/app/", false, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("index status = %d", w.Code)
	}
	if strings.Contains(w.Body.String(), `href="/app/users"`) {
		t.Error("non-admin navbar must not contain the Admin link")
	}
	// Admin navbar must contain it.
	w = admin.do(t, "GET", "/app/", false, nil)
	if !strings.Contains(w.Body.String(), `href="/app/users"`) {
		t.Error("admin navbar must contain the Admin link")
	}
}

func TestWebUserCRUDFlow(t *testing.T) {
	r := newWebEngine(t)
	admin := newLoggedInClient(t, r, "admin@email.com", "admin")

	// Create.
	form := url.Values{
		"email": {"bob@example.com"}, "password": {"pw-bob"}, "isadmin": {"on"},
		"csrf_token": {admin.csrf},
	}
	if w := admin.do(t, "POST", "/app/users", true, form); w.Code != http.StatusOK {
		t.Fatalf("create status = %d; body=%s", w.Code, w.Body.String())
	}
	if w := admin.do(t, "GET", "/app/users", false, nil); !strings.Contains(w.Body.String(), "bob@example.com") {
		t.Fatal("bob missing from user list after create")
	}

	// Find bob's id from the list page (search the row containing his email).
	w := admin.do(t, "GET", "/app/users", false, nil)
	body := w.Body.String()
	emailIdx := strings.Index(body, "bob@example.com")
	rowPrefix := `id="user-row-`
	rowIdx := strings.LastIndex(body[:emailIdx], rowPrefix)
	bobID := body[rowIdx+len(rowPrefix):]
	bobID = bobID[:strings.Index(bobID, `"`)]

	// Edit: change email only.
	form = url.Values{"email": {"robert@example.com"}, "csrf_token": {admin.csrf}}
	w = admin.do(t, "PATCH", "/app/users/"+bobID, true, form)
	if w.Code != http.StatusOK {
		t.Fatalf("update status = %d; body=%s", w.Code, w.Body.String())
	}
	// Password unchanged: robert can still log in with pw-bob.
	robert := newLoggedInClient(t, r, "robert@example.com", "pw-bob")
	if robert == nil {
		t.Fatal("robert login failed after email change")
	}

	// Self-delete is blocked server-side.
	form = url.Values{}
	w = admin.do(t, "DELETE", "/app/users/bogus-id", true, form) // 404 first
	if w.Code == http.StatusForbidden && strings.Contains(w.Body.String(), "own account") {
		t.Fatal("unexpected self-delete path for bogus id")
	}

	// Delete bob (robert) — admin is not the target, so allowed.
	w = admin.do(t, "DELETE", "/app/users/"+bobID, true, url.Values{})
	if w.Code != http.StatusOK {
		t.Fatalf("delete status = %d; body=%s", w.Code, w.Body.String())
	}
	if w := admin.do(t, "GET", "/app/users", false, nil); strings.Contains(w.Body.String(), "robert@example.com") {
		t.Error("robert still listed after delete")
	}
}

func TestWebUserCreateDuplicateEmail(t *testing.T) {
	r := newWebEngine(t)
	admin := newLoggedInClient(t, r, "admin@email.com", "admin")
	form := url.Values{
		"email": {"admin@email.com"}, "password": {"x"}, "csrf_token": {admin.csrf},
	}
	w := admin.do(t, "POST", "/app/users", true, form)
	if w.Code != http.StatusConflict {
		t.Fatalf("duplicate email status = %d, want 409; body=%s", w.Code, w.Body.String())
	}
}

func TestWebUserDeleteSelfForbidden(t *testing.T) {
	r := newWebEngine(t)
	admin := newLoggedInClient(t, r, "admin@email.com", "admin")
	// Resolve the admin's own id from the users page.
	w := admin.do(t, "GET", "/app/users", false, nil)
	body := w.Body.String()
	marker := `hx-delete="/app/users/`
	i := strings.Index(body, marker)
	if i < 0 {
		// No delete buttons rendered for the only (own) user — that's the
		// expected UX: self-delete is not even offered.
		return
	}
	rest := body[i+len(marker):]
	selfID := rest[:strings.Index(rest, `"`)]
	w = admin.do(t, "DELETE", "/app/users/"+selfID, true, url.Values{})
	if w.Code != http.StatusForbidden {
		t.Fatalf("self-delete status = %d, want 403", w.Code)
	}
}

func TestWebUserCSRFRequired(t *testing.T) {
	r := newWebEngine(t)
	admin := newLoggedInClient(t, r, "admin@email.com", "admin")
	// Strip the CSRF token: form POST without token must be 403.
	form := url.Values{"email": {"x@example.com"}, "password": {"y"}}
	saved := admin.csrf
	admin.csrf = ""
	w := admin.do(t, "POST", "/app/users", true, form)
	admin.csrf = saved
	if w.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status = %d, want 403", w.Code)
	}
}
