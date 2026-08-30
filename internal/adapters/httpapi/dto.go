// dto.go defines the JSON data-transfer objects of the REST API and the
// conversions between them, application input, and domain values.
//
// Public API (types):
//   - CreateTaskRequest, UpdateTaskRequest — request bodies
//   - TaskResponse, ListResponse, StatsResponse, ErrorResponse, ErrorBody —
//     response bodies
//
// Private:
//   - conversions: CreateTaskRequest.toCreateInput, toTaskResponse,
//     toStatsResponse
package httpapi

import (
	"encoding/json"
	"time"

	"hexarch/internal/adapters/httpconv"
	"hexarch/internal/application"
	"hexarch/internal/domain"
)

// CreateTaskRequest is the JSON body accepted by POST /tasks.
// Fields use JSON tags and are bound by Gin via ShouldBindJSON.
type CreateTaskRequest struct {
	Title       string `json:"title" binding:"required"`
	Description string `json:"description"`
	Priority    int    `json:"priority"`
	Deadline    string `json:"deadline"` // "YYYY-MM-DD" or empty
}

// UpdateTaskRequest is the JSON body accepted by PATCH /tasks/{id}.
// Any field that is non-nil is applied.
// Deadline is a RawMessage so we can distinguish absent (nil), null (clear),
// and a concrete "YYYY-MM-DD" value (set).
type UpdateTaskRequest struct {
	Title    *string         `json:"title"`
	Status   *string         `json:"status"`
	Priority *int            `json:"priority"`
	Deadline json.RawMessage `json:"deadline"`
}

// LoginRequest is the JSON body accepted by POST /api/login.
type LoginRequest struct {
	Email    string `json:"email" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// UserResponse is the JSON representation of a domain.User for API
// responses. It deliberately exposes id/email/apikey/isadmin only — the
// bcrypt password hash is never serialized (spec NFR-8).
type UserResponse struct {
	ID      string `json:"id"`
	Email   string `json:"email"`
	APIKey  string `json:"apikey"`
	IsAdmin bool   `json:"isadmin"`
}

// TaskResponse is the JSON representation of a domain.Task.
type TaskResponse struct {
	ID          string  `json:"id"`
	UserID      string  `json:"userid"`
	Title       string  `json:"title"`
	Description string  `json:"description"`
	Status      string  `json:"status"`
	Priority    int     `json:"priority"`
	Deadline    *string `json:"deadline"` // null when absent
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
}

// ListResponse wraps a page of tasks plus the count.
type ListResponse struct {
	Tasks []TaskResponse `json:"tasks"`
	Count int            `json:"count"`
}

// StatsResponse mirrors application.TaskStats.
type StatsResponse struct {
	Total      int `json:"total"`
	Todo       int `json:"todo"`
	InProgress int `json:"in_progress"`
	Done       int `json:"done"`
	Archived   int `json:"archived"`
}

// ErrorResponse is the consistent error envelope.
type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

// ErrorBody is the payload of an ErrorResponse: Code mirrors the domain
// error Kind when the error is a *domain.DomainError, otherwise "INTERNAL".
type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// toUserResponse converts a domain.User into its hash-free JSON form.
func toUserResponse(u domain.User) UserResponse {
	return UserResponse{
		ID:      u.ID().String(),
		Email:   u.Email(),
		APIKey:  u.APIKey(),
		IsAdmin: u.IsAdmin(),
	}
}

// ---- conversions: service input <- request ----

// toCreateInput converts a validated create request into the application input.
func (r CreateTaskRequest) toCreateInput() (application.CreateTaskInput, error) {
	var dl *time.Time
	if r.Deadline != "" {
		t, err := httpconv.ParseDeadline(r.Deadline)
		if err != nil {
			return application.CreateTaskInput{}, err
		}
		dl = &t
	}
	return application.CreateTaskInput{
		Title:       r.Title,
		Description: r.Description,
		Priority:    r.Priority,
		Deadline:    dl,
	}, nil
}

// toTaskResponse converts a domain.Task into its JSON representation.
func toTaskResponse(t domain.Task) TaskResponse {
	var dl *string
	if d := t.Deadline(); d != nil {
		s := d.Format(time.DateOnly)
		dl = &s
	}
	return TaskResponse{
		ID:          t.ID().String(),
		UserID:      t.UserID().String(),
		Title:       t.Title(),
		Description: t.Description(),
		Status:      t.Status().String(),
		Priority:    t.Priority(),
		Deadline:    dl,
		CreatedAt:   t.CreatedAt().Format(time.RFC3339),
		UpdatedAt:   t.UpdatedAt().Format(time.RFC3339),
	}
}

func toStatsResponse(s application.TaskStats) StatsResponse {
	return StatsResponse{
		Total:      s.Total,
		Todo:       s.Todo,
		InProgress: s.InProgress,
		Done:       s.Done,
		Archived:   s.Archived,
	}
}
