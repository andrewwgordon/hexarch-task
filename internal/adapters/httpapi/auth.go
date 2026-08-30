// auth.go implements the authentication middleware for the /api group
// (spec docs/auth.md FR-A2): every route requires either HTTP Basic auth
// (email + password, verified via TaskService.AuthUser) or an X-API-Key
// header (resolved via TaskService.UserByAPIKey). On success the
// authenticated user is stored in the Gin context; on failure the request is
// aborted with 401 and a WWW-Authenticate challenge.
//
// Public API: (none — middleware and context key are unexported)
//
// Private:
//   - ctxUserKey, currentUser
//   - authMiddleware
package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"hexarch/internal/application"
	"hexarch/internal/domain"
	"hexarch/internal/repository"
)

// ctxUserKey is the Gin context key under which the authenticated
// domain.User is stored.
const ctxUserKey = "auth.user"

// currentUser returns the authenticated user stored by the middleware.
func currentUser(c *gin.Context) domain.User {
	v, ok := c.Get(ctxUserKey)
	if !ok {
		return domain.User{}
	}
	u, _ := v.(domain.User)
	return u
}

// authMiddleware enforces per-request authentication via HTTP Basic or
// X-API-Key (spec FR-A2). Bad credentials and unknown API keys produce the
// same 401 response — no account enumeration.
func authMiddleware(svc application.TaskService) gin.HandlerFunc {
	return func(c *gin.Context) {
		// X-API-Key path first: it takes precedence over Basic when both are
		// present.
		if key := c.GetHeader("X-API-Key"); key != "" {
			user, err := svc.UserByAPIKey(c.Request.Context(), key)
			if err != nil {
				unauthorized(c)
				return
			}
			c.Set(ctxUserKey, user)
			c.Next()
			return
		}

		if user, pass, ok := c.Request.BasicAuth(); ok {
			u, err := svc.AuthUser(c.Request.Context(), user, pass)
			if err != nil {
				unauthorized(c)
				return
			}
			c.Set(ctxUserKey, u)
			c.Next()
			return
		}

		unauthorized(c)
	}
}

// unauthorized aborts with the uniform 401 envelope and Basic challenge.
func unauthorized(c *gin.Context) {
	c.Header("WWW-Authenticate", `Basic realm="hexarch", charset="UTF-8"`)
	c.AbortWithStatusJSON(http.StatusUnauthorized, ErrorResponse{Error: ErrorBody{
		Code:    "UNAUTHORIZED",
		Message: "authentication required: Basic auth or X-API-Key",
	}})
}

// scopeFilter returns the task filter for list/stats requests: regular users
// are always scoped to their own tasks; admins may pass ?user_id= to inspect
// another user's tasks, or ?user_id=all to span every user.
func scopeFilter(c *gin.Context, f repository.TaskFilter) repository.TaskFilter {
	user := currentUser(c)
	if user.IsAdmin() {
		switch v := c.Query("user_id"); v {
		case "", "all":
			// Admin default: all users.
		default:
			uid := domain.UserID(v)
			f.UserID = &uid
		}
		return f
	}
	uid := user.ID()
	f.UserID = &uid
	return f
}
