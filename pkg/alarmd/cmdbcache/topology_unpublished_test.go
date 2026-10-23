// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package cmdbcache

// A topology reference to a node the topology cache does not list is a
// dangling configuration only when the caches say the node is gone: the
// topology cache was read as published, and no host sits under the node.
// A host the host cache places under the node proves it exists; a topology
// cache that listed no node at all is a missing hash, not a published empty
// topology (decision-017 section 4, E). Neither is a verdict for the close.

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/targetplan"
)

func TestATopologyNodeIsMissingOnlyWhenTheCachesSayItIsGone(t *testing.T) {
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }
	set12 := contract.TargetPlanTopologyV1{BusinessID: "2", ObjectID: "set", InstanceID: "12"}
	set99 := contract.TargetPlanTopologyV1{BusinessID: "2", ObjectID: "set", InstanceID: "99"}
	plan := func(node contract.TargetPlanTopologyV1) *contract.TargetPlanV1 {
		target := hostPlan(contract.TargetPlanRuleHostID)
		target.DynamicGroups = nil
		target.DynamicTopologies = []contract.TargetPlanTopologyV1{node}
		return target
	}
	for _, c := range []struct {
		name     string
		nodes    []string
		node     contract.TargetPlanTopologyV1
		state    targetplan.SelectorState
		reason   string
		whole    targetplan.ResolutionState
		contains bool
	}{
		// Host 501 is under 2|set|12 in the host cache.
		{"hosts under the node, topology hash missing", nil, set12, targetplan.SelectorOK, targetplan.ReasonNone, targetplan.ResolutionComplete, true},
		{"hosts under the node, topology hash lists other nodes", []string{"module|31"}, set12, targetplan.SelectorOK, targetplan.ReasonNone, targetplan.ResolutionComplete, true},
		{"no host under the node, topology hash missing", nil, set99, targetplan.SelectorIncomplete, targetplan.ReasonIndexIncomplete, targetplan.ResolutionIncomplete, false},
		{"no host under the node, topology hash lists other nodes", []string{"module|31"}, set99, targetplan.SelectorOKEmpty, targetplan.ReasonNodeMissing, targetplan.ResolutionComplete, false},
	} {
		hosts := hostStore(t, clock, []string{"501", hostUnderSet}, c.nodes)
		resolution := NewTargetResolver(nil, hosts, clock).Resolve(context.Background(), plan(c.node), time.Minute)
		got := selector(resolution, targetplan.SelectorKindTopology, c.node.Key())
		if got.State != c.state || got.Reason != c.reason || resolution.State != c.whole || resolution.Contains("501") != c.contains {
			t.Errorf("%s: selector %s %s, resolution %s, contains 501 %v; want %s %s, %s, %v",
				c.name, got.State, got.Reason, resolution.State, resolution.Contains("501"), c.state, c.reason, c.whole, c.contains)
		}
	}
}
