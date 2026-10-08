// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package cmdbcache

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/targetplan"
)

func TestExclusionsSubtractFromStaticGroupsAndTopologyWithoutChangingSharedCaches(t *testing.T) {
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }
	client := &groupClient{values: map[string]string{"cw:dynamic_group:g": `{"model_id":"cw-Host","model_inst_ids":["501","502"],"member_list":[{"model_id":"cw-Host","model_inst_id":"501","bk_host_id":501},{"model_id":"cw-Host","model_inst_id":"502","bk_host_id":502}]}`}}
	reader, _ := NewGroupReader(client, "cw:")
	groups, _ := NewGroupStore(reader, GroupStoreOptions{RefreshInterval: time.Minute, MaxAge: 10 * time.Minute, ReadBound: testGroupReadBound, Now: clock})
	hosts := hostStore(t, clock, []string{"501", hostUnderSet, "502", hostUnderSetNoIdentity}, []string{"set|12"})
	resolver := NewTargetResolver(groups, hosts, clock)
	plan := hostPlan(contract.TargetPlanRuleHostID)
	plan.StaticKeys, plan.ExcludeKeys = []string{"501"}, []string{"501"}
	plan.DynamicGroups = []string{"g"}
	plan.DynamicTopologies = []contract.TargetPlanTopologyV1{{BusinessID: "2", ObjectID: "set", InstanceID: "12"}}
	got := resolver.Resolve(context.Background(), plan, time.Minute)
	if got.State != targetplan.ResolutionComplete || got.Contains("501") || !reflect.DeepEqual(got.Members(), []string{"502"}) {
		t.Fatalf("resolution %+v members %v", got, got.Members())
	}
	// Another plan still sees every cached member; subtraction never mutates
	// a shared selector snapshot.
	plan.ExcludeKeys = nil
	other := resolver.Resolve(context.Background(), plan, time.Minute)
	if !reflect.DeepEqual(other.Members(), []string{"501", "502"}) {
		t.Fatalf("shared members lost: %v", other.Members())
	}
	plan.ExcludeKeys = []string{"501", "502"}
	empty := resolver.Resolve(context.Background(), plan, time.Minute)
	if empty.State != targetplan.ResolutionComplete || len(empty.Members()) != 0 {
		t.Fatalf("fully excluded target = %+v", empty)
	}
	client.values["cw:dynamic_group:g"] = `{"model_id":"cw-Host","model_inst_ids":["503"],"member_list":[{"model_id":"cw-Host","model_inst_id":"503","bk_host_id":503}]}`
	now = now.Add(2 * time.Minute)
	if err := groups.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	newMember := resolver.Resolve(context.Background(), plan, time.Minute)
	if newMember.State != targetplan.ResolutionComplete || !reflect.DeepEqual(newMember.Members(), []string{"503"}) {
		t.Fatalf("empty target did not admit a later dynamic member: %v", newMember.Members())
	}
	// A failed inclusion selector keeps the known remainder, still excluded,
	// while absence remains unavailable.
	plan.ExcludeKeys, plan.DynamicGroups = []string{"501"}, []string{"missing"}
	partial := resolver.Resolve(context.Background(), plan, time.Minute)
	if partial.State != targetplan.ResolutionUnavailable || partial.Contains("501") || !partial.Contains("502") {
		t.Fatalf("partial target widened or lost known members: %+v", partial)
	}
}

type refreshDuringGroupRead struct {
	groupClient
	refresh func()
}

func (client *refreshDuringGroupRead) Pipelined(ctx context.Context, fn func(redis.Pipeliner) error) ([]redis.Cmder, error) {
	client.refresh()
	return client.groupClient.Pipelined(ctx, fn)
}

