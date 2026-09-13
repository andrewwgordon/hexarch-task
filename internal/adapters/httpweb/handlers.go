// handlers.go implements the Gin handlers for the web UI (/app group).
//
// The adapter follows a dual-render strategy: HTMX fragment requests
// (HX-Request) receive partial templates, full browser loads receive a
// whole page shell. Mutations answer with a multi-region fragment (task
// list + stats + toast) and fall back to a PRG redirect without JavaScript.
//
// Public API: (none — handlers is unexported; see router.go)
//
// Private:
//   - handlers, newHandlers
//   - detection:  isHX, markVary
//   - data:       listAndTotal
//   - handlers:   index, list, stats, create, editForm, update,
//     changeStatus, deleteForm, delete, empty, writeMutation
//   - helpers:    optionalDeadline, postPriority
package httpweb

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"hexarch/internal/adapters/httpconv"
	"hexarch/internal/application"
	"hexarch/internal/domain"
	"hexarch/internal/repository"
)

// handlers bundles the service and session manager and implements the /app
// route handlers. Each method corresponds to one route registered in
// router.go.
type handlers struct {
	svc application.TaskService
	sm  *sessionManager
}

// newHandlers builds the handler set for the given service.
func newHandlers(svc application.TaskService, sm *sessionManager) *handlers {
	return &handlers{svc: svc, sm: sm}
}

// ---- login / logout (phase 10 of docs/auth-plan.md) ----

// loginForm handles GET /app/login: the login page. Already-authenticated
// visitors are redirected to the task page.
func (h *handlers) loginForm(c *gin.Context) {
	if value, err := c.Cookie(sessionCookieName); err == nil && value != "" {
		if uid, ok := h.sm.verify(value, time.Now()); ok {
			if _, uerr := h.svc.UserByID(c.Request.Context(), uid); uerr == nil {
				c.Redirect(http.StatusSeeOther, "/app/")
				return
			}
		}
	}
	h.renderLogin(c, http.StatusOK, c.Query("next"), "")
}

// login handles POST /app/login: verifies CSRF, calls AuthUser, issues the
// session cookie, and redirects (PRG) to the requested page or /app/.
func (h *handlers) login(c *gin.Context) {
	if !verifyCSRF(c) {
		h.renderLogin(c, http.StatusForbidden, c.PostForm("next"), "")
		return
	}
	user, err := h.svc.AuthUser(c.Request.Context(), c.PostForm("email"), c.PostForm("password"))
	if err != nil {
		// Same generic message for unknown email and wrong password (NFR-2).
		h.renderLogin(c, http.StatusUnauthorized, c.PostForm("next"), "invalid email or password")
		return
	}
	c.SetCookie(sessionCookieName, h.sm.issue(user.ID(), time.Now()),
		int(sessionTTL.Seconds()), "/", "", secureCookie(c), true)
	c.Redirect(http.StatusSeeOther, sanitizeNext(c.PostForm("next")))
}

// renderLogin renders the login page with the data the shared shell needs.
// message is empty on a plain GET and carries the generic error on a failed
// POST; next is the same-origin redirect target. It is the single place the
// login.html contract (Title/HTMX/User/CSRF/Next/message) is defined.
func (h *handlers) renderLogin(c *gin.Context, status int, next, message string) {
	markVary(c)
	c.HTML(status, "login.html", gin.H{
		"Title":   "Sign in — To Do",
		"HTMX":    false,
		"User":    toUserView(domain.User{}),
		"CSRF":    ensureCSRFToken(c),
		"Next":    sanitizeNext(next),
		"message": message,
	})
}

// logout handles POST /app/logout: verifies CSRF, clears the session cookie,
// and returns to the login page. Logout is POST-only so it cannot be
// triggered by a link, a prefetch, or a cross-site GET.
func (h *handlers) logout(c *gin.Context) {
	if !verifyCSRF(c) {
		return
	}
	c.SetCookie(sessionCookieName, "", -1, "/", "", secureCookie(c), true)
	c.Redirect(http.StatusSeeOther, "/app/login")
}

