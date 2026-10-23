package execution_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// Every production constructor names its hold explicitly. Adding a caller
// without deciding whose frozen hold it uses must fail this check.
func TestEveryFreezeRequestExplicitlyNamesItsReadHold(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Dir(filepath.Dir(file))
	want := []string{"cmd/alarmd/runtime_phase_two_cli_slot.go", "cmd/alarmd/runtime_phase_two_production.go", "scheduler/expired_range.go", "scheduler/slot_source.go", "scheduler/supplement.go"}
	var got []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			literal, ok := node.(*ast.CompositeLit)
			if !ok {
				return true
			}
			selector, ok := literal.Type.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "FreezeSlotContractRequest" {
				return true
			}
			rel, _ := filepath.Rel(root, path)
			got = append(got, filepath.ToSlash(rel))
			for _, element := range literal.Elts {
				pair, ok := element.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := pair.Key.(*ast.Ident)
				if ok && key.Name == "ReadHoldMillis" {
					return true
				}
			}
			t.Errorf("%s freezes a contract without explicitly supplying its read hold", rel)
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("freeze callers = %v; decide and document each new caller's frozen read hold", got)
	}
}