func TestOneSlotPinsTheHostSnapshotForInclusionAndExclusion(t *testing.T) {
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }
	hosts := hostStore(t, clock, []string{"501", hostUnderSet}, []string{"set|12"})
	newHost := strings.Replace(hostUnderSet, `"bk_host_id":501`, `"bk_host_id":601`, 1)
	updated := hostStore(t, clock, []string{"601", newHost}, []string{"set|12"})
	client := &refreshDuringGroupRead{groupClient: groupClient{values: map[string]string{"cw:dynamic_group:g": `{"model_id":"cw-Host","model_inst_ids":[],"member_list":[]}`}}, refresh: func() {
		hosts.mutex.Lock()
		hosts.index = updated.index
		hosts.mutex.Unlock()
	}}
	reader, _ := NewGroupReader(client, "cw:")
	groups, _ := NewGroupStore(reader, GroupStoreOptions{RefreshInterval: time.Minute, MaxAge: 10 * time.Minute, ReadBound: testGroupReadBound, Now: clock})
	plan := &contract.TargetPlanV1{SchemaVersion: 1, ModelID: "cw-Host", Rule: contract.TargetPlanRuleModelInstID,
		Identity: contract.TargetPlanIdentityV1{Dimensions: []string{"bk_host_id"}, HostIdentity: true}, StaticKeys: []string{},
		StaticMembers: []contract.TargetPlanMemberV1{{ModelID: "cw-Host", ModelInstID: "501"}}, ExcludeMembers: []contract.TargetPlanMemberV1{{ModelID: "cw-Host", ModelInstID: "501"}}, DynamicGroups: []string{"g"},
		DynamicTopologies: []contract.TargetPlanTopologyV1{{BusinessID: "2", ObjectID: "set", InstanceID: "12"}}}
	got := NewTargetResolver(groups, hosts, clock).Resolve(context.Background(), plan, time.Minute)
	if got.State != targetplan.ResolutionComplete || len(got.Members()) != 0 {
		t.Fatalf("mixed host snapshots widened target: %+v members %v", got, got.Members())
	}
	if _, found := got.Excluded["501"]; !found {
		t.Fatalf("exclusion used the later host snapshot: %v", got.Excluded)
	}
}

func TestAbsentExcludedHostIdentityKeepsACompletePlanUsable(t *testing.T) {
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }
	hosts := hostStore(t, clock, []string{"501", hostUnderSet}, []string{"set|12"})
	resolver := NewTargetResolver(nil, hosts, clock)
	plan := &contract.TargetPlanV1{SchemaVersion: 1, ModelID: "cw-Host", Rule: contract.TargetPlanRuleModelInstID,
		Identity: contract.TargetPlanIdentityV1{Dimensions: []string{"bk_host_id"}, HostIdentity: true}, StaticKeys: []string{},
		StaticMembers:  []contract.TargetPlanMemberV1{{ModelID: "cw-Host", ModelInstID: "501"}},
		ExcludeMembers: []contract.TargetPlanMemberV1{{ModelID: "cw-Host", ModelInstID: "unmapped"}}}
	got := resolver.Resolve(context.Background(), plan, time.Minute)
	if got.State != targetplan.ResolutionComplete || !got.Contains("501") || got.ExclusionUnavailable {
		t.Fatalf("absent exclusion stopped a complete target: %+v", got)
	}
	if absence := selector(got, targetplan.SelectorKindExclude, "cw-Host"); absence.State != targetplan.SelectorOKEmpty || absence.Reason != targetplan.ReasonExcludedAbsent || absence.Dropped != 1 || len(got.Failures) != 0 {
		t.Fatalf("missing absence evidence: %+v", absence)
	}
	plan.ExcludeMembers[0].ModelInstID = "501"
	got = resolver.Resolve(context.Background(), plan, time.Minute)
	if got.State != targetplan.ResolutionComplete || len(got.Members()) != 0 {
		t.Fatalf("resolved exclusion = %+v", got)
	}
	// Missing members do not prevent known exclusions from being applied.
	plan.ExcludeMembers = append(plan.ExcludeMembers, contract.TargetPlanMemberV1{ModelID: "cw-Host", ModelInstID: "unmapped"})
	got = resolver.Resolve(context.Background(), plan, time.Minute)
	if got.State != targetplan.ResolutionComplete || got.ExclusionUnavailable || got.Contains("501") {
		t.Fatalf("an absent exclusion blocked the known one: %+v", got)
	}
	if exclusion := selector(got, targetplan.SelectorKindExclude, "cw-Host"); exclusion.Reason != targetplan.ReasonExcludedAbsent || exclusion.Dropped != 1 || exclusion.Kept != 1 {
		t.Fatalf("partial absence evidence: %+v", exclusion)
	}
}

