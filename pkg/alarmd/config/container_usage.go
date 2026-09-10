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
	return usage
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
