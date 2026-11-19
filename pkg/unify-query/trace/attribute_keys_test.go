// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package trace

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// spanAttributeKey returns the literal key a call sets on a span: the first argument of Set on a
// receiver named like a span (span.Set, querySpan.Set, s.span.Set), or of an OpenTelemetry attribute
// constructor (attribute.Int, attribute.String).
func spanAttributeKey(call *ast.CallExpr) (string, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || len(call.Args) == 0 {
		return "", false
	}
	var receiver string
	constructor := false
	switch x := sel.X.(type) {
	case *ast.Ident:
		receiver, constructor = x.Name, x.Name == "attribute"
	case *ast.SelectorExpr:
		// A span held in a field: s.span.Set, r.writeSpan.Set.
		receiver = x.Sel.Name
	default:
		return "", false
	}
	spanSet := sel.Sel.Name == "Set" && strings.HasSuffix(strings.ToLower(receiver), "span")
	if !spanSet && !constructor {
		return "", false
	}
	literal, ok := call.Args[0].(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return "", false
	}
	key, err := strconv.Unquote(literal.Value)
	return key, err == nil
}

// The trace store reads a dot in an attribute key as an object path. A key that is another key followed
// by a dot (query-cost and query-cost.points) makes the shorter key an object in one daily index and a
// value in another, and whichever kind of span comes second is rejected for the whole day.
func TestNoSpanAttributeKeyIsAnotherKeysObjectPath(t *testing.T) {
	root, err := filepath.Abs("..")
	require.NoError(t, err)
	where := make(map[string][]string)
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == "third_party" || rel == "gen" || strings.HasSuffix(rel, "/gen") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if key, ok := spanAttributeKey(call); ok {
					position := fset.Position(call.Pos())
					where[key] = append(where[key], rel+":"+strconv.Itoa(position.Line))
				}
			}
			return true
		})
		return nil
	})
	require.NoError(t, err)

	var conflicts []string
	for key := range where {
		for i := strings.IndexByte(key, '.'); i >= 0; i = nextDot(key, i) {
			if leaf, ok := where[key[:i]]; ok {
				conflicts = append(conflicts, key[:i]+" ("+leaf[0]+") is the object path of "+key+" ("+where[key][0]+")")
			}
		}
	}
	sort.Strings(conflicts)
	require.Empty(t, conflicts)
	// Positive control: the walk saw the keys unify-query sets, through span.Set, through a span held in
	// a field (streamed is set only as s.span.Set) and through the attribute constructors.
	require.GreaterOrEqual(t, len(where), 500)
	require.Contains(t, where, "query-cost")
	require.Contains(t, where, "query-profile.points")
	require.Contains(t, where, "query-profile.result-points")
	require.Contains(t, where, "streamed")
}

func nextDot(key string, after int) int {
	next := strings.IndexByte(key[after+1:], '.')
	if next < 0 {
		return -1
	}
	return after + 1 + next
}
