package execution

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestEveryResultContractRefusalIsInTheClosedVocabulary reads the source of the
// result contract and checks that every code it hands to
// resultContractViolation appears in resultContractCodes.
//
// It parses the implementation rather than listing the codes here on purpose.
// A guard that keeps its own copy of the set it guards agrees with that set on
// the day it is written and never afterwards, and nothing signals the day it
// stops: it then checks that the copy still equals the list it was copied from,
// which is true by construction. Reading the call sites means the only way to
// add a refusal without a listed code is to make this test fail.
func TestEveryResultContractRefusalIsInTheClosedVocabulary(t *testing.T) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, "result_contract.go", nil, 0)
	if err != nil {
		t.Fatalf("parse the result contract: %v", err)
	}

	listed := make(map[string]bool, len(resultContractCodes))
	for _, code := range resultContractCodes {
		if listed[code] {
			t.Errorf("resultContractCodes lists %q twice; two rules sharing a code cannot be told apart", code)
		}
		listed[code] = true
	}

	// The constant names are resolved to their values through the package's own
	// declarations, so a renamed constant does not quietly stop being checked.
	byName := map[string]string{}
	codeFile, err := parser.ParseFile(fileSet, "result_contract_code.go", nil, 0)
	if err != nil {
		t.Fatalf("parse the code vocabulary: %v", err)
	}
	ast.Inspect(codeFile, func(node ast.Node) bool {
		spec, ok := node.(*ast.ValueSpec)
		if !ok || len(spec.Names) != 1 || len(spec.Values) != 1 {
			return true
		}
		literal, ok := spec.Values[0].(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		byName[spec.Names[0].Name] = literal.Value[1 : len(literal.Value)-1]
		return true
	})

	sites := 0
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		name, ok := call.Fun.(*ast.Ident)
		if !ok || name.Name != "resultContractViolation" || len(call.Args) == 0 {
			return true
		}
		sites++
		argument, ok := call.Args[0].(*ast.Ident)
		if !ok {
			t.Errorf("a refusal at %s passes a computed code; the vocabulary has to stay readable from the call site",
				fileSet.Position(call.Pos()))
			return true
		}
		value, known := byName[argument.Name]
		if !known {
			t.Errorf("a refusal at %s uses %s, which is not a code constant",
				fileSet.Position(call.Pos()), argument.Name)
			return true
		}
		if !listed[value] {
			t.Errorf("a refusal at %s uses %q, which resultContractCodes does not list",
				fileSet.Position(call.Pos()), value)
		}
		return true
	})

	// Without this the test passes on a file where every refusal has been
	// renamed away, which is the state it exists to notice.
	if sites == 0 {
		t.Fatal("found no result contract refusals to check; the parser is looking at the wrong thing")
	}
}

// TestResultContractRefusalCarriesItsCodeThroughAWrap checks the route the code
// actually travels. The worker prefers a code the error declares over the one
// its wrap site would supply, and it finds it with errors.As through whatever
// wrapping happened in between; a code that only exists on the concrete type
// would never reach the log line or the page.
func TestResultContractRefusalCarriesItsCodeThroughAWrap(t *testing.T) {
	refusal := resultContractViolation(codeStateFactContradictsOutcome, "State Level fact contradicts its Level outcome")
	wrapped := fmt.Errorf("alarmd worker: invalid series evaluation: %w", refusal)

	var declared interface{ QueryFailure() (string, string) }
	if !errors.As(wrapped, &declared) {
		t.Fatal("a wrapped refusal does not declare a query failure; the code cannot reach the observation")
	}
	category, code := declared.QueryFailure()
	if code != codeStateFactContradictsOutcome {
		t.Fatalf("declared code %q, want %q", code, codeStateFactContradictsOutcome)
	}
	if category != "" {
		t.Fatalf("declared category %q; the category belongs to the stage that wraps the error, not to the rule", category)
	}
	if wrapped.Error() != "alarmd worker: invalid series evaluation: alarmd execution: State Level fact contradicts its Level outcome" {
		t.Fatalf("the message changed: %s", wrapped.Error())
	}
}
