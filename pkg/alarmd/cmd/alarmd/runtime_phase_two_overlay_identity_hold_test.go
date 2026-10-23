// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// overlayEventsSince is every event written after the first skip, about the
// agent-live series.
func overlayEventsSince(fixture *lifecycleFixture, skip int) []contract.TriggerEventV1 {
	fixture.sink.mu.Lock()
	defer fixture.sink.mu.Unlock()
	var matched []contract.TriggerEventV1
	for _, event := range fixture.sink.written[skip:] {
		if string(event.RecordRef.Dimensions["bk_agent_id"]) == `"agent-live"` {
			matched = append(matched, event)
		}
	}
	return matched
}

func writtenCount(fixture *lifecycleFixture) int {
	fixture.sink.mu.Lock()
	defer fixture.sink.mu.Unlock()
	return len(fixture.sink.written)
}

// A writer measured at one deployment published 16.5 minutes apart against
// a 15-minute bound, and for the minute and a half between the bound and its
// next publish the held index was stale. Through the production
// bundle: the series placed by its agent is abnormal; the writer's publish
// time is moved past the bound and the index read again; the next round's
// alert is still filed under the fingerprint CMDB's address gives it, never
// under the record's raw one.
func TestAStaleCMDBIndexKeepsAHostPlacedAlertsIdentity(t *testing.T) {
	identity := contract.MonitorOutputIdentity{DimensionFields: overlayAggregation}
	series := overlaySeriesSet[0]
	cmdb, err := contract.MonitorDedupeMD5(lifecycleStrategyID, "2", series.pythonDimensions(), identity)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := contract.MonitorDedupeMD5(lifecycleStrategyID, "2", series.rawDimensions(), identity)
	if err != nil {
		t.Fatal(err)
	}
	address, client := startPhaseTwoRedis(t)
	ctx := context.Background()
	published := "tenant-a." + validGoAccessRuntimeConfig().PlatformKeyPrefix() + ".cache.cmdb_published_at.host_topo"
	if err := client.Set(ctx, published, strconv.FormatInt(time.Now().Unix(), 10), 0).Err(); err != nil {
		t.Fatal(err)
	}
	fixture, _ := startOverlayFixtureOn(t, address, client, config.OutputProtocolNative)

	fixture.mustAttempt(1)
	first := overlayEventsSince(fixture, 0)
	if len(first) == 0 || first[0].DedupeMD5 != cmdb {
		t.Fatalf("round 1 wrote %+v for the series, want its alert under CMDB's %s", first, cmdb)
	}
	// The writer's last publish is 15m17s old: the index read again is
	// stale.
	if err := client.Set(ctx, published, strconv.FormatInt(time.Now().Add(-15*time.Minute-17*time.Second).Unix(), 10), 0).Err(); err != nil {
		t.Fatal(err)
	}
	_ = fixture.bundle.cmdbIndex.Refresh(ctx)
	if _, reason := fixture.bundle.cmdbIndex.For("tenant-a").Usable(); reason != "index_stale" {
		t.Fatalf("the tenant's index is %q after the late publish, want index_stale", reason)
	}
	before := writtenCount(fixture)
	fixture.mustAttempt(2)
	second := overlayEventsSince(fixture, before)
	if len(second) == 0 {
		t.Fatal("round 2 wrote nothing for the series; the case needs an event to read")
	}
	for _, event := range second {
		if event.DedupeMD5 != cmdb || event.DedupeMD5 == raw {
			t.Fatalf("round 2, index stale, filed %s under %s; want CMDB's %s, never the raw %s",
				event.EventKind, event.DedupeMD5, cmdb, raw)
		}
	}
}

// A tenant whose CMDB cache holds no host - its first load returned empty -
// gives a host-placed series no identity to read, and none is waited for: the
// round completes and its alert goes out under the record's own address, as
// Python's does when CMDB holds no host for it. Once a load holds the host,
// the next round files the alert under CMDB's address.
func TestATenantCacheWithNoHostsEmitsAHostPlacedSeriesUnderItsOwnAddress(t *testing.T) {
	identity := contract.MonitorOutputIdentity{DimensionFields: overlayAggregation}
	series := overlaySeriesSet[0]
	cmdb, err := contract.MonitorDedupeMD5(lifecycleStrategyID, "2", series.pythonDimensions(), identity)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := contract.MonitorDedupeMD5(lifecycleStrategyID, "2", series.rawDimensions(), identity)
	if err != nil {
		t.Fatal(err)
	}
	address, client := startPhaseTwoRedis(t)
	ctx := context.Background()
	fixture, _ := startOverlayFixtureOn(t, address, client, config.OutputProtocolNative)
	hosts := "tenant-a." + fixture.cfg.PlatformKeyPrefix() + ".cache.cmdb.host"
	saved, err := client.HGetAll(ctx, hosts).Result()
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Del(ctx, hosts).Err(); err != nil {
		t.Fatal(err)
	}

	fixture.mustAttempt(1)
	first := overlayEventsSince(fixture, 0)
	if len(first) == 0 || first[0].DedupeMD5 != raw {
		t.Fatalf("round 1, a tenant cache with no host, wrote %+v; want the alert under the record's own %s", first, raw)
	}

	fields := make([]any, 0, 2*len(saved))
	for field, value := range saved {
		fields = append(fields, field, value)
	}
	if err := client.HSet(ctx, hosts, fields...).Err(); err != nil {
		t.Fatal(err)
	}
	_ = fixture.bundle.cmdbIndex.Refresh(ctx)
	before := writtenCount(fixture)
	fixture.mustAttempt(2)
	second := overlayEventsSince(fixture, before)
	if len(second) == 0 {
		t.Fatal("round 2 wrote nothing for the series")
	}
	for _, event := range second {
		if event.DedupeMD5 != cmdb {
			t.Fatalf("round 2, the host in CMDB, filed %s under %s, want CMDB's %s", event.EventKind, event.DedupeMD5, cmdb)
		}
	}
}
