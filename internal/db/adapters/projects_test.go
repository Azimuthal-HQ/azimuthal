package adapters

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/projects"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

func TestDbItemToProject(t *testing.T) {
	now := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	id := uuid.New()
	spaceID := uuid.New()
	parentID := uuid.New()
	reporterID := uuid.New()
	assigneeID := uuid.New()
	sprintID := uuid.New()
	due := now.Add(72 * time.Hour)
	resolved := now.Add(48 * time.Hour)
	deleted := now.Add(96 * time.Hour)
	desc := "A project item"

	dbItem := generated.Item{
		ID:          id,
		SpaceID:     spaceID,
		ParentID:    pgtype.UUID{Bytes: parentID, Valid: true},
		Kind:        "story",
		Title:       "Build feature X",
		Description: &desc,
		Status:      "in_progress",
		Priority:    "high",
		ReporterID:  reporterID,
		AssigneeID:  pgtype.UUID{Bytes: assigneeID, Valid: true},
		SprintID:    pgtype.UUID{Bytes: sprintID, Valid: true},
		Labels:      []string{"feature", "frontend"},
		DueAt:       pgtype.Timestamptz{Time: due, Valid: true},
		ResolvedAt:  pgtype.Timestamptz{Time: resolved, Valid: true},
		Rank:        "0|bbbbbb:",
		CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:   pgtype.Timestamptz{Time: now.Add(time.Hour), Valid: true},
		DeletedAt:   pgtype.Timestamptz{Time: deleted, Valid: true},
	}

	got := dbItemToProject(dbItem)

	if got.ID != id {
		t.Errorf("ID mismatch")
	}
	if got.SpaceID != spaceID {
		t.Errorf("SpaceID mismatch")
	}
	if got.ParentID == nil || *got.ParentID != parentID {
		t.Errorf("ParentID mismatch")
	}
	if got.Kind != "story" {
		t.Errorf("Kind mismatch: got %v", got.Kind)
	}
	if got.Title != "Build feature X" {
		t.Errorf("Title mismatch")
	}
	if got.Description != "A project item" {
		t.Errorf("Description mismatch: got %v", got.Description)
	}
	if got.Status != "in_progress" {
		t.Errorf("Status mismatch: got %v", got.Status)
	}
	if got.Priority != "high" {
		t.Errorf("Priority mismatch: got %v", got.Priority)
	}
	if got.ReporterID != reporterID {
		t.Errorf("ReporterID mismatch")
	}
	if got.AssigneeID == nil || *got.AssigneeID != assigneeID {
		t.Errorf("AssigneeID mismatch")
	}
	if got.SprintID == nil || *got.SprintID != sprintID {
		t.Errorf("SprintID mismatch")
	}
	if len(got.Labels) != 2 {
		t.Errorf("Labels mismatch: got %v", got.Labels)
	}
	if got.DueAt == nil || !got.DueAt.Equal(due) {
		t.Errorf("DueAt mismatch")
	}
	if got.ResolvedAt == nil || !got.ResolvedAt.Equal(resolved) {
		t.Errorf("ResolvedAt mismatch")
	}
	if got.DeletedAt == nil || !got.DeletedAt.Equal(deleted) {
		t.Errorf("DeletedAt mismatch")
	}
	if got.Rank != "0|bbbbbb:" {
		t.Errorf("Rank mismatch")
	}
}

func TestDbItemToProjectNilOptionals(t *testing.T) {
	dbItem := generated.Item{
		ID:         uuid.New(),
		SpaceID:    uuid.New(),
		Kind:       "task",
		Title:      "Minimal item",
		Status:     "open",
		Priority:   "medium",
		ReporterID: uuid.New(),
		ParentID:   pgtype.UUID{},
		AssigneeID: pgtype.UUID{},
		SprintID:   pgtype.UUID{},
		DueAt:      pgtype.Timestamptz{},
		ResolvedAt: pgtype.Timestamptz{},
		DeletedAt:  pgtype.Timestamptz{},
		CreatedAt:  pgtype.Timestamptz{Time: time.Now(), Valid: true},
		UpdatedAt:  pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}

	got := dbItemToProject(dbItem)
	if got.ParentID != nil {
		t.Errorf("expected nil ParentID")
	}
	if got.AssigneeID != nil {
		t.Errorf("expected nil AssigneeID")
	}
	if got.SprintID != nil {
		t.Errorf("expected nil SprintID")
	}
	if got.DueAt != nil {
		t.Errorf("expected nil DueAt")
	}
	if got.ResolvedAt != nil {
		t.Errorf("expected nil ResolvedAt")
	}
	if got.DeletedAt != nil {
		t.Errorf("expected nil DeletedAt")
	}
}