// sanitizeNext whitelists a redirect target: same-origin paths only,
// defaulting to /app/.
func sanitizeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		return "/app/"
	}
	return next
}

// isHX reports whether the request is an HTMX fragment request.
func isHX(c *gin.Context) bool {
	return c.GetHeader("HX-Request") == "true"
}

// markVary sets Vary: HX-Request so caches do not mix partial and full pages.
func markVary(c *gin.Context) {
	c.Header("Vary", "HX-Request")
}

// scopeFilter returns the task filter for list/stats requests in the web UI:
// regular users are always scoped to their own tasks; admins see every
// user's tasks (the web UI has no cross-user inspector — spec docs/auth.md
// §3.4).
func scopeFilter(c *gin.Context, f repository.TaskFilter) repository.TaskFilter {
	user := currentUser(c)
	if !user.IsAdmin() {
		uid := user.ID()
		f.UserID = &uid
	}
	return f
}

// ---- index (full page shell) ----

// index handles GET /app/: the full page shell combining the task list, the
// filter bar, and the stats strip.
func (h *handlers) index(c *gin.Context) {
	tasks, stats, filter, err := h.listAndTotal(c)
	if err != nil {
		writeError(c, err)
		return
	}
	markVary(c)
	c.HTML(http.StatusOK, "index.html", gin.H{
		"Title":          "To Do",
		"HTMX":           true,
		"NavActionLabel": "+ New Task",
		"NavActionModal": "modal-create",
		"List":           toListPage(tasks, filter, len(tasks), stats.Total),
		"Stats":          toStatsView(stats, filter, stats.Total > 0),
		"User":           toUserView(currentUser(c)),
		"CSRF":           ensureCSRFToken(c),
	})
}

// listAndTotal returns the page of tasks, the scoped stats, and the parsed
// filter used for the query. It is the single place that parses the query
// string, so callers never swallow a filterFromQuery error.
func (h *handlers) listAndTotal(c *gin.Context) ([]domain.Task, application.TaskStats, repository.TaskFilter, error) {
	filter, err := filterFromQuery(c)
	if err != nil {
		return nil, application.TaskStats{}, repository.TaskFilter{}, err
	}
	filter = scopeFilter(c, filter)
	tasks, err := h.svc.ListTasks(c.Request.Context(), filter)
	if err != nil {
		return nil, application.TaskStats{}, repository.TaskFilter{}, err
	}
	stats, err := h.svc.Stats(c.Request.Context(), filter)
	if err != nil {
		return nil, application.TaskStats{}, repository.TaskFilter{}, err
	}
	return tasks, stats, filter, nil
}

// ---- FR-1: list / filter / pagination (fragment) ----

// list handles GET /app/tasks: the task list fragment re-rendered after
// filtering, searching, or paging.
func (h *handlers) list(c *gin.Context) {
	tasks, stats, filter, err := h.listAndTotal(c)
	if err != nil {
		writeError(c, err)
		return
	}
	markVary(c)
	c.HTML(http.StatusOK, "partials/task_list.html", gin.H{
		"List": toListPage(tasks, filter, len(tasks), stats.Total),
	})
}

// ---- FR-6: stats (fragment) ----

// stats handles GET /app/stats: the stats strip fragment with per-status
// counters and the "Done" percentage meter, scoped to the caller's role
// (regular users see only their own counts).
func (h *handlers) stats(c *gin.Context) {
	filter, err := filterFromQuery(c)
	if err != nil {
		writeError(c, err)
		return
	}
	stats, err := h.svc.Stats(c.Request.Context(), scopeFilter(c, filter))
	if err != nil {
		writeError(c, err)
		return
	}
	markVary(c)
	c.HTML(http.StatusOK, "partials/stats.html", gin.H{
		"Stats": toStatsView(stats, filter, stats.Total > 0),
	})
}

// ---- FR-2: create ----

