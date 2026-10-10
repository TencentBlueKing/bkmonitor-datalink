// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package cmdbcache

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/targetplan"
)

// ipCloudSample is the plan and host of the ip_cloud sample the writer and
// this reader share (targetplan/testdata).
func ipCloudSample(t *testing.T) (*contract.TargetPlanV1, string, string) {
	t.Helper()
	raw, err := os.ReadFile("../targetplan/testdata/ip_cloud_target_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var sample struct {
		Tenant    string          `json:"tenant"`
		Plan      json.RawMessage `json:"plan"`
		Host      json.RawMessage `json:"host"`
		MemberKey string          `json:"member_key"`
	}
	if err := json.Unmarshal(raw, &sample); err != nil {
		t.Fatal(err)
	}
	plan, refusal := targetplan.Decode(sample.Plan, targetplan.Options{TenantID: sample.Tenant})
	if refusal != nil {
		t.Fatal(refusal)
	}
	return plan, string(sample.Host), sample.MemberKey
}

// addressedHost is a host record the way the writer writes one: its tenant
// and, when address is not empty, its target address "ip|cloud".
func addressedHost(id int, tenant, address string, business int, topology string) string {
	record := map[string]any{"bk_host_id": id, "bk_host_innerip": fmt.Sprintf("192.0.2.%d", id%250), "bk_cloud_id": 0,
		"bk_biz_id": business, "model_id": "cw-Host", "model_inst_id": fmt.Sprint(id), "bk_tenant_id": tenant}
	if address != "" {
		ip, cloud, _ := strings.Cut(address, "|")
		record["target_address"] = map[string]any{"bk_target_ip": ip, "bk_target_cloud_id": json.Number(cloud)}
	}
	if topology != "" {
		record["topo_link"] = json.RawMessage(topology)
	}
	encoded, _ := json.Marshal(record)
	return string(encoded)
}