func TestARecentlyReadOldSourceCannotTurnExcludedGroupMembersIntoAbsentHosts(t *testing.T) {
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }
	for _, test := range []struct {
		name                 string
		source               time.Time
		groupHasExcludedHost bool
		reason               string
	}{
		{"source expired", now.Add(-10*time.Minute - time.Nanosecond), true, targetplan.ReasonStale},
		{"source only one second behind the group", now.Add(-time.Second), true, targetplan.ReasonNone},
		{"source at the age bound", now.Add(-10 * time.Minute), true, targetplan.ReasonNone},
		{"source marker absent and excluded host absent from both caches", time.Time{}, false, targetplan.ReasonExcludedAbsent},
	} {
		t.Run(test.name, func(t *testing.T) {
			// The host hash is an old, otherwise complete enumeration. Reading
			// it again refreshes BuiltAt, while the independent group already
			// names a new host that this source has not published.
			hosts := hostStore(t, clock, []string{"501", hostUnderSet}, nil)
			hosts.index.sourceRefreshedAt = test.source
			groupPayload := `{"model_id":"cw-Host","model_inst_ids":["501"],"member_list":[{"model_id":"cw-Host","model_inst_id":"501","bk_host_id":501}]}`
			groupHost := "501"
			if test.groupHasExcludedHost {
				groupPayload = `{"model_id":"cw-Host","model_inst_ids":["502"],"member_list":[{"model_id":"cw-Host","model_inst_id":"502","bk_host_id":602}]}`
				groupHost = "602"
			}
			client := &groupClient{values: map[string]string{"cw:dynamic_group:g": groupPayload}}
			reader, _ := NewGroupReader(client, "cw:")
			groups, _ := NewGroupStore(reader, GroupStoreOptions{RefreshInterval: time.Minute, MaxAge: 10 * time.Minute, ReadBound: testGroupReadBound, Now: clock})
			plan := &contract.TargetPlanV1{SchemaVersion: 1, ModelID: "cw-Host", Rule: contract.TargetPlanRuleModelInstID,
				Identity: contract.TargetPlanIdentityV1{Dimensions: []string{"bk_host_id"}, HostIdentity: true}, StaticKeys: []string{},
				DynamicGroups: []string{"g"}, ExcludeMembers: []contract.TargetPlanMemberV1{{ModelID: "cw-Host", ModelInstID: "502"}}}
			got := NewTargetResolver(groups, hosts, clock).Resolve(context.Background(), plan, time.Minute)
			group := selector(got, targetplan.SelectorKindGroup, "g")
			if _, held := group.Members[groupHost]; !held || group.State != targetplan.SelectorOK {
				t.Fatalf("the fresh group must include its host: %+v", group)
			}
			exclusion := selector(got, targetplan.SelectorKindExclude, "cw-Host")
			if test.reason == targetplan.ReasonStale {
				if got.State != targetplan.ResolutionUnavailable || !got.ExclusionUnavailable || exclusion.Reason != targetplan.ReasonStale || got.Contains("602") || len(got.Members()) != 0 {
					t.Fatalf("old source admitted an excluded host: state %s exclusion %+v members %v", got.State, exclusion, got.Members())
				}
			} else if got.State != targetplan.ResolutionComplete || got.ExclusionUnavailable || exclusion.Reason != test.reason || got.Contains("602") || (test.groupHasExcludedHost && (exclusion.Kept != 1 || len(got.Members()) != 0)) || (!test.groupHasExcludedHost && (!got.Contains("501") || exclusion.Dropped != 1)) {
				t.Fatalf("the source and group did not jointly subtract the excluded host: %+v exclusion %+v", got, exclusion)
			}
		})
	}
}

