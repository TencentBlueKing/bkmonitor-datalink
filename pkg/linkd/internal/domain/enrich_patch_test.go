// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package domain

import (
	"encoding/json"
	"testing"
)

func TestEnrichPatchIsolationAndProtectedFields(t *testing.T) {
	input := map[string]any{"labels": map[string]any{"strategy_id": 351}, "extra_data": map[string]any{"items": []any{map[string]any{"name": "old"}}}}
	first, err := NewEnrichPatch("$.labels.strategy_id", 9001)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewEnrichPatch("$.extra_data.items[0].name", "new")
	if err != nil {
		t.Fatal(err)
	}
	out, err := ApplyEnrichPatches(input, []EnrichPatch{first, second})
	if err != nil {
		t.Fatal(err)
	}
	if out["labels"].(map[string]any)["strategy_id"] != json.Number("9001") || input["labels"].(map[string]any)["strategy_id"] != 351 {
		t.Fatal("mutated source or lost patch")
	}
	for _, path := range []string{"$", "$.labels", "$.bk_tenant_id", "$.enrich.x", "$.dimensions.x", "$.labels[*]", "$.extra_data.items[-1]"} {
		if _, err := NewEnrichPatch(path, "x"); err == nil {
			t.Fatalf("accepted %s", path)
		}
	}
	bad, _ := NewEnrichPatch("$.extra_data.items[3]", "x")
	if _, err := ApplyEnrichPatches(input, []EnrichPatch{first, bad}); err == nil {
		t.Fatal("accepted missing array index")
	}
	if input["labels"].(map[string]any)["strategy_id"] != 351 {
		t.Fatal("partial commit")
	}
}
