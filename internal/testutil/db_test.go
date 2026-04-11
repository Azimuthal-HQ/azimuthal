package testutil_test

import (
	"context"
	"testing"

	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
	"github.com/stretchr/testify/require"
)

// TestNewTestDB_Canary proves the test infrastructure works end-to-end:
// connect to a real database, run migrations, execute a query.
func TestNewTestDB_Canary(t *testing.T) {
	db := testutil.NewTestDB(t)
	var result int
	err := db.Pool.QueryRow(context.Background(), "SELECT 1").Scan(&result)
	require.NoError(t, err)
	require.Equal(t, 1, result)
}

// TestNewTestDB_SchemaIsolation proves that two parallel tests each get
// their own schema and cannot see each other's data.
func TestNewTestDB_SchemaIsolation(t *testing.T) {
	t.Parallel()

	t.Run("writer", func(t *testing.T) {
		t.Parallel()
		db := testutil.NewTestDB(t)
		_, err := db.Pool.Exec(context.Background(),
			`INSERT INTO organizations (id, slug, name) VALUES (gen_random_uuid(), 'isolation-test', 'Isolation Test')`)
		require.NoError(t, err)

		var count int
		err = db.Pool.QueryRow(context.Background(),
			`SELECT count(*) FROM organizations WHERE slug = 'isolation-test'`).Scan(&count)
		require.NoError(t, err)
		require.Equal(t, 1, count)
	})

	t.Run("reader", func(t *testing.T) {
		t.Parallel()
		db := testutil.NewTestDB(t)
		var count int
		err := db.Pool.QueryRow(context.Background(),
			`SELECT count(*) FROM organizations WHERE slug = 'isolation-test'`).Scan(&count)
		require.NoError(t, err)
		require.Equal(t, 0, count, "reader schema must not see writer schema data")
	})
}