func TestExcludedCanonicalIdentityMustAgreeAcrossTheSlotSnapshots(t *testing.T) {
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }
	for _, test := range []struct {
		name, cachedHost, firstGroup, secondGroup, reason string
	}{
		{"a group still knows the removed host", hostUnderSet,
			`{"model_id":"cw-Host","member_list":[{"model_id":"cw-Host","model_inst_id":"502","bk_host_id":602}]}`, "", targetplan.ReasonNone},
		{"host index and group disagree", strings.Replace(hostUnderSet, `"model_inst_id":"501"`, `"model_inst_id":"502"`, 1),
			`{"model_id":"cw-Host","member_list":[{"model_id":"cw-Host","model_inst_id":"502","bk_host_id":602}]}`, "", targetplan.ReasonModelUnresolved},
		{"two groups disagree", hostUnderSet,
			`{"model_id":"cw-Host","member_list":[{"model_id":"cw-Host","model_inst_id":"502","bk_host_id":602}]}`,
			`{"model_id":"cw-Host","member_list":[{"model_id":"cw-Host","model_inst_id":"502","bk_host_id":603}]}`, targetplan.ReasonModelUnresolved},
		{"one group names two hosts for an instance", hostUnderSet,
			`{"model_id":"cw-Host","member_list":[{"model_id":"cw-Host","model_inst_id":"502","bk_host_id":602},{"model_id":"cw-Host","model_inst_id":"502","bk_host_id":603}]}`, "", targetplan.ReasonModelUnresolved},
		{"the removed host is absent from both caches", hostUnderSet,
			`{"model_id":"cw-Host","member_list":[{"model_id":"cw-Host","model_inst_id":"501","bk_host_id":501}]}`, "", targetplan.ReasonExcludedAbsent},
	} {
		t.Run(test.name, func(t *testing.T) {
			hosts := hostStore(t, clock, []string{"501", test.cachedHost}, nil)
			client := &groupClient{values: map[string]string{"cw:dynamic_group:g": test.firstGroup, "cw:dynamic_group:h": test.secondGroup}}
			reader, _ := NewGroupReader(client, "cw:")
			groups, _ := NewGroupStore(reader, GroupStoreOptions{RefreshInterval: time.Minute, MaxAge: 10 * time.Minute, ReadBound: testGroupReadBound, Now: clock})
			plan := &contract.TargetPlanV1{SchemaVersion: 1, ModelID: "cw-Host", Rule: contract.TargetPlanRuleModelInstID,
				Identity: contract.TargetPlanIdentityV1{Dimensions: []string{"bk_host_id"}, HostIdentity: true}, StaticKeys: []string{},
				DynamicGroups: []string{"g"}, ExcludeMembers: []contract.TargetPlanMemberV1{{ModelID: "cw-Host", ModelInstID: "502"}}}
			if test.secondGroup != "" {
				plan.DynamicGroups = append(plan.DynamicGroups, "h")
			}
			got := NewTargetResolver(groups, hosts, clock).Resolve(context.Background(), plan, time.Minute)
			if client.gets() != len(plan.DynamicGroups) {
				t.Fatalf("a group was read again for exclusions: %d reads for %d groups", client.gets(), len(plan.DynamicGroups))
			}
			exclusion := selector(got, targetplan.SelectorKindExclude, "cw-Host")
			if test.reason == targetplan.ReasonModelUnresolved {
				if got.State != targetplan.ResolutionUnavailable || !got.ExclusionUnavailable || exclusion.Reason != test.reason || got.Contains("501") || got.Contains("602") || got.Contains("603") || len(got.Members()) != 0 {
					t.Fatalf("conflicting identities widened the target: %+v exclusion %+v", got, exclusion)
				}
			} else if got.State != targetplan.ResolutionComplete || got.ExclusionUnavailable || exclusion.Reason != test.reason || got.Contains("602") || (test.reason == targetplan.ReasonNone && exclusion.Kept != 1) || (test.reason == targetplan.ReasonExcludedAbsent && (!got.Contains("501") || exclusion.Dropped != 1)) {
				t.Fatalf("the Slot's known identity was not respected: %+v exclusion %+v", got, exclusion)
			}
		})
	}
}

