package httpconv_test

import (
	"testing"

	"hexarch/internal/adapters/httpconv"
	"hexarch/internal/domain"
	"hexarch/internal/repository"
)

func TestParseDue(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want repository.DueBucket
	}{
		{"", ""},
		{"overdue", repository.DueOverdue},
		{"today", repository.DueToday},
		{"soon", repository.DueSoon},
	} {
		got, err := httpconv.ParseDue(tc.in)
		if err != nil {
			t.Fatalf("ParseDue(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("ParseDue(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	_, err := httpconv.ParseDue("bogus")
	assertInvalid(t, err)
}

func TestParseSort(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want repository.SortField
	}{
		{"", ""},
		{"priority", repository.SortPriority},
		{"created", repository.SortCreated},
		{"updated", repository.SortUpdated},
		{"deadline", repository.SortDeadline},
		{"title", repository.SortTitle},
	} {
		got, err := httpconv.ParseSort(tc.in)
		if err != nil {
			t.Fatalf("ParseSort(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("ParseSort(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	_, err := httpconv.ParseSort("bogus")
	assertInvalid(t, err)
}

func TestParseSortOrder(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want repository.SortOrder
	}{
		{"", ""},
		{"asc", repository.SortAsc},
		{"desc", repository.SortDesc},
	} {
		got, err := httpconv.ParseSortOrder(tc.in)
		if err != nil {
			t.Fatalf("ParseSortOrder(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("ParseSortOrder(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	_, err := httpconv.ParseSortOrder("bogus")
	assertInvalid(t, err)
}

func TestTaskFilterDefaults(t *testing.T) {
	sort, order := (repository.TaskFilter{}).EffectiveSort()
	if sort != repository.SortPriority || order != repository.SortDesc {
		t.Errorf("default sort = %s/%s, want priority/desc", sort, order)
	}
	sort, order = (repository.TaskFilter{Sort: repository.SortTitle}).EffectiveSort()
	if sort != repository.SortTitle || order != repository.SortAsc {
		t.Errorf("title sort = %s/%s, want title/asc", sort, order)
	}
	sort, order = (repository.TaskFilter{Order: repository.SortAsc}).EffectiveSort()
	if sort != repository.SortPriority || order != repository.SortAsc {
		t.Errorf("order-only sort = %s/%s, want priority/asc", sort, order)
	}
}

func assertInvalid(t *testing.T, err error) {
	t.Helper()
	de, ok := err.(*domain.DomainError)
	if !ok || de.Kind() != domain.KindInvalid {
		t.Fatalf("error = %v, want INVALID_ARGUMENT", err)
	}
}