func TestDbItemsToProjects(t *testing.T) {
	items := []generated.Item{
		{ID: uuid.New(), SpaceID: uuid.New(), Kind: "task", ReporterID: uuid.New(),
			CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
			UpdatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}},
		{ID: uuid.New(), SpaceID: uuid.New(), Kind: "story", ReporterID: uuid.New(),
			CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
			UpdatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}},
	}

	got := dbItemsToProjects(items)
	if len(got) != 2 {
		t.Errorf("expected 2 items, got %d", len(got))
	}
	if got[0].Kind != "task" {
		t.Errorf("first item Kind mismatch: got %v", got[0].Kind)
	}
	if got[1].Kind != "story" {
		t.Errorf("second item Kind mismatch: got %v", got[1].Kind)
	}
}

func TestDbItemsToProjectsEmpty(t *testing.T) {
	got := dbItemsToProjects(nil)
	if len(got) != 0 {
		t.Errorf("expected 0 items for nil input, got %d", len(got))
	}
}

func TestDbSprintToProject(t *testing.T) {
	now := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	id := uuid.New()
	spaceID := uuid.New()
	createdBy := uuid.New()
	start := now.Add(24 * time.Hour)
	end := now.Add(14 * 24 * time.Hour)
	goal := "Complete feature work"

	dbSprint := generated.Sprint{
		ID:        id,
		SpaceID:   spaceID,
		Name:      "Sprint 1",
		Goal:      &goal,
		Status:    "planned",
		StartsAt:  pgtype.Timestamptz{Time: start, Valid: true},
		EndsAt:    pgtype.Timestamptz{Time: end, Valid: true},
		CreatedBy: createdBy,
		CreatedAt: pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true},
	}

	got := dbSprintToProject(dbSprint)

	if got.ID != id {
		t.Errorf("ID mismatch")
	}
	if got.SpaceID != spaceID {
		t.Errorf("SpaceID mismatch")
	}
	if got.Name != "Sprint 1" {
		t.Errorf("Name mismatch: got %v", got.Name)
	}
	if got.Goal != "Complete feature work" {
		t.Errorf("Goal mismatch: got %v", got.Goal)
	}
	if got.Status != "planned" {
		t.Errorf("Status mismatch: got %v", got.Status)
	}
	if got.StartsAt == nil || !got.StartsAt.Equal(start) {
		t.Errorf("StartsAt mismatch")
	}
	if got.EndsAt == nil || !got.EndsAt.Equal(end) {
		t.Errorf("EndsAt mismatch")
	}
	if got.CreatedBy != createdBy {
		t.Errorf("CreatedBy mismatch")
	}
}

func TestDbSprintToProjectNilOptionals(t *testing.T) {
	dbSprint := generated.Sprint{
		ID:        uuid.New(),
		SpaceID:   uuid.New(),
		Name:      "Sprint 2",
		Goal:      nil,
		Status:    "planned",
		StartsAt:  pgtype.Timestamptz{},
		EndsAt:    pgtype.Timestamptz{},
		CreatedBy: uuid.New(),
		CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
		UpdatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}

	got := dbSprintToProject(dbSprint)
	if got.Goal != "" {
		t.Errorf("expected empty Goal for nil, got %v", got.Goal)
	}
	if got.StartsAt != nil {
		t.Errorf("expected nil StartsAt, got %v", got.StartsAt)
	}
	if got.EndsAt != nil {
		t.Errorf("expected nil EndsAt, got %v", got.EndsAt)
	}
}

