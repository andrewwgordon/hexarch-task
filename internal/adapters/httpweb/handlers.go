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
//   - data:       pageData, listAndTotal
//   - handlers:   index, list, stats, create, editForm, update,
//     changeStatus, deleteForm, delete, empty, writeMutation
//   - helpers:    optionalDeadline, postPriority
package httpweb

import (
	"net/http"
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
	markVary(c)
	c.HTML(http.StatusOK, "login.html", gin.H{
		"CSRF": ensureCSRFToken(c),
		"Next": sanitizeNext(c.Query("next")),
	})
}

// login handles POST /app/login: verifies CSRF, calls AuthUser, issues the
// session cookie, and redirects (PRG) to the requested page or /app/.
func (h *handlers) login(c *gin.Context) {
	if !verifyCSRF(c) {
		c.HTML(http.StatusForbidden, "login.html", gin.H{
			"CSRF": ensureCSRFToken(c),
			"Next": sanitizeNext(c.PostForm("next")),
		})
		return
	}
	user, err := h.svc.AuthUser(c.Request.Context(), c.PostForm("email"), c.PostForm("password"))
	if err != nil {
		// Same generic message for unknown email and wrong password (NFR-2).
		markVary(c)
		c.HTML(http.StatusUnauthorized, "login.html", gin.H{
			"CSRF":    ensureCSRFToken(c),
			"Next":    sanitizeNext(c.PostForm("next")),
			"message": "invalid email or password",
		})
		return
	}
	c.SetCookie(sessionCookieName, h.sm.issue(user.ID(), time.Now()),
		int(sessionTTL.Seconds()), "/", "", secureCookie(c), true)
	c.Redirect(http.StatusSeeOther, sanitizeNext(c.PostForm("next")))
}

// logout handles GET and POST /app/logout: clears the session cookie and
// returns to the login page. (POST is CSRF-protected; GET is permitted as a
// convenience link.)
func (h *handlers) logout(c *gin.Context) {
	if c.Request.Method == http.MethodPost && !verifyCSRF(c) {
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

// pageData gathers the stats needed by both the list and stats views.
func (h *handlers) pageData(c *gin.Context) (application.TaskStats, error) {
	return h.svc.Stats(c.Request.Context(), scopeFilter(c, repository.DefaultTaskFilter()))
}

// ---- index (full page shell) ----

// index handles GET /app/: the full page shell combining the task list, the
// filter bar, and the stats strip.
func (h *handlers) index(c *gin.Context) {
	tasks, total, err := h.listAndTotal(c)
	if err != nil {
		writeError(c, err)
		return
	}
	filter, err := filterFromQuery(c)
	if err != nil {
		writeError(c, err)
		return
	}
	stats, err := h.svc.Stats(c.Request.Context(), scopeFilter(c, repository.DefaultTaskFilter()))
	if err != nil {
		writeError(c, err)
		return
	}
	markVary(c)
	c.HTML(http.StatusOK, "index.html", gin.H{
		"List":  toListPage(tasks, filter, len(tasks), total),
		"Stats": toStatsView(stats, filter, total > 0),
		"User":  toUserView(currentUser(c)),
		"CSRF":  ensureCSRFToken(c),
	})
}

// listAndTotal returns the page of tasks plus the repository-wide total.
func (h *handlers) listAndTotal(c *gin.Context) ([]domain.Task, int, error) {
	filter, err := filterFromQuery(c)
	if err != nil {
		return nil, 0, err
	}
	filter = scopeFilter(c, filter)
	tasks, err := h.svc.ListTasks(c.Request.Context(), filter)
	if err != nil {
		return nil, 0, err
	}
	total, err := h.svc.Stats(c.Request.Context(), filter)
	if err != nil {
		return nil, 0, err
	}
	return tasks, total.Total, nil
}

// ---- FR-1: list / filter / pagination (fragment) ----

// list handles GET /app/tasks: the task list fragment re-rendered after
// filtering, searching, or paging.
func (h *handlers) list(c *gin.Context) {
	tasks, total, err := h.listAndTotal(c)
	if err != nil {
		writeError(c, err)
		return
	}
	filter, _ := filterFromQuery(c)
	markVary(c)
	c.HTML(http.StatusOK, "partials/task_list.html", gin.H{
		"List": toListPage(tasks, filter, len(tasks), total),
	})
}

// ---- FR-6: stats (fragment) ----

// stats handles GET /app/stats: the stats strip fragment with per-status
// counters and the "Done" percentage meter, scoped to the caller's role
// (regular users see only their own counts).
func (h *handlers) stats(c *gin.Context) {
	filter, _ := filterFromQuery(c)
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
	deadline := optionalDeadline(c.PostForm("deadline"))
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
func (h *handlers) writeMutation(c *gin.Context, _ domain.Task, toast string) {
	tasks, total, err := h.listAndTotal(c)
	if err != nil {
		writeError(c, err)
		return
	}
	stats, err := h.svc.Stats(c.Request.Context(), scopeFilter(c, repository.DefaultTaskFilter()))
	if err != nil {
		writeError(c, err)
		return
	}
	filter, _ := filterFromQuery(c)

	markVary(c)
	if !isHX(c) {
		c.Redirect(http.StatusSeeOther, "/app/?status="+c.Query("status")+"&search="+c.Query("search"))
		return
	}
	c.HTML(http.StatusOK, "partials/mutation.html", gin.H{
		"List":  toListPage(tasks, filter, len(tasks), total),
		"Stats": toStatsView(stats, filter, total > 0),
		"Toast": toast,
	})
}

// ---- small helpers ----

// callerOf resolves the authenticated caller identity from the DB-loaded user
// stored by requireAuth.
func callerOf(c *gin.Context) application.Caller {
	return application.CallerOf(currentUser(c))
}

// optionalDeadline parses the create-form deadline, returning nil for empty
// or invalid input (the domain treats an absent deadline as none).
func optionalDeadline(s string) *time.Time {
	if s == "" {
		return nil
	}
	t, err := httpconv.ParseDeadline(s)
	if err != nil {
		return nil
	}
	return &t
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
