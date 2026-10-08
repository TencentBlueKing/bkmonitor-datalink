// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package models

import "testing"

func TestStrategySetConfigExactIdentity(t *testing.T) {
	t.Parallel()
	const id = "d98eb9bc-9c70-4d9a-820d-2c7cb8c51eab"
	for _, tc := range []struct {
		name             string
		configs          []StrategySetConfig
		query            string
		found, wantError bool
	}{
		{"exact UUID", []StrategySetConfig{{ID: id}}, "d98eb9bc9c704d9a820d2c7cb8c51eab", true, false},
		{"no default fallback", []StrategySetConfig{{ID: id}}, "00112233-4455-6677-8899-aabbccddeeff", false, false},
		{"invalid query", []StrategySetConfig{{ID: id}}, "invalid", false, true},
		{"duplicate", []StrategySetConfig{{ID: id}, {ID: id}}, id, false, true},
		{"invalid stored identity", []StrategySetConfig{{ID: "bad"}}, id, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, found, err := (StrategySet{Spec: StrategySetSpec{StrategyConfigs: tc.configs}}).Config(tc.query)
			if found != tc.found || (err != nil) != tc.wantError {
				t.Fatalf("found=%t err=%v", found, err)
			}
		})
	}
}
