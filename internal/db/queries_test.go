package db_test

import (
	"context"
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Azimuthal-HQ/azimuthal/internal/db"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// testQ returns a Queries instance; skips if DATABASE_URL unset.
func testQ(t *testing.T) (*generated.Queries, func()) {
	t.Helper()
	pool, cleanup := testPool(t)
	return generated.New(pool), cleanup
}

// TestListOrganizations verifies all active organisations are returned.
func TestListOrganizations(t *testing.T) {
	q, cleanup := testQ(t)
	defer cleanup()
	ctx := context.Background()
	org := setupOrg(t, q, uuid.New().String()[:8])
	orgs, err := q.ListOrganizations(ctx)
	if err != nil {
		t.Fatalf("ListOrganizations: %v", err)
	}
	found := false
	for _, o := range orgs {
		if o.ID == org.ID {
			found = true
			break
		}
	}
	if !found {
		t.Error("created org not found in ListOrganizations")
	}
}

// TestUserGetByIDAndUpdate verifies GetUserByID and UpdateUser.
func TestUserGetByIDAndUpdate(t *testing.T) {
	q, cleanup := testQ(t)
	defer cleanup()
	ctx := context.Background()
	org := setupOrg(t, q, uuid.New().String()[:8])
	user := setupUser(t, q, org.ID, "byid@example.com")
	got, err := q.GetUserByID(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if got.ID != user.ID {
		t.Error("user ID mismatch")
	}
	updated, err := q.UpdateUser(ctx, generated.UpdateUserParams{
		ID: user.ID, DisplayName: "New Name", Role: "admin", IsActive: true,
	})
	if err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}
	if updated.DisplayName != "New Name" {
		t.Errorf("display name not updated: %s", updated.DisplayName)
	}
}

// TestUserPasswordHashAndLastLogin verifies password hash update and last login touch.
func TestUserPasswordHashAndLastLogin(t *testing.T) {
	q, cleanup := testQ(t)
	defer cleanup()
	ctx := context.Background()
	org := setupOrg(t, q, uuid.New().String()[:8])
	user := setupUser(t, q, org.ID, "pwupdate@example.com")
	newHash := "new-bcrypt-hash"
	if err := q.UpdateUserPasswordHash(ctx, generated.UpdateUserPasswordHashParams{
		ID: user.ID, PasswordHash: &newHash,
	}); err != nil {
		t.Fatalf("UpdateUserPasswordHash: %v", err)
	}
	if err := q.UpdateUserLastLogin(ctx, user.ID); err != nil {
		t.Fatalf("UpdateUserLastLogin: %v", err)
	}
}

// TestListUsersByOrg verifies listing all users in an org.
func TestListUsersByOrg(t *testing.T) {
	q, cleanup := testQ(t)
	defer cleanup()
	ctx := context.Background()
	org := setupOrg(t, q, uuid.New().String()[:8])
	_ = setupUser(t, q, org.ID, "listA@example.com")
	_ = setupUser(t, q, org.ID, "listB@example.com")
	users, err := q.ListUsersByOrg(ctx, org.ID)
	if err != nil {
		t.Fatalf("ListUsersByOrg: %v", err)
	}
	if len(users) < 2 {
		t.Errorf("expected at least 2 users, got %d", len(users))
	}
}

// TestSessionExtras verifies sessions with metadata, bulk revocation, and expiry cleanup.
func TestSessionExtras(t *testing.T) {
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("DATABASE_URL not set")
	}
	q, cleanup := testQ(t)
	defer cleanup()
	ctx := context.Background()
	org := setupOrg(t, q, uuid.New().String()[:8])
	user := setupUser(t, q, org.ID, "sess2@example.com")
	ip := netip.MustParseAddr("127.0.0.1")
	ua := "test-agent/1.0"
	_, err := q.CreateSession(ctx, generated.CreateSessionParams{
		ID: uuid.New(), UserID: user.ID, TokenHash: "tok1:" + uuid.New().String(),
		IpAddress: &ip, UserAgent: &ua,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateSession with metadata: %v", err)
	}
	_, err = q.CreateSession(ctx, generated.CreateSessionParams{
		ID: uuid.New(), UserID: user.ID, TokenHash: "tok2:" + uuid.New().String(),
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateSession 2: %v", err)
	}
	if err := q.RevokeAllUserSessions(ctx, user.ID); err != nil {
		t.Fatalf("RevokeAllUserSessions: %v", err)
	}
	if err := q.DeleteExpiredSessions(ctx); err != nil {
		t.Fatalf("DeleteExpiredSessions: %v", err)
	}
}

// TestListMembershipsByOrg verifies memberships are listed.
func TestListMembershipsByOrg(t *testing.T) {
	q, cleanup := testQ(t)
	defer cleanup()
	ctx := context.Background()
	org := setupOrg(t, q, uuid.New().String()[:8])
	user := setupUser(t, q, org.ID, "mem2@example.com")
	_, err := q.CreateMembership(ctx, generated.CreateMembershipParams{
		ID: uuid.New(), OrgID: org.ID, UserID: user.ID, Role: "member",
	})
	if err != nil {
		t.Fatalf("CreateMembership: %v", err)
	}
	rows, err := q.ListMembershipsByOrg(ctx, org.ID)
	if err != nil {
		t.Fatalf("ListMembershipsByOrg: %v", err)
	}
	if len(rows) == 0 {
		t.Error("expected at least one membership")
	}
}

// TestSpaceUpdateAndMembers verifies UpdateSpace and space member management.
func TestSpaceUpdateAndMembers(t *testing.T) {
	q, cleanup := testQ(t)
	defer cleanup()
	ctx := context.Background()
	org := setupOrg(t, q, uuid.New().String()[:8])
	owner := setupUser(t, q, org.ID, "owner@example.com")
	member := setupUser(t, q, org.ID, "smember@example.com")
	space := setupSpace(t, q, org.ID, owner.ID, "wiki")
	desc := "updated desc"
	upd, err := q.UpdateSpace(ctx, generated.UpdateSpaceParams{
		ID: space.ID, Name: "Updated Wiki", Description: &desc, IsPrivate: false,
	})
	if err != nil {
		t.Fatalf("UpdateSpace: %v", err)
	}
	if upd.Name != "Updated Wiki" {
		t.Errorf("name not updated: %s", upd.Name)
	}
	sm, err := q.AddSpaceMember(ctx, generated.AddSpaceMemberParams{
		ID: uuid.New(), SpaceID: space.ID, UserID: member.ID, Role: "viewer",
	})
	if err != nil {
		t.Fatalf("AddSpaceMember: %v", err)
	}
	if sm.UserID != member.ID {
		t.Error("space member user ID mismatch")
	}
	got, err := q.GetSpaceMember(ctx, generated.GetSpaceMemberParams{
		SpaceID: space.ID, UserID: member.ID,
	})
	if err != nil {
		t.Fatalf("GetSpaceMember: %v", err)
	}
	if got.Role != "viewer" {
		t.Errorf("unexpected role: %s", got.Role)
	}
	members, err := q.ListSpaceMembers(ctx, space.ID)
	if err != nil {
		t.Fatalf("ListSpaceMembers: %v", err)
	}
	if len(members) == 0 {
		t.Error("expected at least one space member")
	}
	if err := q.RemoveSpaceMember(ctx, generated.RemoveSpaceMemberParams{
		SpaceID: space.ID, UserID: member.ID,
	}); err != nil {
		t.Fatalf("RemoveSpaceMember: %v", err)
	}
	spaces, err := q.ListSpacesByType(ctx, generated.ListSpacesByTypeParams{
		OrgID: org.ID, Type: "wiki",
	})
	if err != nil {
		t.Fatalf("ListSpacesByType: %v", err)
	}
	if len(spaces) == 0 {
		t.Error("expected at least one wiki space")
	}
}

// TestItemListAndUpdate verifies listing, full update, assignee filtering, and FTS.
func TestItemListAndUpdate(t *testing.T) {
	q, cleanup := testQ(t)
	defer cleanup()
	ctx := context.Background()
	org := setupOrg(t, q, uuid.New().String()[:8])
	user := setupUser(t, q, org.ID, "item2@example.com")
	space := setupSpace(t, q, org.ID, user.ID, "project")
	item, err := q.CreateItem(ctx, generated.CreateItemParams{
		ID: uuid.New(), SpaceID: space.ID, Kind: "bug", Title: "Fix regression",
		Status: "open", Priority: "medium", ReporterID: user.ID,
		Labels: []string{}, Rank: "b",
	})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}
	defer func() { _ = q.SoftDeleteItem(ctx, item.ID) }()
	fetched, err := q.GetItemByID(ctx, item.ID)
	if err != nil {
		t.Fatalf("GetItemByID: %v", err)
	}
	if fetched.ID != item.ID {
		t.Error("ID mismatch")
	}
	items, err := q.ListItemsBySpace(ctx, space.ID)
	if err != nil {
		t.Fatalf("ListItemsBySpace: %v", err)
	}
	if len(items) == 0 {
		t.Error("expected at least one item")
	}
	statusItems, err := q.ListItemsByStatus(ctx, generated.ListItemsByStatusParams{
		SpaceID: space.ID, Status: "open",
	})
	if err != nil {
		t.Fatalf("ListItemsByStatus: %v", err)
	}
	if len(statusItems) == 0 {
		t.Error("expected items with open status")
	}
	assigneeUID := pgtype.UUID{Bytes: user.ID, Valid: true}
	desc := "full update"
	_, err = q.UpdateItem(ctx, generated.UpdateItemParams{
		ID: item.ID, Title: "Fix regression v2", Description: &desc,
		Status: "in_progress", Priority: "high",
		AssigneeID: assigneeUID, Labels: []string{"backend"}, Rank: "b",
	})
	if err != nil {
		t.Fatalf("UpdateItem: %v", err)
	}
	assigneeItems, err := q.ListItemsByAssignee(ctx, generated.ListItemsByAssigneeParams{
		SpaceID: space.ID, AssigneeID: assigneeUID,
	})
	if err != nil {
		t.Fatalf("ListItemsByAssignee: %v", err)
	}
	if len(assigneeItems) == 0 {
		t.Error("expected assigned items")
	}
	searched, err := q.SearchItems(ctx, generated.SearchItemsParams{
		SpaceID: space.ID, PlaintoTsquery: "regression", Limit: 10,
	})
	if err != nil {
		t.Fatalf("SearchItems: %v", err)
	}
	_ = searched
}

// TestItemRelations verifies creating, listing, and deleting item relations.
func TestItemRelations(t *testing.T) {
	q, cleanup := testQ(t)
	defer cleanup()
	ctx := context.Background()
	org := setupOrg(t, q, uuid.New().String()[:8])
	user := setupUser(t, q, org.ID, "relations@example.com")
	space := setupSpace(t, q, org.ID, user.ID, "project")
	from, err := q.CreateItem(ctx, generated.CreateItemParams{
		ID: uuid.New(), SpaceID: space.ID, Kind: "task", Title: "From",
		Status: "open", Priority: "low", ReporterID: user.ID, Labels: []string{}, Rank: "a",
	})
	if err != nil {
		t.Fatalf("CreateItem from: %v", err)
	}
	defer func() { _ = q.SoftDeleteItem(ctx, from.ID) }()
	to, err := q.CreateItem(ctx, generated.CreateItemParams{
		ID: uuid.New(), SpaceID: space.ID, Kind: "task", Title: "To",
		Status: "open", Priority: "low", ReporterID: user.ID, Labels: []string{}, Rank: "b",
	})
	if err != nil {
		t.Fatalf("CreateItem to: %v", err)
	}
	defer func() { _ = q.SoftDeleteItem(ctx, to.ID) }()
	rel, err := q.CreateItemRelation(ctx, generated.CreateItemRelationParams{
		ID: uuid.New(), FromID: from.ID, ToID: to.ID, Kind: "blocks", CreatedBy: user.ID,
	})
	if err != nil {
		t.Fatalf("CreateItemRelation: %v", err)
	}
	rels, err := q.ListItemRelations(ctx, from.ID)
	if err != nil {
		t.Fatalf("ListItemRelations: %v", err)
	}
	if len(rels) == 0 {
		t.Error("expected at least one relation")
	}
	if err := q.DeleteItemRelation(ctx, rel.ID); err != nil {
		t.Fatalf("DeleteItemRelation: %v", err)
	}
}

// TestLabels verifies label create, list, and delete.
func TestLabels(t *testing.T) {
	q, cleanup := testQ(t)
	defer cleanup()
	ctx := context.Background()
	org := setupOrg(t, q, uuid.New().String()[:8])
	label, err := q.CreateLabel(ctx, generated.CreateLabelParams{
		ID: uuid.New(), OrgID: org.ID, Name: "backend", Color: "#3b82f6",
	})
	if err != nil {
		t.Fatalf("CreateLabel: %v", err)
	}
	labels, err := q.ListLabelsByOrg(ctx, org.ID)
	if err != nil {
		t.Fatalf("ListLabelsByOrg: %v", err)
	}
	if len(labels) == 0 {
		t.Error("expected at least one label")
	}
	if err := q.DeleteLabel(ctx, label.ID); err != nil {
		t.Fatalf("DeleteLabel: %v", err)
	}
}

// TestSprints verifies sprint lifecycle including item assignment.
func TestSprints(t *testing.T) {
	q, cleanup := testQ(t)
	defer cleanup()
	ctx := context.Background()
	org := setupOrg(t, q, uuid.New().String()[:8])
	user := setupUser(t, q, org.ID, "sprint@example.com")
	space := setupSpace(t, q, org.ID, user.ID, "project")
	now := time.Now()
	goal := "ship v1"
	sprint, err := q.CreateSprint(ctx, generated.CreateSprintParams{
		ID: uuid.New(), SpaceID: space.ID, Name: "Sprint 1", Goal: &goal,
		Status:    "planned",
		StartsAt:  pgtype.Timestamptz{Time: now, Valid: true},
		EndsAt:    pgtype.Timestamptz{Time: now.Add(14 * 24 * time.Hour), Valid: true},
		CreatedBy: user.ID,
	})
	if err != nil {
		t.Fatalf("CreateSprint: %v", err)
	}
	fetched, err := q.GetSprintByID(ctx, sprint.ID)
	if err != nil {
		t.Fatalf("GetSprintByID: %v", err)
	}
	if fetched.ID != sprint.ID {
		t.Error("sprint ID mismatch")
	}
	newGoal := "ship v1.1"
	_, err = q.UpdateSprint(ctx, generated.UpdateSprintParams{
		ID: sprint.ID, Name: "Sprint 1 Updated", Goal: &newGoal,
		StartsAt: pgtype.Timestamptz{Time: now, Valid: true},
		EndsAt:   pgtype.Timestamptz{Time: now.Add(14 * 24 * time.Hour), Valid: true},
	})
	if err != nil {
		t.Fatalf("UpdateSprint: %v", err)
	}
	active, err := q.UpdateSprintStatus(ctx, generated.UpdateSprintStatusParams{
		ID: sprint.ID, Status: "active",
	})
	if err != nil {
		t.Fatalf("UpdateSprintStatus: %v", err)
	}
	if active.Status != "active" {
		t.Errorf("expected active, got %s", active.Status)
	}
	activeSprint, err := q.GetActiveSprintBySpace(ctx, space.ID)
	if err != nil {
		t.Fatalf("GetActiveSprintBySpace: %v", err)
	}
	if activeSprint.ID != sprint.ID {
		t.Error("active sprint mismatch")
	}
	sprints, err := q.ListSprintsBySpace(ctx, space.ID)
	if err != nil {
		t.Fatalf("ListSprintsBySpace: %v", err)
	}
	if len(sprints) == 0 {
		t.Error("expected at least one sprint")
	}
	item, err := q.CreateItem(ctx, generated.CreateItemParams{
		ID: uuid.New(), SpaceID: space.ID, Kind: "task", Title: "Sprint task",
		Status: "open", Priority: "medium", ReporterID: user.ID, Labels: []string{}, Rank: "a",
	})
	if err != nil {
		t.Fatalf("CreateItem for sprint: %v", err)
	}
	defer func() { _ = q.SoftDeleteItem(ctx, item.ID) }()
	sprintUID := pgtype.UUID{Bytes: sprint.ID, Valid: true}
	if err := q.UpdateItemSprint(ctx, generated.UpdateItemSprintParams{
		ID: item.ID, SprintID: sprintUID,
	}); err != nil {
		t.Fatalf("UpdateItemSprint: %v", err)
	}
	sprintItems, err := q.ListItemsBySprint(ctx, sprintUID)
	if err != nil {
		t.Fatalf("ListItemsBySprint: %v", err)
	}
	if len(sprintItems) == 0 {
		t.Error("expected at least one item in sprint")
	}
}

// TestPageExtras verifies GetPageByID, hierarchy, position, revision retrieval, and FTS.
func TestPageExtras(t *testing.T) {
	q, cleanup := testQ(t)
	defer cleanup()
	ctx := context.Background()
	org := setupOrg(t, q, uuid.New().String()[:8])
	user := setupUser(t, q, org.ID, "pageext@example.com")
	space := setupSpace(t, q, org.ID, user.ID, "wiki")
	root, err := q.CreatePage(ctx, generated.CreatePageParams{
		ID: uuid.New(), SpaceID: space.ID, Title: "Root Page",
		Content: "Root content", AuthorID: user.ID, Position: 0,
	})
	if err != nil {
		t.Fatalf("CreatePage root: %v", err)
	}
	got, err := q.GetPageByID(ctx, root.ID)
	if err != nil {
		t.Fatalf("GetPageByID: %v", err)
	}
	if got.ID != root.ID {
		t.Error("page ID mismatch")
	}
	rootParentID := pgtype.UUID{Bytes: root.ID, Valid: true}
	child, err := q.CreatePage(ctx, generated.CreatePageParams{
		ID: uuid.New(), SpaceID: space.ID, ParentID: rootParentID,
		Title: "Child Page", Content: "Child content", AuthorID: user.ID, Position: 0,
	})
	if err != nil {
		t.Fatalf("CreatePage child: %v", err)
	}
	bySpace, err := q.ListPagesBySpace(ctx, space.ID)
	if err != nil {
		t.Fatalf("ListPagesBySpace: %v", err)
	}
	if len(bySpace) < 2 {
		t.Errorf("expected at least 2 pages, got %d", len(bySpace))
	}
	roots, err := q.ListRootPagesBySpace(ctx, space.ID)
	if err != nil {
		t.Fatalf("ListRootPagesBySpace: %v", err)
	}
	if len(roots) == 0 {
		t.Error("expected at least one root page")
	}
	children, err := q.ListChildPages(ctx, rootParentID)
	if err != nil {
		t.Fatalf("ListChildPages: %v", err)
	}
	if len(children) == 0 {
		t.Error("expected at least one child page")
	}
	if err := q.UpdatePagePosition(ctx, generated.UpdatePagePositionParams{
		ID: child.ID, Position: 1,
	}); err != nil {
		t.Fatalf("UpdatePagePosition: %v", err)
	}
	_, err = q.CreatePageRevision(ctx, generated.CreatePageRevisionParams{
		ID: uuid.New(), PageID: root.ID, Version: 1,
		Title: "Root Page", Content: "Root content", AuthorID: user.ID,
	})
	if err != nil {
		t.Fatalf("CreatePageRevision: %v", err)
	}
	rev, err := q.GetPageRevision(ctx, generated.GetPageRevisionParams{
		PageID: root.ID, Version: 1,
	})
	if err != nil {
		t.Fatalf("GetPageRevision: %v", err)
	}
	if rev.PageID != root.ID {
		t.Error("revision page ID mismatch")
	}
	searched, err := q.SearchPages(ctx, generated.SearchPagesParams{
		SpaceID: space.ID, PlaintoTsquery: "root", Limit: 10,
	})
	if err != nil {
		t.Fatalf("SearchPages: %v", err)
	}
	_ = searched
	if err := q.SoftDeletePage(ctx, child.ID); err != nil {
		t.Fatalf("SoftDeletePage child: %v", err)
	}
	if err := q.SoftDeletePage(ctx, root.ID); err != nil {
		t.Fatalf("SoftDeletePage root: %v", err)
	}
}

// TestComments verifies the full comment lifecycle on items and pages.
func TestComments(t *testing.T) {
	q, cleanup := testQ(t)
	defer cleanup()
	ctx := context.Background()
	org := setupOrg(t, q, uuid.New().String()[:8])
	user := setupUser(t, q, org.ID, "commenter@example.com")
	space := setupSpace(t, q, org.ID, user.ID, "project")
	item, err := q.CreateItem(ctx, generated.CreateItemParams{
		ID: uuid.New(), SpaceID: space.ID, Kind: "task", Title: "Comment target",
		Status: "open", Priority: "low", ReporterID: user.ID, Labels: []string{}, Rank: "a",
	})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}
	defer func() { _ = q.SoftDeleteItem(ctx, item.ID) }()
	itemUID := pgtype.UUID{Bytes: item.ID, Valid: true}
	comment, err := q.CreateComment(ctx, generated.CreateCommentParams{
		ID: uuid.New(), ItemID: itemUID, AuthorID: user.ID, Body: "This is a comment",
	})
	if err != nil {
		t.Fatalf("CreateComment: %v", err)
	}
	got, err := q.GetCommentByID(ctx, comment.ID)
	if err != nil {
		t.Fatalf("GetCommentByID: %v", err)
	}
	if got.ID != comment.ID {
		t.Error("comment ID mismatch")
	}
	comments, err := q.ListCommentsByItem(ctx, itemUID)
	if err != nil {
		t.Fatalf("ListCommentsByItem: %v", err)
	}
	if len(comments) == 0 {
		t.Error("expected at least one comment")
	}
	updated, err := q.UpdateComment(ctx, generated.UpdateCommentParams{
		ID: comment.ID, Body: "Updated comment",
	})
	if err != nil {
		t.Fatalf("UpdateComment: %v", err)
	}
	if updated.Body != "Updated comment" {
		t.Errorf("body not updated: %s", updated.Body)
	}
	parentUID := pgtype.UUID{Bytes: comment.ID, Valid: true}
	reply, err := q.CreateComment(ctx, generated.CreateCommentParams{
		ID: uuid.New(), ItemID: itemUID, ParentID: parentUID,
		AuthorID: user.ID, Body: "This is a reply",
	})
	if err != nil {
		t.Fatalf("CreateComment reply: %v", err)
	}
	replies, err := q.ListCommentReplies(ctx, parentUID)
	if err != nil {
		t.Fatalf("ListCommentReplies: %v", err)
	}
	if len(replies) == 0 {
		t.Error("expected at least one reply")
	}
	wikiSpace := setupSpace(t, q, org.ID, user.ID, "wiki")
	page, err := q.CreatePage(ctx, generated.CreatePageParams{
		ID: uuid.New(), SpaceID: wikiSpace.ID, Title: "Page",
		Content: "Content", AuthorID: user.ID, Position: 0,
	})
	if err != nil {
		t.Fatalf("CreatePage: %v", err)
	}
	pageUID := pgtype.UUID{Bytes: page.ID, Valid: true}
	pageComment, err := q.CreateComment(ctx, generated.CreateCommentParams{
		ID: uuid.New(), PageID: pageUID, AuthorID: user.ID, Body: "Page comment",
	})
	if err != nil {
		t.Fatalf("CreateComment on page: %v", err)
	}
	pageComments, err := q.ListCommentsByPage(ctx, pageUID)
	if err != nil {
		t.Fatalf("ListCommentsByPage: %v", err)
	}
	if len(pageComments) == 0 {
		t.Error("expected at least one page comment")
	}
	if err := q.SoftDeleteComment(ctx, reply.ID); err != nil {
		t.Fatalf("SoftDeleteComment reply: %v", err)
	}
	if err := q.SoftDeleteComment(ctx, comment.ID); err != nil {
		t.Fatalf("SoftDeleteComment: %v", err)
	}
	if err := q.SoftDeleteComment(ctx, pageComment.ID); err != nil {
		t.Fatalf("SoftDeleteComment page: %v", err)
	}
	if err := q.SoftDeletePage(ctx, page.ID); err != nil {
		t.Fatalf("SoftDeletePage: %v", err)
	}
}