func TestItemToCreateParams(t *testing.T) {
	parentID := uuid.New()
	assigneeID := uuid.New()
	due := time.Date(2025, 8, 1, 0, 0, 0, 0, time.UTC)

	item := &projects.Item{
		ID:          uuid.New(),
		SpaceID:     uuid.New(),
		ParentID:    &parentID,
		Kind:        "story",
		Title:       "Build feature",
		Description: "Details here",
		Status:      "open",
		Priority:    "high",
		ReporterID:  uuid.New(),
		AssigneeID:  &assigneeID,
		Labels:      []string{"frontend"},
		DueAt:       &due,
		Rank:        "0|ccc:",
	}

	got := itemToCreateParams(item)

	if got.ID != item.ID {
		t.Errorf("ID mismatch")
	}
	if got.Kind != "story" {
		t.Errorf("Kind mismatch: got %v", got.Kind)
	}
	if !got.ParentID.Valid {
		t.Error("ParentID should be valid")
	}
	if got.Description == nil || *got.Description != "Details here" {
		t.Errorf("Description mismatch")
	}
	if !got.AssigneeID.Valid {
		t.Error("AssigneeID should be valid")
	}
	if !got.DueAt.Valid {
		t.Error("DueAt should be valid")
	}
}

func TestItemToCreateParamsNilOptionals(t *testing.T) {
	item := &projects.Item{
		ID:         uuid.New(),
		SpaceID:    uuid.New(),
		Kind:       "task",
		Title:      "Simple task",
		Status:     "open",
		Priority:   "medium",
		ReporterID: uuid.New(),
	}

	got := itemToCreateParams(item)
	if got.ParentID.Valid {
		t.Error("ParentID should be invalid for nil")
	}
	if got.AssigneeID.Valid {
		t.Error("AssigneeID should be invalid for nil")
	}
	if got.DueAt.Valid {
		t.Error("DueAt should be invalid for nil")
	}
}

func TestItemToUpdateParams(t *testing.T) {
	assignee := uuid.New()
	item := &projects.Item{
		ID:          uuid.New(),
		Title:       "Updated task",
		Description: "New desc",
		Status:      "in_progress",
		Priority:    "urgent",
		AssigneeID:  &assignee,
		Labels:      []string{"critical"},
		Rank:        "0|ddd:",
	}

	got := itemToUpdateParams(item)
	if got.ID != item.ID {
		t.Errorf("ID mismatch")
	}
	if got.Title != "Updated task" {
		t.Errorf("Title mismatch")
	}
	if got.Status != "in_progress" {
		t.Errorf("Status mismatch")
	}
	if !got.AssigneeID.Valid {
		t.Error("AssigneeID should be valid")
	}
}

func TestSprintToCreateParams(t *testing.T) {
	start := time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2025, 7, 14, 0, 0, 0, 0, time.UTC)

	sprint := &projects.Sprint{
		ID:        uuid.New(),
		SpaceID:   uuid.New(),
		Name:      "Sprint 3",
		Goal:      "Ship MVP",
		Status:    "planned",
		StartsAt:  &start,
		EndsAt:    &end,
		CreatedBy: uuid.New(),
	}

	got := sprintToCreateParams(sprint)
	if got.ID != sprint.ID {
		t.Errorf("ID mismatch")
	}
	if got.Name != "Sprint 3" {
		t.Errorf("Name mismatch")
	}
	if got.Goal == nil || *got.Goal != "Ship MVP" {
		t.Errorf("Goal mismatch")
	}
	if !got.StartsAt.Valid {
		t.Error("StartsAt should be valid")
	}
	if !got.EndsAt.Valid {
		t.Error("EndsAt should be valid")
	}
}

func TestSprintToCreateParamsNilDates(t *testing.T) {
	sprint := &projects.Sprint{
		ID:        uuid.New(),
		SpaceID:   uuid.New(),
		Name:      "Sprint undated",
		Status:    "planned",
		CreatedBy: uuid.New(),
	}

	got := sprintToCreateParams(sprint)
	if got.StartsAt.Valid {
		t.Error("StartsAt should be invalid for nil")
	}
	if got.EndsAt.Valid {
		t.Error("EndsAt should be invalid for nil")
	}
	if got.Goal == nil || *got.Goal != "" {
		t.Errorf("Goal should be empty string pointer for empty goal")
	}
}

// Verify interface compliance at compile time.
var _ projects.ItemRepository = (*ItemAdapter)(nil)
var _ projects.SprintRepository = (*SprintAdapter)(nil)
var _ projects.RelationRepository = (*RelationAdapter)(nil)
var _ projects.LabelRepository = (*LabelAdapter)(nil)
