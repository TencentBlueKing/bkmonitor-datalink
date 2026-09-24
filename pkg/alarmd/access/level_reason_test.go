// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package access

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// Every reason the access layer names is one it puts on a query's outcome, and
// every query outcome can reach a Level: a binding that is not FULL makes the
// Levels reading it UNAVAILABLE, and their Detect facts carry its reason into
// the trigger. So every one of them has to be a reason a Level may be
// unavailable for.
//
// Asked of the source rather than of the functions that stamp the reasons,
// because a reason reaches a binding through a call site - a literal passed
// to a helper - and a test of the helper is a test of whatever the test
// passes it. EXECUTION_BUDGET_EXHAUSTED was not one, and nothing noticed
// until a replay whose dependency query ran out of time failed its whole Slot
// with TRIGGER_INVARIANT.
func TestEveryReasonTheAccessLayerStampsIsOneALevelMayBeUnavailableFor(t *testing.T) {
	values := contractReasonValues(t)
	named := map[string]bool{}
	for _, directory := range []string{".", "uq"} {
		for _, name := range reasonsNamedIn(t, directory) {
			named[name] = true
		}
	}
	if len(named) == 0 {
		t.Fatal("the scan found no reason at all; it is not reading the access layer")
	}
	names := make([]string, 0, len(named))
	for name := range named {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		value, ok := values[name]
		if !ok {
			t.Errorf("contract.%s is not a string constant of the contract package", name)
			continue
		}
		if !contract.LevelUnavailableReasonV2(value) {
			t.Errorf("the access layer stamps %s, which no Level may be unavailable for: "+
				"a Detect fact carrying it fails the trigger and the Slot with it", value)
		}
	}
	// The reason this test exists for, so a scan that silently stopped seeing
	// it cannot pass on the others.
	if !named["ReasonExecutionBudgetExhausted"] {
		t.Fatal("the scan no longer sees EXECUTION_BUDGET_EXHAUSTED")
	}
}

func reasonsNamedIn(t *testing.T, directory string) []string {
	t.Helper()
	var names []string
	for _, path := range goSources(t, directory) {
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if pkg, ok := selector.X.(*ast.Ident); ok && pkg.Name == "contract" &&
				strings.HasPrefix(selector.Sel.Name, "Reason") {
				names = append(names, selector.Sel.Name)
			}
			return true
		})
	}
	return names
}

// contractReasonValues reads the contract package's string constants, so the
// scan can turn a name it found into the code it stands for.
func contractReasonValues(t *testing.T) map[string]string {
	t.Helper()
	values := map[string]string{}
	for _, path := range goSources(t, filepath.Join("..", "contract")) {
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range file.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok || general.Tok != token.CONST {
				continue
			}
			for _, spec := range general.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok || len(value.Names) != len(value.Values) {
					continue
				}
				for index, name := range value.Names {
					literal, ok := value.Values[index].(*ast.BasicLit)
					if !ok || literal.Kind != token.STRING {
						continue
					}
					if unquoted, err := strconv.Unquote(literal.Value); err == nil {
						values[name.Name] = unquoted
					}
				}
			}
		}
	}
	return values
}

func goSources(t *testing.T, directory string) []string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		paths = append(paths, filepath.Join(directory, name))
	}
	return paths
}
