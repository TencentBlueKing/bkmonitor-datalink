// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package targetplan_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/targetplan"
)

// The composition rule, pinned where it lives: any Unavailable selector
// makes the plan Unavailable, else any Incomplete makes it Incomplete, else
// Complete; failures are listed by name, dangling nodes collected, the
// largest stale age kept; Contains reads the static set and every
// selector's members whatever the state, and Members is their sorted
// union.
func TestAResolutionComposesFromItsSelectors(t *testing.T) {
	ok := targetplan.SelectorResult{Kind: targetplan.SelectorKindGroup, ID: "g1", State: targetplan.SelectorOK, Reason: targetplan.ReasonNone, Members: map[string]struct{}{"101": {}, "102": {}}, Kept: 2}
	incomplete := targetplan.SelectorResult{Kind: targetplan.SelectorKindGroup, ID: "g2", State: targetplan.SelectorIncomplete, Reason: targetplan.ReasonMembersDropped, Members: map[string]struct{}{"103": {}}, Kept: 1, Dropped: 2, StaleAge: 3 * time.Minute}
	unavailable := targetplan.SelectorResult{Kind: targetplan.SelectorKindTopology, ID: "2|set|12", State: targetplan.SelectorUnavailable, Reason: targetplan.ReasonIndexUnavailable}
	dangling := targetplan.SelectorResult{Kind: targetplan.SelectorKindTopology, ID: "2|set|99", State: targetplan.SelectorOKEmpty, Reason: targetplan.ReasonNodeMissing, NodeMissing: true}
	for name, test := range map[string]struct {
		selectors []targetplan.SelectorResult
		state     targetplan.ResolutionState
		failures  int
	}{
		"all ok":                       {selectors: []targetplan.SelectorResult{ok, dangling}, state: targetplan.ResolutionComplete},
		"one incomplete":               {selectors: []targetplan.SelectorResult{ok, incomplete}, state: targetplan.ResolutionIncomplete, failures: 1},
		"one unavailable":              {selectors: []targetplan.SelectorResult{ok, unavailable}, state: targetplan.ResolutionUnavailable, failures: 1},
		"unavailable beats incomplete": {selectors: []targetplan.SelectorResult{incomplete, unavailable, ok}, state: targetplan.ResolutionUnavailable, failures: 2},
		"no selector":                  {selectors: nil, state: targetplan.ResolutionComplete},
	} {
		t.Run(name, func(t *testing.T) {
			resolution := &targetplan.Resolution{Static: map[string]struct{}{"1": {}}, Selectors: test.selectors}
			resolution.Compose()
			if resolution.State != test.state || len(resolution.Failures) != test.failures {
				t.Fatalf("state %s failures %+v, want %s and %d", resolution.State, resolution.Failures, test.state, test.failures)
			}
			if !resolution.Contains("1") {
				t.Fatal("the static key is not contained")
			}
			for _, selector := range test.selectors {
				for key := range selector.Members {
					if !resolution.Contains(key) {
						t.Fatalf("member %s of %s is not contained under state %s", key, selector.ID, resolution.State)
					}
				}
			}
		})
	}
	resolution := &targetplan.Resolution{Static: map[string]struct{}{"1": {}, "101": {}}, Selectors: []targetplan.SelectorResult{ok, incomplete, unavailable, dangling}}
	resolution.Compose()
	if !reflect.DeepEqual(resolution.Members(), []string{"1", "101", "102", "103"}) {
		t.Fatalf("members = %v", resolution.Members())
	}
	if !reflect.DeepEqual(resolution.NodesMissing, []string{"2|set|99"}) || resolution.StaleAge != 3*time.Minute {
		t.Fatalf("nodes missing %v stale %s", resolution.NodesMissing, resolution.StaleAge)
	}
	if failure := resolution.Failures[0]; failure.Kind != targetplan.SelectorKindGroup || failure.ID != "g2" || failure.Reason != targetplan.ReasonMembersDropped || failure.Dropped != 2 || failure.Kept != 1 {
		t.Fatalf("incomplete failure = %+v", failure)
	}
	if failure := resolution.Failures[1]; failure.ID != "2|set|12" || failure.Reason != targetplan.ReasonIndexUnavailable {
		t.Fatalf("unavailable failure = %+v", failure)
	}
	if (*targetplan.Resolution)(nil).Contains("1") || (*targetplan.Resolution)(nil).Members() != nil {
		t.Fatal("a nil resolution contains something")
	}
}
