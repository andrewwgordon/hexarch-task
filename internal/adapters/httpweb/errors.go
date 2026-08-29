// errors.go centralizes web UI error fragments.
//
// writeError renders the shared error_alert.html partial with the message
// extracted from the error and aborts with the mapped status: domain Kinds
// map to 4xx/5xx (see httpconv), unknown errors become a generic 500.
//
// Public API: (none — writeError is unexported)
package httpweb

import (
	"github.com/gin-gonic/gin"

	"hexarch/internal/adapters/httpconv"
)

// writeError renders the shared HTML error fragment and aborts with the
// mapped status. Domain errors are mapped via their Kind; everything else
// becomes a generic 500 without leaking internal details.
func writeError(c *gin.Context, err error) {
	if err == nil {
		return
	}
	status, _, msg := httpconv.ExtractStatusAndMessage(err)
	c.Status(status)
	c.HTML(status, "partials/error_alert.html", gin.H{"message": msg})
	c.Abort()
}
