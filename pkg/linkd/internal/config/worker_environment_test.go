// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import (
	"maps"
	"strings"
	"testing"
)

func TestWorkerLabelsEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name      string
		value     string
		exists    bool
		want      map[string]string
		wantError bool
	}{
		{name: "unset preserves YAML", want: map[string]string{"pool": "old"}},
		{name: "replaces YAML", exists: true, value: `{"pool":"alarmd"}`, want: map[string]string{"pool": "alarmd"}},
		{name: "empty object clears YAML", exists: true, value: `{}`, want: map[string]string{}},
		{name: "null rejected", exists: true, value: `null`, wantError: true},
		{name: "empty rejected", exists: true, wantError: true},
		{name: "non-string rejected", exists: true, value: `{"pool":1}`, wantError: true},
		{name: "invalid label rejected", exists: true, value: `{" ":"x"}`, wantError: true},
		{name: "bounded input", exists: true, value: strings.Repeat("x", 16385), wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Default()
			cfg.Worker.Labels = map[string]string{"pool": "old"}
			err := applyWorkerLabels(&cfg, func(key string) (string, bool) {
				if key != "LINKD_WORKER_LABELS" {
					t.Fatalf("unexpected key: %s", key)
				}
				return tc.value, tc.exists
			})
			if (err != nil) != tc.wantError {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.wantError && !maps.Equal(cfg.Worker.Labels, tc.want) {
				t.Fatalf("labels = %v, want %v", cfg.Worker.Labels, tc.want)
			}
		})
	}
}
