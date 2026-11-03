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
	return &Store{index: builder.index, now: now, maxAge: 10 * time.Minute}
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
	groups, err := NewGroupStore(reader, GroupStoreOptions{RefreshInterval: time.Minute, MaxAge: 10 * time.Minute, ReadBound: testGroupReadBound, Now: clock})
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
// makes every topology reference unavailable as stale too.
func TestTheResolverNeverReadsUnavailableAsEmpty(t *testing.T) {
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }
	client := &groupClient{values: map[string]string{"cw:dynamic_group:ok": hostGroup}}
	reader, _ := NewGroupReader(client, "cw:")
	groups, _ := NewGroupStore(reader, GroupStoreOptions{RefreshInterval: time.Minute, MaxAge: 10 * time.Minute, ReadBound: testGroupReadBound, Now: clock})
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
	if topology := selector(tooOld, targetplan.SelectorKindTopology, "2|set|12"); topology.State != targetplan.SelectorUnavailable || topology.Reason != targetplan.ReasonStale {
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

// A host index whose latest refresh failed is served as the snapshot before,
// within the bound. Every selector answered from it says so - stale - as a
// group served past a failed refresh does (decision-017 section 3.2,
// resolved_from_stale_snapshot), so the resolution is not a verdict the
// target-scope close may act on (decision-024: facts not current are not
// closed on). With the refresh succeeding, it is current.
func TestSelectorsAnsweredFromAHostIndexPastAFailedRefreshAreStale(t *testing.T) {
	at := time.Unix(1700000000, 0).UTC()
	now := at.Add(2 * time.Minute)
	clock := func() time.Time { return now }
	records := map[int]string{101: addressedHost(101, "tenant-a", "192.0.2.1|0", 2, "")}
	plan := &contract.TargetPlanV1{SchemaVersion: 1, ModelID: "cw-Host", Rule: contract.TargetPlanRuleIPCloud,
		Identity: contract.TargetPlanIdentityV1{Dimensions: []string{"bk_target_ip", "bk_target_cloud_id"}, Address: true}, TenantID: "tenant-a",
		StaticKeys: []string{}, StaticHosts: []string{"101"}}

	hosts := hostStore(t, func() time.Time { return at }, hostFields(records), nil)
	hosts.now = clock
	current := NewTargetResolver(nil, hosts, clock).Resolve(context.Background(), plan, time.Minute)
	if current.StaleAge != 0 || selector(current, targetplan.SelectorKindStatic, "cw-Host").StaleAge != 0 {
		t.Fatalf("a current index answered stale: %+v", current)
	}

	hosts.lastError = errors.New("scan host cache: i/o timeout")
	stale := NewTargetResolver(nil, hosts, clock).Resolve(context.Background(), plan, time.Minute)
	static := selector(stale, targetplan.SelectorKindStatic, "cw-Host")
	if static.State != targetplan.SelectorOK || static.StaleAge != 2*time.Minute || stale.StaleAge != 2*time.Minute {
		t.Fatalf("served past a failed refresh: selector %+v, resolution stale %s; want OK and two minutes stale", static, stale.StaleAge)
	}

	// A group under an ip_cloud plan names hosts by id, and the index turns
	// them into addresses: answered from the same index, so stale the same.
	// The group's own read is current, so the age is the index's alone.
	client := &groupClient{values: map[string]string{"cw:dynamic_group:g": `{"model_id":"cw-Host","bk_tenant_id":"tenant-a",` +
		`"model_inst_ids":["101"],"member_list":[{"model_id":"cw-Host","model_inst_id":"101","bk_host_id":101}]}`}}
	reader, _ := NewGroupReader(client, "cw:")
	groups, _ := NewGroupStore(reader, GroupStoreOptions{RefreshInterval: time.Minute, MaxAge: 10 * time.Minute, ReadBound: testGroupReadBound, Now: clock})
	grouped := *plan
	grouped.StaticHosts, grouped.DynamicGroups = nil, []string{"g"}
	byGroup := NewTargetResolver(groups, hosts, clock).Resolve(context.Background(), &grouped, time.Minute)
	if group := selector(byGroup, targetplan.SelectorKindGroup, "g"); group.State != targetplan.SelectorOK || group.StaleAge != 2*time.Minute {
		t.Fatalf("ip_cloud group served past a failed host refresh: %+v; want OK and two minutes stale", group)
	}
	// A host_id plan's group is answered by the group alone: the index is not
	// read for it, and its age is the group's.
	byID := &contract.TargetPlanV1{SchemaVersion: 1, ModelID: "cw-Host", Rule: contract.TargetPlanRuleHostID,
		Identity: contract.TargetPlanIdentityV1{Dimensions: []string{"bk_host_id"}, HostIdentity: true}, DynamicGroups: []string{"g"}}
	if group := selector(NewTargetResolver(groups, hosts, clock).Resolve(context.Background(), byID, time.Minute), targetplan.SelectorKindGroup, "g"); group.StaleAge != 0 {
		t.Fatalf("host_id group marked by the host index: %+v", group)
	}
}
