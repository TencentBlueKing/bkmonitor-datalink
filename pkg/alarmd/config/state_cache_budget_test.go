// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import "testing"

// The State cache holds at most 128 MiB on a 4 GiB container, borrowed from
// the retained-bytes pool, which stays the container's quarter, 1,024 MiB:
// the cache takes nothing out of it, so the share one Query Group may hold
// does not move. Nothing else moves either: the execution ceiling and its
// query permits are derived on their own line.
func TestTheStateCacheTakesNothingOutOfThePool(t *testing.T) {
	inputs := CapacityInputs{MemoryLimitBytes: 4 << 30}
	if got := DeriveStateCacheBytes(inputs); got != 128<<20 {
		t.Fatalf("state cache budget = %d, want 128 MiB", got)
	}
	if got := DeriveCoordinator(inputs, 1, 1).MaxRetainedBytes; got != 1<<30 {
		t.Fatalf("retained-bytes pool = %d, want the container's quarter, 1 GiB", got)
	}
	if got := DeriveScheduler(inputs).ActiveExecutionCeiling; got != (1<<30)/bytesPerParkedExecution {
		t.Fatalf("execution ceiling = %d, want the 1 GiB line's, untouched", got)
	}
}

// The pool is the container's quarter whether the file turns the State cache
// off or leaves it on: turning it off - the rollback without a rebuild -
// changes what is read back, not what a Slot may hold.
func TestThePoolIsTheSameWithTheStateCacheOnOrOff(t *testing.T) {
	inputs := DetectCapacityInputs()
	for _, test := range []struct {
		document string
		want     uint64
	}{
		{"phase_two:\n  state_cache:\n    disabled: true\n", inputs.MemoryLimitBytes / 4},
		{"phase_two:\n  state_cache:\n    disabled: false\n", inputs.MemoryLimitBytes / 4},
	} {
		cfg := Default().WithContainerCapacity()
		if err := decodeDocument([]byte(test.document), &cfg); err != nil {
			t.Fatal(err)
		}
		if got := cfg.PhaseTwo.Coordinator.MaxRetainedBytes; got != test.want {
			t.Fatalf("%q: pool = %d, want %d", test.document, got, test.want)
		}
	}
}
