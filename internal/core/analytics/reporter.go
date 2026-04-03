// Package analytics provides usage and performance reporting for Azimuthal.
// Analytics is a standard feature available to all users.
package analytics

import (
	"context"
	"time"
)

// TicketMetrics summarises service-desk performance for a time window.
type TicketMetrics struct {
	// TotalCreated is the number of tickets opened in the window.
	TotalCreated int64
	// TotalResolved is the number of tickets resolved in the window.
	TotalResolved int64
	// AverageResolutionHours is the mean time to resolution in hours.
	AverageResolutionHours float64
	// OpenByStatus maps status names to their current ticket counts.
	OpenByStatus map[string]int64
}

// UserActivitySummary describes how active a user has been in a window.
type UserActivitySummary struct {
	// UserID is the user this summary belongs to.
	UserID string
	// ItemsCreated is the total number of items this user created.
	ItemsCreated int64
	// CommentsPosted is the number of comments this user posted.
	CommentsPosted int64
	// LastActiveAt is the timestamp of the user's most recent action.
	LastActiveAt time.Time
}

// OrgSummary is the top-level analytics snapshot for an organisation.
type OrgSummary struct {
	// OrgID is the organisation this summary belongs to.
	OrgID string
	// WindowStart is the beginning of the reporting window.
	WindowStart time.Time
	// WindowEnd is the end of the reporting window.
	WindowEnd time.Time
	// Tickets holds service-desk metrics for the window.
	Tickets TicketMetrics
	// ActiveUsers is the list of users with activity in the window.
	ActiveUsers []UserActivitySummary
	// TotalPageViews is the number of wiki page views recorded.
	TotalPageViews int64
}

// ErrNotImplemented is returned when analytics queries are called before
// the database-backed implementation is wired in.
var ErrNotImplemented = errorString("analytics reporting not yet implemented — coming soon")

type errorString string

func (e errorString) Error() string { return string(e) }

// Reporter produces usage and performance reports for an organisation.
type Reporter interface {
	// OrgSummary returns aggregated metrics for orgID over the given time window.
	OrgSummary(ctx context.Context, orgID string, from, to time.Time) (*OrgSummary, error)

	// UserActivity returns per-user activity summaries for orgID in the given window.
	UserActivity(ctx context.Context, orgID string, from, to time.Time) ([]UserActivitySummary, error)

	// IsAvailable reports whether the analytics engine is active.
	IsAvailable() bool
}

// defaultReporter is a placeholder until the database-backed analytics
// implementation is wired in.
type defaultReporter struct{}

// NewReporter returns the default Reporter.
// Returns a placeholder until the database-backed analytics engine is implemented.
func NewReporter() Reporter {
	return &defaultReporter{}
}

// OrgSummary returns ErrNotImplemented until the analytics engine is wired in.
func (s *defaultReporter) OrgSummary(_ context.Context, _ string, _, _ time.Time) (*OrgSummary, error) {
	return nil, ErrNotImplemented
}

// UserActivity returns ErrNotImplemented until the analytics engine is wired in.
func (s *defaultReporter) UserActivity(_ context.Context, _ string, _, _ time.Time) ([]UserActivitySummary, error) {
	return nil, ErrNotImplemented
}

// IsAvailable returns false until the analytics engine is wired in.
func (s *defaultReporter) IsAvailable() bool {
	return false
}
