// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package metric

import (
	"sort"
	"strings"
	"testing"
)

// Every result this counter publishes has to be one that something can produce.
//
// It published result="share" on every object with no code path anywhere that
// incremented the field behind it, so it read zero for the life of the metric.
// The test that covered the label fed the collector an object named "snapshot",
// which the production wiring does not emit -- the label was exercised, the
// test was green, and the mechanism did not exist.
//
// Reading zero off a label that cannot be non-zero is worse than having no
// label. It reads as a mechanism that is wired and never firing, which is an
// invitation to go and wire it; someone took the invitation and wrote the
// coalescing before a file comment stopped them, because for the version header
// serving a cached value is not a slower answer but a missed publication
// cutover.
//
// Pinned as an exact set rather than a list of required members, so adding a
// result means coming here and naming what writes it.
func TestControlCachePublishesOnlyResultsSomethingCanProduce(t *testing.T) {
	gathered := gatherControlCache(t, []ControlCacheCounts{{Object: "version"}})
	published := map[string]bool{}
	for key := range gathered["bkmonitor_alarmd_control_cache_total"] {
		parts := strings.Split(key, "/")
		published[parts[len(parts)-1]] = true
	}
	// hit, miss and refresh are written by controlReadObjectCounters; evict by
	// the timeline cache's eviction count; clear by the key segment memos and by
	// a Worker that fell more than one revision behind.
	want := []string{"clear", "evict", "hit", "miss", "refresh"}
	got := make([]string, 0, len(published))
	for result := range published {
		got = append(got, result)
	}
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("control_cache_total publishes results %v, want exactly %v -- a result nothing "+
			"writes reads as a mechanism that is wired and not firing", got, want)
	}
}

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
		{Object: "timeline", Hits: 497, Misses: 340, Refreshes: 0, Evictions: 12,
			Occupancy: &ControlCacheOccupancy{Entries: 931, Bytes: 160 << 20, BytesLimit: 512 << 20}},
	})
	outcomes := gathered["bkmonitor_alarmd_control_cache_total"]
	for key, want := range map[string]float64{
		"timeline/hit": 497, "timeline/miss": 340, "timeline/refresh": 0, "timeline/evict": 12,
		"version/hit": 5, "version/miss": 1, "version/evict": 0,
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
