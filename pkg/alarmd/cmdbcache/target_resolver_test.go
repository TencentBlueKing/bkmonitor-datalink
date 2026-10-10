// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package cmdbcache

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/targetplan"
)

const (
	hostUnderSet = `{"bk_host_id":501,"bk_host_innerip":"192.0.2.1","bk_cloud_id":0,"bk_biz_id":2,"model_id":"cw-Host","model_inst_id":"501",
		"topo_link":{"module|31":[{"bk_obj_id":"module","bk_inst_id":31},{"bk_obj_id":"set","bk_inst_id":12},{"bk_obj_id":"biz","bk_inst_id":2}]}}`
	hostUnderSetNoIdentity = `{"bk_host_id":502,"bk_host_innerip":"192.0.2.2","bk_cloud_id":0,"bk_biz_id":2,
		"topo_link":{"module|31":[{"bk_obj_id":"module","bk_inst_id":31},{"bk_obj_id":"set","bk_inst_id":12}]}}`
	hostUnderSetOtherBusiness = `{"bk_host_id":503,"bk_host_innerip":"192.0.2.3","bk_cloud_id":0,"bk_biz_id":3,"model_id":"cw-Host","model_inst_id":"503",
		"topo_link":{"module|31":[{"bk_obj_id":"module","bk_inst_id":31},{"bk_obj_id":"set","bk_inst_id":12}]}}`
)

func hostStore(t *testing.T, now func() time.Time, hosts []string, nodes []string) *Store {
	t.Helper()
	builder := newIndexBuilder(now())
	builder.addFields(hosts)
	builder.addTopologyNodes(nodes)
	return &Store{index: builder.index, now: now, maxAge: 10 * time.Minute, interval: time.Minute}
}

func selector(resolution *targetplan.Resolution, kind, id string) targetplan.SelectorResult {
	for _, candidate := range resolution.Selectors {
		if candidate.Kind == kind && candidate.ID == id {
			return candidate
		}
	}
	return targetplan.SelectorResult{}
}