// create handles POST /app/tasks: it reads the form fields, creates the
// task, and answers with the multi-region mutation response.
func (h *handlers) create(c *gin.Context) {
	deadline, err := optionalDeadline(c.PostForm("deadline"))
	if err != nil {
		writeError(c, err)
		return
	}
	priority, err := postPriority(c.PostForm("priority"))
	if err != nil {
		writeError(c, err)
		return
	}
	task, err := h.svc.CreateTask(c.Request.Context(), application.CreateTaskInput{
		UserID:      currentUser(c).ID(),
		Title:       c.PostForm("title"),
		Description: c.PostForm("description"),
		Priority:    priority,
		Deadline:    deadline,
	})
	if err != nil {
		writeError(c, err)
		return
	}
	h.writeMutation(c, task, "Task created")
}

// ---- FR-3: edit form fragment ----

// editForm handles GET /app/tasks/:id/edit: the edit modal prefilled with
// the current task values. Ownership is enforced by the service (FR-U4): a
// regular user can only open the edit form for their own tasks.
func (h *handlers) editForm(c *gin.Context) {
	task, err := h.svc.GetTask(c.Request.Context(), domain.TaskID(c.Param("id")), callerOf(c))
	if err != nil {
		writeError(c, err)
		return
	}
	markVary(c)
	c.HTML(http.StatusOK, "partials/modal_edit.html", gin.H{
		"Task": toTaskView(task),
		"CSRF": ensureCSRFToken(c),
	})
}

// ---- FR-3: update ----

// update handles PATCH /app/tasks/:id: it applies whichever of title,
// priority, or deadline fields are present in the form and answers with the
// mutation response.
func (h *handlers) update(c *gin.Context) {
	id := domain.TaskID(c.Param("id"))
	caller := callerOf(c)

	var task domain.Task
	var err error
	applied := false

	if title, present := c.GetPostForm("title"); present {
		applied = true
		if title == "" {
			writeError(c, domain.Invalid("title must not be empty"))
			return
		}
		task, err = h.svc.RenameTask(c.Request.Context(), id, title, caller)
		if err != nil {
			writeError(c, err)
			return
		}
	}
	if p, present := c.GetPostForm("priority"); present && p != "" {
		priority, perr := strconv.Atoi(p)
		if perr != nil {
			writeError(c, domain.Invalid("priority must be an integer"))
			return
		}
		applied = true
		task, err = h.svc.ChangePriority(c.Request.Context(), id, priority, caller)
		if err != nil {
			writeError(c, err)
			return
		}
	}
	if c.PostForm("clear_deadline") == "on" {
		applied = true
		task, err = h.svc.ClearDeadline(c.Request.Context(), id, caller)
		if err != nil {
			writeError(c, err)
			return
		}
	} else if d := c.PostForm("deadline"); d != "" {
		applied = true
		deadline, derr := httpconv.ParseDeadline(d)
		if derr != nil {
			writeError(c, derr)
			return
		}
		task, err = h.svc.SetDeadline(c.Request.Context(), id, deadline, caller)
		if err != nil {
			writeError(c, err)
			return
		}
	}

	if !applied {
		writeError(c, domain.Invalid("no recognized field to update"))
		return
	}
	h.writeMutation(c, task, "Task updated")
}

// ---- FR-4: change status ----

// changeStatus handles POST /app/tasks/:id/status: it moves the task
// through the domain state machine and answers with the mutation response
// (toast carries the new status label). Ownership is enforced (FR-U4).
func (h *handlers) changeStatus(c *gin.Context) {
	status, err := httpconv.ParseStatus(c.PostForm("status"))
	if err != nil {
		writeError(c, err)
		return
	}
	task, err := h.svc.ChangeStatus(c.Request.Context(), domain.TaskID(c.Param("id")), status, callerOf(c))
	if err != nil {
		writeError(c, err)
		return
	}
	h.writeMutation(c, task, "Task moved to "+statusMeta[status].label)
}

