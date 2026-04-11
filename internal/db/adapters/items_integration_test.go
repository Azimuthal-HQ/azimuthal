package adapters_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/projects"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/tickets"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/adapters"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// --- Ticket adapter tests ---

// TestCreateItem_MinimumFields creates a ticket with ONLY required fields.
// This was the exact production bug — labels null constraint was breaking this.
func TestCreateItem_MinimumFields(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "service_desk")
	queries := generated.New(db.Pool)
	adapter := adapters.NewTicketAdapter(queries)

	ticket := &tickets.Ticket{
		ID:         uuid.New(),
		SpaceID:    space.ID,
		Title:      "Minimal ticket",
		Status:     tickets.StatusOpen,
		Priority:   tickets.PriorityMedium,
		ReporterID: user.ID,
		// Labels intentionally nil — must not cause SQLSTATE 23502
	}

	err := adapter.Create(context.Background(), ticket)
	require.NoError(t, err, "creating ticket with minimum fields must succeed")
}

// TestCreateItem_LabelsDefaultsToEmptyArray verifies that nil labels are stored
// as an empty array, not null.
func TestCreateItem_LabelsDefaultsToEmptyArray(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "service_desk")
	queries := generated.New(db.Pool)
	adapter := adapters.NewTicketAdapter(queries)

	ticket := &tickets.Ticket{
		ID:         uuid.New(),
		SpaceID:    space.ID,
		Title:      "Labels test",
		Status:     tickets.StatusOpen,
		Priority:   tickets.PriorityMedium,
		ReporterID: user.ID,
		Labels:     nil, // nil — adapter must convert to []
	}

	err := adapter.Create(context.Background(), ticket)
	require.NoError(t, err)

	fetched, err := adapter.GetByID(context.Background(), ticket.ID)
	require.NoError(t, err)
	require.NotNil(t, fetched.Labels, "labels must not be nil")
	require.Empty(t, fetched.Labels, "labels must be empty array, not null")
}

// TestCreateItem_PriorityStoredAsLowercase verifies priority round-trips correctly.
func TestCreateItem_PriorityStoredAsLowercase(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "service_desk")
	queries := generated.New(db.Pool)
	adapter := adapters.NewTicketAdapter(queries)

	ticket := &tickets.Ticket{
		ID:         uuid.New(),
		SpaceID:    space.ID,
		Title:      "Priority test",
		Status:     tickets.StatusOpen,
		Priority:   tickets.PriorityMedium,
		ReporterID: user.ID,
	}

	err := adapter.Create(context.Background(), ticket)
	require.NoError(t, err)

	fetched, err := adapter.GetByID(context.Background(), ticket.ID)
	require.NoError(t, err)
	require.Equal(t, tickets.PriorityMedium, fetched.Priority, "priority must be lowercase 'medium'")
}

// TestCreateItem_StatusStoredAsLowercase verifies status round-trips correctly.
func TestCreateItem_StatusStoredAsLowercase(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "service_desk")
	queries := generated.New(db.Pool)
	adapter := adapters.NewTicketAdapter(queries)

	ticket := &tickets.Ticket{
		ID:         uuid.New(),
		SpaceID:    space.ID,
		Title:      "Status test",
		Status:     tickets.StatusOpen,
		Priority:   tickets.PriorityHigh,
		ReporterID: user.ID,
	}

	err := adapter.Create(context.Background(), ticket)
	require.NoError(t, err)

	fetched, err := adapter.GetByID(context.Background(), ticket.ID)
	require.NoError(t, err)
	require.Equal(t, tickets.StatusOpen, fetched.Status, "status must be lowercase 'open'")
}

