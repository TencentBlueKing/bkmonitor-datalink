// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package nodata

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The buckets a no-data Plan can land in are stated twice: once as the outcomes
// EvaluateSlot returns and the worker adds to them, and once as SlotOutcomes,
// which is what the metric family pre-creates its labels from. Neither statement
// mentions the other, so a fourth outcome added to the first one leaves both
// sides' tests green and the metric silently missing a bucket - and a bucket
// missing from a metric reads exactly like a bucket that stayed at zero.
//
// Enumerating the expected outcomes here would be a third statement of the same
// relation and would fail the same way. So this reads the declarations
// themselves: every SlotOutcome constant this package declares, except the zero
// one, has to be in SlotOutcomes. Adding a constant and not the list is the
// thing that fails, which is the mistake being guarded against.
func TestEverySlotOutcomeConstantIsInTheList(t *testing.T) {
	declared := declaredSlotOutcomes(t, ".")
	if len(declared) < 4 {
		t.Fatalf("found %d SlotOutcome constants, want at least the four that exist; the scan is not reading them",
			len(declared))
	}

	listed := make(map[string]struct{}, len(SlotOutcomes))
	for _, outcome := range SlotOutcomes {
		if _, duplicate := listed[string(outcome)]; duplicate {
			t.Fatalf("SlotOutcomes lists %q twice", outcome)
		}
		listed[string(outcome)] = struct{}{}
	}

	for name, value := range declared {
		_, inList := listed[value]
		if value == string(OutcomeNone) {
			if inList {
				t.Fatalf("%s is the zero outcome and must not be in SlotOutcomes: "+
					"a refused or unconfigured Plan is not a round", name)
			}
			continue
		}
		if !inList {
			t.Fatalf("%s = %q is declared but not in SlotOutcomes, so the metric family never creates that label "+
				"and a Plan landing there reads as a bucket that stayed at zero", name, value)
		}
		delete(listed, value)
	}
	for value := range listed {
		t.Fatalf("SlotOutcomes lists %q, which no SlotOutcome constant declares", value)
	}
}

// declaredSlotOutcomes reads every `Name SlotOutcome = "..."` constant out of
// this package's own non-test source, as name to value.
func declaredSlotOutcomes(t *testing.T, directory string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read %s: %v", directory, err)
	}
	found := make(map[string]string)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(directory, name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, declaration := range file.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok || general.Tok != token.CONST {
				continue
			}
			var carried ast.Expr
			for _, item := range general.Specs {
				spec, ok := item.(*ast.ValueSpec)
				if !ok {
					continue
				}
				// A const block states the type once and the rest of the lines
				// inherit it, so an omitted type means the one above it.
				if spec.Type != nil {
					carried = spec.Type
				}
				typeName, ok := carried.(*ast.Ident)
				if !ok || typeName.Name != "SlotOutcome" {
					continue
				}
				for index, identifier := range spec.Names {
					if index >= len(spec.Values) {
						continue
					}
					literal, ok := spec.Values[index].(*ast.BasicLit)
					if !ok || literal.Kind != token.STRING {
						t.Fatalf("%s is a SlotOutcome constant whose value is not a string literal; "+
							"this scan cannot read it and would silently skip it", identifier.Name)
					}
					value, err := strconv.Unquote(literal.Value)
					if err != nil {
						t.Fatalf("unquote %s: %v", identifier.Name, err)
					}
					found[identifier.Name] = value
				}
			}
		}
	}
	return found
}