// The ruling's table, one row per case: a group answers OK, OKEmpty,
// Incomplete or Unavailable by the closed reason, and a topology reference
// answers from the reverse index under its business, with a node the
// topology cache does not list named apart from a node with no host. The
// whole plan composes to Unavailable, else Incomplete, else Complete.
func TestTheResolverAnswersEachSelectorByTheRulingsTable(t *testing.T) {
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }
	client := &groupClient{values: map[string]string{
		"cw:dynamic_group:ok":       hostGroup,
		"cw:dynamic_group:empty":    `{"model_id":"cw-Host","model_inst_ids":[],"member_list":[]}`,
		"cw:dynamic_group:badjson":  `{`,
		"cw:dynamic_group:nomember": `{"model_id":"cw-Host"}`,
		"cw:dynamic_group:mysql":    `{"model_id":"cw-MySQL","model_inst_ids":["db-1"],"member_list":[{"model_id":"cw-MySQL","model_inst_id":"db-1"}]}`,
		"cw:dynamic_group:alldrop":  `{"model_id":"cw-Host","model_inst_ids":["7"],"member_list":[{"model_id":"cw-Host","model_inst_id":"7"}]}`,
	}}
	reader, _ := NewGroupReader(client, "cw:")
	groups, err := NewGroupStore(reader, GroupStoreOptions{RefreshInterval: time.Minute, MaxAge: 10 * time.Minute, Now: clock})
	if err != nil {
		t.Fatal(err)
	}
	hosts := hostStore(t, clock, []string{"501", hostUnderSet, "502", hostUnderSetNoIdentity, "503", hostUnderSetOtherBusiness}, []string{"set|12", "module|31", "set|13"})
	resolver := NewTargetResolver(groups, hosts, clock)

	plan := func(rule contract.TargetPlanRule, groupIDs []string, nodes ...contract.TargetPlanTopologyV1) *contract.TargetPlanV1 {
		target := hostPlan(rule)
		target.StaticKeys = []string{"900"}
		target.DynamicGroups = groupIDs
		target.DynamicTopologies = nodes
		return target
	}
	set12 := contract.TargetPlanTopologyV1{BusinessID: "2", ObjectID: "set", InstanceID: "12"}
	set13 := contract.TargetPlanTopologyV1{BusinessID: "2", ObjectID: "set", InstanceID: "13"}
	set99 := contract.TargetPlanTopologyV1{BusinessID: "2", ObjectID: "set", InstanceID: "99"}

	for name, test := range map[string]struct {
		plan     *contract.TargetPlanV1
		kind, id string
		state    targetplan.SelectorState
		reason   string
		members  []string
		whole    targetplan.ResolutionState
	}{
		"group ok (host rule drops the member without a host id)": {plan: plan(contract.TargetPlanRuleHostID, []string{"ok"}),
			kind: targetplan.SelectorKindGroup, id: "ok", state: targetplan.SelectorIncomplete, reason: targetplan.ReasonMembersDropped, members: []string{"101", "102"}, whole: targetplan.ResolutionIncomplete},
		"group ok under the instance rule": {plan: plan(contract.TargetPlanRuleModelInstID, []string{"ok"}),
			kind: targetplan.SelectorKindGroup, id: "ok", state: targetplan.SelectorIncomplete, reason: targetplan.ReasonMembersDropped, members: []string{"101", "102", "103"}, whole: targetplan.ResolutionIncomplete},
		"group empty":                                   {plan: plan(contract.TargetPlanRuleHostID, []string{"empty"}), kind: targetplan.SelectorKindGroup, id: "empty", state: targetplan.SelectorOKEmpty, reason: targetplan.ReasonNone, whole: targetplan.ResolutionComplete},
		"group key missing":                             {plan: plan(contract.TargetPlanRuleHostID, []string{"absent"}), kind: targetplan.SelectorKindGroup, id: "absent", state: targetplan.SelectorUnavailable, reason: targetplan.ReasonKeyMissing, whole: targetplan.ResolutionUnavailable},
		"group bad json":                                {plan: plan(contract.TargetPlanRuleHostID, []string{"badjson"}), kind: targetplan.SelectorKindGroup, id: "badjson", state: targetplan.SelectorUnavailable, reason: targetplan.ReasonJSONInvalid, whole: targetplan.ResolutionUnavailable},
		"group no member_list":                          {plan: plan(contract.TargetPlanRuleHostID, []string{"nomember"}), kind: targetplan.SelectorKindGroup, id: "nomember", state: targetplan.SelectorUnavailable, reason: targetplan.ReasonStructureInvalid, whole: targetplan.ResolutionUnavailable},
		"group of another model":                        {plan: plan(contract.TargetPlanRuleHostID, []string{"mysql"}), kind: targetplan.SelectorKindGroup, id: "mysql", state: targetplan.SelectorUnavailable, reason: targetplan.ReasonModelMismatch, whole: targetplan.ResolutionUnavailable},
		"group every member dropped":                    {plan: plan(contract.TargetPlanRuleHostID, []string{"alldrop"}), kind: targetplan.SelectorKindGroup, id: "alldrop", state: targetplan.SelectorIncomplete, reason: targetplan.ReasonMembersDropped, whole: targetplan.ResolutionIncomplete},
		"topology under the business":                   {plan: plan(contract.TargetPlanRuleHostID, nil, set12), kind: targetplan.SelectorKindTopology, id: "2|set|12", state: targetplan.SelectorOK, reason: targetplan.ReasonNone, members: []string{"501", "502"}, whole: targetplan.ResolutionComplete},
		"topology under the instance rule":              {plan: plan(contract.TargetPlanRuleModelInstID, nil, set12), kind: targetplan.SelectorKindTopology, id: "2|set|12", state: targetplan.SelectorIncomplete, reason: targetplan.ReasonMembersDropped, members: []string{"501"}, whole: targetplan.ResolutionIncomplete},
		"topology node with no host":                    {plan: plan(contract.TargetPlanRuleHostID, nil, set13), kind: targetplan.SelectorKindTopology, id: "2|set|13", state: targetplan.SelectorOKEmpty, reason: targetplan.ReasonNone, whole: targetplan.ResolutionComplete},
		"topology node the cache does not list":         {plan: plan(contract.TargetPlanRuleHostID, nil, set99), kind: targetplan.SelectorKindTopology, id: "2|set|99", state: targetplan.SelectorOKEmpty, reason: targetplan.ReasonNodeMissing, whole: targetplan.ResolutionComplete},
		"unavailable beside ok composes to unavailable": {plan: plan(contract.TargetPlanRuleHostID, []string{"absent"}, set12), kind: targetplan.SelectorKindTopology, id: "2|set|12", state: targetplan.SelectorOK, reason: targetplan.ReasonNone, members: []string{"501", "502"}, whole: targetplan.ResolutionUnavailable},
	} {
		t.Run(name, func(t *testing.T) {
			resolution := resolver.Resolve(context.Background(), test.plan, time.Minute)
			got := selector(resolution, test.kind, test.id)
			if got.State != test.state || got.Reason != test.reason || !reflect.DeepEqual(sortedKeys(got.Members), nonNil(test.members)) {
				t.Fatalf("selector = state %s reason %s members %v, want %s %s %v", got.State, got.Reason, sortedKeys(got.Members), test.state, test.reason, test.members)
			}
			if resolution.State != test.whole {
				t.Fatalf("composed state = %s, want %s (failures %+v)", resolution.State, test.whole, resolution.Failures)
			}
			if !resolution.Contains("900") {
				t.Fatal("the static key is not a member")
			}
			for _, member := range test.members {
				if !resolution.Contains(member) {
					t.Fatalf("member %s is not contained", member)
				}
			}
			if test.reason == targetplan.ReasonNodeMissing && !reflect.DeepEqual(resolution.NodesMissing, []string{test.id}) {
				t.Fatalf("nodes missing = %v", resolution.NodesMissing)
			}
		})
	}
	// The host of another business under the same node is not a member of
	// the business-2 reference.
	resolution := resolver.Resolve(context.Background(), plan(contract.TargetPlanRuleHostID, nil, set12), time.Minute)
	if resolution.Contains("503") {
		t.Fatal("a host of another business resolved under the reference's business")
	}
	// The topology cache lists nodes without a business. A node belongs to
	// one business, so a reference to set 12 under a business that has no
	// host there while another business does is a reference written against
	// the wrong business: empty, resolved, and named apart from a dangling
	// node and from a node that holds no host anywhere.
	other := resolver.Resolve(context.Background(), plan(contract.TargetPlanRuleHostID, nil, contract.TargetPlanTopologyV1{BusinessID: "9", ObjectID: "set", InstanceID: "12"}), time.Minute)
	if got := selector(other, targetplan.SelectorKindTopology, "9|set|12"); got.State != targetplan.SelectorOKEmpty || !got.NodeForeign || got.NodeMissing || got.Reason != targetplan.ReasonNodeForeign {
		t.Fatalf("known node hosted under another business = %+v", got)
	}
	if !reflect.DeepEqual(other.NodesForeign, []string{"9|set|12"}) || len(other.NodesMissing) != 0 || other.State != targetplan.ResolutionComplete {
		t.Fatalf("foreign node resolution = foreign %v missing %v state %s", other.NodesForeign, other.NodesMissing, other.State)
	}
	// The Slot path reads nothing: every group above was read once, on its
	// first reference, and resolving them all again issues no command.
	reads := len(client.calls)
	for _, id := range []string{"ok", "empty", "badjson", "nomember", "mysql", "alldrop", "absent"} {
		resolver.Resolve(context.Background(), plan(contract.TargetPlanRuleHostID, []string{id}, set12, set13, set99), time.Minute)
	}
	if len(client.calls) != reads {
		t.Fatalf("resolving referenced groups again read Redis %d more times", len(client.calls)-reads)
	}
}

