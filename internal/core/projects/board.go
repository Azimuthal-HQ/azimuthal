package projects

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// BoardColumn is one column of a space's board: a name, an order, an optional
// soft WIP limit, and the item statuses that land in it.
type BoardColumn struct {
	ID       uuid.UUID `json:"id"`
	SpaceID  uuid.UUID `json:"space_id"`
	Name     string    `json:"name"`
	Position int       `json:"position"`
	// WIPLimit nil means no limit. Limits are advisory: nothing in this
	// package refuses a transition because a column is over its limit.
	WIPLimit  *int      `json:"wip_limit"`
	Statuses  []string  `json:"statuses"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// BoardConfig is a space's whole board configuration.
type BoardConfig struct {
	SpaceID uuid.UUID     `json:"space_id"`
	Columns []BoardColumn `json:"columns"`
	// Customized is false when the space has no stored configuration and these
	// columns were derived from its workflow states. Clients use it to tell
	// "never customised" from "customised to look like the default".
	Customized bool `json:"customized"`
}

// BoardConfigRepository is the data access contract for board configuration.
type BoardConfigRepository interface {
	// ListColumns returns a space's stored columns, ordered by position, each
	// carrying its mapped statuses. An empty slice means no stored config.
	ListColumns(ctx context.Context, spaceID uuid.UUID) ([]BoardColumn, error)
	// ReplaceConfig atomically replaces a space's entire configuration.
	ReplaceConfig(ctx context.Context, spaceID uuid.UUID, columns []BoardColumn) error
	// DeleteColumn removes a column after re-homing its statuses onto
	// remapTo, in one transaction.
	DeleteColumn(ctx context.Context, spaceID, columnID, remapTo uuid.UUID) error
}

// WorkflowStateReader supplies the space's status vocabulary — the same
// workflow states the board has always derived its columns from.
type WorkflowStateReader interface {
	StatusesForSpace(ctx context.Context, spaceID uuid.UUID) ([]string, error)
}

// DefaultColumnNames is the fallback column set, matching what the board
// rendered before configuration existed. Kept in sync with the frontend's
// FALLBACK_COLUMNS; used only when a space has no workflow states.
var DefaultColumnNames = []string{"open", "todo", "in_progress", "in_review", "done"}

// BoardConfigService reads and writes per-space board configuration.
type BoardConfigService struct {
	repo      BoardConfigRepository
	workflows WorkflowStateReader
}

// NewBoardConfigService creates a BoardConfigService.
func NewBoardConfigService(repo BoardConfigRepository, workflows WorkflowStateReader) *BoardConfigService {
	return &BoardConfigService{repo: repo, workflows: workflows}
}

// GetConfig returns a space's board configuration, deriving the default from
// the space's workflow states when nothing is stored. A space that has never
// been customised therefore renders exactly as it always did.
func (s *BoardConfigService) GetConfig(ctx context.Context, spaceID uuid.UUID) (*BoardConfig, error) {
	columns, err := s.repo.ListColumns(ctx, spaceID)
	if err != nil {
		return nil, fmt.Errorf("getting board config: %w", err)
	}
	if len(columns) > 0 {
		return &BoardConfig{SpaceID: spaceID, Columns: columns, Customized: true}, nil
	}

	statuses, err := s.statusVocabulary(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	return &BoardConfig{SpaceID: spaceID, Columns: defaultColumns(spaceID, statuses), Customized: false}, nil
}

// SaveConfig validates and stores a whole configuration, replacing whatever
// was there. Validation is the point: it refuses any layout that would leave
// a status with nowhere to appear.
func (s *BoardConfigService) SaveConfig(ctx context.Context, spaceID uuid.UUID, columns []BoardColumn) (*BoardConfig, error) {
	statuses, err := s.statusVocabulary(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	normalized, err := validateColumns(spaceID, columns, statuses)
	if err != nil {
		return nil, fmt.Errorf("saving board config: %w", err)
	}
	if err := s.repo.ReplaceConfig(ctx, spaceID, normalized); err != nil {
		return nil, fmt.Errorf("saving board config: %w", err)
	}
	return &BoardConfig{SpaceID: spaceID, Columns: normalized, Customized: true}, nil
}

// ResetConfig drops a space's stored configuration, returning it to the
// derived default.
func (s *BoardConfigService) ResetConfig(ctx context.Context, spaceID uuid.UUID) (*BoardConfig, error) {
	if err := s.repo.ReplaceConfig(ctx, spaceID, nil); err != nil {
		return nil, fmt.Errorf("resetting board config: %w", err)
	}
	return s.GetConfig(ctx, spaceID)
}

// DeleteColumn removes a column, re-homing its statuses onto remapTo. There is
// no variant that drops the statuses: every status must remain mapped, so the
// caller is required to say where its work goes.
func (s *BoardConfigService) DeleteColumn(ctx context.Context, spaceID, columnID, remapTo uuid.UUID) (*BoardConfig, error) {
	if columnID == remapTo {
		return nil, fmt.Errorf("deleting board column: %w", ErrInvalidRemapTarget)
	}

	columns, err := s.repo.ListColumns(ctx, spaceID)
	if err != nil {
		return nil, fmt.Errorf("deleting board column: %w", err)
	}
	if err := checkDeletable(columns, columnID, remapTo); err != nil {
		return nil, fmt.Errorf("deleting board column: %w", err)
	}

	if err := s.repo.DeleteColumn(ctx, spaceID, columnID, remapTo); err != nil {
		return nil, fmt.Errorf("deleting board column: %w", err)
	}
	return s.GetConfig(ctx, spaceID)
}

// checkDeletable reports whether a column may be removed in favour of remapTo,
// given the space's current columns. Both ids must name columns of this space,
// so a target belonging to another space is refused here rather than silently
// re-homing work across a space boundary.
func checkDeletable(columns []BoardColumn, columnID, remapTo uuid.UUID) error {
	switch len(columns) {
	case 0:
		return ErrNotFound
	case 1:
		return ErrLastColumn
	}

	var foundColumn, foundTarget bool
	for _, c := range columns {
		if c.ID == columnID {
			foundColumn = true
		}
		if c.ID == remapTo {
			foundTarget = true
		}
	}
	if !foundColumn {
		return ErrNotFound
	}
	if !foundTarget {
		return ErrInvalidRemapTarget
	}
	return nil
}

// statusVocabulary returns the statuses a board must account for.
func (s *BoardConfigService) statusVocabulary(ctx context.Context, spaceID uuid.UUID) ([]string, error) {
	if s.workflows == nil {
		return append([]string(nil), DefaultColumnNames...), nil
	}
	statuses, err := s.workflows.StatusesForSpace(ctx, spaceID)
	if err != nil {
		return nil, fmt.Errorf("reading space statuses: %w", err)
	}
	if len(statuses) == 0 {
		return append([]string(nil), DefaultColumnNames...), nil
	}
	return statuses, nil
}

// defaultColumns builds the derived default: one column per status, in
// workflow order, named for the status. This reproduces the pre-configuration
// board, where every workflow state was its own column.
func defaultColumns(spaceID uuid.UUID, statuses []string) []BoardColumn {
	columns := make([]BoardColumn, 0, len(statuses))
	for i, status := range statuses {
		columns = append(columns, BoardColumn{
			// Deterministic id derived from the space and status: a derived
			// config is not stored, but clients still need stable keys, and a
			// random uuid would change on every read.
			ID:       uuid.NewSHA1(uuid.NameSpaceOID, []byte(spaceID.String()+"/"+status)),
			SpaceID:  spaceID,
			Name:     status,
			Position: i,
			Statuses: []string{status},
		})
	}
	return columns
}

// validateColumns checks a proposed configuration and returns it normalised
// (positions renumbered from zero, statuses trimmed).
//
// The rules, in the order a caller is likely to break them: at least one
// column; every column named, uniquely; WIP limits positive when present; no
// status in two columns; and every status in the space's vocabulary mapped
// somewhere. That last rule is the one the whole feature turns on — a status
// with no column is work that vanishes from the board.
func validateColumns(spaceID uuid.UUID, columns []BoardColumn, vocabulary []string) ([]BoardColumn, error) {
	if len(columns) == 0 {
		return nil, ErrLastColumn
	}

	seenNames := make(map[string]bool, len(columns))
	seenStatus := make(map[string]string, len(vocabulary))
	out := make([]BoardColumn, 0, len(columns))

	for i, col := range columns {
		name := strings.TrimSpace(col.Name)
		if err := checkColumnShape(name, col.WIPLimit, seenNames); err != nil {
			return nil, err
		}
		seenNames[strings.ToLower(name)] = true

		statuses, err := claimStatuses(col.Statuses, name, seenStatus)
		if err != nil {
			return nil, err
		}

		id := col.ID
		if id == uuid.Nil {
			id = uuid.New()
		}
		out = append(out, BoardColumn{
			ID:       id,
			SpaceID:  spaceID,
			Name:     name,
			Position: i,
			WIPLimit: col.WIPLimit,
			Statuses: statuses,
		})
	}

	for _, st := range vocabulary {
		if _, ok := seenStatus[st]; !ok {
			return nil, fmt.Errorf("%w: %q has no column", ErrStatusUnmapped, st)
		}
	}
	return out, nil
}

// checkColumnShape validates one column's name and WIP limit against the names
// already taken. Names compare case-insensitively: two columns called "Doing"
// and "doing" are the same column to a reader.
func checkColumnShape(name string, wipLimit *int, seenNames map[string]bool) error {
	if name == "" {
		return ErrColumnNameRequired
	}
	if seenNames[strings.ToLower(name)] {
		return ErrColumnNameDuplicate
	}
	if wipLimit != nil && *wipLimit <= 0 {
		return ErrInvalidWIPLimit
	}
	return nil
}

// claimStatuses records this column's statuses in seenStatus, refusing any
// status another column has already taken. Blank entries are dropped rather
// than rejected — a client sending an empty string means "nothing here".
func claimStatuses(statuses []string, columnName string, seenStatus map[string]string) ([]string, error) {
	out := make([]string, 0, len(statuses))
	for _, st := range statuses {
		st = strings.TrimSpace(st)
		if st == "" {
			continue
		}
		if _, dup := seenStatus[st]; dup {
			return nil, fmt.Errorf("%w: %q is mapped twice", ErrStatusUnmapped, st)
		}
		seenStatus[st] = columnName
		out = append(out, st)
	}
	return out, nil
}

// ColumnForStatus returns the column a status belongs to, and whether any
// column claims it.
func (c *BoardConfig) ColumnForStatus(status string) (*BoardColumn, bool) {
	for i := range c.Columns {
		for _, s := range c.Columns[i].Statuses {
			if s == status {
				return &c.Columns[i], true
			}
		}
	}
	return nil, false
}

// ErrIsBoardValidation reports whether err is one of the board configuration
// validation failures, so the API layer can answer 400 rather than 500.
func ErrIsBoardValidation(err error) bool {
	return errors.Is(err, ErrColumnNameRequired) ||
		errors.Is(err, ErrColumnNameDuplicate) ||
		errors.Is(err, ErrInvalidWIPLimit) ||
		errors.Is(err, ErrStatusUnmapped) ||
		errors.Is(err, ErrLastColumn) ||
		errors.Is(err, ErrInvalidRemapTarget)
}
