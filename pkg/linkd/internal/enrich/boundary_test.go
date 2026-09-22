// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package enrich_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// TestPackageBoundaries 保护实际复用所需的依赖边界，防止新增入口把运行时装配重新拉回核心。
func TestPackageBoundaries(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate source")
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(file)))
	cases := []struct {
		directory string
		forbidden []string
	}{
		{"internal/domain", []string{"linkd/internal/enrich", "linkd/internal/config", "linkd/internal/lifecycle"}},
		{"internal/enrich", []string{"linkd/internal/config", "linkd/internal/lifecycle", "linkd/internal/telemetry", "linkd/internal/store", "linkd/internal/enrich/assembly"}},
		{"internal/enrich/custom", []string{"linkd/internal/config", "linkd/internal/lifecycle", "linkd/internal/telemetry", "linkd/internal/store"}},
		{"internal/enrich/view", []string{"linkd/internal/config", "linkd/internal/lifecycle", "linkd/internal/telemetry", "linkd/internal/store", "linkd/internal/enrich/assembly"}},
		{"internal/enrich/kingeye", []string{"linkd/internal/config", "linkd/internal/lifecycle", "linkd/internal/telemetry", "linkd/internal/store"}},
		{"internal/enrich/preview", []string{"linkd/internal/lifecycle", "linkd/internal/telemetry", "linkd/internal/enrich/assembly", "linkd/internal/enrich/datasources", "linkd/internal/store/assembly", "linkd/internal/store/mysql", "linkd/internal/store/elasticsearch", "linkd/internal/controlplane/process"}},
		{"internal/taskdispatch", []string{"linkd/internal/controlplane/api", "linkd/internal/enrich/preview", "linkd/internal/enrich/assembly", "linkd/internal/dynamicconfig"}},
		{"internal/config", []string{"linkd/internal/telemetry", "linkd/internal/lifecycle/kachook", "linkd/internal/lifecycle/kafkahook", "linkd/internal/enrich/assembly"}},
	}
	for _, tc := range cases {
		t.Run(tc.directory, func(t *testing.T) {
			entries, err := os.ReadDir(filepath.Join(root, tc.directory))
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
					continue
				}
				f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, tc.directory, entry.Name()), nil, parser.ImportsOnly)
				if err != nil {
					t.Fatal(err)
				}
				for _, imp := range f.Imports {
					path, err := strconv.Unquote(imp.Path.Value)
					if err != nil {
						t.Fatal(err)
					}
					for _, prefix := range tc.forbidden {
						if path == prefix || strings.HasPrefix(path, prefix+"/") {
							t.Errorf("%s imports runtime dependency %s", entry.Name(), path)
						}
					}
				}
			}
		})
	}
}
