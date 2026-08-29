// router.go wires the web routes onto a Gin engine under the /app group and
// loads the HTML templates.
//
// NewRouter is exported so tests (including the end-to-end suite under
// ./test) can drive the engine via httptest without binding a real port.
//
// Public API:
//   - NewRouter
//
// Private:
//   - templateDir, loadTemplates
package httpweb

import (
	"fmt"
	"html/template"
	"path/filepath"
	"runtime"

	"github.com/gin-gonic/gin"

	"hexarch/internal/application"
)

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

	h := newHandlers(svc)

	web := r.Group("/app")
	{
		web.GET("/", h.index)
		web.GET("/tasks", h.list)
		// Specific routes before generic ones (Gin order rule).
		web.POST("/tasks", h.create)
		web.GET("/tasks/:id/edit", h.editForm)
		web.GET("/tasks/:id/delete", h.deleteForm)
		web.PATCH("/tasks/:id", h.update)
		web.POST("/tasks/:id/status", h.changeStatus)
		web.DELETE("/tasks/:id", h.delete)
		web.GET("/stats", h.stats)
		web.GET("/partials/empty", h.empty)
	}
	return r, nil
}

// templateDir resolves the templates directory relative to this package's
// source location, so it works both when the binary runs from the repo root
// and when tests run from ./test (where the working directory differs).
func templateDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "internal/adapters/httpweb/templates"
	}
	return filepath.Join(filepath.Dir(file), "templates")
}

// loadTemplates parses the full page shell and all partial fragments. The
// stdlib glob does not recurse, so both directories are parsed explicitly.
func loadTemplates() (*template.Template, error) {
	dir := templateDir()
	tmpl := template.New("")
	shell := filepath.Join(dir, "*.html")
	if _, err := tmpl.ParseGlob(shell); err != nil {
		return nil, fmt.Errorf("parse shell templates: %w", err)
	}
	partials := filepath.Join(dir, "partials", "*.html")
	if _, err := tmpl.ParseGlob(partials); err != nil {
		return nil, fmt.Errorf("parse partial templates: %w", err)
	}
	return tmpl, nil
}
