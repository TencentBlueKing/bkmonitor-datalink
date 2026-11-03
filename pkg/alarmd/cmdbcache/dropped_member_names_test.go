// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package cmdbcache

import (
	"context"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/targetplan"
)

// hostUnderSetNoHostID is a host under set 12 the writer put no host id on.
const hostUnderSetNoHostID = `{"bk_host_innerip":"192.0.2.9","bk_cloud_id":0,"bk_biz_id":2,"model_id":"cw-Host","model_inst_id":"509",
	"topo_link":{"module|31":[{"bk_obj_id":"module","bk_inst_id":31},{"bk_obj_id":"set","bk_inst_id":12},{"bk_obj_id":"biz","bk_inst_id":2}]}}`

// Every path that drops a member names it, with the word of its path, and
// every word named is one of targetplan.DroppedMemberReasons: a group's
// document refusals (member_invalid, member_unlisted, member_lost in id
// order), a group member a host-keyed plan cannot place (no_host_id), a
// topology host with no host id under each rule that keys by host or by
// address, a host without the plan's model (model_mismatch, its empty id
// written "-"), and a static member the host cache does not list
// (host_unindexed).
func TestEveryDropPathNamesItsMembersFromTheClosedList(t *testing.T) {
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }
	client := &groupClient{values: map[string]string{"cw:dynamic_group:g": `{"model_id":"cw-Host","model_inst_ids":["701","703","709","708"],"member_list":[` +
		`{"model_id":"cw-Other","model_inst_id":"x-1","bk_host_id":9},` +
		`{"model_id":"cw-Host","model_inst_id":"702","bk_host_id":702},` +
		`{"model_id":"cw-Host","model_inst_id":"703"},` +
		`{"model_id":"cw-Host","model_inst_id":"701","bk_host_id":701}]}`}}
	reader, _ := NewGroupReader(client, "cw:")
	groups, err := NewGroupStore(reader, GroupStoreOptions{RefreshInterval: time.Minute, MaxAge: 10 * time.Minute, ReadBound: testGroupReadBound, Now: clock})
	if err != nil {
		t.Fatal(err)
	}
	hosts := hostStore(t, clock, []string{"501", hostUnderSet, "502", hostUnderSetNoIdentity, "509", hostUnderSetNoHostID}, []string{"set|12", "module|31"})
	resolver := NewTargetResolver(groups, hosts, clock)
	set12 := contract.TargetPlanTopologyV1{BusinessID: "2", ObjectID: "set", InstanceID: "12"}
	documentDrops := []targetplan.DroppedMember{
		{ID: "x-1", Reason: targetplan.DroppedMemberInvalid},
		{ID: "702", Reason: targetplan.DroppedMemberUnlisted},
		{ID: "708", Reason: targetplan.DroppedMemberLost},
		{ID: "709", Reason: targetplan.DroppedMemberLost},
	}
	byHost := hostPlan(contract.TargetPlanRuleHostID)
	byHost.DynamicGroups, byHost.DynamicTopologies = []string{"g"}, []contract.TargetPlanTopologyV1{set12}
	byInstance := hostPlan(contract.TargetPlanRuleModelInstID)
	byInstance.DynamicGroups, byInstance.DynamicTopologies = []string{"g"}, []contract.TargetPlanTopologyV1{set12}
	byInstanceHost := &contract.TargetPlanV1{SchemaVersion: 1, ModelID: "cw-Host", Rule: contract.TargetPlanRuleModelInstID,
		Identity: contract.TargetPlanIdentityV1{Dimensions: []string{"bk_host_id"}, HostIdentity: true}, StaticKeys: []string{},
		StaticMembers:     []contract.TargetPlanMemberV1{{ModelID: "cw-Host", ModelInstID: "501"}, {ModelID: "cw-Host", ModelInstID: "901"}},
		DynamicTopologies: []contract.TargetPlanTopologyV1{set12}}
	sample, _, _ := ipCloudSample(t)
	byAddress := *sample
	byAddress.StaticHosts, byAddress.DynamicTopologies = nil, []contract.TargetPlanTopologyV1{set12}
	addressed := NewTargetResolver(groups, hostStore(t, clock, append(hostFields(map[int]string{
		504: addressedHost(504, byAddress.TenantID, "192.0.2.44|0", 2, `{"module|31":[{"bk_obj_id":"module","bk_inst_id":31},{"bk_obj_id":"set","bk_inst_id":12}]}`),
	}), "509", hostUnderSetNoHostID), []string{"set|12", "module|31"}), clock)

	for name, test := range map[string]struct {
		resolver *TargetResolver
		plan     *contract.TargetPlanV1
		kind, id string
		want     []targetplan.DroppedMember
		wantKept int
	}{
		"group under host_id": {plan: byHost, kind: targetplan.SelectorKindGroup, id: "g", wantKept: 1,
			want: append(slices.Clone(documentDrops), targetplan.DroppedMember{ID: "703", Reason: targetplan.DroppedNoHostID})},
		"group under model_inst_id": {plan: byInstance, kind: targetplan.SelectorKindGroup, id: "g", wantKept: 2, want: documentDrops},
		"topology under host_id": {plan: byHost, kind: targetplan.SelectorKindTopology, id: set12.Key(), wantKept: 2,
			want: []targetplan.DroppedMember{{ID: "509", Reason: targetplan.DroppedNoHostID}}},
		"topology under model_inst_id": {plan: byInstance, kind: targetplan.SelectorKindTopology, id: set12.Key(), wantKept: 2,
			want: []targetplan.DroppedMember{{ID: "-", Reason: targetplan.DroppedModelMismatch}}},
		"topology under model_inst_id by host": {plan: byInstanceHost, kind: targetplan.SelectorKindTopology, id: set12.Key(), wantKept: 1,
			want: []targetplan.DroppedMember{{ID: "-", Reason: targetplan.DroppedModelMismatch}, {ID: "509", Reason: targetplan.DroppedNoHostID}}},
		"topology under ip_cloud": {resolver: addressed, plan: &byAddress, kind: targetplan.SelectorKindTopology, id: set12.Key(), wantKept: 1,
			want: []targetplan.DroppedMember{{ID: "509", Reason: targetplan.DroppedNoHostID}}},
		"static members by host": {plan: byInstanceHost, kind: targetplan.SelectorKindStatic, id: "cw-Host", wantKept: 1,
			want: []targetplan.DroppedMember{{ID: "901", Reason: targetplan.DroppedHostUnindexed}}},
	} {
		t.Run(name, func(t *testing.T) {
			if test.resolver == nil {
				test.resolver = resolver
			}
			resolution := test.resolver.Resolve(context.Background(), test.plan, time.Minute)
			got := selector(resolution, test.kind, test.id)
			if got.State != targetplan.SelectorIncomplete || got.Reason != targetplan.ReasonMembersDropped || got.Kept != test.wantKept ||
				got.Dropped != len(test.want) || !reflect.DeepEqual(got.DroppedMembers, test.want) {
				t.Fatalf("%s %s = %s %s kept %d dropped %d named %v, want Incomplete members_dropped, kept %d, dropped and named %v",
					test.kind, test.id, got.State, got.Reason, got.Kept, got.Dropped, got.DroppedMembers, test.wantKept, test.want)
			}
			for _, selector := range resolution.Selectors {
				for _, member := range selector.DroppedMembers {
					if !slices.Contains(targetplan.DroppedMemberReasons, member.Reason) {
						t.Fatalf("%s %s named %q, outside the closed list %v", selector.Kind, selector.ID, member.Reason, targetplan.DroppedMemberReasons)
					}
				}
			}
		})
	}
}

// The members a group's summary lists and no member carries are named in
// id order, whatever order the map that found them gives: the names a round
// shows do not move between rounds of the same document.
func TestLostMembersAreNamedInIDOrderEveryRead(t *testing.T) {
	document := []byte(`{"model_id":"cw-Host","model_inst_ids":["l5","l3","l1","l4","l2"],"member_list":[]}`)
	want := []targetplan.DroppedMember{
		{ID: "l1", Reason: targetplan.DroppedMemberLost}, {ID: "l2", Reason: targetplan.DroppedMemberLost},
		{ID: "l3", Reason: targetplan.DroppedMemberLost}, {ID: "l4", Reason: targetplan.DroppedMemberLost},
		{ID: "l5", Reason: targetplan.DroppedMemberLost},
	}
	// One read in map order matches id order by chance often enough; twenty
	// in a row do not.
	for read := 0; read < 20; read++ {
		snapshot := decodeGroup("g", document, time.Unix(1000, 0))
		if snapshot.Dropped != len(want) || !reflect.DeepEqual(snapshot.DroppedNamed, want) {
			t.Fatalf("read %d: dropped %d named %v, want %v", read, snapshot.Dropped, snapshot.DroppedNamed, want)
		}
	}
}