// TestNotifications verifies create, count, list unread, mark read, mark all read.
func TestNotifications(t *testing.T) {
	q, cleanup := testQ(t)
	defer cleanup()
	ctx := context.Background()
	org := setupOrg(t, q, uuid.New().String()[:8])
	user := setupUser(t, q, org.ID, "notify@example.com")
	body := "You have a new comment"
	kind := "mention"
	notif, err := q.CreateNotification(ctx, generated.CreateNotificationParams{
		ID: uuid.New(), UserID: user.ID, Kind: kind,
		Title: "New mention", Body: &body, EntityKind: &kind,
	})
	if err != nil {
		t.Fatalf("CreateNotification: %v", err)
	}
	count, err := q.CountUnreadNotifications(ctx, user.ID)
	if err != nil {
		t.Fatalf("CountUnreadNotifications: %v", err)
	}
	if count == 0 {
		t.Error("expected at least 1 unread notification")
	}
	unread, err := q.ListUnreadNotificationsByUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("ListUnreadNotificationsByUser: %v", err)
	}
	if len(unread) == 0 {
		t.Error("expected at least one unread notification")
	}
	if err := q.MarkNotificationRead(ctx, generated.MarkNotificationReadParams{
		ID: notif.ID, UserID: user.ID,
	}); err != nil {
		t.Fatalf("MarkNotificationRead: %v", err)
	}
	all, err := q.ListNotificationsByUser(ctx, generated.ListNotificationsByUserParams{
		UserID: user.ID, Limit: 50, Offset: 0,
	})
	if err != nil {
		t.Fatalf("ListNotificationsByUser: %v", err)
	}
	if len(all) == 0 {
		t.Error("expected at least one notification")
	}
	if err := q.MarkAllNotificationsRead(ctx, user.ID); err != nil {
		t.Fatalf("MarkAllNotificationsRead: %v", err)
	}
}

