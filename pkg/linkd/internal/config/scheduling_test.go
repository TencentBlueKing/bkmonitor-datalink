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
	"encoding/json"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestPlacementCountsAndSelectors(t *testing.T) {
	for _, tt := range []struct {
		input string
		want  int
		valid bool
	}{{`"all"`, 4, true}, {`0`, 0, true}, {`2`, 2, true}, {`9`, 4, true}, {`-1`, 0, false}, {`"2"`, 0, false}, {`null`, 0, false}, {`1.5`, 0, false}} {
		t.Run(tt.input, func(t *testing.T) {
			var r ReplicaCount
			e := json.Unmarshal([]byte(tt.input), &r)
			if (e == nil) != tt.valid {
				t.Fatalf("decode %v", e)
			}
			if e == nil && r.Limit(4) != tt.want {
				t.Fatal("wrong count")
			}
		})
	}
	p := Placement{Selector: map[string]string{"pool": "a", "zone": "b"}}
	if !p.Matches(map[string]string{"pool": "a", "zone": "b", "extra": "c"}, true) || p.Matches(map[string]string{"pool": "a"}, false) {
		t.Fatal("AND selector violated")
	}
	if (Placement{}).Matches(nil, true) || !(Placement{}).Matches(nil, false) {
		t.Fatal("empty selector violated")
	}
	var s SourceScheduling
	if e := yaml.Unmarshal([]byte("cleaner:\n  replicas: 0\nlifecycle:\n  replicas: all\n"), &s); e != nil || s.Cleaner.Replicas.Limit(5) != 0 || s.Lifecycle.Replicas.Limit(5) != 5 {
		t.Fatal("YAML zero/all lost", e)
	}
}
