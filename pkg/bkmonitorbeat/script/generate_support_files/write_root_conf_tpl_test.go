// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package generate_support_files

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResourceLimitSetDoesNotTrimFollowingNewline(t *testing.T) {
	paths := []string{"write_root_conf_tpl.sh"}

	err := filepath.WalkDir("../../support-files/templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && filepath.Base(path) == "bkmonitorbeat.conf.tpl" {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk support-files templates: %v", err)
	}

	for _, path := range paths {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}

		if strings.Contains(string(content), "{%- set resource_limit = resource_limit | default({}) -%}") {
			t.Fatalf("%s trims the newline after resource_limit set, which can render invalid YAML", path)
		}
	}
}