// TestCreateItem_AllFieldsRoundTrip creates a ticket with all fields and verifies each.
func TestCreateItem_AllFieldsRoundTrip(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "service_desk")
	queries := generated.New(db.Pool)
	adapter := adapters.NewTicketAdapter(queries)

	assigneeID := user.ID
	ticket := &tickets.Ticket{
		ID:          uuid.New(),
		SpaceID:     space.ID,
		Title:       "Full ticket",
		Description: "A detailed description",
		Status:      tickets.StatusOpen,
		Priority:    tickets.PriorityUrgent,
		ReporterID:  user.ID,
		AssigneeID:  &assigneeID,
		Labels:      []string{"bug", "critical"},
	}

	err := adapter.Create(context.Background(), ticket)
	require.NoError(t, err)

	fetched, err := adapter.GetByID(context.Background(), ticket.ID)
	require.NoError(t, err)
	require.Equal(t, ticket.Title, fetched.Title)
	require.Equal(t, ticket.Description, fetched.Description)
	require.Equal(t, ticket.Status, fetched.Status)
	require.Equal(t, ticket.Priority, fetched.Priority)
	require.Equal(t, ticket.ReporterID, fetched.ReporterID)
	require.NotNil(t, fetched.AssigneeID)
	require.Equal(t, assigneeID, *fetched.AssigneeID)
	require.Equal(t, []string{"bug", "critical"}, fetched.Labels)
}

// TestCreateItem_AllThreeSpaceTypes verifies items can be created in all space types.
func TestCreateItem_AllThreeSpaceTypes(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	queries := generated.New(db.Pool)
	ticketAdapter := adapters.NewTicketAdapter(queries)
	itemAdapter := adapters.NewItemAdapter(queries)

	for _, spaceType := range []string{"service_desk", "project"} {
		space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, spaceType)
		if spaceType == "service_desk" {
			ticket := &tickets.Ticket{
				ID:         uuid.New(),
				SpaceID:    space.ID,
				Title:      "Ticket in " + spaceType,
				Status:     tickets.StatusOpen,
				Priority:   tickets.PriorityMedium,
				ReporterID: user.ID,
			}
			err := ticketAdapter.Create(context.Background(), ticket)
			require.NoError(t, err, "create ticket in %s", spaceType)
		} else {
			item := &projects.Item{
				ID:         uuid.New(),
				SpaceID:    space.ID,
				Kind:       "task",
				Title:      "Item in " + spaceType,
				Status:     "open",
				Priority:   "medium",
				ReporterID: user.ID,
			}
			err := itemAdapter.Create(context.Background(), item)
			require.NoError(t, err, "create item in %s", spaceType)
		}
	}
}

// TestCreateTicket_TypeIsTicket verifies the kind field round-trips as "ticket".
func TestCreateTicket_TypeIsTicket(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "service_desk")
	queries := generated.New(db.Pool)
	adapter := adapters.NewTicketAdapter(queries)

	ticket := &tickets.Ticket{
		ID:         uuid.New(),
		SpaceID:    space.ID,
		Title:      "Type test",
		Status:     tickets.StatusOpen,
		Priority:   tickets.PriorityMedium,
		ReporterID: user.ID,
	}

	err := adapter.Create(context.Background(), ticket)
	require.NoError(t, err)

	// Verify it's stored as kind='ticket' by reading the raw item.
	var kind string
	err = db.Pool.QueryRow(context.Background(),
		"SELECT kind FROM items WHERE id = $1", ticket.ID).Scan(&kind)
	require.NoError(t, err)
	require.Equal(t, "ticket", kind)
}

// --- Project item adapter tests ---

// TestCreateProjectItem_MinimumFields creates a project item with minimum fields.
func TestCreateProjectItem_MinimumFields(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "project")
	queries := generated.New(db.Pool)
	adapter := adapters.NewItemAdapter(queries)

	item := &projects.Item{
		ID:         uuid.New(),
		SpaceID:    space.ID,
		Kind:       "task",
		Title:      "Minimal item",
		Status:     "open",
		Priority:   "medium",
		ReporterID: user.ID,
		// Labels nil — must succeed
	}

	err := adapter.Create(context.Background(), item)
	require.NoError(t, err)

	fetched, err := adapter.GetByID(context.Background(), item.ID)
	require.NoError(t, err)
	require.Equal(t, "task", fetched.Kind)
	require.Equal(t, "Minimal item", fetched.Title)
	require.Equal(t, "open", fetched.Status)
	require.Equal(t, "medium", fetched.Priority)
}

