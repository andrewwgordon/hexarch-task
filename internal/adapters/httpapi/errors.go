// errors.go centralizes REST error responses.
//
// writeError converts any error into the uniform JSON envelope
// { "error": { "code", "message" } } and aborts the request with the HTTP
// status derived from the domain error Kind (see httpconv). Non-domain
// errors map to a generic 500 so internals never leak.
//
// Public API: (none — writeError is unexported)
package httpapi

import (
	"github.com/gin-gonic/gin"

	"hexarch/internal/adapters/httpconv"
)

// writeError writes the consistent JSON error envelope for any error.
// Domain errors are mapped via their Kind; everything else becomes a generic
// 500 without leaking internal details.
func writeError(c *gin.Context, err error) {
	if err == nil {
		return
	}
	status, code, msg := httpconv.ExtractStatusAndMessage(err)
	c.AbortWithStatusJSON(status, ErrorResponse{Error: ErrorBody{
		Code:    code,
		Message: msg,
	}})
}