// TestAuditLog verifies creating audit events and all three listing queries.
func TestAuditLog(t *testing.T) {
	q, cleanup := testQ(t)
	defer cleanup()
	ctx := context.Background()
	org := setupOrg(t, q, uuid.New().String()[:8])
	user := setupUser(t, q, org.ID, "auditor@example.com")
	entityID := uuid.New()
	actorUID := pgtype.UUID{Bytes: user.ID, Valid: true}
	ip := netip.MustParseAddr("10.0.0.1")
	ua := "audit-test/1.0"
	_, err := q.CreateAuditEvent(ctx, generated.CreateAuditEventParams{
		ID: uuid.New(), OrgID: org.ID, ActorID: actorUID,
		Action: "item.created", EntityKind: "item", EntityID: entityID,
		Payload: []byte("{}"), IpAddress: &ip, UserAgent: &ua,
	})
	if err != nil {
		t.Fatalf("CreateAuditEvent: %v", err)
	}
	byActor, err := q.ListAuditEventsByActor(ctx, generated.ListAuditEventsByActorParams{
		ActorID: actorUID, Limit: 10, Offset: 0,
	})
	if err != nil {
		t.Fatalf("ListAuditEventsByActor: %v", err)
	}
	if len(byActor) == 0 {
		t.Error("expected at least one audit event by actor")
	}
	byEntity, err := q.ListAuditEventsByEntity(ctx, generated.ListAuditEventsByEntityParams{
		EntityKind: "item", EntityID: entityID,
	})
	if err != nil {
		t.Fatalf("ListAuditEventsByEntity: %v", err)
	}
	if len(byEntity) == 0 {
		t.Error("expected at least one audit event by entity")
	}
	byOrg, err := q.ListAuditEventsByOrg(ctx, generated.ListAuditEventsByOrgParams{
		OrgID: org.ID, Limit: 10, Offset: 0,
	})
	if err != nil {
		t.Fatalf("ListAuditEventsByOrg: %v", err)
	}
	if len(byOrg) == 0 {
		t.Error("expected at least one audit event by org")
	}
}

// TestWithTx verifies rolled-back transaction does not persist data.
func TestWithTx(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := generated.New(pool).WithTx(tx)
	org, err := q.CreateOrganization(ctx, generated.CreateOrganizationParams{
		ID: uuid.New(), Slug: "tx-" + uuid.New().String()[:8],
		Name: "Tx Org", Plan: "community",
	})
	if err != nil {
		t.Fatalf("CreateOrganization in tx: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	mainQ := generated.New(pool)
	_, err = mainQ.GetOrganizationByID(ctx, org.ID)
	if err == nil {
		t.Error("expected error after rollback: org should not exist")
	}
}

// TestMigrateDown verifies MigrateDown runs without error and Migrate restores state.
func TestMigrateDown(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	if err := db.MigrateDown(ctx, pool); err != nil {
		t.Fatalf("MigrateDown: %v", err)
	}
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate (restore): %v", err)
	}
}