// TestCreateProjectItem_SoftDelete verifies soft-delete sets deleted_at.
func TestCreateProjectItem_SoftDelete(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "project")
	queries := generated.New(db.Pool)
	adapter := adapters.NewItemAdapter(queries)

	item := &projects.Item{
		ID:         uuid.New(),
		SpaceID:    space.ID,
		Kind:       "task",
		Title:      "Delete me",
		Status:     "open",
		Priority:   "medium",
		ReporterID: user.ID,
	}

	err := adapter.Create(context.Background(), item)
	require.NoError(t, err)

	err = adapter.SoftDelete(context.Background(), item.ID)
	require.NoError(t, err)

	// Verify deleted_at is set in the database.
	var deletedAt *string
	err = db.Pool.QueryRow(context.Background(),
		"SELECT deleted_at::text FROM items WHERE id = $1", item.ID).Scan(&deletedAt)
	require.NoError(t, err)
	require.NotNil(t, deletedAt, "deleted_at must be set after soft delete")
}

// TestTicketAdapter_ListBySpace verifies listing returns only tickets.
func TestTicketAdapter_ListBySpace(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "service_desk")
	queries := generated.New(db.Pool)
	ticketAdapter := adapters.NewTicketAdapter(queries)
	itemAdapter := adapters.NewItemAdapter(queries)

	// Create a ticket.
	ticket := &tickets.Ticket{
		ID:         uuid.New(),
		SpaceID:    space.ID,
		Title:      "A ticket",
		Status:     tickets.StatusOpen,
		Priority:   tickets.PriorityMedium,
		ReporterID: user.ID,
	}
	err := ticketAdapter.Create(context.Background(), ticket)
	require.NoError(t, err)

	// Create a task item in the same space.
	item := &projects.Item{
		ID:         uuid.New(),
		SpaceID:    space.ID,
		Kind:       "task",
		Title:      "A task",
		Status:     "open",
		Priority:   "medium",
		ReporterID: user.ID,
	}
	err = itemAdapter.Create(context.Background(), item)
	require.NoError(t, err)

	// ListBySpace on ticket adapter should return only the ticket.
	result, err := ticketAdapter.ListBySpace(context.Background(), space.ID)
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Equal(t, ticket.ID, result[0].ID)
}

// TestTicketAdapter_UpdateStatus verifies status transitions.
func TestTicketAdapter_UpdateStatus(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "service_desk")
	queries := generated.New(db.Pool)
	adapter := adapters.NewTicketAdapter(queries)

	ticket := &tickets.Ticket{
		ID:         uuid.New(),
		SpaceID:    space.ID,
		Title:      "Status transition",
		Status:     tickets.StatusOpen,
		Priority:   tickets.PriorityMedium,
		ReporterID: user.ID,
	}
	err := adapter.Create(context.Background(), ticket)
	require.NoError(t, err)

	updated, err := adapter.UpdateStatus(context.Background(), ticket.ID, tickets.StatusInProgress)
	require.NoError(t, err)
	require.Equal(t, tickets.StatusInProgress, updated.Status)
}

// --- Page tests ---

// TestCreatePage_MinimumFields verifies page creation with minimum fields.
// Pages table has: id, space_id, parent_id, title, content, version, author_id, position.
func TestCreatePage_MinimumFields(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "wiki")

	_, err := db.Pool.Exec(context.Background(),
		`INSERT INTO pages (id, space_id, author_id, title, content, version)
		 VALUES ($1, $2, $3, $4, $5, 1)`,
		uuid.New(), space.ID, user.ID, "Test page", "")
	require.NoError(t, err, "creating page with empty content must succeed")
}

// TestCreatePage_EmptyContentAccepted verifies that content="" is valid.
func TestCreatePage_EmptyContentAccepted(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "wiki")

	pageID := uuid.New()
	_, err := db.Pool.Exec(context.Background(),
		`INSERT INTO pages (id, space_id, author_id, title, content, version)
		 VALUES ($1, $2, $3, $4, $5, 1)`,
		pageID, space.ID, user.ID, "Empty content page", "")
	require.NoError(t, err)

	// Verify content round-trips as empty string.
	var content string
	err = db.Pool.QueryRow(context.Background(),
		"SELECT content FROM pages WHERE id = $1", pageID).Scan(&content)
	require.NoError(t, err)
	require.Equal(t, "", content)
}

