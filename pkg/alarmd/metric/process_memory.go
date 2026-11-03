// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"bufio"
	"os"
	"runtime/metrics"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
)

// processMemoryCollector splits what the container is charged into what a
// reader can act on: the process's resident set, the Go heap the runtime holds
// idle and the part of it already handed back, and the cgroup's anonymous
// against file-backed memory. Before it, telling "heap not returned" from
// "page cache" meant getting into the Pod to read memory.stat.
type processMemoryCollector struct {
	resident, heapIdle, heapReleased, cgroup, readFailed *prometheus.Desc
	// statmFailed and statFailed count the collections a source could not
	// be read or parsed in, so an absent gauge reads as a source that failed
	// rather than as nothing to say; which failure (no such file, refused,
	// not parsable) is not told apart.
	statmFailed, statFailed atomic.Uint64
	// read and stat are the sources, replaceable in tests.
	read func(name string) ([]byte, error)
	stat func() (idle, released uint64)
}

// cgroupMemoryKinds are the cgroup's charge, by kind.
var cgroupMemoryKinds = []string{"anon", "file"}

func newProcessMemoryCollector() *processMemoryCollector {
	descriptor := func(name, help string, labels []string) *prometheus.Desc {
		return prometheus.NewDesc(prometheus.BuildFQName("", metricSubsystem, name), help, labels, nil)
	}
	return &processMemoryCollector{
		resident: descriptor("process_resident_bytes",
			"The process's resident set (/proc/self/statm). It holds the Go heap in use, the heap the runtime "+
				"keeps idle and has not handed back (go_heap_idle_bytes minus go_heap_released_bytes), stacks "+
				"and the runtime's own memory. container_memory_used_bytes above it is page cache and the "+
				"container's other processes: read container_memory_stat_bytes{kind=\"file\"} for the first. "+
				"Absent where it cannot be read, which is not zero.", nil),
		heapIdle: descriptor("go_heap_idle_bytes",
			"Go heap spans holding no objects (runtime/metrics heap free plus released). The part not yet "+
				"released (minus go_heap_released_bytes) is resident and charged to the container: a high "+
				"process_resident_bytes with a high idle-minus-released is the runtime keeping memory it "+
				"no longer uses, not a leak.", nil),
		heapReleased: descriptor("go_heap_released_bytes",
			"Go heap spans handed back to the operating system (runtime/metrics heap released): no longer "+
				"resident, not charged.", nil),
		cgroup: descriptor("container_memory_stat_bytes",
			"The container cgroup's charge by kind (memory.stat): anon is process memory (anon in cgroup v2, "+
				"rss in v1), file is page cache (file in v2, cache in v1), which the kernel reclaims under "+
				"pressure before it kills. anon plus file is about container_memory_used_bytes. Absent where "+
				"memory.stat cannot be read.", []string{"kind"}),
		readFailed: descriptor("process_memory_read_failed_total",
			"Collections in which a source of the memory gauges could not be read or parsed, by source: "+
				"statm (/proc/self/statm, process_resident_bytes) and memory_stat (cgroup v2 then v1 "+
				"memory.stat, container_memory_stat_bytes), whether the file is missing, refused or not "+
				"parsable. It rises at each scrape while its gauge is absent.", []string{"source"}),
		read: os.ReadFile,
		stat: goHeapIdleReleased,
	}
}

func (c *processMemoryCollector) Describe(descriptions chan<- *prometheus.Desc) {
	descriptions <- c.resident
	descriptions <- c.heapIdle
	descriptions <- c.heapReleased
	descriptions <- c.cgroup
	descriptions <- c.readFailed
}

func (c *processMemoryCollector) Collect(out chan<- prometheus.Metric) {
	if resident, ok := c.residentBytes(); ok {
		out <- prometheus.MustNewConstMetric(c.resident, prometheus.GaugeValue, float64(resident))
	} else {
		c.statmFailed.Add(1)
	}
	idle, released := c.stat()
	out <- prometheus.MustNewConstMetric(c.heapIdle, prometheus.GaugeValue, float64(idle))
	out <- prometheus.MustNewConstMetric(c.heapReleased, prometheus.GaugeValue, float64(released))
	if kinds, ok := c.cgroupKinds(); ok {
		for _, kind := range cgroupMemoryKinds {
			out <- prometheus.MustNewConstMetric(c.cgroup, prometheus.GaugeValue, float64(kinds[kind]), kind)
		}
	} else {
		c.statFailed.Add(1)
	}
	out <- prometheus.MustNewConstMetric(c.readFailed, prometheus.CounterValue, float64(c.statmFailed.Load()), "statm")
	out <- prometheus.MustNewConstMetric(c.readFailed, prometheus.CounterValue, float64(c.statFailed.Load()), "memory_stat")
}

// residentBytes is the resident set from /proc/self/statm: its second field,
// in pages.
func (c *processMemoryCollector) residentBytes() (uint64, bool) {
	raw, err := c.read("/proc/self/statm")
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(string(raw))
	if len(fields) < 2 {
		return 0, false
	}
	pages, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0, false
	}
	return pages * uint64(os.Getpagesize()), true
}

// cgroupKinds reads memory.stat, cgroup v2 first, as anon and file.
func (c *processMemoryCollector) cgroupKinds() (map[string]uint64, bool) {
	for _, source := range []struct {
		path       string
		anon, file string
	}{
		{path: "/sys/fs/cgroup/memory.stat", anon: "anon", file: "file"},
		{path: "/sys/fs/cgroup/memory/memory.stat", anon: "rss", file: "cache"},
	} {
		raw, err := c.read(source.path)
		if err != nil {
			continue
		}
		kinds := map[string]uint64{}
		found := 0
		scanner := bufio.NewScanner(strings.NewReader(string(raw)))
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) != 2 {
				continue
			}
			value, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				continue
			}
			switch fields[0] {
			case source.anon:
				kinds["anon"] = value
				found++
			case source.file:
				kinds["file"] = value
				found++
			}
		}
		if found == 2 {
			return kinds, true
		}
	}
	return nil, false
}

// goHeapIdleReleased reads the heap's idle and released bytes from
// runtime/metrics, which does not stop the world as ReadMemStats does.
func goHeapIdleReleased() (idle, released uint64) {
	samples := []metrics.Sample{{Name: "/memory/classes/heap/free:bytes"}, {Name: "/memory/classes/heap/released:bytes"}}
	metrics.Read(samples)
	var free uint64
	if samples[0].Value.Kind() == metrics.KindUint64 {
		free = samples[0].Value.Uint64()
	}
	if samples[1].Value.Kind() == metrics.KindUint64 {
		released = samples[1].Value.Uint64()
	}
	return free + released, released
}
