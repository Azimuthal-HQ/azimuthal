package main

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// cobra prints a command's whole usage block after any error its RunE returns.
// On `azimuthal restore` that buried the line an operator most needs — the one
// saying the database is in an indeterminate state — under forty lines of flag
// documentation, and docs/self-hosting.md now instructs them to read it.
//
// Every RunE therefore sets cmd.SilenceUsage as its first statement. Doing it
// there rather than as a struct field is what preserves usage for a mistyped
// flag (parsed before RunE runs) while dropping it for runtime failures. See
// runRestore for the full rationale.
//
// These two tests are why that stays true: the first makes forgetting it on a
// new command a build failure, the second pins the behaviour through cobra
// itself, in both directions.

// TestCommands_EveryRunESilencesUsage walks the package AST and requires every
// function used as a RunE to open with `cmd.SilenceUsage = true`.
//
// Position matters and is asserted: set anywhere later, an early `return err`
// above it would still print usage. So "first statement" is the property, not a
// style preference.
//
// The AST is walked rather than grepped for the reason recorded on
// TestCmdServer_NoDirectConfigLoad in config_test.go — a text search cannot
// tell a call from a mention, and once failed the build over a doc comment.
// This file's own comments say "cmd.SilenceUsage" repeatedly and must not
// register as compliance.
func TestCommands_EveryRunESilencesUsage(t *testing.T) {
	fset := token.NewFileSet()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading package directory: %v", err)
	}

	// Pass 1: collect every top-level func, and every function name or literal
	// assigned to a RunE field.
	funcs := map[string]*ast.FuncDecl{}
	var runENames []string
	var runELiterals []*ast.FuncLit
	runELocation := map[string]string{}

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
			continue
		}

		file, parseErr := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			t.Fatalf("parsing %s: %v", name, parseErr)
		}

		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil {
				funcs[fn.Name.Name] = fn
			}
		}

		ast.Inspect(file, func(n ast.Node) bool {
			kv, ok := n.(*ast.KeyValueExpr)
			if !ok {
				return true
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok || key.Name != "RunE" {
				return true
			}
			switch v := kv.Value.(type) {
			case *ast.Ident: // RunE: runBackup
				runENames = append(runENames, v.Name)
				runELocation[v.Name] = name
			case *ast.FuncLit: // RunE: func(cmd *cobra.Command, ...) error { ... }
				runELiterals = append(runELiterals, v)
				runELocation[name+" (inline RunE)"] = name
			}
			return true
		})
	}

	// A guard that finds nothing passes; make that impossible.
	if len(runENames)+len(runELiterals) == 0 {
		t.Fatal("found no RunE handlers at all — this guard has stopped guarding anything")
	}

	// Pass 2: each one must open with cmd.SilenceUsage = true.
	check := func(label string, body *ast.BlockStmt) {
		t.Helper()
		if body == nil || len(body.List) == 0 {
			t.Errorf("%s: empty RunE body", label)
			return
		}
		if !isSilenceUsageAssignment(body.List[0]) {
			t.Errorf("%s: first statement is not `cmd.SilenceUsage = true`.\n"+
				"Every RunE in this binary opens with it, so a runtime failure reports its "+
				"error without forty lines of flag documentation on top — see runRestore. "+
				"It must be FIRST: set later, any earlier `return err` still prints usage.",
				label)
		}
	}

	for _, name := range runENames {
		fn, ok := funcs[name]
		if !ok {
			t.Errorf("RunE references %s, which is not a function in this package", name)
			continue
		}
		check(name+" (in "+runELocation[name]+")", fn.Body)
	}
	for _, lit := range runELiterals {
		check("inline RunE literal", lit.Body)
	}
}

