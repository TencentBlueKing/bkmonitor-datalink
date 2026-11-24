// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"encoding/json"
	"strings"
	"testing"
)

// A replica whose link answered that it cannot search degrades the verdict
// by itself: its copy is never refreshed and no close is made at the link.
// A copy that searches, or a replica without a copy, degrades nothing.
func TestAnUnsupportedLinkDegradesTheVerdictOnItsOwn(t *testing.T) {
	for _, arm := range []struct {
		name  string
		facts *ActiveCopyFacts
		want  Health
	}{
		{name: "no copy", facts: nil, want: HealthHealthy},
		{name: "a copy that searches", facts: &ActiveCopyFacts{Configured: true}, want: HealthHealthy},
		{name: "filtered close unsupported", facts: &ActiveCopyFacts{Configured: true, Unsupported: true}, want: HealthDegraded},
	} {
		t.Run(arm.name, func(t *testing.T) {
			snapshots := healthySnapshots()
			snapshots[1].ActiveCopy = arm.facts
			view := Aggregate(Expectation{QueryGroups: 949, Known: true}, snapshots, replicas(), now, freshness)
			if view.Health != arm.want {
				t.Fatalf("health = %s, want %s (degradations %+v)", view.Health, arm.want, view.Degradations)
			}
			if arm.want == HealthDegraded && (len(view.Degradations) != 1 ||
				view.Degradations[0] != (Degradation{Kind: DegradationActiveCopyUnsupported, Replica: "pod-b"})) {
				t.Fatalf("degradations = %+v, want the unsupported link named with its replica", view.Degradations)
			}
		})
	}
}

// The facts reach the page's JSON under their own names.
func TestActiveCopyFactsEncodeUnderTheirNames(t *testing.T) {
	encoded, err := json.Marshal(Snapshot{Replica: "pod-a", ActiveCopy: &ActiveCopyFacts{Configured: true}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"active_copy":{"configured":true,"known":0,"queued":0,"unsupported":false}`) {
		t.Fatalf("encoded %s", encoded)
	}
}
