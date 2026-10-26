// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import "testing"

func TestAlertSearchBudgets(t *testing.T) {
	t.Parallel()
	defaults := (AlertSearchConfig{}).WithDefaults()
	if defaults.MaxConcurrent != 2 || defaults.TimeoutSeconds != 5 || defaults.MaxRequestBytes != 1<<20 || defaults.MaxResponseBytes != 8<<20 {
		t.Fatal(defaults)
	}
	for _, test := range []struct {
		name string
		cfg  AlertSearchConfig
		bad  bool
	}{
		{"defaults", AlertSearchConfig{}, false},
		{"configured", AlertSearchConfig{MaxConcurrent: 4, TimeoutSeconds: 10, MaxRequestBytes: 2048, MaxResponseBytes: 4096}, false},
		{"negative concurrency", AlertSearchConfig{MaxConcurrent: -1}, true},
		{"negative timeout", AlertSearchConfig{TimeoutSeconds: -1}, true},
		{"outer deadline", AlertSearchConfig{TimeoutSeconds: 11}, true},
		{"negative request bytes", AlertSearchConfig{MaxRequestBytes: -1}, true},
		{"negative response bytes", AlertSearchConfig{MaxResponseBytes: -1}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := (DispatchConfig{AlertSearch: test.cfg}).Validate()
			if (err != nil) != test.bad {
				t.Fatal(err)
			}
		})
	}
}
