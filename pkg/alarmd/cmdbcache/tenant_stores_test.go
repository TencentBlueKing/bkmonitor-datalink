// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package cmdbcache

// A tenant other than the default keeps its CMDB cache under its own keys,
// "<tenant>.<prefix>.cache.cmdb.*" (bk-monitor's CMDBCacheManager,
// core/cache/cmdb/base.py:25-44). A record of that tenant's host read against
// the default tenant's keys finds no host and is dropped as unknown; read
// against its tenant's, it is placed. A tenant asked for the first time is
// read then, on that caller's path; a first read that fails leaves its
// records admitted with the facts named unavailable until a refresh reads it.

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/admission"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/targetplan"
)

func tenantHost(id, ip string, business int) string {
	return `{"bk_host_id":` + id + `,"bk_host_innerip":"` + ip + `","bk_cloud_id":0,"bk_biz_id":` + strconv.Itoa(business) +
		`,"model_id":"cw-Host","model_inst_id":"` + id + `","bk_state":"运营中[需告警]",` +
		`"topo_link":{"module|91":[{"bk_obj_id":"module","bk_inst_id":91}]}}`
}

func tenantStoresWith(t *testing.T) (*TenantStores, *hashClient) {
	t.Helper()
	system, other := tenantHost("740001", "192.0.2.191", 2), tenantHost("740002", "192.0.2.192", 7)
	client := &hashClient{hashes: map[string][]string{
		"bk_monitorv3.ce.cache.cmdb.host":          {"740001", system, "192.0.2.191|0", system},
		"tenant-a.bk_monitorv3.ce.cache.cmdb.host": {"740002", other, "192.0.2.192|0", other},
	}}
	stores, err := NewTenantStores(client, "bk_monitorv3.ce", StoreOptions{RefreshInterval: time.Minute, MaxAge: 10 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if err := stores.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	return stores, client
}

func TestEachTenantsRecordsArePlacedAgainstItsOwnCMDBKeys(t *testing.T) {
	stores, _ := tenantStoresWith(t)
	if stores.For("") != stores.Default() || stores.For(DefaultTenant) != stores.Default() || stores.Tenants() != 0 {
		t.Fatal("the default tenant is not the default store")
	}
	chain := admission.NewChain([]admission.Fuller{admission.IdentityFuller{}, NewHostTopologyFuller(stores), NewServiceInstanceTopologyFuller(stores)},
		[]admission.Filter{admission.NewHostStatusFilter([]string{"备用机"})})
	record := map[string]json.RawMessage{"bk_host_id": json.RawMessage(`"740002"`)}

	// The first reference reads the tenant's keys: placed there, under its
	// own business.
	placed := chain.EnrichFor("tenant-a", record)
	if !placed.HostResolved || placed.HostBusinessID != "7" {
		t.Fatalf("a tenant's host read against its keys = %+v, want placed under business 7", placed)
	}
	// The same record read against the default tenant's keys is the case
	// this fixes: no such host there, dropped as unknown.
	elsewhere := chain.EnrichFor("", record)
	if elsewhere.HostResolved || elsewhere.HostFactsUnavailable {
		t.Fatalf("another tenant's host read against the default keys = %+v, want unknown", elsewhere)
	}
	if admit, _, reason := chain.Admit(admission.PlanContext{}, &elsewhere); admit || reason != "host_unknown" {
		t.Fatalf("another tenant's host under the default tenant: admit %v %s, want dropped host_unknown", admit, reason)
	}
}

// A tenant whose first read fails is never_loaded: its records are admitted
// with the facts named unavailable, not dropped as unknown, and the next
// refresh reads it.
func TestATenantWhoseFirstReadFailsIsUnavailableUntilARefreshReadsIt(t *testing.T) {
	stores, client := tenantStoresWith(t)
	failing := &mappingClient{hashClient: client, failing: map[string]bool{"tenant-a.bk_monitorv3.ce.cache.cmdb.host": true}}
	stores.client = failing
	chain := admission.NewChain([]admission.Fuller{admission.IdentityFuller{}, NewHostTopologyFuller(stores)},
		[]admission.Filter{admission.NewHostStatusFilter(nil)})
	record := map[string]json.RawMessage{"bk_host_id": json.RawMessage(`"740002"`)}
	facts := chain.EnrichFor("tenant-a", record)
	if !facts.HostFactsUnavailable || facts.HostResolved {
		t.Fatalf("a tenant whose first read failed = %+v, want facts unavailable", facts)
	}
	if admit, _, _ := chain.Admit(admission.PlanContext{TenantID: "tenant-a"}, &facts); !admit {
		t.Fatal("a tenant's record was dropped while its index was not read")
	}
	failing.failing = map[string]bool{}
	_ = stores.Refresh(context.Background())
	if again := chain.EnrichFor("tenant-a", record); !again.HostResolved {
		t.Fatalf("after a refresh that read it = %+v, want placed", again)
	}
}

func TestATenantsPlansAndLookupsReadItsOwnIndex(t *testing.T) {
	stores, _ := tenantStoresWith(t)
	stores.For("tenant-a")
	if err := stores.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	clock := func() time.Time { return time.Now() }
	plan := &contract.TargetPlanV1{SchemaVersion: 1, ModelID: "cw-Host", Rule: contract.TargetPlanRuleModelInstID,
		Identity: contract.TargetPlanIdentityV1{Dimensions: []string{"bk_host_id"}, HostIdentity: true}, StaticKeys: []string{},
		StaticMembers: []contract.TargetPlanMemberV1{{ModelID: "cw-Host", ModelInstID: "740002"}}}
	resolver := NewTargetResolver(nil, stores, clock)
	if got := resolver.ResolveFor(context.Background(), "tenant-a", plan, time.Minute); got.State != targetplan.ResolutionComplete || !got.Contains("740002") {
		t.Fatalf("a tenant's plan against its index = %s, contains 740002 %v", got.State, got.Contains("740002"))
	}
	if got := resolver.Resolve(context.Background(), plan, time.Minute); got.Contains("740002") {
		t.Fatal("a tenant's member was placed against the default tenant's index")
	}

	lookup := NewHostBusinessLookup(stores)
	if business, held := lookup.ForTenant("tenant-a").LookupHostBusiness("192.0.2.192|0"); !held || business != "7" {
		t.Fatalf("no-data lookup in the tenant's index = %q, %v; want 7", business, held)
	}
	if _, held := lookup.LookupHostBusiness("192.0.2.192|0"); held {
		t.Fatal("a tenant's host was found in the default tenant's index")
	}
	record := map[string]json.RawMessage{"bk_host_id": json.RawMessage(`"740002"`)}
	if business, placed := lookup.ForTenant("tenant-a").PlaceHostBusiness(record); !placed || business != "7" {
		t.Fatalf("attribution in the tenant's index = %q, %v; want 7", business, placed)
	}
	if lookup.ForTenant("tenant-b").HostIndexResolved() {
		t.Fatal("a tenant never loaded answered as resolved")
	}
}
