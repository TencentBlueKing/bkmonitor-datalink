// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	ants "github.com/panjf2000/ants/v2"
	"github.com/stretchr/testify/require"
)

// recoverExempt are the packages whose recover turns a parse failure of the caller's query text into a
// parse error: counting them would raise the panic alarm on the caller's input.
var recoverExempt = []string{"internal/lucene_parser/", "internal/doris_parser/"}

func selectorIs(expr ast.Expr, pkg, name string) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	return ok && ident.Name == pkg && sel.Sel.Name == name
}

// bodyCalls reports whether body calls match directly, not inside a nested function literal.
func bodyCalls(body *ast.BlockStmt, match func(*ast.CallExpr) bool) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if found {
			return false
		}
		if _, ok := n.(*ast.FuncLit); ok {
			return false
		}
		if call, ok := n.(*ast.CallExpr); ok && match(call) {
			found = true
		}
		return true
	})
	return found
}

// Every ants pool of unify-query sets the counting panic handler, and every function that recovers a
// panic counts it: a recovered panic leaves no other trace, and the result in flight is lost.
func TestEveryRecoveredPanicIsCounted(t *testing.T) {
	root, err := filepath.Abs("..")
	require.NoError(t, err)
	var missing []string
	pools, recovers := 0, 0
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == "third_party" || strings.HasSuffix(rel, "/gen") || rel == "gen" {
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
		exempt := false
		for _, prefix := range recoverExempt {
			exempt = exempt || strings.HasPrefix(rel, prefix)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.CallExpr:
				if selectorIs(node.Fun, "ants", "NewPool") || selectorIs(node.Fun, "ants", "NewPoolWithFunc") {
					pools++
					handled := false
					for _, arg := range node.Args {
						if call, ok := arg.(*ast.CallExpr); ok && selectorIs(call.Fun, "ants", "WithPanicHandler") {
							handled = true
						}
					}
					if !handled {
						missing = append(missing, fset.Position(node.Pos()).String()+": ants pool without WithPanicHandler")
					}
				}
			case *ast.FuncLit:
				recovers += countRecover(node.Body, exempt, fset, &missing)
			case *ast.FuncDecl:
				if node.Body != nil {
					recovers += countRecover(node.Body, exempt, fset, &missing)
				}
			}
			return true
		})
		return nil
	})
	require.NoError(t, err)
	require.Empty(t, missing)
	// Positive control: the walk saw the pools and recovers this package's callers have.
	require.GreaterOrEqual(t, pools, 16)
	require.GreaterOrEqual(t, recovers, 17)
}

func countRecover(body *ast.BlockStmt, exempt bool, fset *token.FileSet, missing *[]string) int {
	recovers := bodyCalls(body, func(call *ast.CallExpr) bool {
		ident, ok := call.Fun.(*ast.Ident)
		return ok && ident.Name == "recover" && len(call.Args) == 0
	})
	if !recovers || exempt {
		return 0
	}
	counted := bodyCalls(body, func(call *ast.CallExpr) bool {
		if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "PanicInc" {
			return true
		}
		// The metric package may be imported under another name.
		sel, ok := call.Fun.(*ast.SelectorExpr)
		return ok && sel.Sel.Name == "PanicInc"
	})
	if !counted {
		*missing = append(*missing, fset.Position(body.Pos()).String()+": recover() without PanicInc")
	}
	return 1
}

// PanicHandler counts a panic of a real ants pool task under its site.
func TestPanicHandlerCountsPoolPanics(t *testing.T) {
	ctx := context.Background()
	before := panicCounterValue(t, PanicSiteQueryWorker)
	storageBefore := panicCounterValue(t, PanicSiteStorage)
	pool, err := ants.NewPool(1, ants.WithPanicHandler(PanicHandler(ctx, PanicSiteQueryWorker)))
	require.NoError(t, err)
	defer pool.Release()
	var wg sync.WaitGroup
	wg.Add(1)
	require.NoError(t, pool.Submit(func() {
		defer wg.Done()
		panic("boom")
	}))
	wg.Wait()
	// ants calls the handler after the task's deferred calls have run.
	require.Eventually(t, func() bool { return panicCounterValue(t, PanicSiteQueryWorker) == before+1 }, 5*time.Second, 10*time.Millisecond)
	require.Equal(t, storageBefore, panicCounterValue(t, PanicSiteStorage))
}

func panicCounterValue(t *testing.T, site string) float64 {
	t.Helper()
	metric, err := panicsTotal.GetMetricWithLabelValues(site)
	require.NoError(t, err)
	return counterValueOf(t, metric)
}
