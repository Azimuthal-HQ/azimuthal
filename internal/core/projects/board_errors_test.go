package projects

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// The failure paths of BoardConfigService. They matter for one reason: the
// alternative to reporting a failure is presenting a board that belongs to
// nobody. "Nothing is stored" and "storage is unreachable" are one branch
// apart, as are "this space has no workflow" and "its workflow could not be
// read" — and in both cases the wrong branch produces a plausible-looking
// default board rather than anything a user would notice as broken.

var (
	errRepoDown           = errors.New("board repository unavailable")
	errWorkflowUnreadable = errors.New("workflow states unreadable")
)

// replaceFailingRepo is fakeBoardRepo with a ReplaceConfig that refuses. The
// shared fake records saves rather than failing them, which is the right
// default for the success-path tests but leaves the write failure unreachable.
type replaceFailingRepo struct {
	fakeBoardRepo
	replaceErr error
}

func (r *replaceFailingRepo) ReplaceConfig(_ context.Context, _ uuid.UUID, _ []BoardColumn) error {
	return r.replaceErr
}

func TestBoardConfig_GetReportsRepositoryFailure(t *testing.T) {
	svc := NewBoardConfigService(
		&fakeBoardRepo{listErr: errRepoDown},
		&fakeStatusReader{statuses: []string{"open", "done"}},
	)

	cfg, err := svc.GetConfig(context.Background(), uuid.New())

	if !errors.Is(err, errRepoDown) {
		t.Fatalf("err = %v, want it to wrap %v", err, errRepoDown)
	}
	// A configuration returned here would be the derived default, which is
	// indistinguishable to a client from a space that was never customised.
	if cfg != nil {
		t.Errorf("got a config alongside the error: %+v", cfg)
	}
}

func TestBoardConfig_GetReportsWorkflowFailure(t *testing.T) {
	// An unreadable workflow is not an empty workflow. Empty legitimately
	// falls back to DefaultColumnNames; a read failure must not, or a space
	// with its own vocabulary is shown someone else's column set.
	svc := NewBoardConfigService(&fakeBoardRepo{}, &fakeStatusReader{err: errWorkflowUnreadable})

	cfg, err := svc.GetConfig(context.Background(), uuid.New())

	if !errors.Is(err, errWorkflowUnreadable) {
		t.Fatalf("err = %v, want it to wrap %v", err, errWorkflowUnreadable)
	}
	if cfg != nil {
		t.Errorf("fell back to a default board on a read failure: %+v", cfg)
	}
}

func TestBoardConfig_SaveRefusesAgainstAnUnreadableVocabulary(t *testing.T) {
	// The reader returns a usable-looking list *and* an error. Validating
	// against a partial vocabulary is exactly how a status ends up with no
	// column, so the partial answer must not be trusted.
	repo := &fakeBoardRepo{}
	svc := NewBoardConfigService(repo, &fakeStatusReader{
		statuses: []string{"open"},
		err:      errWorkflowUnreadable,
	})

	cfg, err := svc.SaveConfig(context.Background(), uuid.New(), []BoardColumn{col("Everything", "open")})

	if !errors.Is(err, errWorkflowUnreadable) {
		t.Fatalf("err = %v, want it to wrap %v", err, errWorkflowUnreadable)
	}
	if cfg != nil {
		t.Errorf("got a config alongside the error: %+v", cfg)
	}
	if repo.saveCalls != 0 {
		t.Error("an unvalidated configuration reached the repository")
	}
}

func TestBoardConfig_SaveReportsStorageFailure(t *testing.T) {
	// The returned config is what the client renders. Returning it after a
	// failed write shows the user a layout they have not got.
	repo := &replaceFailingRepo{replaceErr: errRepoDown}
	svc := NewBoardConfigService(repo, &fakeStatusReader{statuses: []string{"open", "done"}})

	cfg, err := svc.SaveConfig(context.Background(), uuid.New(), []BoardColumn{col("Everything", "open", "done")})

	if !errors.Is(err, errRepoDown) {
		t.Fatalf("err = %v, want it to wrap %v", err, errRepoDown)
	}
	if cfg != nil {
		t.Errorf("reported a saved config that was never stored: %+v", cfg)
	}
}

func TestBoardConfig_ResetReportsStorageFailure(t *testing.T) {
	// Reset clears storage and then re-reads. If the clear fails and the
	// re-read still happens, the caller is handed the columns that are still
	// stored, presented as the result of the reset.
	repo := &replaceFailingRepo{
		fakeBoardRepo: fakeBoardRepo{columns: []BoardColumn{col("Everything", "open", "done")}},
		replaceErr:    errRepoDown,
	}
	svc := NewBoardConfigService(repo, &fakeStatusReader{statuses: []string{"open", "done"}})

	cfg, err := svc.ResetConfig(context.Background(), uuid.New())

	if !errors.Is(err, errRepoDown) {
		t.Fatalf("err = %v, want it to wrap %v", err, errRepoDown)
	}
	if cfg != nil {
		t.Errorf("reported a reset that did not happen: %+v", cfg)
	}
}

