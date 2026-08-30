// handlers.go implements the Gin handlers for the /api group.
//
// Handlers are deliberately thin: they decode input, call exactly one
// application method, and marshal the result. All error mapping funnels
// through writeError (errors.go). Authentication is enforced by the
// middleware in auth.go; handlers read the user via currentUser.
//
// Public API: (none — handlers is unexported; NewRouter in router.go
// exposes the engine)
//
// Private:
//   - handlers, newHandlers
//   - handlers: login, create, get, list, update, delete, stats
//   - helper:   filterFromQuery
package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"hexarch/internal/adapters/httpconv"
	"hexarch/internal/application"
	"hexarch/internal/domain"
	"hexarch/internal/repository"
)

// handlers bundles the service and implements the /api route handlers.
// Each method corresponds to one route registered in router.go.
type handlers struct {
	svc application.TaskService
}

// newHandlers builds the handler set for the given service.
func newHandlers(svc application.TaskService) *handlers {
	return &handlers{svc: svc}
}

// ---- login (optional; lets clients fetch their API key) ----

// login handles POST /api/login: a JSON {email, password} body verified via
// AuthUser. The response is limited to id/email/apikey/isadmin — the bcrypt
// hash is never included (spec NFR-8).
func (h *handlers) login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, domain.Invalid("invalid body: "+err.Error()))
		return
	}
	user, err := h.svc.AuthUser(c.Request.Context(), req.Email, req.Password)
	if err != nil {
		// Login failures are always 401 with the generic message (the service
		// already returns a single invalid-credentials error, NFR-2).
		c.AbortWithStatusJSON(http.StatusUnauthorized, ErrorResponse{Error: ErrorBody{
			Code:    "UNAUTHORIZED",
			Message: "invalid email or password",
		}})
		return
	}
	c.JSON(http.StatusOK, toUserResponse(user))
}

// ---- create ----

// create handles POST /api/tasks: it binds the JSON body, converts it into
// application input, creates the task, and answers 201 Created with the task.
func (h *handlers) create(c *gin.Context) {
	var req CreateTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, domain.Invalid("invalid body: "+err.Error()))
		return
	}
	in, err := req.toCreateInput()
	if err != nil {
		writeError(c, err)
		return
	}
	in.UserID = currentUser(c).ID()
	task, err := h.svc.CreateTask(c.Request.Context(), in)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, toTaskResponse(task))
}

// ---- get ----

// get handles GET /api/tasks/:id and answers 200 with the task. Ownership
// is enforced by the service (FR-U4): a regular user may only read their own
// tasks; another user's task answers 404 like a missing one.
func (h *handlers) get(c *gin.Context) {
	task, err := h.svc.GetTask(c.Request.Context(), domain.TaskID(c.Param("id")), callerOf(c))
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, toTaskResponse(task))
}

// ---- list ----

// list handles GET /api/tasks: it builds a TaskFilter from the query
// parameters and answers 200 with a ListResponse (tasks + count).
func (h *handlers) list(c *gin.Context) {
	filter, err := filterFromQuery(c)
	if err != nil {
		writeError(c, err)
		return
	}
	filter = scopeFilter(c, filter)
	tasks, err := h.svc.ListTasks(c.Request.Context(), filter)
	if err != nil {
		writeError(c, err)
		return
	}
	resp := ListResponse{Tasks: make([]TaskResponse, 0, len(tasks)), Count: len(tasks)}
	for _, t := range tasks {
		resp.Tasks = append(resp.Tasks, toTaskResponse(t))
	}
	c.JSON(http.StatusOK, resp)
}

// ---- patch (rename / status / priority / deadline) ----

// update handles PATCH /api/tasks/:id. Any subset of title, status, priority
// and deadline may be present; each non-nil field is applied in order, and
// the final task state is returned. deadline null clears the deadline.
// Ownership is enforced per operation (FR-U4).
func (h *handlers) update(c *gin.Context) {
	id := domain.TaskID(c.Param("id"))
	caller := callerOf(c)

	var req UpdateTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, domain.Invalid("invalid body: "+err.Error()))
		return
	}

	var task domain.Task
	var err error
	changed := false

	if req.Title != nil {
		changed = true
		task, err = h.svc.RenameTask(c.Request.Context(), id, *req.Title, caller)
		if err != nil {
			writeError(c, err)
			return
		}
	}
	if req.Status != nil {
		changed = true
		status, perr := httpconv.ParseStatus(*req.Status)
		if perr != nil {
			writeError(c, perr)
			return
		}
		task, err = h.svc.ChangeStatus(c.Request.Context(), id, status, caller)
		if err != nil {
			writeError(c, err)
			return
		}
	}
	if req.Priority != nil {
		changed = true
		task, err = h.svc.ChangePriority(c.Request.Context(), id, *req.Priority, caller)
		if err != nil {
			writeError(c, err)
			return
		}
	}
	if req.Deadline != nil {
		changed = true
		raw := bytes.TrimSpace(req.Deadline)
		if bytes.Equal(raw, []byte("null")) {
			task, err = h.svc.ClearDeadline(c.Request.Context(), id, caller)
		} else {
			var s string
			if uerr := json.Unmarshal(raw, &s); uerr != nil {
				writeError(c, domain.Invalid("deadline must be a YYYY-MM-DD string or null"))
				return
			}
			d, perr := httpconv.ParseDeadline(s)
			if perr != nil {
				writeError(c, perr)
				return
			}
			task, err = h.svc.SetDeadline(c.Request.Context(), id, d, caller)
		}
		if err != nil {
			writeError(c, err)
			return
		}
	}

	// No recognized field was provided.
	if !changed {
		writeError(c, domain.Invalid("no recognized field to update (title|status|priority|deadline)"))
		return
	}

	c.JSON(http.StatusOK, toTaskResponse(task))
}

// ---- delete ----

// delete handles DELETE /api/tasks/:id and answers 204 No Content. Ownership
// is enforced by the service (FR-U4): a regular user may only delete their
// own tasks.
func (h *handlers) delete(c *gin.Context) {
	if err := h.svc.DeleteTask(c.Request.Context(), domain.TaskID(c.Param("id")), callerOf(c)); err != nil {
		writeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ---- stats ----

// stats handles GET /api/stats and answers 200 with per-status counts,
// scoped by role: regular users see only their own counts; admins see every
// user's (or ?user_id=<id> to narrow to one user).
func (h *handlers) stats(c *gin.Context) {
	filter := scopeFilter(c, repository.DefaultTaskFilter())
	s, err := h.svc.Stats(c.Request.Context(), filter)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, toStatsResponse(s))
}

// ---- helpers ----

// callerOf resolves the authenticated caller identity from the request
// context (set by authMiddleware).
func callerOf(c *gin.Context) application.Caller {
	return application.CallerOf(currentUser(c))
}

// filterFromQuery builds a repository.TaskFilter from query parameters,
// using DefaultTaskFilter() defaults.
func filterFromQuery(c *gin.Context) (repository.TaskFilter, error) {
	f := repository.DefaultTaskFilter()
	if s := c.Query("status"); s != "" {
		st, err := httpconv.ParseStatus(s)
		if err != nil {
			return repository.TaskFilter{}, err
		}
		f.Status = &st
	}
	f.Search = c.Query("search")
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return repository.TaskFilter{}, domain.Invalid("limit must be an integer")
		}
		f.Limit = n
	}
	if v := c.Query("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return repository.TaskFilter{}, domain.Invalid("offset must be an integer")
		}
		f.Offset = n
	}
	return f, nil
}