func TestUntrustedGroupFactsCannotProveExcludedMembersAbsent(t *testing.T) {
	for _, test := range []struct {
		name, payload, reason string
		refresh               string
	}{
		{"missing host identity", `{"model_id":"cw-Host","member_list":[{"model_id":"cw-Host","model_inst_id":"502"}]}`, targetplan.ReasonMembersDropped, ""},
		{"unlisted identity", `{"model_id":"cw-Host","model_inst_ids":["501"],"member_list":[{"model_id":"cw-Host","model_inst_id":"502","bk_host_id":602}]}`, targetplan.ReasonMembersDropped, ""},
		{"read failed", `{"model_id":"cw-Host","member_list":[{"model_id":"cw-Host","model_inst_id":"502","bk_host_id":602}]}`, targetplan.ReasonReadFailed, "failed"},
		{"empty refresh held", `{"model_id":"cw-Host","member_list":[{"model_id":"cw-Host","model_inst_id":"502","bk_host_id":602}]}`, targetplan.ReasonEmptiedHeld, "empty"},
		{"snapshot aged", `{"model_id":"cw-Host","member_list":[{"model_id":"cw-Host","model_inst_id":"502","bk_host_id":602}]}`, targetplan.ReasonStale, "aged"},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := time.Unix(1000, 0)
			clock := func() time.Time { return now }
			hosts := hostStore(t, clock, []string{"501", hostUnderSet}, nil)
			client := &groupClient{values: map[string]string{"cw:dynamic_group:g": test.payload}}
			reader, _ := NewGroupReader(client, "cw:")
			groups, _ := NewGroupStore(reader, GroupStoreOptions{RefreshInterval: time.Minute, MaxAge: 10 * time.Minute, ReadBound: testGroupReadBound, Now: clock})
			if test.refresh != "" {
				groups.Group(context.Background(), "g", time.Minute)
				now = now.Add(time.Minute)
				switch test.refresh {
				case "failed":
					client.err = errors.New("read failed")
					if err := groups.Refresh(context.Background()); err == nil {
						t.Fatal("group refresh did not fail")
					}
				case "empty":
					client.values["cw:dynamic_group:g"] = `{"model_id":"cw-Host","member_list":[]}`
					if err := groups.Refresh(context.Background()); err != nil {
						t.Fatal(err)
					}
				case "aged":
					now = now.Add(10 * time.Minute)
				}
				hosts.index.builtAt = now
			}
			plan := &contract.TargetPlanV1{SchemaVersion: 1, ModelID: "cw-Host", Rule: contract.TargetPlanRuleModelInstID,
				Identity: contract.TargetPlanIdentityV1{Dimensions: []string{"bk_host_id"}, HostIdentity: true}, StaticKeys: []string{"501"},
				DynamicGroups: []string{"g"}, ExcludeMembers: []contract.TargetPlanMemberV1{{ModelID: "cw-Host", ModelInstID: "502"}}}
			got := NewTargetResolver(groups, hosts, clock).Resolve(context.Background(), plan, time.Minute)
			exclusion := selector(got, targetplan.SelectorKindExclude, "cw-Host")
			if got.State != targetplan.ResolutionUnavailable || !got.ExclusionUnavailable || exclusion.Reason != test.reason || got.Contains("501") || got.Contains("602") || len(got.Members()) != 0 {
				t.Fatalf("untrusted group facts proved an absence: %+v exclusion %+v", got, exclusion)
			}
		})
	}
}

func TestExcludedHostModelsUseTheSameCanonicalTextAsThePlan(t *testing.T) {
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }
	for _, test := range []struct {
		name, model string
		unavailable bool
	}{
		{"blank model identity is incomplete", " ", true},
		{"padded model identity matches the plan", "cw-Host ", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			payload := strings.Replace(hostUnderSet, `"model_id":"cw-Host"`, `"model_id":"`+test.model+`"`, 1)
			hosts := hostStore(t, clock, []string{"501", payload}, nil)
			client := &groupClient{values: map[string]string{"cw:dynamic_group:g": `{"model_id":"cw-Host","model_inst_ids":["501"],"member_list":[{"model_id":"cw-Host","model_inst_id":"501","bk_host_id":501}]}`}}
			reader, _ := NewGroupReader(client, "cw:")
			groups, _ := NewGroupStore(reader, GroupStoreOptions{RefreshInterval: time.Minute, MaxAge: 10 * time.Minute, ReadBound: testGroupReadBound, Now: clock})
			plan := &contract.TargetPlanV1{SchemaVersion: 1, ModelID: "cw-Host", Rule: contract.TargetPlanRuleModelInstID,
				Identity: contract.TargetPlanIdentityV1{Dimensions: []string{"bk_host_id"}, HostIdentity: true}, StaticKeys: []string{},
				DynamicGroups: []string{"g"}, ExcludeMembers: []contract.TargetPlanMemberV1{{ModelID: "cw-Host", ModelInstID: "501"}}}
			got := NewTargetResolver(groups, hosts, clock).Resolve(context.Background(), plan, time.Minute)
			group := selector(got, targetplan.SelectorKindGroup, "g")
			if _, held := group.Members["501"]; !held || group.State != targetplan.SelectorOK {
				t.Fatalf("the group must include the excluded host: %+v", group)
			}
			exclusion := selector(got, targetplan.SelectorKindExclude, "cw-Host")
			if test.unavailable {
				if got.State != targetplan.ResolutionUnavailable || !got.ExclusionUnavailable || exclusion.Reason != targetplan.ReasonModelUnresolved || got.Contains("501") || len(got.Members()) != 0 {
					t.Fatalf("blank model identity proved a false absence: %+v exclusion %+v", got, exclusion)
				}
			} else if got.State != targetplan.ResolutionComplete || got.ExclusionUnavailable || exclusion.Reason != targetplan.ReasonNone || exclusion.Kept != 1 || got.Contains("501") || len(got.Members()) != 0 {
				t.Fatalf("canonical model identity did not subtract the host: %+v exclusion %+v", got, exclusion)
			}
		})
	}
}

