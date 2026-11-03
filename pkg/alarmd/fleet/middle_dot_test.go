// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

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

// No text alarmd writes for a reader -- a board or view line, a conclusion, a
// trend's name, a page's words -- uses "·" as a separator: the product's UI
// rules (2026-10-28) write clauses with "，" and lists with "、". Read from
// the source, every string literal of the packages that write lines and the
// page outside its comments, so a new line written the old way fails here
// whichever function writes it.
func TestNoTextAReaderSeesUsesAMiddleDot(t *testing.T) {
	const dot = "·"
	fset := token.NewFileSet()
	for _, dir := range []string{".", "../obchannel", "../obevidence", "../controlplane"} {
		packages, err := parser.ParseDir(fset, dir, func(info os.FileInfo) bool { return !strings.HasSuffix(info.Name(), "_test.go") }, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, pkg := range packages {
			for name, file := range pkg.Files {
				ast.Inspect(file, func(node ast.Node) bool {
					literal, ok := node.(*ast.BasicLit)
					if !ok || literal.Kind != token.STRING {
						return true
					}
					if text, err := strconv.Unquote(literal.Value); err == nil && strings.Contains(text, dot) {
						t.Errorf("%s:%d writes %q", name, fset.Position(literal.Pos()).Line, text)
					}
					return true
				})
			}
		}
	}
	pages, err := filepath.Glob("ui/*.html")
	if err != nil || len(pages) < 2 {
		t.Fatalf("pages %v (%v), want the page and the strategy view", pages, err)
	}
	for _, name := range pages {
		page, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for number, line := range strings.Split(string(page), "\n") {
			if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "*") || strings.HasPrefix(trimmed, "/*") {
				continue
			}
			if strings.Contains(line, dot) {
				t.Errorf("%s:%d shows %q", name, number+1, strings.TrimSpace(line))
			}
		}
	}
}
