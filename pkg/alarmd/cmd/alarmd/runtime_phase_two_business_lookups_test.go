// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/cmdbcache"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The evaluator attributes through both the host business and the cluster
// mapping of the one CMDB index lookup. A lookup left out of the pair would
// attribute every event of its kind as a host the cache does not hold, or a
// cluster nobody mapped, and compile without complaint.
func TestBusinessAttributionReadsHostsAndClustersFromTheOneIndexLookup(t *testing.T) {
	index := cmdbcache.NewHostBusinessLookup(nil)
	lookups := businessAttributionLookups(index)
	if lookups.Hosts != index || lookups.Clusters != index || lookups.Namespaces != index {
		t.Fatalf("lookups = %+v, want the index lookup for hosts, clusters and namespaces", lookups)
	}
}

// A tenant's events are attributed, and its no-data hosts resolved, from
// that tenant's CMDB keys: the lookups the evaluator and the worker are
// handed route by tenant, and the default tenant's keys do not hold another
// tenant's host.
func TestAttributionAndNoDataHostsReadTheirTenantsCMDBKeys(t *testing.T) {
	_, client := startPhaseTwoRedis(t)
	ctx := context.Background()
	host := `{"bk_host_id":740002,"bk_host_innerip":"192.0.2.192","bk_cloud_id":0,"bk_biz_id":7,"bk_state":"运营中[需告警]"}`
	if err := client.HSet(ctx, "tenant-a.bk_monitorv3.test.cache.cmdb.host", "740002", host, "192.0.2.192|0", host).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(ctx, "bk_monitorv3.test.cache.cmdb.host", "1", `{"bk_host_id":1,"bk_host_innerip":"192.0.2.1","bk_cloud_id":0,"bk_biz_id":2}`).Err(); err != nil {
		t.Fatal(err)
	}
	stores, err := cmdbcache.NewTenantStores(client, "bk_monitorv3.test", cmdbcache.StoreOptions{RefreshInterval: time.Minute, MaxAge: 10 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	stores.For("tenant-a")
	if err := stores.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	index := cmdbcache.NewHostBusinessLookup(stores)
	record := map[string]json.RawMessage{"bk_host_id": json.RawMessage(`"740002"`)}
	lookups := businessAttributionLookups(index)
	if business, placed := lookups.For("tenant-a").Hosts.PlaceHostBusiness(record); !placed || business != "7" {
		t.Fatalf("tenant-a's event attributed %q, %v; want its host's business 7", business, placed)
	}
	if _, placed := lookups.For("").Hosts.PlaceHostBusiness(record); placed {
		t.Fatal("tenant-a's host was placed from the default tenant's keys")
	}
	hosts := execution.HostBusinessFor(tenantHostLookup{index}, "tenant-a")
	if business, held := hosts.LookupHostBusiness("192.0.2.192|0"); !held || business != "7" || !hosts.HostIndexResolved() {
		t.Fatalf("tenant-a's no-data host = %q, %v; want 7 from a resolved index", business, held)
	}
}
