package wiki

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// TreeNode represents a page and its children in the page tree.
type TreeNode struct {
	ID       uuid.UUID   `json:"id"`
	SpaceID  uuid.UUID   `json:"space_id"`
	ParentID pgtype.UUID `json:"parent_id"`
	Title    string      `json:"title"`
	Version  int32       `json:"version"`
	AuthorID uuid.UUID   `json:"author_id"`
	Position int32       `json:"position"`
	Children []*TreeNode `json:"children"`
}

// ListPagesBySpace returns all pages in a space as a flat list.
func (s *Service) ListPagesBySpace(ctx context.Context, spaceID uuid.UUID) ([]generated.ListPagesBySpaceRow, error) {
	pages, err := s.store.ListPagesBySpace(ctx, spaceID)
	if err != nil {
		return nil, fmt.Errorf("listing pages by space: %w", err)
	}
	return pages, nil
}

// ListRootPages returns root pages (no parent) for a space.
func (s *Service) ListRootPages(ctx context.Context, spaceID uuid.UUID) ([]generated.ListRootPagesBySpaceRow, error) {
	pages, err := s.store.ListRootPagesBySpace(ctx, spaceID)
	if err != nil {
		return nil, fmt.Errorf("listing root pages: %w", err)
	}
	return pages, nil
}

// ListChildPages returns the direct children of a page.
func (s *Service) ListChildPages(ctx context.Context, parentID uuid.UUID) ([]generated.ListChildPagesRow, error) {
	pgID := pgtype.UUID{Bytes: parentID, Valid: true}
	children, err := s.store.ListChildPages(ctx, pgID)
	if err != nil {
		return nil, fmt.Errorf("listing child pages: %w", err)
	}
	return children, nil
}

// BuildTree constructs the full page tree for a space. It fetches all pages
// in the space, organises them by parent, and returns a slice of root nodes
// with their children nested recursively.
func (s *Service) BuildTree(ctx context.Context, spaceID uuid.UUID) ([]*TreeNode, error) {
	pages, err := s.store.ListPagesBySpace(ctx, spaceID)
	if err != nil {
		return nil, fmt.Errorf("building tree: %w", err)
	}

	// Build nodes and index by ID.
	nodeByID := make(map[uuid.UUID]*TreeNode, len(pages))
	for _, p := range pages {
		nodeByID[p.ID] = &TreeNode{
			ID:       p.ID,
			SpaceID:  p.SpaceID,
			ParentID: p.ParentID,
			Title:    p.Title,
			Version:  p.Version,
			AuthorID: p.AuthorID,
			Position: p.Position,
			Children: []*TreeNode{},
		}
	}

	// Assemble the tree.
	var roots []*TreeNode
	for _, node := range nodeByID {
		if node.ParentID.Valid {
			parent, ok := nodeByID[node.ParentID.Bytes]
			if ok {
				parent.Children = append(parent.Children, node)
			} else {
				// Orphaned — treat as root.
				roots = append(roots, node)
			}
		} else {
			roots = append(roots, node)
		}
	}

	// Sort children by position (pages come from DB ordered, but map iteration is random).
	sortTreeNodes(roots)
	for _, node := range nodeByID {
		sortTreeNodes(node.Children)
	}

	return roots, nil
}

// sortTreeNodes sorts tree nodes by position, then title.
func sortTreeNodes(nodes []*TreeNode) {
	// Simple insertion sort — page counts per level are typically small.
	for i := 1; i < len(nodes); i++ {
		key := nodes[i]
		j := i - 1
		for j >= 0 && (nodes[j].Position > key.Position ||
			(nodes[j].Position == key.Position && nodes[j].Title > key.Title)) {
			nodes[j+1] = nodes[j]
			j--
		}
		nodes[j+1] = key
	}
}
