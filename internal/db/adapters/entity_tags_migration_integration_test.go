package adapters_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/tags"
	"github.com/Azimuthal-HQ/azimuthal/migrations"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// TestEntityTagsMigration_LabelValuesAndWorkflowRowsConvert is the regression
// test for migration 055's convergence half, run the only way a data migration
// can honestly be tested: against a database that actually holds the
// pre-migration shape. The test walks its own isolated database DOWN to 054 —
// which restores the labels columns and the 'labels' workflow vocabulary —
// seeds the data the migration exists to convert, and runs 055 UP again.
//
// Verified in both directions while writing: with the migration's backfill CTE
// deleted, the tag assertions below fail; with the workflow UPDATE deleted,
// the up migration itself fails on the rewritten CHECK — which is the
// "stranded row" failure mode the conversion exists to prevent, caught at
// migration time rather than as a transition refused forever.
func TestEntityTagsMigration_LabelValuesAndWorkflowRowsConvert(t *testing.T) {
	tdb := testutil.NewTestDB(t)
	ctx := context.Background()

	// A database/sql handle for goose, addressing this test's own database.
	db, err := sql.Open("pgx", tdb.DSN)
	require.NoError(t, err)
	defer db.Close()

	goose.SetTableName("goose_db_version")
	goose.SetBaseFS(migrations.FS)
	defer goose.SetBaseFS(nil)
	require.NoError(t, goose.SetDialect("postgres"))

	// Back to the world before the convergence: labels columns exist, the
	// workflow vocabulary says 'labels', entity_tags is page_tags again.
	require.NoError(t, goose.DownToContext(ctx, db, ".", 54))

	// ── Seed the pre-migration shape ────────────────────────────────────────
	org := testutil.CreateTestOrg(t, tdb.Pool)
	user := testutil.CreateTestUser(t, tdb.Pool, org.ID)
	space := testutil.CreateTestSpace(t, tdb.Pool, org.ID, user.ID, "beacon")

	// One tag already in the vocabulary, so the backfill's collision arm (an
	// existing slug keeps its existing spelling) is exercised too.
	_, err = tdb.Pool.Exec(ctx,
		`INSERT INTO tags (org_id, slug, name) VALUES ($1, 'urgent', 'Urgent')`, org.ID)
	require.NoError(t, err)

	ticketID := uuid.New()
	_, err = tdb.Pool.Exec(ctx,
		`INSERT INTO tickets (id, space_id, number, title, description, reporter_id, labels)
		 VALUES ($1, $2, 1, 'Pre-migration ticket', '', $3,
		         ARRAY['Design Docs', 'design docs', 'urgent!!', '!!!'])`,
		ticketID, space.ID, user.ID)
	require.NoError(t, err)

	itemID := uuid.New()
	_, err = tdb.Pool.Exec(ctx,
		`INSERT INTO project_items (id, space_id, org_id, number, item_key, kind, title,
		     status, priority, reporter_id, rank, labels)
		 SELECT $1, $2, s.org_id, 1, s.key || '-1', 'task', 'Pre-migration item',
		     'open', 'medium', $3, '0|a:', ARRAY['Design Docs']
		 FROM spaces s WHERE s.id = $2`,
		itemID, space.ID, user.ID)
	require.NoError(t, err)

	// A workflow edge carrying a guard AND a post-function on 'labels' — the
	// stored configuration a naive column drop would strand.
	var transitionID uuid.UUID
	require.NoError(t, tdb.Pool.QueryRow(ctx, `WITH wf AS (
		     INSERT INTO workflows (id, org_id, name, applies_to)
		     VALUES (gen_random_uuid(), $1, 'Conv', 'both')
		     RETURNING id
		 ), s1 AS (
		     INSERT INTO workflow_states (id, workflow_id, name, category, position)
		     SELECT gen_random_uuid(), id, 'Open', 'todo', 0 FROM wf RETURNING id, workflow_id
		 ), s2 AS (
		     INSERT INTO workflow_states (id, workflow_id, name, category, position)
		     SELECT gen_random_uuid(), workflow_id, 'Done', 'done', 1 FROM s1 RETURNING id, workflow_id
		 )
		 INSERT INTO workflow_transitions (id, workflow_id, from_state_id, to_state_id, name)
		 SELECT gen_random_uuid(), s1.workflow_id, s1.id, s2.id, 'Close' FROM s1, s2
		 RETURNING id`, org.ID).Scan(&transitionID))
	var guardID, pfID uuid.UUID
	require.NoError(t, tdb.Pool.QueryRow(ctx,
		`INSERT INTO workflow_transition_guards (transition_id, guard_class, kind, field_key)
		 VALUES ($1, 'validator', 'field_required', 'labels') RETURNING id`, transitionID).Scan(&guardID))
	require.NoError(t, tdb.Pool.QueryRow(ctx,
		`INSERT INTO workflow_transition_post_functions (transition_id, kind, field_key, field_value)
		 VALUES ($1, 'set_field', 'labels', 'escalated,urgent') RETURNING id`, transitionID).Scan(&pfID))

	// ── The migration under test ────────────────────────────────────────────
	require.NoError(t, goose.UpContext(ctx, db, "."))

	// ── Label values became entity tags, under the stated normalization ─────
	// 'Design Docs' and 'design docs' collapse to one slug; 'urgent!!'
	// normalises onto the EXISTING 'urgent' tag, whose spelling wins; '!!!'
	// normalises to nothing and is dropped.
	readTags := func(entityType string, entityID uuid.UUID) map[string]string {
		rows, err := tdb.Pool.Query(ctx,
			`SELECT t.slug, t.name FROM entity_tags et
			  JOIN tags t ON t.id = et.tag_id
			 WHERE et.entity_type = $1 AND et.entity_id = $2`, entityType, entityID)
		require.NoError(t, err)
		defer rows.Close()
		out := map[string]string{}
		for rows.Next() {
			var slug, name string
			require.NoError(t, rows.Scan(&slug, &name))
			out[slug] = name
		}
		require.NoError(t, rows.Err())
		return out
	}
	require.Equal(t, map[string]string{
		"design_docs": "Design Docs",
		"urgent":      "Urgent",
	}, readTags("ticket", ticketID),
		"labels must backfill as tags: normalised, deduped, unusable ones dropped, existing spellings kept")
	require.Equal(t, map[string]string{"design_docs": "Design Docs"}, readTags("project_item", itemID))

	// The normalization in the migration must agree with the one slug helper —
	// a backfilled label and the same label typed tomorrow must be ONE tag.
	for _, label := range []string{"Design Docs", "design docs", "urgent!!"} {
		var sqlSlug string
		require.NoError(t, tdb.Pool.QueryRow(ctx,
			`SELECT btrim(regexp_replace(lower($1), '[^a-z0-9]+', '_', 'g'), '_')`, label).Scan(&sqlSlug))
		require.Equal(t, tags.Slugify(label), sqlSlug,
			"the migration's SQL normalization must match itemtypes.Slugify for %q", label)
	}

	// ── The stored workflow rows converted rather than stranding ────────────
	var guardKey, pfKey string
	require.NoError(t, tdb.Pool.QueryRow(ctx,
		`SELECT field_key FROM workflow_transition_guards WHERE id = $1`, guardID).Scan(&guardKey))
	require.NoError(t, tdb.Pool.QueryRow(ctx,
		`SELECT field_key FROM workflow_transition_post_functions WHERE id = $1`, pfID).Scan(&pfKey))
	require.Equal(t, "tags", guardKey, "a stored 'labels' guard must convert, not strand")
	require.Equal(t, "tags", pfKey, "a stored 'labels' post-function must convert, not strand")

	// The rewritten CHECKs refuse the old key, so nothing can write a new
	// stranded row either.
	_, err = tdb.Pool.Exec(ctx,
		`INSERT INTO workflow_transition_guards (transition_id, guard_class, kind, field_key)
		 VALUES ($1, 'validator', 'field_required', 'labels')`, transitionID)
	require.Error(t, err, "the guard CHECK must no longer admit 'labels'")

	// And the columns are gone.
	var count int
	require.NoError(t, tdb.Pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.columns
		 WHERE table_name IN ('tickets', 'project_items') AND column_name = 'labels'`).Scan(&count))
	require.Zero(t, count, "the labels columns must be dropped")
}
