// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// planViewForIsTheOnlyChooser names the one function that may pick which view
// of a Plan the rest of an evaluation sees.
const planViewForIsTheOnlyChooser = "PlanViewFor"

// packagesThatCouldChooseAView are the ones that hold a Plan and evaluate
// something with it. A package added to this list is a package that has to obey
// the rule; one left off is not scanned, which is why the list is short and the
// test fails when a directory in it is missing.
var packagesThatCouldChooseAView = []string{
	"../worker", "../evaluation", "../detect", "../trigger", ".",
}

// Which levels a series is judged against is decided in one place, across every
// package that could decide it.
//
// Sixteen places ask the question - the worker's EffectiveTime binding, the
// evaluator, the detector, the trigger and this package's own validators - and
// all sixteen ask it by calling Levels() on the Plan they were handed. That is
// safe exactly as long as one function decides which Plan that is. A second
// chooser would put a series in front of the wrong levels quietly, because all
// sixteen would keep agreeing with whatever they were given.
//
// The scan reads the packages rather than a list of call sites, because the
// seventeenth site is the one nobody remembers to add to a list.
func TestOnlyOneFunctionChoosesWhichPlanAnEvaluationSees(t *testing.T) {
	checked := 0
	for _, directory := range packagesThatCouldChooseAView {
		entries, err := os.ReadDir(directory)
		if err != nil {
			t.Fatalf("read %s: %v; the scan cannot skip a package it is meant to cover", directory, err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			fileSet := token.NewFileSet()
			file, err := parser.ParseFile(fileSet, filepath.Join(directory, name), nil, 0)
			if err != nil {
				t.Fatalf("parse %s/%s: %v", directory, name, err)
			}
			checked++
			var enclosing string
			ast.Inspect(file, func(node ast.Node) bool {
				switch typed := node.(type) {
				case *ast.FuncDecl:
					enclosing = typed.Name.Name
				case *ast.CallExpr:
					selector, ok := typed.Fun.(*ast.SelectorExpr)
					if !ok || selector.Sel.Name != "NoDataView" || len(typed.Args) != 0 {
						return true
					}
					if enclosing == planViewForIsTheOnlyChooser {
						return true
					}
					t.Errorf("%s/%s:%d: %s chooses a Plan view; that is decided in %s and nowhere else, "+
						"because everything downstream reads the levels off the Plan it was handed",
						directory, name, fileSet.Position(typed.Pos()).Line, enclosing,
						planViewForIsTheOnlyChooser)
				}
				return true
			})
		}
	}
	if checked == 0 {
		t.Fatal("no source was read; the scan would pass vacuously")
	}

	// And the one allowed call is there, so the rule is not being kept by there
	// being nothing to keep.
	source, err := os.ReadFile("plan_view.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), "due.CompiledPlan.NoDataView()") {
		t.Fatalf("%s no longer chooses a view, so this scan is checking nothing", planViewForIsTheOnlyChooser)
	}
}