// ---- FR-5: delete confirm fragment ----

// deleteForm handles GET /app/tasks/:id/delete: the delete confirmation
// modal prefilled with the task title. Ownership is enforced by the service
// (FR-U4).
func (h *handlers) deleteForm(c *gin.Context) {
	task, err := h.svc.GetTask(c.Request.Context(), domain.TaskID(c.Param("id")), callerOf(c))
	if err != nil {
		writeError(c, err)
		return
	}
	markVary(c)
	c.HTML(http.StatusOK, "partials/modal_delete.html", gin.H{
		"Task": toTaskView(task),
		"CSRF": ensureCSRFToken(c),
	})
}

// ---- FR-5: delete ----

// delete handles DELETE /app/tasks/:id: it removes the task and answers
// with the mutation response. Ownership is enforced (FR-U4).
func (h *handlers) delete(c *gin.Context) {
	id := domain.TaskID(c.Param("id"))
	if err := h.svc.DeleteTask(c.Request.Context(), id, callerOf(c)); err != nil {
		writeError(c, err)
		return
	}
	h.writeMutation(c, domain.Task{}, "Task deleted")
}

// ---- toast auto-dismiss backing endpoint ----

// empty handles GET /app/partials/empty: an intentionally blank response
// that HTMX uses as the target for auto-dismissing toasts.
func (h *handlers) empty(c *gin.Context) {
	markVary(c)
	c.String(http.StatusOK, "")
}

// ---- shared mutation response ----

// writeMutation renders the uniform multi-region response: the list, the
// stats, and a toast, distributed via <hx-partial> elements. For non-HX
// browsers, fall back to a PRG redirect to the canonical list URL.
//
// The whole list + stats are re-rendered deliberately: a mutation can change
// row ordering (priority), list membership (status/search/page filters), and
// the per-status counters, so a targeted single-row or OOB swap would leave
// the UI inconsistent. The <hx-partial> regions keep the payload to the
// regions that actually need updating while the server stays the source of
// truth.
func (h *handlers) writeMutation(c *gin.Context, _ domain.Task, toast string) {
	tasks, stats, filter, err := h.listAndTotal(c)
	if err != nil {
		writeError(c, err)
		return
	}

	markVary(c)
	if !isHX(c) {
		c.Redirect(http.StatusSeeOther, listURL(c))
		return
	}
	c.HTML(http.StatusOK, "partials/mutation.html", gin.H{
		"List":  toListPage(tasks, filter, len(tasks), stats.Total),
		"Stats": toStatsView(stats, filter, stats.Total > 0),
		"Toast": toast,
	})
}

// ---- small helpers ----

// callerOf resolves the authenticated caller identity from the DB-loaded user
// stored by requireAuth.
func callerOf(c *gin.Context) application.Caller {
	return application.CallerOf(currentUser(c))
}

// optionalDeadline parses the create-form deadline. An empty value means
// "no deadline" (nil); a non-empty invalid value is a 400, matching the
// update path.
func optionalDeadline(s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	t, err := httpconv.ParseDeadline(s)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// listURL builds the canonical /app/ URL for a non-HTMX PRG redirect,
// carrying the active status/search filters as properly encoded query
// parameters.
func listURL(c *gin.Context) string {
	q := url.Values{}
	if s := c.Query("status"); s != "" {
		q.Set("status", s)
	}
	if s := c.Query("search"); s != "" {
		q.Set("search", s)
	}
	if len(q) == 0 {
		return "/app/"
	}
	return "/app/?" + q.Encode()
}

// postPriority parses the priority from a create form; empty defaults to 0.
func postPriority(s string) (int, error) {
	if s == "" {
		return 0, nil
	}
	p, err := strconv.Atoi(s)
	if err != nil {
		return 0, domain.Invalid("priority must be an integer")
	}
	if p < domain.MinPriority || p > domain.MaxPriority {
		return 0, domain.Invalid("priority must be between 0 and 5")
	}
	return p, nil
}
