// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package filterclose

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdmanage"
)

var ipCloud = TargetShape{Dimensions: []string{"bk_target_ip", "bk_target_cloud_id"}}

func address(n int) Target {
	return Target{"bk_target_ip": fmt.Sprintf("192.0.2.%d", n), "bk_target_cloud_id": "0"}
}

// The scope filter is the strategy's scope, an exists for each dimension
// of the target's shape, and a must_not of each current target with all
// its keys together - read back from the body, not from the builder.
func TestTheScopeFilterIsExistsPerKeyAndMustNotPerTarget(t *testing.T) {
	filter, err := ScopeFilter(testScope, ipCloud, []Target{address(1), address(2)})
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Bool struct {
			Filter  []map[string]map[string]any `json:"filter"`
			MustNot []struct {
				Bool struct {
					Filter []map[string]map[string]string `json:"filter"`
				} `json:"bool"`
			} `json:"must_not"`
		} `json:"bool"`
	}
	if err := json.Unmarshal(filter.JSON(), &body); err != nil {
		t.Fatal(err)
	}
	existing := map[string]bool{}
	for _, clause := range body.Bool.Filter {
		if field, ok := clause["exists"]["field"].(string); ok {
			existing[field] = true
		}
	}
	if len(existing) != 2 || !existing["dimensions.bk_target_ip"] || !existing["dimensions.bk_target_cloud_id"] {
		t.Fatalf("exists %v in %s", existing, filter.JSON())
	}
	if len(body.Bool.MustNot) != 2 {
		t.Fatalf("must_not %s", filter.JSON())
	}
	for i, not := range body.Bool.MustNot {
		got := map[string]string{}
		for _, clause := range not.Bool.Filter {
			for field, value := range clause["term"] {
				got[field] = value
			}
		}
		want := map[string]string{"dimensions.bk_target_ip": fmt.Sprintf("192.0.2.%d", i+1), "dimensions.bk_target_cloud_id": "0"}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("must_not %d = %v, want %v", i, got, want)
		}
	}
	if requireScope(filter) != nil {
		t.Fatal("unscoped")
	}
}

// The bound: 2 exists and 249 targets of two keys make 500 leaf clauses
// and fit; 250 do not, and nothing is built - no partial filter. A target
// missing a key of the shape is refused.
func TestTheScopeFilterIsRefusedPastTheBoundAndNeverCut(t *testing.T) {
	targets := make([]Target, 250)
	for i := range targets {
		targets[i] = Target{"bk_target_ip": fmt.Sprintf("192.0.%d.%d", i/250, i%250), "bk_target_cloud_id": "0"}
	}
	if _, err := ScopeFilter(testScope, ipCloud, targets[:249]); err != nil {
		t.Fatalf("249 targets: %v", err)
	}
	filter, err := ScopeFilter(testScope, ipCloud, targets)
	if !errors.Is(err, ErrTargetListTooLarge) || filter.body != nil {
		t.Fatalf("250 targets: %v, %s", err, filter.body)
	}
	if _, err := ScopeFilter(testScope, ipCloud, []Target{{"bk_target_ip": "192.0.2.1"}}); err == nil || errors.Is(err, ErrTargetListTooLarge) {
		t.Fatalf("a target without its cloud area: %v", err)
	}
}

func scopeAlert(id, ip string, labels map[string]string) *fakeAlert {
	dimensions := map[string]string{"device": "sda"}
	if ip != "" {
		dimensions["bk_target_ip"], dimensions["bk_target_cloud_id"] = ip, "0"
	}
	if labels == nil {
		labels = map[string]string{"strategy_id": "41", "strategy_version": "7"}
	}
	return &fakeAlert{id: id, labels: labels, dimensions: dimensions}
}

