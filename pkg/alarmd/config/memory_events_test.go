// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import "testing"

// The memory half of the throttling counter. Until this existed the panel could
// say how much memory was used and what the limit was, and had no way to say
// whether the process ever actually reached it -- which left "is 6 GiB out of 8
// a lot" to whoever was reading, a question they have no basis to answer. The
// container answers it about itself.
func TestTheEventsFileSaysHowOftenTheLimitWasActuallyReached(t *testing.T) {
	usage := ContainerUsage{}
	parseMemoryEvents("low 0\nhigh 4\nmax 17\noom 2\noom_kill 1\n", &usage)

	if usage.MemoryLimitHits != 17 || !usage.MemoryLimitHitsKnown {
		t.Fatalf("hits = %d known = %v, want the 17 times it was held at the limit",
			usage.MemoryLimitHits, usage.MemoryLimitHitsKnown)
	}
	// Told apart from a hit on purpose: a kill restarts the container and zeroes
	// every cumulative figure beside it, so a deployment being killed over and
	// over reads as a quiet one unless the kills are counted separately.
	if usage.MemoryOOMKills != 1 || !usage.MemoryOOMKillsKnown {
		t.Fatalf("kills = %d known = %v, want the one kill reported",
			usage.MemoryOOMKills, usage.MemoryOOMKillsKnown)
	}
}

// A container that has never been near its limit reports zero, and zero here is
// the useful answer -- it is what lets the page say "memory is not the problem"
// without anyone choosing a threshold. So it has to be a known zero rather than
// an absent reading.
func TestAContainerThatNeverReachedTheLimitReportsAKnownZero(t *testing.T) {
	usage := ContainerUsage{}
	parseMemoryEvents("low 0\nhigh 0\nmax 0\noom 0\noom_kill 0\n", &usage)

	if usage.MemoryLimitHits != 0 || !usage.MemoryLimitHitsKnown {
		t.Fatalf("hits = %d known = %v, want a measured zero rather than no reading",
			usage.MemoryLimitHits, usage.MemoryLimitHitsKnown)
	}
}

// The kernel has added counters to this file over releases. A line this build
// does not recognise must cost nothing: reading it as a malformed file would
// lose the two counts the page came for, on exactly the newer kernels the
// deployment is moving towards.
func TestAnUnrecognisedCounterDoesNotCostTheOnesWeCameFor(t *testing.T) {
	usage := ContainerUsage{}
	parseMemoryEvents("max 3\nsomething_new 99\nnot-a-number x\noom_kill 2\n", &usage)

	if usage.MemoryLimitHits != 3 || usage.MemoryOOMKills != 2 {
		t.Fatalf("hits = %d kills = %d, want both read past the unknown lines",
			usage.MemoryLimitHits, usage.MemoryOOMKills)
	}
}

// cgroup v1 has no kill counter at all. Reporting zero there would say "nothing
// was killed", which is a different claim from "this kernel does not say" --
// and the reassuring one of the two.
func TestAMissingKillCounterStaysUnknownRatherThanZero(t *testing.T) {
	usage := ContainerUsage{}
	parseMemoryEvents("max 5\n", &usage)

	if usage.MemoryOOMKillsKnown {
		t.Fatal("a kill count was claimed from a file that does not carry one")
	}
}
