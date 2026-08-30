// parse.go builds repository.TaskFilter values from the web UI's query
// string (status, search, offset), pinning the limit to the fixed page
// size. It is shared by the list, stats, and mutation handlers.
//
// Public API: (none — filterFromQuery is unexported)
package httpweb

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"hexarch/internal/adapters/httpconv"
	"hexarch/internal/application"
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

// ---- user forms (phase 11 of docs/auth-plan.md) ----

// parseCreateUserForm reads email/password/isadmin from a user-create form.
func parseCreateUserForm(c *gin.Context) (application.CreateUserInput, error) {
	input := application.CreateUserInput{
		Email:    strings.ToLower(strings.TrimSpace(c.PostForm("email"))),
		Password: c.PostForm("password"),
	}
	if input.Email == "" {
		return input, domain.Invalid("email must not be empty")
	}
	if input.Password == "" {
		return input, domain.Invalid("password must not be empty")
	}
	input.IsAdmin = c.PostForm("isadmin") == "on"
	return input, nil
}

// parseUpdateUserForm reads the user-edit form into an UpdateUserInput where
// absent/empty fields mean "unchanged" (nil pointers); an empty password
// field is the documented way to keep the current password.
func parseUpdateUserForm(c *gin.Context) (application.UpdateUserInput, error) {
	input := application.UpdateUserInput{}
	if e := c.PostForm("email"); e != "" {
		email := strings.ToLower(strings.TrimSpace(e))
		input.Email = &email
	}
	if p := c.PostForm("password"); p != "" {
		input.Password = &p
	}
	if c.PostForm("isadmin") != "" {
		isAdmin := c.PostForm("isadmin") == "on" || c.PostForm("isadmin") == "true"
		input.IsAdmin = &isAdmin
	}
	return input, nil
}
