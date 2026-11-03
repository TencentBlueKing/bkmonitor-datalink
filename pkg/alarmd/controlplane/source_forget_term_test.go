// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// A Leader that becomes a follower keeps nothing of its term but the
// departures, and the round that starts its next term is a first round: a
// full read under "elected" that compiles every strategy and publishes where
// the fleet already is.
func TestAFormerLeaderHoldsNothingOfItsTermAndItsNextTermStartsAsAFirstOne(t *testing.T) {
	harness := newChangeGateHarness(t)
	settled := harness.settleActive()
	reused := harness.refresh(controlplane.SourceRefreshUnchanged, controlplane.SourceReadSkipped, controlplane.SourceReadUnchanged, 0)
	harness.wantBuild(reused, controlplane.SourceRefreshReused)
	observation, lastGood, reusable, candidates := harness.reconciler.HeldTermForTest()
	if !observation || !lastGood || !reusable || candidates == 0 {
		t.Fatalf("setup: a Leader held observation=%v last-good=%v reusable=%v candidates=%d, want all of them",
			observation, lastGood, reusable, candidates)
	}
	if _, ok := harness.reconciler.ObservedSnapshot(); !ok {
		t.Fatal("setup: a Leader that read its source has no observation")
	}
	departedBefore, refusedBefore := harness.reconciler.DepartedStrategies()

	harness.reconciler.ForgetTerm()

	observation, lastGood, reusable, candidates = harness.reconciler.HeldTermForTest()
	if observation || lastGood || reusable || candidates != 0 {
		t.Fatalf("a former Leader holds observation=%v last-good=%v reusable=%v candidates=%d, want none",
			observation, lastGood, reusable, candidates)
	}
	if observed, ok := harness.reconciler.ObservedSnapshot(); ok {
		t.Fatalf("a former Leader still answers an observation: %+v", observed)
	}
	if lookup := harness.reconciler.LookupStrategy("1001"); lookup.Available {
		t.Fatalf("a former Leader still answers a lookup: %+v", lookup)
	}
	departedAfter, refusedAfter := harness.reconciler.DepartedStrategies()
	if !reflect.DeepEqual(departedAfter, departedBefore) || refusedAfter != refusedBefore {
		t.Fatalf("departures went from (%+v, %d) to (%+v, %d), want them kept", departedBefore, refusedBefore, departedAfter, refusedAfter)
	}

	first := harness.refresh(controlplane.SourceRefreshUnchanged, controlplane.SourceReadFull, controlplane.SourceReadElected, 1)
	harness.wantBuild(first, controlplane.SourceRefreshRebuilt)
	if first.Publication != settled.Publication {
		t.Fatalf("the next term's first round published %+v, want where the fleet already is: %+v", first.Publication, settled.Publication)
	}
	every := reused.CompiledStrategies + reused.ReusedStrategies
	if first.CompiledStrategies != every || first.ReusedStrategies != 0 {
		t.Fatalf("the next term's first round compiled %d and reused %d, want %d and 0",
			first.CompiledStrategies, first.ReusedStrategies, every)
	}
	if lookup := harness.reconciler.LookupStrategy("1001"); !lookup.Available {
		t.Fatalf("the next term's first round answers no lookup: %+v", lookup)
	}
}

// The absent close reads the observation from its own goroutine while the
// control loop replaces it in a round that reads in full and clears it on a
// follower tick. Under -race this is the check that the two never share the
// pointer unsynchronised; the plain pointer before this raced on the first.
func TestTheObservationIsReadableWhileTheControlLoopReplacesAndForgetsIt(t *testing.T) {
	harness := newChangeGateHarness(t)
	harness.settleActive()
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
				harness.reconciler.ObservedSnapshot()
			}
		}
	}()
	for round := range 6 {
		// Odd rounds replace the observation with a full read on a moved
		// change signal, even ones clear it and read in full as elected.
		if round%2 == 1 {
			harness.clock = harness.clock.Add(time.Second)
			harness.signal(harness.clock)
		} else {
			harness.reconciler.ForgetTerm()
		}
		if _, err := harness.reconciler.Refresh(harness.ctx, harness.source, harness.planner); err != nil {
			close(stop)
			<-done
			t.Fatal(err)
		}
	}
	close(stop)
	<-done
}
