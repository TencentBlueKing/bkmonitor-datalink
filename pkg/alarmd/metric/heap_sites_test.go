// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package metric

import (
	"math"
	"runtime"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// heapSiteBallast keeps what retainHeapSiteBallast allocated alive across the
// profile read; a local would be dead before the collector runs.
var heapSiteBallast [][]byte

func retainHeapSiteBallast(size int) {
	block := make([]byte, size)
	for index := 0; index < len(block); index += 4096 {
		block[index] = 1
	}
	heapSiteBallast = append(heapSiteBallast, block)
}

func gatherHeapSites(t *testing.T, collector prometheus.Collector) map[string]map[string]float64 {
	t.Helper()
	registry := prometheus.NewRegistry()
	registry.MustRegister(collector)
	families, err := registry.Gather()
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
			gathered[family.GetName()][key] = series.GetGauge().GetValue()
		}
	}
	return gathered
}

// The instrument exists to say which function's allocations stay. Before it
// is trusted on a heap nobody can profile, it has to find a retention that is
// planted where it is known: sixty-four megabytes kept alive by one named
// function must show under that function's name, at about that size, and
// both totals it is read against must be at least that large.
func TestHeapSitesChargeAPlantedRetentionToTheFunctionThatAllocatedIt(t *testing.T) {
	const size = 64 << 20
	retainHeapSiteBallast(size)
	t.Cleanup(func() { heapSiteBallast = nil })
	runtime.GC()

	gathered := gatherHeapSites(t, newHeapSiteCollector(time.Now))
	const site = "metric.retainHeapSiteBallast"
	bytes, ok := gathered["bkmonitor_alarmd_heap_inuse_site_bytes"][site]
	if !ok {
		t.Fatalf("planted retention was not charged to its function; sites: %v", gathered["bkmonitor_alarmd_heap_inuse_site_bytes"])
	}
	if bytes < size || bytes > size*1.1 {
		t.Fatalf("planted %d bytes, charged %.0f", size, bytes)
	}
	objects := gathered["bkmonitor_alarmd_heap_inuse_site_objects"][site]
	if objects < 1 || objects > 1.1 {
		t.Fatalf("planted one object, charged %.2f", objects)
	}
	if sites := len(gathered["bkmonitor_alarmd_heap_inuse_site_bytes"]); sites > heapSiteTop {
		t.Fatalf("published %d sites, the instrument keeps %d", sites, heapSiteTop)
	}
	if profiled := gathered["bkmonitor_alarmd_heap_inuse_profiled_bytes"][""]; profiled < bytes {
		t.Fatalf("profiled total %.0f is below the one site it includes, %.0f", profiled, bytes)
	}
	if live := gathered["bkmonitor_alarmd_heap_inuse_live_bytes"][""]; live < size {
		t.Fatalf("runtime live heap %.0f is below the %d bytes kept alive", live, size)
	}
	if records := gathered["bkmonitor_alarmd_heap_inuse_profile_records"][""]; records < 1 {
		t.Fatalf("profile records %.0f", records)
	}
}

func TestHeapSitesReadTheProfileAtMostOnceAMinute(t *testing.T) {
	clock := time.Date(2026, 9, 13, 16, 0, 0, 0, time.UTC)
	collector := newHeapSiteCollector(func() time.Time { return clock })
	reads := 0
	read := collector.profile
	collector.profile = func() []runtime.MemProfileRecord {
		reads++
		return read()
	}
	gatherHeapSites(t, collector)
	clock = clock.Add(heapSiteRefresh - time.Second)
	gatherHeapSites(t, collector)
	if reads != 1 {
		t.Fatalf("two scrapes inside a minute read the profile %d times", reads)
	}
	clock = clock.Add(time.Second)
	gatherHeapSites(t, collector)
	if reads != 2 {
		t.Fatalf("a scrape a minute later read the profile %d times in all", reads)
	}
}

