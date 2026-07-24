package projects

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// These exercise BoardConfigService's rules against fake repositories. The
// storage-level guarantees (the (space_id, status) primary key and the ON
// DELETE RESTRICT foreign key) are covered against real PostgreSQL in
// internal/db/adapters/board_config_integration_test.go.

type fakeBoardRepo struct {
	columns []BoardColumn
	// saved records what ReplaceConfig was last handed.
	saved      []BoardColumn
	saveCalls  int
	deleteArgs [3]uuid.UUID
	deleteErr  error
	listErr    error
}

func (f *fakeBoardRepo) ListColumns(_ context.Context, _ uuid.UUID) ([]BoardColumn, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.columns, nil
}

func (f *fakeBoardRepo) ReplaceConfig(_ context.Context, _ uuid.UUID, columns []BoardColumn) error {
	f.saveCalls++
	f.saved = columns
	f.columns = columns
	return nil
}

func (f *fakeBoardRepo) DeleteColumn(_ context.Context, spaceID, columnID, remapTo uuid.UUID) error {
	f.deleteArgs = [3]uuid.UUID{spaceID, columnID, remapTo}
	if f.deleteErr != nil {
		return f.deleteErr
	}
	remaining := make([]BoardColumn, 0, len(f.columns))
	for _, c := range f.columns {
		if c.ID == columnID {
			continue
		}
		if c.ID == remapTo {
			for _, other := range f.columns {
				if other.ID == columnID {
					c.Statuses = append(append([]string{}, c.Statuses...), other.Statuses...)
				}
			}
		}
		remaining = append(remaining, c)
	}
	f.columns = remaining
	return nil
}

type fakeStatusReader struct {
	statuses []string
	err      error
}

func (f *fakeStatusReader) StatusesForSpace(_ context.Context, _ uuid.UUID) ([]string, error) {
	return f.statuses, f.err
}

func col(name string, statuses ...string) BoardColumn {
	return BoardColumn{ID: uuid.New(), Name: name, Statuses: statuses}
}

func TestBoardConfig_DefaultMatchesWorkflowStates(t *testing.T) {
	// The regression protection every existing space depends on: with nothing
	// stored, the board's columns are exactly the space's workflow states, in
	// workflow order, one column each — which is what the board rendered
	// before configuration existed.
	spaceID := uuid.New()
	states := []string{"open", "in_progress", "in_review", "done"}
	svc := NewBoardConfigService(&fakeBoardRepo{}, &fakeStatusReader{statuses: states})

	cfg, err := svc.GetConfig(context.Background(), spaceID)
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}

	if cfg.Customized {
		t.Error("a space with no stored config must report customized=false")
	}
	if len(cfg.Columns) != len(states) {
		t.Fatalf("got %d columns, want %d", len(cfg.Columns), len(states))
	}
	for i, want := range states {
		got := cfg.Columns[i]
		if got.Name != want {
			t.Errorf("column %d: name = %q, want %q", i, got.Name, want)
		}
		if got.Position != i {
			t.Errorf("column %d: position = %d, want %d", i, got.Position, i)
		}
		if len(got.Statuses) != 1 || got.Statuses[0] != want {
			t.Errorf("column %d: statuses = %v, want [%q]", i, got.Statuses, want)
		}
		if got.WIPLimit != nil {
			t.Errorf("column %d: default config must carry no WIP limit, got %d", i, *got.WIPLimit)
		}
	}
}

func TestBoardConfig_DefaultColumnIDsAreStable(t *testing.T) {
	// A derived config is not stored, so its ids must be reproducible —
	// otherwise every poll hands the client new React keys and the board
	// remounts under the user.
	spaceID := uuid.New()
	svc := NewBoardConfigService(&fakeBoardRepo{}, &fakeStatusReader{statuses: []string{"open", "done"}})

	first, err := svc.GetConfig(context.Background(), spaceID)
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	second, err := svc.GetConfig(context.Background(), spaceID)
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	for i := range first.Columns {
		if first.Columns[i].ID != second.Columns[i].ID {
			t.Errorf("column %d id changed between reads: %s vs %s", i, first.Columns[i].ID, second.Columns[i].ID)
		}
	}
}