func TestBoardConfig_DeleteColumnReportsRepositoryFailures(t *testing.T) {
	spaceID := uuid.New()
	doing := col("Doing", "in_progress")
	done := col("Done", "done")

	t.Run("the current columns cannot be listed", func(t *testing.T) {
		// Deletability is decided from the current columns, so an unreadable
		// list must stop the deletion rather than let it proceed against an
		// empty one.
		repo := &fakeBoardRepo{columns: []BoardColumn{doing, done}, listErr: errRepoDown}
		svc := NewBoardConfigService(repo, &fakeStatusReader{statuses: []string{"in_progress", "done"}})

		cfg, err := svc.DeleteColumn(context.Background(), spaceID, doing.ID, done.ID)

		if !errors.Is(err, errRepoDown) {
			t.Fatalf("err = %v, want it to wrap %v", err, errRepoDown)
		}
		if cfg != nil {
			t.Errorf("got a config alongside the error: %+v", cfg)
		}
		if repo.deleteArgs != [3]uuid.UUID{} {
			t.Error("a deletion was attempted against an unknown column set")
		}
	})

	t.Run("the deletion itself fails", func(t *testing.T) {
		repo := &fakeBoardRepo{columns: []BoardColumn{doing, done}, deleteErr: errRepoDown}
		svc := NewBoardConfigService(repo, &fakeStatusReader{statuses: []string{"in_progress", "done"}})

		cfg, err := svc.DeleteColumn(context.Background(), spaceID, doing.ID, done.ID)

		if !errors.Is(err, errRepoDown) {
			t.Fatalf("err = %v, want it to wrap %v", err, errRepoDown)
		}
		// Re-reading after a failed delete would return the board with the
		// column still on it, which reads as a successful no-op.
		if cfg != nil {
			t.Errorf("reported a deletion that did not happen: %+v", cfg)
		}
	})
}

func TestBoardConfig_NilWorkflowReaderUsesTheDefaultVocabulary(t *testing.T) {
	// The constructor accepts a nil reader and board.go handles it explicitly.
	// A space wired that way must degrade to the documented default set rather
	// than panic — and must validate against it too, since a vocabulary used
	// only for display would let a save drop statuses off the board.
	svc := NewBoardConfigService(&fakeBoardRepo{}, nil)
	spaceID := uuid.New()

	cfg, err := svc.GetConfig(context.Background(), spaceID)
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if len(cfg.Columns) != len(DefaultColumnNames) {
		t.Fatalf("got %d columns, want the %d default names", len(cfg.Columns), len(DefaultColumnNames))
	}
	for i, want := range DefaultColumnNames {
		if cfg.Columns[i].Name != want {
			t.Errorf("column %d: name = %q, want %q", i, cfg.Columns[i].Name, want)
		}
	}

	_, err = svc.SaveConfig(context.Background(), spaceID, []BoardColumn{col("Everything", "open")})
	if !errors.Is(err, ErrStatusUnmapped) {
		t.Fatalf("err = %v, want ErrStatusUnmapped: the remaining default statuses have no column", err)
	}
}

func TestBoardConfig_SaveDropsBlankStatuses(t *testing.T) {
	// Clients build a column's status list from form rows, so an unfilled row
	// arrives as an empty string — twice over if the user added two. Blanks
	// are dropped, which means two of them are not "the same status mapped
	// twice", and a column left claiming nothing still keeps its place.
	repo := &fakeBoardRepo{}
	svc := NewBoardConfigService(repo, &fakeStatusReader{statuses: []string{"open", "done"}})

	cfg, err := svc.SaveConfig(context.Background(), uuid.New(), []BoardColumn{
		{Name: "Doing", Statuses: []string{" open ", "", "   "}},
		{Name: "Parking", Statuses: []string{"\t"}},
		{Name: "Done", Statuses: []string{"done"}},
	})
	if err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	if len(repo.saved) != 3 {
		t.Fatalf("stored %d columns, want 3", len(repo.saved))
	}
	// " open " having satisfied the vocabulary check proves the trimmed form
	// is what was claimed, not the padded one.
	if got := repo.saved[0].Statuses; len(got) != 1 || got[0] != "open" {
		t.Errorf("Doing stored statuses %q, want the single trimmed [open]", got)
	}
	if got := repo.saved[1].Statuses; len(got) != 0 {
		t.Errorf("Parking stored statuses %q, want none: its only entry was blank", got)
	}
	if _, ok := cfg.ColumnForStatus(""); ok {
		t.Error("a column claims the empty status, so items with no status would collect in it")
	}
}