// Two stacks captured in two named functions stand in for the profile: the
// charge goes to the capturing function, sites are ordered by bytes, the top
// is a cut and not a filter of the total, and a record with nothing live
// counts for nothing.
func TestHeapSitesOrderByBytesAndCutAtTheTop(t *testing.T) {
	first := heapSiteTestStackFirst()
	second := heapSiteTestStackSecond()
	const mib = 1 << 20
	records := []runtime.MemProfileRecord{
		{AllocBytes: 10 * mib, AllocObjects: 1, Stack0: first},
		{AllocBytes: 12 * mib, AllocObjects: 1, Stack0: first},
		{AllocBytes: 8 * mib, AllocObjects: 1, Stack0: first},
		{AllocBytes: 5 * mib, AllocObjects: 1, Stack0: second},
		{AllocBytes: 40 * mib, FreeBytes: 40 * mib, AllocObjects: 1, FreeObjects: 1, Stack0: second},
	}
	sites, total := chargeHeapSites(records, runtime.MemProfileRate, 10)
	if len(sites) != 2 {
		t.Fatalf("sites %+v", sites)
	}
	if sites[0].name != "metric.heapSiteTestStackFirst" || sites[1].name != "metric.heapSiteTestStackSecond" {
		t.Fatalf("sites %+v", sites)
	}
	if !near(sites[0].bytes, 30*mib) || !near(sites[0].objects, 3) || !near(sites[1].bytes, 5*mib) || !near(sites[1].objects, 1) {
		t.Fatalf("sites %+v", sites)
	}
	if !near(total, 35*mib) {
		t.Fatalf("total %.0f", total)
	}
	cut, cutTotal := chargeHeapSites(records, runtime.MemProfileRate, 1)
	if len(cut) != 1 || cut[0].name != sites[0].name || cutTotal != total {
		t.Fatalf("cut %+v total %.0f", cut, cutTotal)
	}
}

func heapSiteTestStackFirst() [32]uintptr {
	var stack [32]uintptr
	runtime.Callers(1, stack[:])
	return stack
}

func heapSiteTestStackSecond() [32]uintptr {
	var stack [32]uintptr
	runtime.Callers(1, stack[:])
	return stack
}

func near(got, want float64) bool {
	return math.Abs(got-want) <= want*0.01
}

func TestHeapSiteIsTheInnermostAlarmdFunctionOrTheInnermostOutsideTheStandardLibrary(t *testing.T) {
	cases := []struct {
		name  string
		stack []string
		want  string
	}{
		{"decoder called by alarmd", []string{
			"encoding/json.(*decodeState).literalStore", "encoding/json.(*decodeState).object", "reflect.Value.SetBytes",
			heapSiteModulePrefix + "controlplane.(*RedisCatalogRepository).loadPublishedGroups",
			heapSiteModulePrefix + "controlplane.(*SourceReconciler).currentSnapshot",
		}, "controlplane.(*RedisCatalogRepository).loadPublishedGroups"},
		{"library goroutine", []string{
			"bufio.NewReaderSize", "github.com/redis/go-redis/v9/internal/pool.NewConn",
			"github.com/redis/go-redis/v9/internal/pool.(*ConnPool).dialConn",
		}, "github.com/redis/go-redis/v9/internal/pool.NewConn"},
		{"runtime only", []string{"runtime.malg", "runtime.newproc1"}, "runtime.malg"},
		{"vendored path without a host is standard library", []string{"vendor/golang.org/x/net/http2.(*Framer).ReadFrame", "net/http.(*conn).serve"}, "vendor/golang.org/x/net/http2.(*Framer).ReadFrame"},
		{"empty", nil, heapSiteUnknown},
	}
	for _, c := range cases {
		if got := chooseHeapSite(c.stack); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

// The scale is the one runtime/pprof applies, checked against values worked
// out by hand: a record whose average allocation equals the sampling rate
// stands for e/(e-1) of itself, an allocation far above the rate stands for
// itself, and the scale never changes the average size.
func TestHeapSampleScaleIsTheProfilerScale(t *testing.T) {
	const rate = 512 << 10
	objects, bytes := scaleHeapSample(4, 4*rate, rate)
	if !near(objects, 4*1.5819767068693265) || !near(bytes, 4*rate*1.5819767068693265) {
		t.Fatalf("average at the rate: objects %.4f bytes %.0f", objects, bytes)
	}
	objects, bytes = scaleHeapSample(1, 64<<20, rate)
	if objects != 1 || bytes != 64<<20 {
		t.Fatalf("large allocation: objects %v bytes %v", objects, bytes)
	}
	objects, bytes = scaleHeapSample(3, 3*1024, rate)
	if bytes/objects != 1024 {
		t.Fatalf("average changed by the scale: %v", bytes/objects)
	}
	if objects <= 3 {
		t.Fatalf("small allocations are scaled up, got %v for 3", objects)
	}
	if objects, bytes = scaleHeapSample(0, 0, rate); objects != 0 || bytes != 0 {
		t.Fatalf("empty record: %v %v", objects, bytes)
	}
	if objects, bytes = scaleHeapSample(7, 700, 1); objects != 7 || bytes != 700 {
		t.Fatalf("rate one is every allocation: %v %v", objects, bytes)
	}
}

func TestRecorderPublishesHeapSites(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	families, err := recorder.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, family := range families {
		found[family.GetName()] = true
	}
	for _, name := range []string{"bkmonitor_alarmd_heap_inuse_profiled_bytes", "bkmonitor_alarmd_heap_inuse_profile_records", "bkmonitor_alarmd_heap_inuse_live_bytes"} {
		if !found[name] {
			t.Fatalf("%s is not published by the recorder", name)
		}
	}
}