// TestCreatePage_TitleRequired verifies that title is NOT NULL.
func TestCreatePage_TitleRequired(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "wiki")

	// Attempt to insert with null title — should fail.
	_, err := db.Pool.Exec(context.Background(),
		`INSERT INTO pages (id, space_id, author_id, title, content, version)
		 VALUES ($1, $2, $3, NULL, $4, 1)`,
		uuid.New(), space.ID, user.ID, "content")
	require.Error(t, err, "null title must fail")
}

// TestCreatePage_MultipleInSameSpace verifies multiple pages can coexist in a space.
func TestCreatePage_MultipleInSameSpace(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "wiki")

	for i := 0; i < 3; i++ {
		_, err := db.Pool.Exec(context.Background(),
			`INSERT INTO pages (id, space_id, author_id, title, content, version)
			 VALUES ($1, $2, $3, $4, $5, 1)`,
			uuid.New(), space.ID, user.ID, fmt.Sprintf("Page %d", i), "content")
		require.NoError(t, err, "page %d", i)
	}

	var count int
	err := db.Pool.QueryRow(context.Background(),
		"SELECT count(*) FROM pages WHERE space_id = $1", space.ID).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 3, count)
}

// --- Space tests ---

// TestCreateSpace_AllThreeTypes verifies all space types can be created.
func TestCreateSpace_AllThreeTypes(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)

	for _, spaceType := range []string{"service_desk", "wiki", "project"} {
		space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, spaceType)
		require.Equal(t, spaceType, space.Type)
	}
}

// TestCreateSpace_SlugUniqueness verifies slug uniqueness per org.
func TestCreateSpace_SlugUniqueness(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)

	_, err := db.Pool.Exec(context.Background(),
		`INSERT INTO spaces (id, org_id, slug, name, type, created_by)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		uuid.New(), org.ID, "same-slug", "Space 1", "project", user.ID)
	require.NoError(t, err)

	_, err = db.Pool.Exec(context.Background(),
		`INSERT INTO spaces (id, org_id, slug, name, type, created_by)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		uuid.New(), org.ID, "same-slug", "Space 2", "project", user.ID)
	require.Error(t, err, "duplicate slug in same org must fail")
}

// --- Sprint adapter tests ---

// TestSprintAdapter_CreateAndRetrieve verifies sprint lifecycle.
func TestSprintAdapter_CreateAndRetrieve(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "project")
	queries := generated.New(db.Pool)
	adapter := adapters.NewSprintAdapter(queries)

	sprint := &projects.Sprint{
		ID:        uuid.New(),
		SpaceID:   space.ID,
		Name:      "Sprint 1",
		Goal:      "Ship MVP",
		Status:    "planned",
		CreatedBy: user.ID,
	}

	err := adapter.Create(context.Background(), sprint)
	require.NoError(t, err)

	fetched, err := adapter.GetByID(context.Background(), sprint.ID)
	require.NoError(t, err)
	require.Equal(t, "Sprint 1", fetched.Name)
	require.Equal(t, "Ship MVP", fetched.Goal)
	require.Equal(t, "planned", fetched.Status)
}

// TestLabelAdapter_CreateAndList verifies label CRUD.
func TestLabelAdapter_CreateAndList(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	queries := generated.New(db.Pool)
	adapter := adapters.NewLabelAdapter(queries)

	label := &projects.Label{
		ID:    uuid.New(),
		OrgID: org.ID,
		Name:  "bug",
		Color: "#ff0000",
	}
	err := adapter.Create(context.Background(), label)
	require.NoError(t, err)

	labels, err := adapter.ListByOrg(context.Background(), org.ID)
	require.NoError(t, err)
	require.Len(t, labels, 1)
	require.Equal(t, "bug", labels[0].Name)
	require.Equal(t, "#ff0000", labels[0].Color)

	// Delete.
	err = adapter.Delete(context.Background(), label.ID)
	require.NoError(t, err)

	labels, err = adapter.ListByOrg(context.Background(), org.ID)
	require.NoError(t, err)
	require.Empty(t, labels)
}
