// middleware.go implements the web UI access-control middlewares
// (spec docs/auth.md §3.7): requireAuth (redirect to /app/login when the
// session cookie is absent/invalid or the user no longer exists) and
// requireAdmin (403 page for non-admins). The authenticated user is loaded
// from the database on EVERY request — the session cookie carries only the
// userid, so demotions and deletions take effect immediately.
//
// Public API: (none — middlewares and context key are unexported)
//
// Private:
//   - ctxWebUserKey, currentUser
//   - requireAuth, requireAdmin, safeNext
package httpweb

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"hexarch/internal/application"
	"hexarch/internal/domain"
)

// ctxWebUserKey is the Gin context key for the authenticated user.
const ctxWebUserKey = "web.user"

// currentUser returns the DB-loaded authenticated user from the context.
func currentUser(c *gin.Context) domain.User {
	v, ok := c.Get(ctxWebUserKey)
	if !ok {
		return domain.User{}
	}
	u, _ := v.(domain.User)
	return u
}

// requireAuth validates the session cookie and loads the live user. Valid
// sessions for deleted users are rejected here (per-request privilege
// refresh). Unauthenticated requests are redirected to /app/login with a
// same-origin `next` parameter.
func requireAuth(svc application.TaskService, sm *sessionManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		value, err := c.Cookie(sessionCookieName)
		if err != nil || value == "" {
			redirectToLogin(c)
			return
		}
		userid, ok := sm.verify(value, time.Now())
		if !ok {
			redirectToLogin(c)
			return
		}
		user, err := svc.UserByID(c.Request.Context(), userid)
		if err != nil {
			// User deleted (or storage failure): treat as unauthenticated
			// rather than leaking a 500.
			redirectToLogin(c)
			return
		}
		c.Set(ctxWebUserKey, user)
		c.Next()
	}
}

// requireAdmin denies non-admin users with the 403 page. IsAdmin always
// comes from the database load in requireAuth, never from the cookie.
func requireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !currentUser(c).IsAdmin() {
			c.AbortWithStatus(http.StatusForbidden)
			c.HTML(http.StatusForbidden, "partials/error_alert.html",
				gin.H{"message": "admin role required"})
			return
		}
		c.Next()
	}
}

// redirectToLogin sends the browser to /app/login, preserving the requested
// path only when it is a same-origin path (open-redirect guard).
func redirectToLogin(c *gin.Context) {
	next := safeNext(c.Request.URL.Path)
	c.Redirect(http.StatusSeeOther, "/app/login?next="+next)
}

// safeNext returns a URL-escaped next value for same-origin paths only.
func safeNext(path string) string {
	if path == "" || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return "%2Fapp%2F"
	}
	return strings.ReplaceAll(path, "/", "%2F")
}