func TestBoardConfig_DefaultFallsBackWhenSpaceHasNoWorkflow(t *testing.T) {
	svc := NewBoardConfigService(&fakeBoardRepo{}, &fakeStatusReader{statuses: nil})
	cfg, err := svc.GetConfig(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if len(cfg.Columns) != len(DefaultColumnNames) {
		t.Fatalf("got %d columns, want the %d default names", len(cfg.Columns), len(DefaultColumnNames))
	}
}

func TestBoardConfig_StoredConfigWins(t *testing.T) {
	stored := []BoardColumn{col("Doing", "open", "in_progress"), col("Done", "in_review", "done")}
	svc := NewBoardConfigService(
		&fakeBoardRepo{columns: stored},
		&fakeStatusReader{statuses: []string{"open", "in_progress", "in_review", "done"}},
	)

	cfg, err := svc.GetConfig(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if !cfg.Customized {
		t.Error("a space with stored columns must report customized=true")
	}
	if len(cfg.Columns) != 2 {
		t.Fatalf("got %d columns, want 2", len(cfg.Columns))
	}
}

func TestBoardConfig_SaveRejectsUnmappedStatus(t *testing.T) {
	// The rule the whole feature turns on. "in_review" exists in the space's
	// vocabulary but no column claims it, so the save must fail — otherwise
	// every item in review silently disappears from the board.
	repo := &fakeBoardRepo{}
	svc := NewBoardConfigService(repo, &fakeStatusReader{
		statuses: []string{"open", "in_progress", "in_review", "done"},
	})

	_, err := svc.SaveConfig(context.Background(), uuid.New(), []BoardColumn{
		col("Doing", "open", "in_progress"),
		col("Done", "done"),
	})

	if !errors.Is(err, ErrStatusUnmapped) {
		t.Fatalf("err = %v, want ErrStatusUnmapped", err)
	}
	if repo.saveCalls != 0 {
		t.Error("a rejected configuration must not reach the repository")
	}
}

func TestBoardConfig_SaveRejectsDoubleMappedStatus(t *testing.T) {
	repo := &fakeBoardRepo{}
	svc := NewBoardConfigService(repo, &fakeStatusReader{statuses: []string{"open", "done"}})

	_, err := svc.SaveConfig(context.Background(), uuid.New(), []BoardColumn{
		col("A", "open", "done"),
		col("B", "done"),
	})

	if !errors.Is(err, ErrStatusUnmapped) {
		t.Fatalf("err = %v, want ErrStatusUnmapped for a twice-mapped status", err)
	}
	if repo.saveCalls != 0 {
		t.Error("a rejected configuration must not reach the repository")
	}
}

func TestBoardConfig_SaveAcceptsFullCoverage(t *testing.T) {
	repo := &fakeBoardRepo{}
	svc := NewBoardConfigService(repo, &fakeStatusReader{
		statuses: []string{"open", "in_progress", "in_review", "done"},
	})
	limit := 3

	cfg, err := svc.SaveConfig(context.Background(), uuid.New(), []BoardColumn{
		{Name: "Backlog", Statuses: []string{"open"}},
		{Name: "Doing", Statuses: []string{"in_progress", "in_review"}, WIPLimit: &limit},
		{Name: "Done", Statuses: []string{"done"}},
	})
	if err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	if !cfg.Customized {
		t.Error("a saved config must report customized=true")
	}
	// Positions come from array order, not from whatever the client sent.
	for i, c := range cfg.Columns {
		if c.Position != i {
			t.Errorf("column %d: position = %d, want %d", i, c.Position, i)
		}
		if c.ID == uuid.Nil {
			t.Errorf("column %d: got the nil UUID, want an assigned id", i)
		}
	}
	if cfg.Columns[1].WIPLimit == nil || *cfg.Columns[1].WIPLimit != 3 {
		t.Error("the WIP limit did not survive the save")
	}
}

func TestBoardConfig_SaveRejectsBadColumns(t *testing.T) {
	zero, negative := 0, -1
	tests := []struct {
		name    string
		columns []BoardColumn
		want    error
	}{
		{"no columns at all", nil, ErrLastColumn},
		{"blank name", []BoardColumn{{Name: "  ", Statuses: []string{"open"}}}, ErrColumnNameRequired},
		{
			"duplicate name, case-insensitively",
			[]BoardColumn{{Name: "Doing", Statuses: []string{"open"}}, {Name: "doing"}},
			ErrColumnNameDuplicate,
		},
		{
			"zero WIP limit closes the column rather than limiting it",
			[]BoardColumn{{Name: "Doing", Statuses: []string{"open"}, WIPLimit: &zero}},
			ErrInvalidWIPLimit,
		},
		{
			"negative WIP limit",
			[]BoardColumn{{Name: "Doing", Statuses: []string{"open"}, WIPLimit: &negative}},
			ErrInvalidWIPLimit,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeBoardRepo{}
			svc := NewBoardConfigService(repo, &fakeStatusReader{statuses: []string{"open"}})
			_, err := svc.SaveConfig(context.Background(), uuid.New(), tc.columns)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if repo.saveCalls != 0 {
				t.Error("a rejected configuration must not reach the repository")
			}
		})
	}
}

func TestBoardConfig_DeleteColumnRemapsStatuses(t *testing.T) {
	spaceID := uuid.New()
	doing := col("Doing", "in_progress")
	done := col("Done", "done")
	repo := &fakeBoardRepo{columns: []BoardColumn{doing, done}}
	svc := NewBoardConfigService(repo, &fakeStatusReader{statuses: []string{"in_progress", "done"}})

	cfg, err := svc.DeleteColumn(context.Background(), spaceID, doing.ID, done.ID)
	if err != nil {
		t.Fatalf("DeleteColumn: %v", err)
	}

	if repo.deleteArgs != [3]uuid.UUID{spaceID, doing.ID, done.ID} {
		t.Errorf("repository got %v, want the space, deleted column and target", repo.deleteArgs)
	}
	if len(cfg.Columns) != 1 {
		t.Fatalf("got %d columns after deletion, want 1", len(cfg.Columns))
	}
	// The deleted column's status must now live on the target — this is the
	// "every status remains mapped" guarantee at the service level.
	if _, ok := cfg.ColumnForStatus("in_progress"); !ok {
		t.Error("in_progress lost its column when Doing was deleted")
	}
	if _, ok := cfg.ColumnForStatus("done"); !ok {
		t.Error("done lost its column")
	}
}

func TestBoardConfig_DeleteColumnRejectsBadTargets(t *testing.T) {
	spaceID := uuid.New()
	doing := col("Doing", "in_progress")
	done := col("Done", "done")
	elsewhere := uuid.New()

	tests := []struct {
		name              string
		columns           []BoardColumn
		columnID, remapTo uuid.UUID
		want              error
	}{
		{"remapping a column onto itself", []BoardColumn{doing, done}, doing.ID, doing.ID, ErrInvalidRemapTarget},
		{"target is not a column of this space", []BoardColumn{doing, done}, doing.ID, elsewhere, ErrInvalidRemapTarget},
		{"the column does not exist", []BoardColumn{doing, done}, elsewhere, done.ID, ErrNotFound},
		{"deleting the only column", []BoardColumn{doing}, doing.ID, done.ID, ErrLastColumn},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeBoardRepo{columns: tc.columns}
			svc := NewBoardConfigService(repo, &fakeStatusReader{statuses: []string{"in_progress", "done"}})
			_, err := svc.DeleteColumn(context.Background(), spaceID, tc.columnID, tc.remapTo)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if repo.deleteArgs != [3]uuid.UUID{} {
				t.Error("a rejected deletion must not reach the repository")
			}
		})
	}
}

