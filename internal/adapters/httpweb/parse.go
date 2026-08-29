// parse.go builds repository.TaskFilter values from the web UI's query
// string (status, search, offset), pinning the limit to the fixed page
// size. It is shared by the list, stats, and mutation handlers.
//
// Public API: (none — filterFromQuery is unexported)
package httpweb

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"hexarch/internal/adapters/httpconv"
	"hexarch/internal/domain"
	"hexarch/internal/repository"
)

// filterFromQuery builds a repository.TaskFilter from query parameters,
// using DefaultTaskFilter() defaults. It returns a filter with pageSize
// applied, ready for the web UI list pages.
func filterFromQuery(c *gin.Context) (repository.TaskFilter, error) {
	f := repository.DefaultTaskFilter()
	f.Limit = pageSize
	if s := c.Query("status"); s != "" {
		st, err := httpconv.ParseStatus(s)
		if err != nil {
			return repository.TaskFilter{}, err
		}
		f.Status = &st
	}
	f.Search = c.Query("search")
	if v := c.Query("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return repository.TaskFilter{}, domain.Invalid("offset must be an integer")
		}
		f.Offset = n
	}
	return f, nil
}
