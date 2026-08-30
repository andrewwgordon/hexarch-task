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
		api.POST("/login", h.login) // public: obtains the apikey via AuthUser
	}
	authed := r.Group("/api", authMiddleware(svc))
	{
		authed.GET("/tasks", h.list)
		authed.POST("/tasks", h.create)
		authed.GET("/tasks/:id", h.get)
		authed.PATCH("/tasks/:id", h.update)
		authed.DELETE("/tasks/:id", h.delete)
		authed.GET("/stats", h.stats)
	}
	return r
}
