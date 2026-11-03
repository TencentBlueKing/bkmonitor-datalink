// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// reasonlessLinesAllowed are the lines written today with a result that is
// not a success and no reason code, by file, function and stage, with how
// many such literals each holds: each normalizes to reason_not_reported, the
// word for a site that failed to report, and each is to be given its
// reason. The counts only go down; a new line, or one more at a listed
// place, fails.
var reasonlessLinesAllowed = map[string]int{
	"cmd/alarmd/runtime_phase_two.go|Shutdown|StageShutdown":                                          1,
	"cmd/alarmd/runtime_phase_two.go|observe|stage":                                                   1,
	"cmd/alarmd/runtime_phase_two_bundle.go|observationWindowObserver|StageObservationWindow":         2,
	"cmd/alarmd/runtime_phase_two_bundle.go|openProductionPhaseTwoBundleAssembly|StageLegacyPodCache": 1,
	"cmd/alarmd/runtime_phase_two_bundle.go|openProductionPhaseTwoBundleAssembly|StageStartup":        1,
	"cmd/alarmd/runtime_phase_two_bundle.go|openProductionPhaseTwoBundleAssembly|StageViewSession":    1,
	"cmd/alarmd/runtime_phase_two_bundle.go|publishOutcomeObserver|StageFleetSnapshotPublish":         2,
	"cmd/alarmd/runtime_phase_two_production.go|AssignedQueryGroups|StageAssignmentIndexRead":         2,
	"controlplane/active_qg_set.go|LoadActiveQueryGroupSet|StageActiveQGSet":                          1,
	"controlplane/active_qg_set.go|persistAndVerifyActiveQGSet|StageActiveQGSet":                      2,
	"controlplane/object_catalog_store.go|ensureObjectCatalog|StageObjectCatalog":                     1,
	"controlplane/object_catalog_store.go|renewObjectCatalog|StageObjectCatalog":                      1,
	"controlplane/redis_catalog.go|RenewCurrentActivationObjects|StageActiveQGSet":                    1,
	"controlplane/timeline_prune.go|observeCutover|StageScheduleCutover":                              1,
}

// A line whose result is not a success names its reason. One built without
// a reason normalizes to reason_not_reported, which reads as a site that
// failed to report even where the cause is known - as a target resolution
// with members dropped did. This reads every Observation literal in alarmd,
// with the Result and ReasonCode assigned to it later in the same function
// when it is bound to a name: one with a Result that is not ResultSuccess or
// ResultStarted and no ReasonCode fails, unless it is listed.
func TestEveryLineThatIsNotASuccessNamesItsReason(t *testing.T) {
	root := ".."
	found := map[string]int{}
	where := map[string][]string{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		relative, _ := filepath.Rel(root, path)
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			for _, line := range observationLines(function.Body) {
				if line.result == "" || line.reason || line.result == "ResultSuccess" || line.result == "ResultStarted" {
					continue
				}
				key := filepath.ToSlash(relative) + "|" + function.Name.Name + "|" + line.stage
				found[key]++
				where[key] = append(where[key], fset.Position(line.literal.Pos()).String()+" "+line.result)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(found)+len(reasonlessLinesAllowed))
	for key := range found {
		keys = append(keys, key)
	}
	for key := range reasonlessLinesAllowed {
		if _, seen := found[key]; !seen {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		switch got, allowed := found[key], reasonlessLinesAllowed[key]; {
		case got > allowed:
			t.Errorf("%s: %d reasonless lines, %d listed: %v; each will read as reason_not_reported", key, got, allowed, where[key])
		case got < allowed:
			t.Errorf("%s: %d reasonless lines, %d listed: lower the count", key, got, allowed)
		}
	}
}

type observationLine struct {
	literal *ast.CompositeLit
	result  string
	stage   string
	reason  bool
}

// observationLines are the Observation literals of one function, with what
// later assignments to a name bound to one add to it.
func observationLines(body *ast.BlockStmt) []*observationLine {
	var lines []*observationLine
	bound := map[string]*observationLine{}
	counted := map[*ast.CompositeLit]bool{}
	ast.Inspect(body, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.CompositeLit:
			if isObservationType(typed.Type) && !counted[typed] {
				counted[typed] = true
				lines = append(lines, lineOf(typed))
			}
		case *ast.AssignStmt:
			for index, left := range typed.Lhs {
				if index >= len(typed.Rhs) {
					break
				}
				if name, ok := left.(*ast.Ident); ok {
					if literal, ok := unwrapLiteral(typed.Rhs[index]); ok && isObservationType(literal.Type) && !counted[literal] {
						counted[literal] = true
						line := lineOf(literal)
						lines = append(lines, line)
						bound[name.Name] = line
					}
					continue
				}
				selector, ok := left.(*ast.SelectorExpr)
				if !ok {
					continue
				}
				owner, ok := selector.X.(*ast.Ident)
				if !ok || bound[owner.Name] == nil {
					continue
				}
				switch selector.Sel.Name {
				case "Result":
					bound[owner.Name].result = lastName(typed.Rhs[index])
				case "ReasonCode":
					bound[owner.Name].reason = true
				}
			}
		}
		return true
	})
	return lines
}

func unwrapLiteral(expression ast.Expr) (*ast.CompositeLit, bool) {
	if unary, ok := expression.(*ast.UnaryExpr); ok {
		expression = unary.X
	}
	literal, ok := expression.(*ast.CompositeLit)
	return literal, ok
}

func lineOf(literal *ast.CompositeLit) *observationLine {
	line := &observationLine{literal: literal}
	for _, element := range literal.Elts {
		pair, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, _ := pair.Key.(*ast.Ident)
		if key == nil {
			continue
		}
		switch key.Name {
		case "Result":
			line.result = lastName(pair.Value)
		case "Stage":
			line.stage = lastName(pair.Value)
		case "ReasonCode":
			line.reason = true
		}
	}
	return line
}

func isObservationType(expression ast.Expr) bool {
	switch typed := expression.(type) {
	case *ast.SelectorExpr:
		return typed.Sel.Name == "Observation"
	case *ast.Ident:
		return typed.Name == "Observation"
	}
	return false
}

// lastName is the identifier an expression ends in - Result(x) reads as x -
// or "expr" for anything else.
func lastName(expression ast.Expr) string {
	switch typed := expression.(type) {
	case *ast.SelectorExpr:
		return typed.Sel.Name
	case *ast.Ident:
		return typed.Name
	case *ast.CallExpr:
		if len(typed.Args) == 1 {
			return lastName(typed.Args[0])
		}
	}
	return "expr"
}
