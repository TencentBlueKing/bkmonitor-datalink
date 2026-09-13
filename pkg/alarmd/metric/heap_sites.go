// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package metric

import (
	"math"
	"runtime"
	runtimemetrics "runtime/metrics"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// The process exports how large its heap is and not what fills it. On the
// reference deployment the Control Leader holds several hundred megabytes
// more live heap than a follower of the same fleet, release after release,
// and no design number accounts for it: the catalog it keeps in memory is a
// tenth of that by every estimate. pprof would say, but it listens on
// loopback by design and nothing on the deployment reaches it, so the answer
// has to come from inside the process.
//
// The runtime samples allocations, one in every MemProfileRate bytes on
// average, and keeps for each sampled allocation the stack that made it and
// how much of it is still live as of the last garbage collection; that is the
// heap profile pprof reads. Read here, the samples are scaled the way
// runtime/pprof scales them, so that their sum estimates the live heap without
// bias, and each is charged to the innermost alarmd function on its stack: the
// function that asked for the memory, which for a decoded object is the caller
// of the decoder and not the decoder. The largest sites are published beside
// the total the samples add up to and the live heap the runtime reports, so a
// reader knows what share of the heap the sites explain before trusting them.
//
// A site is where memory was allocated, not where it is held. Memory two
// holders share is charged once, to the function that allocated it, and a
// holder that keeps what another function allocated shows under that other
// function. That is the question this answers: which code allocates what
// stays.
const (
	heapSiteTop          = 10
	heapSiteRefresh      = time.Minute
	heapSiteModulePrefix = "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/"
	heapSiteLiveMetric   = "/gc/heap/live:bytes"
	heapSiteUnknown      = "unknown"
)

type heapSite struct {
	name    string
	bytes   float64
	objects float64
}

// heapSiteSample is one reading of the profile.
type heapSiteSample struct {
	sites []heapSite
	// profiled is the scaled live bytes of every record, the ones published
	// as sites and the rest.
	profiled float64
	records  int
	// live is the runtime's own live heap after the last collection, the
	// number profiled estimates. liveKnown is false when the runtime does not
	// export it, so that an unknown is not published as a zero.
	live      float64
	liveKnown bool
	taken     time.Time
}

type heapSiteCollector struct {
	mu      sync.Mutex
	now     func() time.Time
	profile func() []runtime.MemProfileRecord
	last    *heapSiteSample

	siteBytes   *prometheus.Desc
	siteObjects *prometheus.Desc
	profiled    *prometheus.Desc
	records     *prometheus.Desc
	live        *prometheus.Desc
}

func newHeapSiteCollector(now func() time.Time) *heapSiteCollector {
	descriptor := func(name, help string, labels []string) *prometheus.Desc {
		return prometheus.NewDesc(prometheus.BuildFQName(metricNamespace, metricSubsystem, name), help, labels, nil)
	}
	return &heapSiteCollector{
		now:     now,
		profile: readHeapProfile,
		siteBytes: descriptor("heap_inuse_site_bytes",
			"Live heap charged to the alarmd function that allocated it, for the ten largest such sites, "+
				"estimated from the runtime's allocation samples the way pprof estimates them and refreshed "+
				"at most once a minute. A site is the innermost alarmd function on the allocating stack, so "+
				"what a decoder allocates lands on the alarmd function that called it; a stack with no alarmd "+
				"function, a library's own goroutine, is charged to the innermost function outside the "+
				"standard library. The ten are a share of heap_inuse_profiled_bytes, and they are only to be "+
				"trusted while that total agrees with heap_inuse_live_bytes: a total far from the live heap "+
				"means the samples do not describe it, and then neither do the sites.",
			[]string{"site"}),
		siteObjects: descriptor("heap_inuse_site_objects",
			"Live objects behind heap_inuse_site_bytes, same sites and same estimate. Bytes over objects "+
				"is the average object size at the site, which tells many small objects from few large ones.",
			[]string{"site"}),
		profiled: descriptor("heap_inuse_profiled_bytes",
			"Live heap the allocation samples add up to after scaling, every site included. It estimates "+
				"heap_inuse_live_bytes, and go_memstats_heap_alloc_bytes is never below either; the estimate "+
				"agreeing with the live heap is the condition for trusting heap_inuse_site_bytes at all. A ratio "+
				"far from one means the samples do not describe the heap, as when the sampling rate was changed.",
			nil),
		records: descriptor("heap_inuse_profile_records",
			"Allocation stacks with live memory in the last profile read, the population the sites were "+
				"chosen from.",
			nil),
		live: descriptor("heap_inuse_live_bytes",
			"Live heap the runtime reports after its last collection, read with the profile so the two "+
				"describe the same moment.",
			nil),
	}
}

func (c *heapSiteCollector) Describe(descriptions chan<- *prometheus.Desc) {
	descriptions <- c.siteBytes
	descriptions <- c.siteObjects
	descriptions <- c.profiled
	descriptions <- c.records
	descriptions <- c.live
}

func (c *heapSiteCollector) Collect(metrics chan<- prometheus.Metric) {
	sample := c.sample()
	for _, site := range sample.sites {
		metrics <- prometheus.MustNewConstMetric(c.siteBytes, prometheus.GaugeValue, site.bytes, site.name)
		metrics <- prometheus.MustNewConstMetric(c.siteObjects, prometheus.GaugeValue, site.objects, site.name)
	}
	metrics <- prometheus.MustNewConstMetric(c.profiled, prometheus.GaugeValue, sample.profiled)
	metrics <- prometheus.MustNewConstMetric(c.records, prometheus.GaugeValue, float64(sample.records))
	if sample.liveKnown {
		metrics <- prometheus.MustNewConstMetric(c.live, prometheus.GaugeValue, sample.live)
	}
}

// sample reads the profile once a minute and serves that reading in between.
// A scrape every few seconds must not pay for symbolizing every live stack,
// and a profile that only moves at collections has nothing new to say that
// often.
func (c *heapSiteCollector) sample() heapSiteSample {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if c.last != nil && now.Sub(c.last.taken) < heapSiteRefresh {
		return *c.last
	}
	records := c.profile()
	sites, profiled := chargeHeapSites(records, runtime.MemProfileRate, heapSiteTop)
	sample := heapSiteSample{sites: sites, profiled: profiled, records: len(records), taken: now}
	sample.live, sample.liveKnown = readLiveHeap()
	c.last = &sample
	return sample
}

// readHeapProfile follows the runtime's protocol for the profile: ask how many
// records there are, allocate with room for what gets sampled meanwhile, ask
// again. Records whose memory was all freed are left out; they are not live
// heap.
func readHeapProfile() []runtime.MemProfileRecord {
	size, _ := runtime.MemProfile(nil, false)
	for {
		records := make([]runtime.MemProfileRecord, size+64)
		count, ok := runtime.MemProfile(records, false)
		if ok {
			return records[:count]
		}
		size = count
	}
}

func readLiveHeap() (float64, bool) {
	samples := []runtimemetrics.Sample{{Name: heapSiteLiveMetric}}
	runtimemetrics.Read(samples)
	if samples[0].Value.Kind() != runtimemetrics.KindUint64 {
		return 0, false
	}
	return float64(samples[0].Value.Uint64()), true
}

// chargeHeapSites scales every live record and charges it to its site. It
// returns the largest sites by bytes, at most top of them, and the scaled
// total of every record, charged or not.
func chargeHeapSites(records []runtime.MemProfileRecord, rate int, top int) ([]heapSite, float64) {
	bySite := map[string]*heapSite{}
	total := 0.0
	for index := range records {
		record := &records[index]
		objects, bytes := scaleHeapSample(record.InUseObjects(), record.InUseBytes(), rate)
		if bytes <= 0 {
			continue
		}
		total += bytes
		name := heapSiteName(record.Stack())
		site := bySite[name]
		if site == nil {
			site = &heapSite{name: name}
			bySite[name] = site
		}
		site.bytes += bytes
		site.objects += objects
	}
	sites := make([]heapSite, 0, len(bySite))
	for _, site := range bySite {
		sites = append(sites, *site)
	}
	sort.Slice(sites, func(i, j int) bool {
		if sites[i].bytes != sites[j].bytes {
			return sites[i].bytes > sites[j].bytes
		}
		return sites[i].name < sites[j].name
	})
	if len(sites) > top {
		sites = sites[:top]
	}
	return sites, total
}

// scaleHeapSample is runtime/pprof's estimate of what one sampled record
// stands for. An allocation of s bytes is sampled with probability
// 1-exp(-s/rate), so each sampled one stands for the inverse of that: small
// allocations are scaled up a lot, one larger than the rate hardly at all.
func scaleHeapSample(count, size int64, rate int) (float64, float64) {
	if count == 0 || size == 0 {
		return 0, 0
	}
	if rate <= 1 {
		return float64(count), float64(size)
	}
	average := float64(size) / float64(count)
	scale := 1 / (1 - math.Exp(-average/float64(rate)))
	return float64(count) * scale, float64(size) * scale
}

// heapSiteName symbolizes a recorded stack, innermost frame first, and
// chooses the site.
func heapSiteName(stack []uintptr) string {
	names := make([]string, 0, len(stack))
	frames := runtime.CallersFrames(stack)
	for {
		frame, more := frames.Next()
		if frame.Function != "" {
			names = append(names, frame.Function)
		}
		if !more {
			break
		}
	}
	return chooseHeapSite(names)
}

// chooseHeapSite charges a stack, innermost function first, to the innermost
// alarmd function on it; a stack with none, which is a library's own
// goroutine, to the innermost function outside the standard library; and a
// stack with none of those to its innermost function.
func chooseHeapSite(names []string) string {
	if len(names) == 0 {
		return heapSiteUnknown
	}
	for _, name := range names {
		if strings.HasPrefix(name, heapSiteModulePrefix) {
			return strings.TrimPrefix(name, heapSiteModulePrefix)
		}
	}
	for _, name := range names {
		if isModuleFunction(name) {
			return name
		}
	}
	return names[0]
}

// isModuleFunction says whether a function belongs to a module rather than
// the standard library: a module path starts with a host, which has a dot
// before its first slash, and no standard library path does.
func isModuleFunction(name string) bool {
	slash := strings.Index(name, "/")
	if slash < 0 {
		return false
	}
	return strings.Contains(name[:slash], ".")
}
