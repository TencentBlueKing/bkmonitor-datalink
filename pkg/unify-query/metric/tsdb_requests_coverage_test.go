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
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// requestExempt are the functions whose backend request does not serve a query: the version and the
// date histogram syntax probes, which an older ES is expected to refuse, and the scroll cleanup. The index
// API probe sits in a function that also fetches the mapping, which is counted, and is not counted itself.
var requestExempt = map[string]bool{
	"serverVersion":              true,
	"probeDateHistogramInterval": true,
	"ClearScroll":                true,
}

// sendsBackendRequest matches the calls that send one request to a storage backend.
func sendsBackendRequest(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	switch sel.Sel.Name {
	case "PerformRequest", "Raw":
		return true
	case "Request":
		// x.curl.Request(...)
		inner, ok := sel.X.(*ast.SelectorExpr)
		return ok && inner.Sel.Name == "curl"
	case "Do":
		// elastic builders: client.Search()...Do(ctx)
		_, ok := sel.X.(*ast.CallExpr)
		return ok
	}
	return false
}

// Every function in tsdb that sends a backend request counts it in tsdb_requests_total.
func TestEveryStorageRequestIsCounted(t *testing.T) {
	root, err := filepath.Abs("../tsdb")
	require.NoError(t, err)
	var missing []string
	senders := 0
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || requestExempt[fn.Name.Name] {
				continue
			}
			sends, counts := false, false
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if sendsBackendRequest(call) {
					sends = true
				}
				if selectorIs(call.Fun, "metric", "TsDBRequestInc") || selectorIs(call.Fun, "metric", "TsDBRequestOutcomeInc") {
					counts = true
				}
				return true
			})
			if sends {
				senders++
				if !counts {
					missing = append(missing, fset.Position(fn.Pos()).String()+": "+fn.Name.Name+" sends a backend request without counting it")
				}
			}
		}
		return nil
	})
	require.NoError(t, err)
	require.Empty(t, missing)
	// Positive control: VM, bk_sql, five InfluxDB HTTP, gRPC, and the ES senders.
	require.GreaterOrEqual(t, senders, 12)
}