// A selector that could not be resolved is never an empty one: the group
// key missing leaves the plan Unavailable, the members it did not add stay
// out of Contains, and a source that was never wired says so. A snapshot
// served past a failed refresh is answered and marked with its age; past
// the staleness bound it is unavailable as stale; an index that is stale
// makes every topology reference unavailable.
func TestTheResolverNeverReadsUnavailableAsEmpty(t *testing.T) {
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }
	client := &groupClient{values: map[string]string{"cw:dynamic_group:ok": hostGroup}}
	reader, _ := NewGroupReader(client, "cw:")
	groups, _ := NewGroupStore(reader, GroupStoreOptions{RefreshInterval: time.Minute, MaxAge: 10 * time.Minute, Now: clock})
	hosts := hostStore(t, clock, []string{"501", hostUnderSet}, []string{"set|12"})
	resolver := NewTargetResolver(groups, hosts, clock)
	set12 := contract.TargetPlanTopologyV1{BusinessID: "2", ObjectID: "set", InstanceID: "12"}
	target := hostPlan(contract.TargetPlanRuleHostID)
	target.DynamicGroups = []string{"ok"}
	target.DynamicTopologies = []contract.TargetPlanTopologyV1{set12}

	first := resolver.Resolve(context.Background(), target, time.Minute)
	if first.State != targetplan.ResolutionIncomplete || first.StaleAge != 0 {
		t.Fatalf("first resolution = %s stale %s", first.State, first.StaleAge)
	}
	client.err = errors.New("connection refused")
	now = now.Add(2 * time.Minute)
	_ = groups.Refresh(context.Background())
	stale := resolver.Resolve(context.Background(), target, time.Minute)
	if group := selector(stale, targetplan.SelectorKindGroup, "ok"); group.State != targetplan.SelectorIncomplete || group.StaleAge != 2*time.Minute || stale.StaleAge != 2*time.Minute {
		t.Fatalf("after a failed refresh the group answered %s with stale age %s (plan %s)", group.State, group.StaleAge, stale.StaleAge)
	}
	now = now.Add(9 * time.Minute)
	tooOld := resolver.Resolve(context.Background(), target, time.Minute)
	if group := selector(tooOld, targetplan.SelectorKindGroup, "ok"); group.State != targetplan.SelectorUnavailable || group.Reason != targetplan.ReasonStale {
		t.Fatalf("past the staleness bound the group answered %s %s", group.State, group.Reason)
	}
	if topology := selector(tooOld, targetplan.SelectorKindTopology, "2|set|12"); topology.State != targetplan.SelectorUnavailable || topology.Reason != targetplan.ReasonIndexUnavailable {
		t.Fatalf("with a stale host index the topology answered %s %s", topology.State, topology.Reason)
	}
	if tooOld.State != targetplan.ResolutionUnavailable || tooOld.Contains("101") || tooOld.Contains("501") {
		t.Fatalf("an unavailable plan still contained members: %s", tooOld.State)
	}

	unwired := NewTargetResolver(nil, nil, clock).Resolve(context.Background(), target, time.Minute)
	for _, candidate := range unwired.Selectors {
		if candidate.State != targetplan.SelectorUnavailable || candidate.Reason != targetplan.ReasonSourceUnwired {
			t.Fatalf("unwired source answered %+v", candidate)
		}
	}
	if unwired.State != targetplan.ResolutionUnavailable || len(unwired.Failures) != 2 {
		t.Fatalf("unwired resolution = %s failures %+v", unwired.State, unwired.Failures)
	}
}

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
