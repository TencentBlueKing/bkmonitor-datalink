// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package aicli

import (
	"encoding/json"
	"testing"
)

func TestSubscriptionMaintenanceFlagIsOptionalStrictBoolean(t *testing.T) {
	op, err := findOperation("event-sources.apply")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		value   any
		present bool
		valid   bool
	}{
		{"omitted", nil, false, true},
		{"false", false, true, true},
		{"true", true, true, true},
		{"string", "true", true, false},
		{"number", json.Number("1"), true, false},
		{"null", nil, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]any{"expected_revision": json.Number("7"), "spec": map[string]any{"event_source_id": "built_in_bk"}}
			if tc.present {
				body["allow_subscription_change"] = tc.value
			}
			err := validateRequest(op, map[string]string{"id": "built_in_bk"}, map[string]string{}, body)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
		})
	}
}
