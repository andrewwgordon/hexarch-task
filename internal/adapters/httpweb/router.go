// router.go wires the web routes onto a Gin engine under the /app group and
// loads the HTML templates. Authentication (phase 10 of docs/auth-plan.md):
// login/logout are public; every task page requires a valid session
// (requireAuth); the /users management pages additionally require the admin
// role (requireAdmin).
//
// NewRouter is exported so tests (including the end-to-end suite under
// ./test) can drive the engine via httptest without binding a real port.
//
// Public API:
//   - NewRouter
//
// Private:
//   - loadTemplates
//
// Embedded assets:
//   - templateFS
package httpweb

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"

	"github.com/gin-gonic/gin"

	"hexarch/internal/application"
)

// templateFS embeds the HTML templates into the executable so the binary is
// self-contained and no longer depends on the templates directory existing
// relative to the source tree at runtime.
//
//go:embed templates
var templateFS embed.FS

// NewRouter wires the web routes onto a new Gin engine under the /app group.
// It is exported so external test packages (./test) can build and drive the
// engine via httptest without binding a real port.
func NewRouter(svc application.TaskService) (*gin.Engine, error) {
	gin.SetMode(gin.ReleaseMode) // silence debug warnings in tests
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())

	tmpl, err := loadTemplates()
	if err != nil {
		return nil, fmt.Errorf("load templates: %w", err)
	}
	r.SetHTMLTemplate(tmpl)

	sm := newSessionManager()
	h := newHandlers(svc, sm)

	web := r.Group("/app")
	{
		// Public: authentication.
		web.GET("/login", h.loginForm)
		web.POST("/login", h.login)
		web.GET("/logout", h.logout)
		web.POST("/logout", h.logout)

		// Authenticated: task management (per-user scope; admin sees all).
		authed := web.Group("/", requireAuth(svc, sm), csrfProtect())
		{
			authed.GET("/", h.index)
			authed.GET("/tasks", h.list)
			// Specific routes before generic ones (Gin order rule).
			authed.POST("/tasks", h.create)
			authed.GET("/tasks/:id/edit", h.editForm)
			authed.GET("/tasks/:id/delete", h.deleteForm)
			authed.PATCH("/tasks/:id", h.update)
			authed.POST("/tasks/:id/status", h.changeStatus)
			authed.DELETE("/tasks/:id", h.delete)
			authed.GET("/stats", h.stats)
			authed.GET("/partials/empty", h.empty)
		}

		// Admin only: user management (phase 11 of docs/auth-plan.md).
		users := web.Group("/users", requireAuth(svc, sm), requireAdmin(), csrfProtect())
		{
			users.GET("", h.usersList)
			users.POST("", h.usersCreate)
			users.GET("/:id/edit", h.userEditForm)
			users.PATCH("/:id", h.userUpdate)
			users.GET("/:id/delete", h.userDeleteForm)
			users.DELETE("/:id", h.userDelete)
		}
	}
	return r, nil
}

// loadTemplates parses the full page shell and all partial fragments from the
// embedded filesystem. The stdlib glob does not recurse, so both directories
// are parsed explicitly. fs.Sub strips the "templates" root so template names
// match what the handlers render ("index.html", "partials/task_list.html", ...).
func loadTemplates() (*template.Template, error) {
	dir, err := fs.Sub(templateFS, "templates")
	if err != nil {
		return nil, fmt.Errorf("open embedded templates: %w", err)
	}
	tmpl := template.New("")
	if _, err := tmpl.ParseFS(dir, "*.html"); err != nil {
		return nil, fmt.Errorf("parse shell templates: %w", err)
	}
	if _, err := tmpl.ParseFS(dir, "partials/*.html"); err != nil {
		return nil, fmt.Errorf("parse shell partials: %w", err)
	}
	return tmpl, nil
}