// hostFields writes each host twice, under its id and its address, as the
// writer does.
func hostFields(hosts map[int]string) []string {
	ids := make([]int, 0, len(hosts))
	for id := range hosts {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	fields := make([]string, 0, 4*len(ids))
	for _, id := range ids {
		fields = append(fields, fmt.Sprint(id), hosts[id], fmt.Sprintf("192.0.2.%d|0", id%250), hosts[id])
	}
	return fields
}

func memberKeys(members map[string]struct{}) []string {
	keys := make([]string, 0, len(members))
	for key := range members {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// An ip_cloud plan's hosts are read at the addresses the host cache has for
// them inside the plan's tenant: the shared sample's host at the sample's
// member key. Another tenant's host is not the plan's, even at the same
// address; a host with no target address cannot be placed; an address two
// hosts of the tenant share names neither. A selector none of whose hosts
// can be placed is unavailable by name, never an empty target.
func TestAnIPCloudPlanReadsItsHostsAtTheirAddressesInsideItsTenant(t *testing.T) {
	plan, sampleHost, memberKey := ipCloudSample(t)
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }
	hosts := map[int]string{
		501: sampleHost,
		502: addressedHost(502, "tenant-a", "192.0.2.2|0", 2, ""),
		503: addressedHost(503, "tenant-b", "192.0.2.1|0", 3, ""),
		504: addressedHost(504, "tenant-a", "", 2, ""),
		505: addressedHost(505, "tenant-a", "192.0.2.55|1", 2, ""),
		506: addressedHost(506, "tenant-a", "192.0.2.55|1", 2, ""),
		508: addressedHost(508, "tenant-a", "192.0.2.080|0", 2, ""),
	}
	resolver := NewTargetResolver(nil, hostStore(t, clock, hostFields(hosts), nil), clock)
	for _, test := range []struct {
		name    string
		hosts   []string
		state   targetplan.SelectorState
		reason  string
		members []string
	}{
		{name: "the shared sample", hosts: []string{"501"}, state: targetplan.SelectorOK, reason: targetplan.ReasonNone, members: []string{memberKey}},
		{name: "another tenant's host", hosts: []string{"501", "503"}, state: targetplan.SelectorIncomplete,
			reason: targetplan.ReasonMembersDropped, members: []string{memberKey}},
		{name: "no target address", hosts: []string{"504"}, state: targetplan.SelectorUnavailable, reason: targetplan.ReasonAddressUnresolved},
		{name: "a target address that does not read", hosts: []string{"508"}, state: targetplan.SelectorUnavailable,
			reason: targetplan.ReasonAddressUnresolved},
		{name: "a shared address alone", hosts: []string{"505"}, state: targetplan.SelectorUnavailable, reason: targetplan.ReasonAddressAmbiguous},
		{name: "a shared address beside another", hosts: []string{"502", "505"}, state: targetplan.SelectorIncomplete,
			reason: targetplan.ReasonAddressAmbiguous, members: []string{"192.0.2.2|0"}},
	} {
		scoped := *plan
		scoped.StaticHosts = test.hosts
		resolution := resolver.Resolve(context.Background(), &scoped, time.Minute)
		got := selector(resolution, targetplan.SelectorKindStatic, "cw-Host")
		if got.State != test.state || got.Reason != test.reason || !reflect.DeepEqual(memberKeys(got.Members), append([]string{}, test.members...)) {
			t.Fatalf("%s: %s/%s with %v, want %s/%s with %v", test.name, got.State, got.Reason, memberKeys(got.Members), test.state, test.reason, test.members)
		}
		// The plan's failures name the selector's reason, not a generic
		// drop: an ambiguous address is read as one on the page.
		if test.state != targetplan.SelectorOK && (len(resolution.Failures) != 1 || resolution.Failures[0].Reason != test.reason) {
			t.Fatalf("%s: failures %+v, want one %s", test.name, resolution.Failures, test.reason)
		}
	}
	if resolution := resolver.Resolve(context.Background(), plan, time.Minute); !resolution.Contains(memberKey) || resolution.State != targetplan.ResolutionComplete {
		t.Fatalf("the sample plan resolves %+v", resolution)
	}

	// The host is readdressed: the next resolution reads it at its new
	// address, with no change to the plan.
	hosts[501] = addressedHost(501, "tenant-a", "192.0.2.201|0", 2, "")
	resolver = NewTargetResolver(nil, hostStore(t, clock, hostFields(hosts), nil), clock)
	if resolution := resolver.Resolve(context.Background(), plan, time.Minute); resolution.Contains(memberKey) || !resolution.Contains("192.0.2.201|0") {
		t.Fatalf("a readdressed host resolves %v", resolution.Members())
	}

	// A host index older than its bound places nobody.
	now = now.Add(11 * time.Minute)
	if got := selector(resolver.Resolve(context.Background(), plan, time.Minute), targetplan.SelectorKindStatic, "cw-Host"); got.State != targetplan.SelectorUnavailable ||
		got.Reason != targetplan.ReasonIndexUnavailable {
		t.Fatalf("a stale index resolves %s/%s", got.State, got.Reason)
	}
}

// A dynamic group of an ip_cloud plan is read only when it is the plan's
// tenant's, and its members, hosts by id, are read at their addresses: a
// member that leaves the group leaves the target on the refresh after, and
// a topology node's hosts are read the same way.
func TestAnIPCloudPlansGroupsAndNodesAreReadAtTheirHostsAddresses(t *testing.T) {
	plan, sampleHost, memberKey := ipCloudSample(t)
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }
	underModule := `{"module|31":[{"bk_obj_id":"module","bk_inst_id":31},{"bk_obj_id":"set","bk_inst_id":12},{"bk_obj_id":"biz","bk_inst_id":2}]}`
	hosts := hostStore(t, clock, hostFields(map[int]string{
		501: sampleHost,
		502: addressedHost(502, "tenant-a", "192.0.2.2|0", 2, ""),
		507: addressedHost(507, "tenant-a", "192.0.2.7|0", 2, underModule),
	}), []string{"module|31", "{}"})
	group := func(tenant string, hosts ...int) string {
		members, ids := []map[string]any{}, []string{}
		for _, host := range hosts {
			members = append(members, map[string]any{"model_id": "cw-Host", "model_inst_id": fmt.Sprint(host), "bk_host_id": host})
			ids = append(ids, fmt.Sprint(host))
		}
		document := map[string]any{"model_id": "cw-Host", "model_inst_ids": ids, "member_list": members}
		if tenant != "" {
			document["bk_tenant_id"] = tenant
		}
		encoded, _ := json.Marshal(document)
		return string(encoded)
	}
	client := &groupClient{values: map[string]string{
		"cw:dynamic_group:1":     group("tenant-a", 501, 502),
		"cw:dynamic_group:other": group("tenant-b", 501),
		"cw:dynamic_group:none":  group("", 501),
	}}
	reader, _ := NewGroupReader(client, "cw:")
	groups, err := NewGroupStore(reader, GroupStoreOptions{RefreshInterval: time.Minute, MaxAge: 10 * time.Minute, ReadBound: testGroupReadBound, Now: clock})
	if err != nil {
		t.Fatal(err)
	}
	resolver := NewTargetResolver(groups, hosts, clock)
	scoped := *plan
	scoped.StaticHosts = nil
	scoped.DynamicGroups = []string{"1", "none", "other"}
	scoped.DynamicTopologies = []contract.TargetPlanTopologyV1{{BusinessID: "2", ObjectID: "module", InstanceID: "31"}}
	resolution := resolver.Resolve(context.Background(), &scoped, time.Minute)
	if got := selector(resolution, targetplan.SelectorKindGroup, "1"); got.State != targetplan.SelectorOK ||
		!reflect.DeepEqual(memberKeys(got.Members), []string{memberKey, "192.0.2.2|0"}) {
		t.Fatalf("the tenant's group resolves %s with %v", got.State, memberKeys(got.Members))
	}
	for _, id := range []string{"other", "none"} {
		if got := selector(resolution, targetplan.SelectorKindGroup, id); got.State != targetplan.SelectorUnavailable ||
			got.Reason != targetplan.ReasonGroupTenantMismatch {
			t.Fatalf("group %s resolves %s/%s, want tenant_mismatch", id, got.State, got.Reason)
		}
	}
	if got := selector(resolution, targetplan.SelectorKindTopology, "2|module|31"); got.State != targetplan.SelectorOK ||
		!reflect.DeepEqual(memberKeys(got.Members), []string{"192.0.2.7|0"}) {
		t.Fatalf("the node resolves %s with %v", got.State, memberKeys(got.Members))
	}

	// 502 leaves the group: the refresh after reads it out of the target.
	client.values["cw:dynamic_group:1"] = group("tenant-a", 501)
	now = now.Add(time.Minute)
	if err := groups.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	scoped.DynamicGroups = []string{"1"}
	if got := selector(resolver.Resolve(context.Background(), &scoped, time.Minute), targetplan.SelectorKindGroup, "1"); !reflect.DeepEqual(memberKeys(got.Members), []string{memberKey}) {
		t.Fatalf("after the member left: %v", memberKeys(got.Members))
	}
}

// The business of a record at an ip_cloud address is its one host's inside
// the tenant: another tenant's host at the same address is not it, and an
// address two hosts share names no business.
func TestAnAddressesBusinessIsItsOneHostsInsideTheTenant(t *testing.T) {
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }
	store := hostStore(t, clock, hostFields(map[int]string{
		501: addressedHost(501, "tenant-a", "192.0.2.1|0", 2, ""),
		503: addressedHost(503, "tenant-b", "192.0.2.1|0", 3, ""),
		505: addressedHost(505, "tenant-a", "192.0.2.55|1", 4, ""),
		506: addressedHost(506, "tenant-a", "192.0.2.55|1", 5, ""),
	}), nil)
	lookup := NewHostBusinessLookup(store)
	for _, test := range []struct {
		tenant, address, business string
		held                      bool
	}{
		{tenant: "tenant-a", address: "192.0.2.1|0", business: "2", held: true},
		{tenant: "tenant-b", address: "192.0.2.1|0", business: "3", held: true},
		{tenant: "tenant-a", address: "192.0.2.55|1"},
		{tenant: "tenant-c", address: "192.0.2.1|0"},
	} {
		if business, held := lookup.LookupAddressBusiness(test.tenant, test.address); business != test.business || held != test.held {
			t.Fatalf("%s at %s: %q, %v; want %q, %v", test.tenant, test.address, business, held, test.business, test.held)
		}
	}
}
