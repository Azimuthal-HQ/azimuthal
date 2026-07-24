package tickets

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// recordingSuggestionStore records what the service asked of it. errOnCall
// makes "the store was reached at all" a test failure, which is what pins the
// empty-readable-set short-circuit: a test that only asserted on the returned
// slice would still pass if the short-circuit were deleted, because
// space_id = ANY('{}') returns nothing anyway.
type recordingSuggestionStore struct {
	calls  int
	got    SuggestParams
	result []Suggestion
	err    error
}

func (s *recordingSuggestionStore) SuggestRefs(_ context.Context, params SuggestParams) ([]Suggestion, error) {
	s.calls++
	s.got = params
	return s.result, s.err
}

func TestSuggest_EmptyReadableSetNeverReachesTheStore(t *testing.T) {
	store := &recordingSuggestionStore{}
	svc := NewSuggestionService(store)

	out, err := svc.Suggest(context.Background(), SuggestParams{
		ReadableSpaceIDs: nil,
		CallerID:         uuid.New(),
		Query:            "BEA",
	})
	if err != nil {
		t.Fatalf("suggest with empty readable set: %v", err)
	}
	if store.calls != 0 {
		t.Fatalf("store was queried for a caller who can read nothing (%d calls)", store.calls)
	}
	if out == nil {
		t.Fatal("suggest returned a nil slice; the wire contract is an empty array, never null")
	}
	if len(out) != 0 {
		t.Fatalf("expected no suggestions, got %d", len(out))
	}
}

func TestSuggest_PassesTheReadableSetThroughUnchanged(t *testing.T) {
	spaceA, spaceB := uuid.New(), uuid.New()
	caller := uuid.New()
	store := &recordingSuggestionStore{result: []Suggestion{{Ref: "BEA-42"}}}
	svc := NewSuggestionService(store)

	out, err := svc.Suggest(context.Background(), SuggestParams{
		ReadableSpaceIDs: []uuid.UUID{spaceA, spaceB},
		CallerID:         caller,
		Query:            "login",
	})
	if err != nil {
		t.Fatalf("suggest: %v", err)
	}
	if store.calls != 1 {
		t.Fatalf("expected exactly one store call, got %d", store.calls)
	}
	if len(store.got.ReadableSpaceIDs) != 2 ||
		store.got.ReadableSpaceIDs[0] != spaceA || store.got.ReadableSpaceIDs[1] != spaceB {
		t.Fatalf("readable set was altered on the way to the store: %v", store.got.ReadableSpaceIDs)
	}
	if store.got.CallerID != caller {
		t.Fatalf("caller id was altered: got %s want %s", store.got.CallerID, caller)
	}
	if store.got.Query != "login" {
		t.Fatalf("query was altered: got %q", store.got.Query)
	}
	if len(out) != 1 || out[0].Ref != "BEA-42" {
		t.Fatalf("unexpected results: %v", out)
	}
}

func TestSuggest_StoreErrorIsWrappedNotSwallowed(t *testing.T) {
	sentinel := errors.New("boom")
	svc := NewSuggestionService(&recordingSuggestionStore{err: sentinel})

	out, err := svc.Suggest(context.Background(), SuggestParams{
		ReadableSpaceIDs: []uuid.UUID{uuid.New()},
		CallerID:         uuid.New(),
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("store error did not survive the service: %v", err)
	}
	if out != nil {
		t.Fatalf("expected no results alongside an error, got %v", out)
	}
}

func TestComposeRef(t *testing.T) {
	for _, tc := range []struct {
		key    string
		number int32
		want   string
	}{
		{"BEA", 42, "BEA-42"},
		{"OPS", 1, "OPS-1"},
		{"A", 1234567, "A-1234567"},
	} {
		if got := ComposeRef(tc.key, tc.number); got != tc.want {
			t.Errorf("ComposeRef(%q, %d) = %q, want %q", tc.key, tc.number, got, tc.want)
		}
	}
}