func TestBoardConfig_ResetReturnsToDerivedDefault(t *testing.T) {
	repo := &fakeBoardRepo{columns: []BoardColumn{col("Everything", "open", "done")}}
	svc := NewBoardConfigService(repo, &fakeStatusReader{statuses: []string{"open", "done"}})

	cfg, err := svc.ResetConfig(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("ResetConfig: %v", err)
	}
	if cfg.Customized {
		t.Error("a reset space must report customized=false")
	}
	if len(cfg.Columns) != 2 {
		t.Fatalf("got %d columns after reset, want the 2 derived ones", len(cfg.Columns))
	}
}

func TestBoardConfig_ColumnForStatus(t *testing.T) {
	cfg := &BoardConfig{Columns: []BoardColumn{col("Doing", "in_progress"), col("Done", "done")}}

	got, ok := cfg.ColumnForStatus("done")
	if !ok || got.Name != "Done" {
		t.Errorf("ColumnForStatus(done) = %v, %v; want the Done column", got, ok)
	}
	if _, ok := cfg.ColumnForStatus("nonexistent"); ok {
		t.Error("ColumnForStatus claimed a column for an unmapped status")
	}
}

func TestErrIsBoardValidation(t *testing.T) {
	// The API layer answers 400 on these and 500 on anything else; a
	// validation error escaping as 500 would read as a server fault.
	for _, err := range []error{
		ErrColumnNameRequired, ErrColumnNameDuplicate, ErrInvalidWIPLimit,
		ErrStatusUnmapped, ErrLastColumn, ErrInvalidRemapTarget,
	} {
		if !ErrIsBoardValidation(err) {
			t.Errorf("ErrIsBoardValidation(%v) = false, want true", err)
		}
	}
	if ErrIsBoardValidation(errors.New("database exploded")) {
		t.Error("an arbitrary error must not be treated as validation")
	}
}
