// user_handlers.go implements the admin-only user management routes
// (spec docs/auth.md FR-A3, phase 11 of docs/auth-plan.md): list, create,
// edit, delete users, following the same dual-render strategy (HTMX
// fragments vs full pages) and PRG fallback as the task handlers.
//
// Public API: (none — handlers are unexported; see router.go)
//
// Private:
//   - handlers: usersList, usersCreate, userEditForm, userUpdate,
//     userDeleteForm, userDelete, writeUserMutation
package httpweb

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"hexarch/internal/domain"
)

// ---- list ----

// usersList handles GET /app/users: the user management page (admin only).
func (h *handlers) usersList(c *gin.Context) {
	users, err := h.svc.ListUsers(c.Request.Context())
	if err != nil {
		writeError(c, err)
		return
	}
	markVary(c)
	c.HTML(http.StatusOK, "users.html", gin.H{
		"Users":       toUserViews(users),
		"CurrentID":   currentUser(c).ID().String(),
		"CurrentUser": currentUser(c).Email(),
		"CSRF":        ensureCSRFToken(c),
	})
}

// ---- create ----

// usersCreate handles POST /app/users: creates a user from the form fields
// (email, password, isadmin) and answers with the user-list mutation.
func (h *handlers) usersCreate(c *gin.Context) {
	input, err := parseCreateUserForm(c)
	if err != nil {
		writeError(c, err)
		return
	}
	if _, err := h.svc.CreateUser(c.Request.Context(), input); err != nil {
		writeError(c, err)
		return
	}
	h.writeUserMutation(c, "User created")
}

// ---- edit ----

// userEditForm handles GET /app/users/:id/edit: the edit modal prefilled
// with the user's current email and admin flag (never the password hash).
func (h *handlers) userEditForm(c *gin.Context) {
	user, err := h.svc.UserByID(c.Request.Context(), domain.UserID(c.Param("id")))
	if err != nil {
		writeError(c, err)
		return
	}
	markVary(c)
	c.HTML(http.StatusOK, "partials/modal_user_edit.html", gin.H{
		"User": toUserView(user),
		"CSRF": ensureCSRFToken(c),
	})
}

// userUpdate handles PATCH /app/users/:id: applies only the provided fields
// (email, password, isadmin); empty password = unchanged.
func (h *handlers) userUpdate(c *gin.Context) {
	id := domain.UserID(c.Param("id"))
	input, err := parseUpdateUserForm(c)
	if err != nil {
		writeError(c, err)
		return
	}
	if _, err := h.svc.UpdateUser(c.Request.Context(), id, input); err != nil {
		writeError(c, err)
		return
	}
	h.writeUserMutation(c, "User updated")
}

// ---- delete ----

// userDeleteForm handles GET /app/users/:id/delete: the delete confirmation
// modal.
func (h *handlers) userDeleteForm(c *gin.Context) {
	user, err := h.svc.UserByID(c.Request.Context(), domain.UserID(c.Param("id")))
	if err != nil {
		writeError(c, err)
		return
	}
	markVary(c)
	c.HTML(http.StatusOK, "partials/modal_user_delete.html", gin.H{
		"User":      toUserView(user),
		"CurrentID": currentUser(c).ID().String(),
		"CSRF":      ensureCSRFToken(c),
	})
}

// userDelete handles DELETE /app/users/:id. Self-delete is blocked at the
// adapter (403); last-admin delete surfaces the service's Conflict.
func (h *handlers) userDelete(c *gin.Context) {
	id := domain.UserID(c.Param("id"))
	if id == currentUser(c).ID() {
		c.AbortWithStatus(http.StatusForbidden)
		c.HTML(http.StatusForbidden, "partials/error_alert.html",
			gin.H{"message": "you cannot delete your own account"})
		return
	}
	if err := h.svc.DeleteUser(c.Request.Context(), id); err != nil {
		writeError(c, err)
		return
	}
	h.writeUserMutation(c, "User deleted")
}

// ---- shared mutation response ----

// writeUserMutation renders the user-list fragment plus a toast for HTMX
// requests; non-HX browsers get the PRG redirect.
func (h *handlers) writeUserMutation(c *gin.Context, toast string) {
	users, err := h.svc.ListUsers(c.Request.Context())
	if err != nil {
		writeError(c, err)
		return
	}
	markVary(c)
	if !isHX(c) {
		c.Redirect(http.StatusSeeOther, "/app/users")
		return
	}
	c.HTML(http.StatusOK, "partials/user_mutation.html", gin.H{
		"Users":     toUserViews(users),
		"CurrentID": currentUser(c).ID().String(),
		"Toast":     toast,
	})
}