// The scope close: the alert of a removed target closes; the alerts of a
// current target, an alert without the target's dimensions, and another
// strategy's alert of the removed target stay. Against the record: two
// entries of removed targets (one more of a current target, one without a
// target) allow 22; a link holding 23 alerts outside is refused, closed
// nothing, counted.
func TestTheScopeCloseClosesOnlyAlertsOfRemovedTargets(t *testing.T) {
	h := newHarness(t)
	h.link.add(scopeAlert("removed", "192.0.2.9", nil))
	h.link.add(scopeAlert("current", "192.0.2.1", nil))
	h.link.add(scopeAlert("aggregated", "", nil))
	h.link.add(scopeAlert("other-strategy", "192.0.2.9", map[string]string{"strategy_id": "42"}))
	h.opened(t, "41", "7", map[string]string{"bk_target_ip": "192.0.2.9", "bk_target_cloud_id": "0"}, fingerprints(0, 1)...)
	h.opened(t, "41", "7", map[string]string{"bk_target_ip": "192.0.2.8", "bk_target_cloud_id": "0"}, fingerprints(1, 1)...)
	h.opened(t, "41", "7", map[string]string{"bk_target_ip": "192.0.2.1", "bk_target_cloud_id": "0"}, fingerprints(2, 1)...)
	h.opened(t, "41", "7", nil, fingerprints(3, 1)...)
	closer := h.closer(t)
	current := []Target{address(1)}
	if word := closer.ScopeChange(context.Background(), testScope, ipCloud, current, 7, "d1", testPeriod); word != OutcomeJobDone {
		t.Fatalf("word %s (stats %v)", word, nonZero(closer.Stats()))
	}
	for id, want := range map[string]int{"removed": 1, "current": 0, "aggregated": 0, "other-strategy": 0} {
		if got := h.link.alert(id).closes; got != want {
			t.Fatalf("%s closed %d, want %d", id, got, want)
		}
	}
	if closer.Stats()[OutcomeExpectOwnRecord] != 1 {
		t.Fatalf("stats %v", nonZero(closer.Stats()))
	}
	for i := 0; i < 23; i++ {
		h.link.add(scopeAlert(fmt.Sprintf("late-%02d", i), fmt.Sprintf("192.0.2.%d", 100+i), nil))
	}
	if word := closer.ScopeChange(context.Background(), testScope, ipCloud, current, 7, "d2", testPeriod); word != OutcomeExpectExceeded {
		t.Fatalf("word %s, want expect_exceeded", word)
	}
	if h.link.alert("late-00").closes != 0 {
		t.Fatal("closed past the record")
	}
}

// Without a record each alert the scope filter pages is decided from its
// own dimensions: the removed target's closes by its id, the current
// target's and the dimensionless alert stay.
func TestTheScopeCloseWithoutARecordDecidesEachAlert(t *testing.T) {
	h := newHarness(t)
	h.link.add(scopeAlert("removed", "192.0.2.9", nil))
	h.link.add(scopeAlert("current", "192.0.2.1", nil))
	h.link.add(scopeAlert("aggregated", "", nil))
	closer := h.closer(t)
	if word := closer.ScopeChange(context.Background(), testScope, ipCloud, []Target{address(1)}, 7, "d1", testPeriod); word != OutcomeJobDone {
		t.Fatalf("word %s (stats %v)", word, nonZero(closer.Stats()))
	}
	for id, want := range map[string]int{"removed": 1, "current": 0, "aggregated": 0} {
		if got := h.link.alert(id).closes; got != want {
			t.Fatalf("%s closed %d, want %d", id, got, want)
		}
	}
	if stats := closer.Stats(); stats[OutcomeRecordMissing] != 1 || stats[OutcomeAlertDecidedClose] != 1 {
		t.Fatalf("stats %v", nonZero(stats))
	}
}

// A scope too large for one filter sends nothing and is counted.
func TestATooLargeScopeSendsNothing(t *testing.T) {
	h := newHarness(t)
	h.link.add(scopeAlert("removed", "192.0.2.9", nil))
	closer := h.closer(t)
	targets := make([]Target, 300)
	for i := range targets {
		targets[i] = Target{"bk_target_ip": fmt.Sprintf("198.51.100.%d", i%250), "bk_target_cloud_id": fmt.Sprint(i / 250)}
	}
	if word := closer.ScopeChange(context.Background(), testScope, ipCloud, targets, 7, "d1", testPeriod); word != OutcomeTargetListTooLarge {
		t.Fatalf("word %s", word)
	}
	if h.link.requests[linkdmanage.RouteCloseFilter]+h.link.requests[linkdmanage.RouteSearch] != 0 ||
		closer.Stats()[OutcomeTargetListTooLarge] != 1 {
		t.Fatalf("requests %v, stats %v", h.link.requests, nonZero(closer.Stats()))
	}
}
