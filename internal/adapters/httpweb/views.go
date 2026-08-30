// views.go defines the presentation-layer value objects handed to the HTML
// templates: taskView, listPage, statsView, and the transition metadata
// tables. Presentation knowledge (badges, labels, buttons) lives here, while
// the domain remains the source of truth for which transitions are legal.
//
// Public API: (none — all view types and converters are unexported)
//
// Private:
//   - types: transitionView, taskView, listPage, statsView
//   - data:  statusMeta, transitionTable, pageSize
//   - funcs: toTaskView, toTaskViews, toListPage, toStatsView
package httpweb

import (
	"fmt"
	"time"

	"hexarch/internal/application"
	"hexarch/internal/domain"
	"hexarch/internal/repository"
)

// pageSize is the number of tasks rendered per list page.
// pageSize is the fixed number of tasks rendered per list page in the web
// UI (and therefore the filter limit used by parse.go).
const pageSize = 20

// transitionView describes one legal status action rendered on a row.
type transitionView struct {
	Label       string
	Target      string // domain.Status string value
	ButtonClass string
}

// taskView is the template-facing representation of a domain.Task.
type taskView struct {
	ID            string
	Title         string
	Description   string
	Status        string
	StatusLabel   string
	BadgeClass    string
	Priority      int
	PriorityLabel string
	DeadlineStr   string
	CreatedAt     string
	UpdatedAt     string
	CanEdit       bool
	CanDelete     bool
	Transitions   []transitionView
}

// listPage is the data bag for the task list fragment.
type listPage struct {
	Tasks         []taskView
	Total         int
	Page          int
	PageSize      int
	HasPrev       bool
	HasNext       bool
	PrevOffset    int
	NextOffset    int
	StatusFilter  string
	Search        string
	FilteredCount int
	ShowEmpty     bool // no tasks at all (empty state #1)
	NoMatch       bool // filters matched nothing (empty state #2)
}

// statsView is the data bag for the stats strip.
type statsView struct {
	Total        int
	Todo         int
	InProgress   int
	Done         int
	Archived     int
	PercentDone  int
	ActiveStatus string // "" means no status filter active
	Search       string
	// HasTasks distinguishes "no tasks at all" from "filters match nothing".
	HasTasks bool
}

// statusMeta is adapter-level presentation knowledge.
var statusMeta = map[domain.Status]struct {
	label      string
	badgeClass string
}{
	domain.StatusTodo:       {"To do", "badge badge-ghost"},
	domain.StatusInProgress: {"In progress", "badge badge-info"},
	domain.StatusDone:       {"Done", "badge badge-success"},
	domain.StatusArchived:   {"Archived", "badge badge-neutral badge-outline"},
}

// transitionTable lists the legal status actions per current status. It is
// presentation knowledge; the domain remains the enforcement source of truth
// via allowedTransitions in task.go.
var transitionTable = map[domain.Status][]transitionView{
	domain.StatusTodo: {
		{"Start", string(domain.StatusInProgress), "btn btn-success btn-sm btn-outline"},
	},
	domain.StatusInProgress: {
		{"Complete", string(domain.StatusDone), "btn btn-success btn-sm"},
		{"Back to to do", string(domain.StatusTodo), "btn btn-ghost btn-sm"},
	},
	domain.StatusDone: {
		{"Archive", string(domain.StatusArchived), "btn btn-neutral btn-sm btn-outline"},
	},
	domain.StatusArchived: {},
}

// toTaskView converts a domain.Task into its template view.
func toTaskView(t domain.Task) taskView {
	meta := statusMeta[t.Status()]
	transitions := transitionTable[t.Status()]

	v := taskView{
		ID:            t.ID().String(),
		Title:         t.Title(),
		Description:   t.Description(),
		Status:        t.Status().String(),
		StatusLabel:   meta.label,
		BadgeClass:    meta.badgeClass,
		Priority:      t.Priority(),
		PriorityLabel: fmt.Sprintf("P%d", t.Priority()),
		CreatedAt:     t.CreatedAt().Format(time.RFC3339),
		UpdatedAt:     t.UpdatedAt().Format(time.RFC3339),
		CanEdit:       t.CanEdit(),
		CanDelete:     true,
		Transitions:   transitions,
	}
	if d := t.Deadline(); d != nil {
		v.DeadlineStr = d.Format(time.DateOnly)
	}
	return v
}

// toTaskViews converts a slice of domain tasks.
func toTaskViews(tasks []domain.Task) []taskView {
	out := make([]taskView, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, toTaskView(t))
	}
	return out
}

// toListPage builds a listPage from tasks, the total filtered count, and the
// requested filter. totalTasks is the repository-wide total (for the "no
// tasks at all" empty state).
func toListPage(tasks []domain.Task, filter repository.TaskFilter, filteredCount, totalTasks int) listPage {
	page := filter.Offset/filter.Limit + 1
	p := listPage{
		Tasks:         toTaskViews(tasks),
		Total:         totalTasks,
		Page:          page,
		PageSize:      filter.Limit,
		HasPrev:       page > 1,
		HasNext:       len(tasks) == filter.Limit,
		FilteredCount: filteredCount,
		ShowEmpty:     totalTasks == 0,
		NoMatch:       totalTasks > 0 && len(tasks) == 0,
	}
	if p.HasPrev {
		p.PrevOffset = (page - 2) * filter.Limit
	}
	p.NextOffset = page * filter.Limit
	if filter.Status != nil {
		p.StatusFilter = filter.Status.String()
	}
	p.Search = filter.Search
	return p
}

// toStatsView converts application.TaskStats plus the active filter into a
// template view.
func toStatsView(s application.TaskStats, filter repository.TaskFilter, hasTasks bool) statsView {
	percent := 0
	if s.Total > 0 {
		percent = int(float64(s.Done) / float64(s.Total) * 100)
	}
	v := statsView{
		Total:       s.Total,
		Todo:        s.Todo,
		InProgress:  s.InProgress,
		Done:        s.Done,
		Archived:    s.Archived,
		PercentDone: percent,
		Search:      filter.Search,
		HasTasks:    hasTasks,
	}
	if filter.Status != nil {
		v.ActiveStatus = filter.Status.String()
	}
	return v
}

// userView is the presentation-layer view of an authenticated user. It
// deliberately carries only ID/Email/IsAdmin — the password hash and API key
// are never rendered (spec NFR-8).
type userView struct {
	ID      string
	Email   string
	IsAdmin bool
}

// toUserView converts a domain.User into its safe presentation view.
func toUserView(u domain.User) userView {
	return userView{ID: u.ID().String(), Email: u.Email(), IsAdmin: u.IsAdmin()}
}

// toUserViews converts a user slice into presentation views.
func toUserViews(users []domain.User) []userView {
	out := make([]userView, 0, len(users))
	for _, u := range users {
		out = append(out, toUserView(u))
	}
	return out
}
