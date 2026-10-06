// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import (
	"os"
	"strconv"
	"strings"
)

// ContainerUsage is what the container is currently consuming, read from the
// same cgroup the limits were read from.
//
// The limits are already known -- the whole capacity profile is derived from
// them -- but nothing reported what was being used against them, so a budget
// rejection could be seen without its denominator. These are the numerators.
type ContainerUsage struct {
	// MemoryBytes is resident charge against the memory limit, not Go heap.
	// The two differ by everything the Go heap does not account for, and it is
	// the cgroup number that gets the process killed.
	MemoryBytes uint64
	MemoryKnown bool
	// ThrottledSeconds is cumulative CPU throttling. It is the difference
	// between "busy" and "not allowed to run", which no busy-time metric can
	// separate: a process at its quota looks identical to an idle one that is
	// being held back.
	ThrottledSeconds float64
	ThrottledKnown   bool
	// CPUSeconds is cumulative CPU time consumed. Two reads give cores in use,
	// which is what the core count on the panel has to be read against.
	CPUSeconds      float64
	CPUSecondsKnown bool
	// MemoryLimitHits counts the times the container was actually held at its
	// memory limit: an allocation that had to reclaim rather than be served.
	//
	// It is the memory counterpart of ThrottledSeconds and exists for the same
	// reason. Used-over-limit is a ratio, and a ratio needs somebody to decide
	// what "close" means -- a decision nobody reading the page is equipped to
	// make and nobody should be asked to. This is the container saying it got
	// there, which needs no threshold at all.
	MemoryLimitHits      uint64
	MemoryLimitHitsKnown bool
	// MemoryOOMKills counts processes the kernel killed for exceeding the
	// limit. A hit was survived; a kill restarted the container, and that
	// restart zeroes every cumulative figure reported beside it -- so a
	// deployment being killed repeatedly reads as a quiet one unless the kills
	// are counted separately.
	MemoryOOMKills      uint64
	MemoryOOMKillsKnown bool
}

// ReadContainerUsage reads current consumption. Anything unreadable is reported
// as unknown rather than as zero: outside a container these files do not exist,
// and a zero would read as "using nothing" instead of "not measured here".
func ReadContainerUsage() ContainerUsage {
	usage := ContainerUsage{}
	if raw, err := os.ReadFile("/sys/fs/cgroup/memory.current"); err == nil {
		if used, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64); err == nil {
			usage.MemoryBytes, usage.MemoryKnown = used, true
		}
	} else if raw, err := os.ReadFile("/sys/fs/cgroup/memory/memory.usage_in_bytes"); err == nil {
		if used, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64); err == nil {
			usage.MemoryBytes, usage.MemoryKnown = used, true
		}
	}
	if seconds, ok := readThrottledSeconds(); ok {
		usage.ThrottledSeconds, usage.ThrottledKnown = seconds, true
	}
	if seconds, ok := readCPUSeconds(); ok {
		usage.CPUSeconds, usage.CPUSecondsKnown = seconds, true
	}
	readMemoryLimitEvents(&usage)
	return usage
}

// readMemoryLimitEvents reads how often the limit was actually reached. v2
// keeps both counts in one file; v1 keeps a hit count and no kill count, and
// the missing one is reported as unknown rather than as zero, because "nothing
// was killed" and "this kernel does not say" are different answers and only one
// of them is reassuring.
func readMemoryLimitEvents(usage *ContainerUsage) {
	if raw, err := os.ReadFile("/sys/fs/cgroup/memory.events"); err == nil {
		parseMemoryEvents(string(raw), usage)
		return
	}
	if raw, err := os.ReadFile("/sys/fs/cgroup/memory/memory.failcnt"); err == nil {
		if hits, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64); err == nil {
			usage.MemoryLimitHits, usage.MemoryLimitHitsKnown = hits, true
		}
	}
}

// parseMemoryEvents reads the v2 event counters. Unrecognised lines are skipped
// rather than treated as a malformed file: the kernel adds counters to this file
// over time, and a version that reports one more of them must not cost the page
// the two counts it came for.
func parseMemoryEvents(raw string, usage *ContainerUsage) {
	for _, line := range strings.Split(raw, "\n") {
		name, value, found := strings.Cut(strings.TrimSpace(line), " ")
		if !found {
			continue
		}
		count, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
		if err != nil {
			continue
		}
		switch name {
		case "max":
			usage.MemoryLimitHits, usage.MemoryLimitHitsKnown = count, true
		case "oom_kill":
			usage.MemoryOOMKills, usage.MemoryOOMKillsKnown = count, true
		}
	}
}

// readCPUSeconds reads cumulative CPU time the container has consumed.
//
// Throttling says whether the quota was hit; this says how much of it is being
// used. A panel showing "8 cores" with nothing beside it says the same thing to
// a container using half a core and one using seven, and only the second is
// near anything. Two reads of this over the wall time between them give cores
// in use, which is the number the limit is there to be compared against.
//
// v2 keeps it in the same cpu.stat that carries the throttling; v1 keeps it in
// cpuacct, in nanoseconds.
func readCPUSeconds() (float64, bool) {
	if raw, err := os.ReadFile("/sys/fs/cgroup/cpu.stat"); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			name, value, found := strings.Cut(strings.TrimSpace(line), " ")
			if !found || name != "usage_usec" {
				continue
			}
			if micros, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64); err == nil {
				return float64(micros) / 1e6, true
			}
		}
	}
	if raw, err := os.ReadFile("/sys/fs/cgroup/cpuacct/cpuacct.usage"); err == nil {
		if nanos, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64); err == nil {
			return float64(nanos) / 1e9, true
		}
	}
	return 0, false
}

// readThrottledSeconds reads CFS throttling from either cgroup version. v2
// reports microseconds in cpu.stat; v1 reports nanoseconds in its own file.
func readThrottledSeconds() (float64, bool) {
	if raw, err := os.ReadFile("/sys/fs/cgroup/cpu.stat"); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			name, value, found := strings.Cut(strings.TrimSpace(line), " ")
			if !found || name != "throttled_usec" {
				continue
			}
			if micros, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64); err == nil {
				return float64(micros) / 1e6, true
			}
		}
	}
	if raw, err := os.ReadFile("/sys/fs/cgroup/cpu/cpu.stat"); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			name, value, found := strings.Cut(strings.TrimSpace(line), " ")
			if !found || name != "throttled_time" {
				continue
			}
			if nanos, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64); err == nil {
				return float64(nanos) / 1e9, true
			}
		}
	}
	return 0, false
}
