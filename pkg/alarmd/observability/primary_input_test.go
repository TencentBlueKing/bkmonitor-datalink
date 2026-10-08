// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import "testing"

// Emptied by the target is a claim about an empty primary: normalization
// keeps it there and drops it anywhere else.
func TestOnlyAnEmptyPrimaryKeepsEmptiedByTheTarget(t *testing.T) {
	for _, state := range []string{"DATA", "EMPTY"} {
		got := normalizePrimaryInputFacts(&PrimaryInputFacts{Completeness: "FULL", DataState: state, EmptiedByTarget: true})
		if got == nil || got.EmptiedByTarget != (state == "EMPTY") {
			t.Fatalf("data state %s: normalized %+v", state, got)
		}
	}
}