func TestUnreadableOrIncompleteExcludedIdentityBlocksTheWholePlan(t *testing.T) {
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }
	plan := &contract.TargetPlanV1{SchemaVersion: 1, ModelID: "cw-Host", Rule: contract.TargetPlanRuleModelInstID,
		Identity: contract.TargetPlanIdentityV1{Dimensions: []string{"bk_host_id"}, HostIdentity: true}, StaticKeys: []string{},
		StaticMembers:  []contract.TargetPlanMemberV1{{ModelID: "cw-Host", ModelInstID: "501"}},
		ExcludeMembers: []contract.TargetPlanMemberV1{{ModelID: "cw-Host", ModelInstID: "502"}}}
	for _, test := range []struct {
		name, reason string
		mutate       func(*Store)
	}{
		{"never loaded", targetplan.ReasonIndexUnavailable, func(s *Store) { s.index = nil }},
		{"empty has no completeness proof", targetplan.ReasonIndexUnavailable, func(s *Store) { s.index = newIndexBuilder(now).index }},
		{"latest refresh failed", targetplan.ReasonReadFailed, func(s *Store) {
			s.reader = stubLoader{err: errors.New("read failed")}
			_ = s.Refresh(context.Background())
		}},
		{"old snapshot", targetplan.ReasonStale, func(s *Store) { s.index.builtAt = now.Add(-11 * time.Minute) }},
		{"refused host", targetplan.ReasonIndexIncomplete, func(s *Store) { s.index.refused.host("502") }},
		{"host without id", targetplan.ReasonIndexIncomplete, func(s *Store) { s.index.hosts++ }},
		{"invalid host id", targetplan.ReasonIndexIncomplete, func(s *Store) {
			builder := newIndexBuilder(now)
			builder.addFields([]string{"501", hostUnderSet, "502", `{"bk_host_id":-1}`})
			s.index = builder.index
		}},
		{"missing canonical model", targetplan.ReasonModelUnresolved, func(s *Store) { s.index.modelledHosts = 0 }},
		{"ambiguous canonical model", targetplan.ReasonModelUnresolved, func(s *Store) { s.index.byModelInstance = nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			hosts := hostStore(t, clock, []string{"501", hostUnderSet}, []string{"set|12"})
			test.mutate(hosts)
			got := NewTargetResolver(nil, hosts, clock).Resolve(context.Background(), plan, time.Minute)
			failure := selector(got, targetplan.SelectorKindExclude, "cw-Host")
			if got.State != targetplan.ResolutionUnavailable || !got.ExclusionUnavailable || got.Contains("501") || len(got.Members()) != 0 || failure.Reason != test.reason {
				t.Fatalf("untrusted exclusion widened target: %+v selector %+v", got, failure)
			}
		})
	}
	// Direct keys do not depend on the host cache, including native model keys.
	for _, rule := range []contract.TargetPlanRule{contract.TargetPlanRuleHostID, contract.TargetPlanRuleModelInstID} {
		direct := &contract.TargetPlanV1{Rule: rule, StaticKeys: []string{"101", "102"}, ExcludeKeys: []string{"101"}}
		got := NewTargetResolver(nil, nil, clock).Resolve(context.Background(), direct, time.Minute)
		if got.State != targetplan.ResolutionComplete || !reflect.DeepEqual(got.Members(), []string{"102"}) {
			t.Fatalf("direct exclusion gained a cache dependency: %+v", got)
		}
	}
}

