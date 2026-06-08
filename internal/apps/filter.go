package apps

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// jobTypeAliases maps user-facing job type names to the internal names used by
// the apps service.
var jobTypeAliases = map[string]string{
	"vice":        "Interactive",
	"interactive": "Interactive",
	"de":          "DE",
	"osg":         "OSG",
	"tapis":       "Tapis",
}

// NormalizeJobType maps a user-facing job type (e.g. "VICE") to its internal
// name (e.g. "Interactive"). Unknown values pass through unchanged.
func NormalizeJobType(jobType string) string {
	if jobType == "" {
		return ""
	}
	if internal, ok := jobTypeAliases[strings.ToLower(jobType)]; ok {
		return internal
	}
	return jobType
}

// StripUserSuffix removes the configured suffix from a username if present.
func StripUserSuffix(username, suffix string) string {
	if username == "" || suffix == "" {
		return username
	}
	return strings.TrimSuffix(username, suffix)
}

// ListFilter holds the client-side filters applied to a list of apps.
type ListFilter struct {
	Description     string
	Integrator      string
	IntegrationDate string
	EditedDate      string
	JobType         string
	UserSuffix      string
}

// FilterApps applies job-type, description, integrator, and date filters to the
// apps list, mirroring the original client-side filtering.
func FilterApps(list []App, f ListFilter) ([]App, error) {
	out := list

	if jt := NormalizeJobType(f.JobType); jt != "" {
		// Compare case-insensitively: different apps-service versions return the
		// job type capitalized ("Interactive") or lowercased ("interactive").
		out = filter(out, func(a App) bool { return strings.EqualFold(a.OverallJobType, jt) })
	}
	if f.Description != "" {
		needle := strings.ToLower(f.Description)
		out = filter(out, func(a App) bool { return strings.Contains(strings.ToLower(a.Description), needle) })
	}
	if f.Integrator != "" {
		needle := strings.ToLower(StripUserSuffix(f.Integrator, f.UserSuffix))
		out = filter(out, func(a App) bool {
			return needle != "" && strings.Contains(strings.ToLower(a.IntegratorName), needle)
		})
	}

	var err error
	if out, err = applyDateFilter(out, f.IntegrationDate, func(a App) string { return a.IntegrationDate }); err != nil {
		return nil, err
	}
	if out, err = applyDateFilter(out, f.EditedDate, func(a App) string { return a.EditedDate }); err != nil {
		return nil, err
	}
	return out, nil
}

func filter(list []App, keep func(App) bool) []App {
	out := make([]App, 0, len(list))
	for _, a := range list {
		if keep(a) {
			out = append(out, a)
		}
	}
	return out
}

func applyDateFilter(list []App, expr string, field func(App) string) ([]App, error) {
	if expr == "" {
		return list, nil
	}
	op, want, err := parseDateFilter(expr)
	if err != nil {
		return nil, err
	}
	out := make([]App, 0, len(list))
	for _, a := range list {
		raw := field(a)
		if raw == "" {
			continue
		}
		got, err := parseISODate(raw)
		if err != nil {
			continue
		}
		if compareDates(got, op, want) {
			out = append(out, a)
		}
	}
	return out, nil
}

var dateFilterRE = regexp.MustCompile(`^(>=|<=|==|>|<)\s*(.+)$`)

// parseDateFilter parses an expression like ">2025-09-29" into a comparison
// operator and a UTC time.
func parseDateFilter(expr string) (string, time.Time, error) {
	m := dateFilterRE.FindStringSubmatch(strings.TrimSpace(expr))
	if m == nil {
		return "", time.Time{}, fmt.Errorf("invalid date filter format: %q (expected <operator><date>, e.g. \">2025-09-29\")", expr)
	}
	op, dateStr := m[1], strings.TrimSpace(m[2])
	t, err := parseISODate(dateStr)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("invalid date format: %q (expected ISO 8601)", dateStr)
	}
	if op == "==" {
		op = "="
	}
	return op, t, nil
}

// parseISODate parses common ISO 8601 forms into a UTC time.
func parseISODate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	layouts := []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02"}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized date: %q", s)
}

func compareDates(got time.Time, op string, want time.Time) bool {
	got = got.UTC()
	switch op {
	case ">":
		return got.After(want)
	case "<":
		return got.Before(want)
	case ">=":
		return !got.Before(want)
	case "<=":
		return !got.After(want)
	case "=":
		return got.Equal(want)
	default:
		return false
	}
}
