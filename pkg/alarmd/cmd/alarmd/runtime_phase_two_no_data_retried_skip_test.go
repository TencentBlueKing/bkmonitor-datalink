// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"reflect"
	"testing"
	"time"
)

// A skipped round that is retried is still one skipped round.
//
// The run that makes a stall is counted in rounds (decomposition section 5.9
// item 3; progress report Q6: three consecutive skipped rounds). Round 1's
// output fails with the acknowledgement unknown, so its Slot is attempted a
// second time; rounds 1 and 2 are two skipped rounds, and the stall is due on
// round 3, not on round 2.
func TestARetriedSkippedRoundCountsOnceTowardAStall(t *testing.T) {
	fixture := startRoundsFixture(t, roundsOptions{hosts: false})
	fixture.value.Store(hostThresholdValue)
	const unresolved = "SKIPPED_HOSTS_UNRESOLVED"

	fixture.sink.setMode(followsACKUnknown)
	mark := fixture.mark()
	fixture.run(1)
	fixture.sink.setMode(followsOK)
	if !fixture.attempt(1, fixture.retry+time.Second) {
		t.Fatal("the retry of round 1 was not attempted")
	}
	if got := fixture.stallsSince(mark); len(got) != 0 {
		t.Fatalf("round 1 and its retry reported stalls %v; one round was skipped", got)
	}
	mark = fixture.mark()
	fixture.run(2)
	if got := fixture.stallsSince(mark); len(got) != 0 {
		t.Fatalf("round 2 reported stalls %v; two rounds were skipped, and the stall is due on the third", got)
	}
	mark = fixture.mark()
	fixture.run(3)
	if got, want := fixture.stallsSince(mark), []string{unresolved}; !reflect.DeepEqual(got, want) {
		t.Fatalf("round 3 reported stalls %v, want %v", got, want)
	}
}