func TestIPCloudExclusionsUseTheIncludedSnapshotAndTenantAddressKeys(t *testing.T) {
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }
	topology := `{"module|31":[{"bk_obj_id":"module","bk_inst_id":31}]}`
	oldFields := hostFields(map[int]string{
		101: addressedHost(101, "tenant-a", "192.0.2.1|0", 2, topology),
		102: addressedHost(102, "tenant-a", "192.0.2.2|0", 2, ""),
		103: addressedHost(103, "tenant-a", "192.0.2.3|0", 2, topology),
		104: addressedHost(104, "tenant-b", "192.0.2.1|0", 3, ""),
	})
	hosts := hostStore(t, clock, oldFields, []string{"module|31"})
	updated := hostStore(t, clock, hostFields(map[int]string{
		101: addressedHost(101, "tenant-a", "192.0.2.11|0", 2, topology),
		102: addressedHost(102, "tenant-a", "192.0.2.12|0", 2, ""),
		103: addressedHost(103, "tenant-a", "192.0.2.13|0", 2, topology),
	}), []string{"module|31"})
	client := &refreshDuringGroupRead{groupClient: groupClient{values: map[string]string{"cw:dynamic_group:g": `{"model_id":"cw-Host","bk_tenant_id":"tenant-a","model_inst_ids":["101","102"],"member_list":[{"model_id":"cw-Host","model_inst_id":"101","bk_host_id":101},{"model_id":"cw-Host","model_inst_id":"102","bk_host_id":102}]}`}},
		refresh: func() { hosts.mutex.Lock(); hosts.index = updated.index; hosts.mutex.Unlock() }}
	reader, _ := NewGroupReader(client, "cw:")
	groups, _ := NewGroupStore(reader, GroupStoreOptions{RefreshInterval: time.Minute, MaxAge: 10 * time.Minute, ReadBound: testGroupReadBound, Now: clock})
	plan := &contract.TargetPlanV1{SchemaVersion: 1, ModelID: "cw-Host", Rule: contract.TargetPlanRuleIPCloud,
		Identity: contract.TargetPlanIdentityV1{Dimensions: []string{"bk_target_ip", "bk_target_cloud_id"}, Address: true}, TenantID: "tenant-a",
		StaticKeys: []string{}, StaticHosts: []string{"101"}, ExcludeHosts: []string{"101"}, DynamicGroups: []string{"g"},
		DynamicTopologies: []contract.TargetPlanTopologyV1{{BusinessID: "2", ObjectID: "module", InstanceID: "31"}}}
	got := NewTargetResolver(groups, hosts, clock).Resolve(context.Background(), plan, time.Minute)
	if got.State != targetplan.ResolutionComplete || got.Contains("192.0.2.1|0") || !reflect.DeepEqual(got.Members(), []string{"192.0.2.2|0", "192.0.2.3|0"}) {
		t.Fatalf("address exclusions mixed snapshots or tenants: %+v members %v", got, got.Members())
	}
	if _, found := got.Excluded["192.0.2.1|0"]; !found {
		t.Fatalf("exclusion was not an address from the pinned snapshot: %v", got.Excluded)
	}
}

