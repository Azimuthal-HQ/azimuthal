// Package analytics defines the AnalyticsReporter interface and its community stub.
// The real reporting engine lives in the enterprise repository.
package analytics

import (
	"context"
	"errors"
	"time"
)

// ErrEnterpriseRequired is returned by community stubs for enterprise-only features.
var ErrEnterpriseRequired = errors.New(
	"this feature requires an enterprise license — see azimuthal.com/enterprise",
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

// AnalyticsReporter produces usage and performance reports for an organisation.
// The community edition returns ErrEnterpriseRequired for all methods.
// The real implementation lives in github.com/Azimuthal-HQ/azimuthal-ee.
type AnalyticsReporter interface {
	// OrgSummary returns aggregated metrics for orgID over the given time window.
	OrgSummary(ctx context.Context, orgID string, from, to time.Time) (*OrgSummary, error)

	// UserActivity returns per-user activity summaries for orgID in the given window.
	UserActivity(ctx context.Context, orgID string, from, to time.Time) ([]UserActivitySummary, error)

	// IsAvailable reports whether the analytics engine is active.
	IsAvailable() bool
}
