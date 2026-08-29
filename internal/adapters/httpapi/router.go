// router.go wires the REST routes onto a Gin engine.
//
// NewRouter is exported so tests (including the end-to-end suite under
// ./test) can drive the API via httptest without binding a real port.
//
// Public API:
//   - NewRouter
//
// Private:
//   - newHandlers (implemented in handlers.go)
package httpapi

import (
	"github.com/gin-gonic/gin"

	"hexarch/internal/application"
)

// NewRouter wires the REST API routes onto a new Gin engine. It is exported
// so external test packages (./test) can build and drive the engine via
// httptest without binding a real port.
func NewRouter(svc application.TaskService) *gin.Engine {
	gin.SetMode(gin.ReleaseMode) // silence debug warnings in tests
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())

	h := newHandlers(svc)

	api := r.Group("/api")
	{
		api.POST("/tasks", h.create)
		api.GET("/tasks", h.list)
		api.GET("/tasks/:id", h.get)
		api.PATCH("/tasks/:id", h.update)
		api.DELETE("/tasks/:id", h.delete)
		api.GET("/stats", h.stats)
	}
	return r
}
