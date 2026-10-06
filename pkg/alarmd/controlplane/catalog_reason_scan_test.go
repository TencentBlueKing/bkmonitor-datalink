// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// screamingLiteral is the shape every reason in this package is written in.
var screamingLiteral = regexp.MustCompile(`^[A-Z][A-Z0-9_]{3,}$`)

// CountReasonShapedLiterals is what bounds the withheld metric's cardinality.
//
// It is deliberately an over-approximation: it counts every SCREAMING_SNAKE
// string literal in this package's own source, not only the ones that end up
// on a disposition. Dispositions, source semantics and anything else spelled
// that way are counted too, so the number is larger than the set of reasons and
// can only ever be larger.
//
// That direction is the whole point. A metric's cardinality bound has to hold;
// a bound computed from an exact list has to be exactly right, and this package
// attaches reasons in six different shapes - a struct field, a helper call, a
// package constant, a bare return, a converted terminal code, a function that
// picks one from an error - so an exact scan would be a guess dressed as a
// guard. An over-approximation needs to catch none of those shapes correctly to
// be safe, only to miss no literal, which "every literal of this shape" does by
// construction.
//
// The alternative that was tried first was a hand-written list of accepted
// reasons with an "other" bucket for the rest. It held twenty of the forty-odd
// this package writes, which on a deployment with hundreds of rejected objects
// would have filed most of them under a label naming nothing - while a test
// called "every reason this package attaches is listed" checked that the list
// was non-empty and said all was well.
func reasonShapedLiterals(t *testing.T, directory string) map[string]struct{} {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read %s: %v", directory, err)
	}
	found := make(map[string]struct{})
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(directory, name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			text, err := strconv.Unquote(literal.Value)
			if err == nil && screamingLiteral.MatchString(text) {
				found[text] = struct{}{}
			}
			return true
		})
	}
	return found
}

// The bound the metric is registered with must still be at or above what the
// source actually holds. This fails when someone adds reasons past the headroom
// rather than when they add one, which is the point: the number in the metric
// registration is a promise about cardinality, and this is what keeps it true
// without anyone having to remember it.
func TestReasonShapedLiteralsStayWithinTheRegisteredHeadroom(t *testing.T) {
	const headroom = 120
	found := reasonShapedLiterals(t, ".")

	// The scan has to be reading this package, and a count alone cannot say so:
	// a scan that returned a constant would satisfy the headroom for ever. Two
	// reasons written in two different shapes - a struct field and a bare
	// return in the query compiler - and a floor well under the real count are
	// what make a detached scan fail here instead of passing quietly.
	for _, reason := range []string{"NO_DATA_CONFIG_INVALID", "QUERY_SOURCE_NOT_MIGRATED"} {
		if _, seen := found[reason]; !seen {
			t.Fatalf("the scan did not find %s in this package's source; it is not reading it", reason)
		}
	}
	if len(found) < 30 {
		t.Fatalf("the scan found %d reason-shaped literals; this package holds far more, so the scan "+
			"is not reading what it claims to", len(found))
	}

	if got := len(found); got > headroom {
		t.Fatalf("this package holds %d reason-shaped literals, past the %d the withheld metric is "+
			"registered for; raise both together or the metric's cardinality bound is no longer true", got, headroom)
	}
}