// isSilenceUsageAssignment reports whether stmt is exactly `X.SilenceUsage = true`.
func isSilenceUsageAssignment(stmt ast.Stmt) bool {
	assign, ok := stmt.(*ast.AssignStmt)
	if !ok || assign.Tok != token.ASSIGN || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
		return false
	}
	sel, ok := assign.Lhs[0].(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "SilenceUsage" {
		return false
	}
	lit, ok := assign.Rhs[0].(*ast.Ident)
	return ok && lit.Name == "true"
}

// TestCommands_SilenceUsageOnRuntimeFailure pins the behaviour through cobra
// rather than through the source, because the AST guard above proves only that
// a line is present, not that cobra honours it.
//
// It has to go through the root command: TestRestorePostgres_PartialRestoreIsAFailure
// calls restoreDatabase directly, so it never reaches the code that prints usage
// and cannot see this defect at all.
//
// Both directions are asserted, and the second is the one that makes the
// in-RunE placement worth choosing over a SilenceUsage field:
//
//	runtime failure  -> the error, and NO usage block
//	bad flag         -> usage IS still printed
func TestCommands_SilenceUsageOnRuntimeFailure(t *testing.T) {
	// `cmd.SilenceUsage = true` MUTATES the command struct, and cobra commands
	// here are package singletons. In the real binary that is harmless — one
	// invocation, one command, then exit — but in-process it means a subtest
	// that reaches RunE leaves SilenceUsage set for whatever runs next. Found
	// the hard way: the bad-flag case below passed a stale `true` from the
	// runtime-failure case and reported the opposite of the truth. Reset it per
	// case, and restore the shared root's plumbing at the end.
	reset := func(t *testing.T) {
		t.Helper()
		restoreCmd.SilenceUsage = false
	}
	t.Cleanup(func() {
		restoreCmd.SilenceUsage = false
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})

	run := func(t *testing.T, args ...string) (string, error) {
		t.Helper()
		var out bytes.Buffer
		rootCmd.SetOut(&out)
		rootCmd.SetErr(&out)
		rootCmd.SetArgs(args)
		err := rootCmd.Execute()
		return out.String(), err
	}

	t.Run("a runtime failure prints the error without usage", func(t *testing.T) {
		reset(t)
		// loadConfig runs before the archive is opened and needs a DSN to be
		// present. It is never connected to — config.Load only validates — so a
		// syntactically valid value is enough to reach the failure under test.
		t.Setenv("DATABASE_URL", "postgres://unused:unused@127.0.0.1:1/unused?sslmode=disable")

		// A path that cannot exist, so restore fails inside its own body, after
		// flag parsing has succeeded.
		missing := filepath.Join(t.TempDir(), "no-such-backup.tar.gz")

		out, err := run(t, "restore", "--input", missing)

		if err == nil {
			t.Fatal("restoring a nonexistent archive must fail")
		}
		if strings.Contains(out, "Usage:") {
			t.Errorf("a runtime failure printed the usage block. That is what buried the "+
				"real error under flag documentation at the moment an operator is "+
				"recovering from an incident.\n--- output ---\n%s", out)
		}
		if !strings.Contains(out, "no-such-backup.tar.gz") {
			t.Errorf("the error should name what went wrong; got:\n%s", out)
		}
	})

	t.Run("a bad flag still prints usage", func(t *testing.T) {
		reset(t)
		// The reason SilenceUsage is set inside RunE rather than as a field:
		// parsing fails before RunE runs, so usage survives where it helps.
		// With the field form — or SilenceUsage on the root — this fails.
		out, err := run(t, "restore", "--inptu", "typo.tar.gz")

		if err == nil {
			t.Fatal("an unknown flag must fail")
		}
		if !strings.Contains(out, "Usage:") {
			t.Errorf("a mistyped flag should still show usage — that is the whole reason "+
				"SilenceUsage is set inside RunE instead of as a field.\n--- output ---\n%s", out)
		}
	})
}

// Keep the cobra import honest if the assertions above are ever restructured.
var _ = (*cobra.Command)(nil)