func TestIPCloudAbsentExclusionsStayUsableAndUnreadableAddressesDoNot(t *testing.T) {
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }
	for _, test := range []struct {
		name, host, reason string
		excluded           []string
		unavailable        bool
	}{
		{"deleted hosts", "", targetplan.ReasonExcludedAbsent, []string{"101", "198", "199"}, false},
		{"other tenant sharing address", addressedHost(104, "tenant-b", "192.0.2.1|0", 3, ""), targetplan.ReasonNone, []string{"101"}, false},
		{"same tenant sharing address", addressedHost(104, "tenant-a", "192.0.2.1|0", 2, ""), targetplan.ReasonAddressAmbiguous, []string{"101"}, true},
		{"present host without address", addressedHost(104, "tenant-a", "", 2, ""), targetplan.ReasonAddressUnresolved, []string{"101", "104"}, true},
		{"present host in another tenant", addressedHost(104, "tenant-b", "192.0.2.4|0", 3, ""), targetplan.ReasonAddressUnresolved, []string{"101", "104"}, true},
		{"refused record is not deleted", `{"bk_host_id":104,`, targetplan.ReasonIndexIncomplete, []string{"101", "104"}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			records := map[int]string{101: addressedHost(101, "tenant-a", "192.0.2.1|0", 2, ""), 102: addressedHost(102, "tenant-a", "192.0.2.2|0", 2, "")}
			if test.host != "" {
				records[104] = test.host
			}
			hosts := hostStore(t, clock, hostFields(records), nil)
			plan := &contract.TargetPlanV1{SchemaVersion: 1, ModelID: "cw-Host", Rule: contract.TargetPlanRuleIPCloud,
				Identity: contract.TargetPlanIdentityV1{Dimensions: []string{"bk_target_ip", "bk_target_cloud_id"}, Address: true}, TenantID: "tenant-a",
				StaticKeys: []string{}, StaticHosts: []string{"101", "102"}, ExcludeHosts: test.excluded}
			got := NewTargetResolver(nil, hosts, clock).Resolve(context.Background(), plan, time.Minute)
			exclusion := selector(got, targetplan.SelectorKindExclude, "cw-Host")
			if got.ExclusionUnavailable != test.unavailable || exclusion.Reason != test.reason {
				t.Fatalf("resolution %+v exclusion %+v", got, exclusion)
			}
			if test.unavailable {
				if got.State != targetplan.ResolutionUnavailable || got.Contains("192.0.2.2|0") || len(got.Members()) != 0 {
					t.Fatalf("failed exclusion admitted a known member: %+v", got)
				}
			} else if got.State != targetplan.ResolutionComplete || !reflect.DeepEqual(got.Members(), []string{"192.0.2.2|0"}) {
				t.Fatalf("usable exclusion did not subtract addresses: %+v members %v", got, got.Members())
			}
			if test.reason == targetplan.ReasonExcludedAbsent && exclusion.Dropped != 2 {
				t.Fatalf("absent members counted as %d, want 2", exclusion.Dropped)
			}
		})
	}
}

// The host enumeration lists hosts, not the instances of another model, so
// its silence about such an instance proves nothing. Only a group of this
// Slot can place one; when none does, the exclusion cannot be applied and
// the plan must not admit the hosts its groups name.
func TestAnExcludedNonHostInstanceNoSnapshotPlacesBlocksThePlan(t *testing.T) {
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }
	for _, test := range []struct {
		name, excluded string
		unavailable    bool
	}{
		{"a group places the instance on its host", "db-1", false},
		{"no snapshot places the instance", "db-9", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			hosts := hostStore(t, clock, []string{"501", hostUnderSet}, nil)
			client := &groupClient{values: map[string]string{"cw:dynamic_group:g": `{"model_id":"cw-MySQL","model_inst_ids":["db-1"],"member_list":[{"model_id":"cw-MySQL","model_inst_id":"db-1","bk_host_id":501}]}`}}
			reader, _ := NewGroupReader(client, "cw:")
			groups, _ := NewGroupStore(reader, GroupStoreOptions{RefreshInterval: time.Minute, MaxAge: 10 * time.Minute, ReadBound: testGroupReadBound, Now: clock})
			plan := &contract.TargetPlanV1{SchemaVersion: 1, ModelID: "cw-MySQL", Rule: contract.TargetPlanRuleModelInstID,
				Identity: contract.TargetPlanIdentityV1{Dimensions: []string{"bk_host_id"}, HostIdentity: true}, StaticKeys: []string{},
				DynamicGroups: []string{"g"}, ExcludeMembers: []contract.TargetPlanMemberV1{{ModelID: "cw-MySQL", ModelInstID: test.excluded}}}
			got := NewTargetResolver(groups, hosts, clock).Resolve(context.Background(), plan, time.Minute)
			if group := selector(got, targetplan.SelectorKindGroup, "g"); group.State != targetplan.SelectorOK {
				t.Fatalf("the group must answer whole: %+v", group)
			}
			exclusion := selector(got, targetplan.SelectorKindExclude, "cw-MySQL")
			if test.unavailable {
				if got.State != targetplan.ResolutionUnavailable || !got.ExclusionUnavailable || exclusion.Reason != targetplan.ReasonModelUnresolved || got.Contains("501") || len(got.Members()) != 0 {
					t.Fatalf("an unplaced non-host exclusion was read as absent: %+v exclusion %+v", got, exclusion)
				}
			} else if got.State != targetplan.ResolutionComplete || got.ExclusionUnavailable || exclusion.Reason != targetplan.ReasonNone || exclusion.Kept != 1 || got.Contains("501") || len(got.Members()) != 0 {
				t.Fatalf("the group-placed exclusion was not subtracted: %+v exclusion %+v", got, exclusion)
			}
		})
	}
}
