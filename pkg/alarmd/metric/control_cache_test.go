// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package metric

import (
	"testing"
)

func gatherControlCache(t *testing.T, counts []ControlCacheCounts) map[string]map[string]float64 {
	t.Helper()
	recorder := NewRecorder(BuildInfo{})
	recorder.SetControlCacheSource(func() []ControlCacheCounts { return counts })
	families, err := recorder.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	gathered := map[string]map[string]float64{}
	for _, family := range families {
		for _, series := range family.Metric {
			key := ""
			for _, pair := range series.Label {
				if key != "" {
					key += "/"
				}
				key += pair.GetValue()
			}
			if gathered[family.GetName()] == nil {
				gathered[family.GetName()] = map[string]float64{}
			}
			value := series.GetGauge().GetValue()
			if series.Counter != nil {
				value = series.GetCounter().GetValue()
			}
			gathered[family.GetName()][key] = value
		}
	}
	return gathered
}

// Deciding whether the derived cache budget is right needs three things next
// to each other: what the cache holds, what it is allowed to hold, and whether
// it is throwing entries away. Before this the counters existed and none of
// the three were published, which is how a budget that evicted the working set
// on every container stayed invisible.
func TestControlCachePublishesOccupancyBesideItsBudget(t *testing.T) {
	gathered := gatherControlCache(t, []ControlCacheCounts{
		{Object: "version", Hits: 5, Misses: 1},
		// A follower whose workers all missed one new revision at once: every
		// worker counts a miss, all but the one that read count a share.
		{Object: "snapshot", Misses: 128, Shared: 127},
		{Object: "timeline", Hits: 497, Misses: 340, Refreshes: 0, Evictions: 12,
			Occupancy: &ControlCacheOccupancy{Entries: 931, Bytes: 160 << 20, BytesLimit: 512 << 20}},
	})
	outcomes := gathered["bkmonitor_alarmd_control_cache_total"]
	for key, want := range map[string]float64{
		"timeline/hit": 497, "timeline/miss": 340, "timeline/refresh": 0, "timeline/evict": 12,
		"version/hit": 5, "version/miss": 1, "version/evict": 0, "version/share": 0,
		"snapshot/miss": 128, "snapshot/share": 127,
	} {
		if got, ok := outcomes[key]; !ok || got != want {
			t.Fatalf("control_cache_total{%s} = %v (present=%v), want %v", key, got, ok, want)
		}
	}
	for name, want := range map[string]float64{
		"bkmonitor_alarmd_control_cache_entries":     931,
		"bkmonitor_alarmd_control_cache_bytes":       160 << 20,
		"bkmonitor_alarmd_control_cache_bytes_limit": 512 << 20,
	} {
		if got, ok := gathered[name]["timeline"]; !ok || got != want {
			t.Fatalf("%s{object=timeline} = %v (present=%v), want %v", name, got, ok, want)
		}
		// An object with no derived budget publishes no ceiling. Reporting one
		// as zero would read as a cache that can hold nothing.
		if _, ok := gathered[name]["version"]; ok {
			t.Fatalf("%s published a ceiling for an object that has none", name)
		}
	}
}
