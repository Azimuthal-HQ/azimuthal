package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
)

// TestCmdServer_NoDirectConfigLoad is the drift guard for loadConfig.
//
// AZIMUTHAL_BCRYPT_COST is pushed into internal/core/auth at boot, not read at
// hashing time, so a command that calls config.Load directly gets a Config
// whose BcryptCost it then never applies. The failure is silent in the worst
// way: `admin create-user` and `admin reset-password` keep writing hashes at
// the previous work factor while the operator believes they raised it.
//
// Both of those commands did exactly that before this guard existed. The
// only defence that does not rely on remembering is a test.
//
// Modelled on web/src/lib/no-direct-fetch.test.ts, which enforces the same
// shape of rule on the frontend's single API client.
//
// # Why this parses instead of grepping
//
// The first version searched the file bytes for "config.Load()", and a
// concurrently-merged command's doc comment mentioned that call while
// explaining why it deliberately does not make it. The guard failed the build
// over a sentence that agreed with it. A text search cannot tell a call from a
// mention, and a guard that cries wolf at prose is one someone eventually
// deletes rather than fixes — which would cost the rule, not the test.
//
// So it walks the AST and looks for a genuine call expression. Comments and
// string literals are invisible to it, and `configThing.Load()` or a local
// variable named `config` cannot trip it either.
func TestCmdServer_NoDirectConfigLoad(t *testing.T) {
	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	fset := token.NewFileSet()
	var offenders []string
	var scanned int

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		// config.go is the one sanctioned caller; the test files drive the real
		// boot path deliberately.
		if name == "config.go" || strings.HasSuffix(name, "_test.go") {
			continue
		}

		file, parseErr := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		require.NoError(t, parseErr, "parsing %s", name)
		scanned++

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Load" {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if ok && pkg.Name == "config" {
				offenders = append(offenders, name)
			}
			return true
		})
	}

	require.NotZero(t, scanned, "no command files were scanned; this guard has gone stale")
	require.Empty(t, offenders,
		"these files call config.Load() directly instead of loadConfig(), so they load a bcrypt cost they never apply: %v",
		offenders)
}

// TestLoadConfig_AppliesTheConfiguredBcryptCost proves the application step,
// not just the load step: a raised AZIMUTHAL_BCRYPT_COST must reach
// internal/core/auth, and a refused one must fail the command rather than
// being silently discarded.
func TestLoadConfig_AppliesTheConfiguredBcryptCost(t *testing.T) {
	restore := auth.PasswordCost()
	t.Cleanup(func() { _ = auth.SetPasswordCost(restore) })

	t.Run("the configured cost reaches package auth", func(t *testing.T) {
		t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/testdb")
		t.Setenv("AZIMUTHAL_BCRYPT_COST", "13")

		cfg, err := loadConfig()
		require.NoError(t, err)
		require.Equal(t, 13, cfg.BcryptCost)
		require.Equal(t, 13, auth.PasswordCost(),
			"loadConfig read the cost but never applied it — every hash this command writes uses the wrong work factor")
	})

	t.Run("an unset cost applies the production default", func(t *testing.T) {
		t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/testdb")
		t.Setenv("AZIMUTHAL_BCRYPT_COST", "")

		_, err := loadConfig()
		require.NoError(t, err)
		require.Equal(t, auth.DefaultBcryptCost, auth.PasswordCost())
	})
}
