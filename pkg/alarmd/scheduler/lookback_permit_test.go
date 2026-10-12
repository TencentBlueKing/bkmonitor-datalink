// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package scheduler

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func lookbackFlights(t *testing.T, clock *mutableClock) *FlightCoordinator {
	t.Helper()
	limits := testRecoveryLimits()
	limits.ProcessQueryPermits, limits.ReadyQueueCapacity = 16, 32
	flights, err := NewFlightCoordinatorWithRecovery(limits, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	return flights
}

func holdNormal(t *testing.T, flights *FlightCoordinator, clock *mutableClock, n int) []*QueryPermit {
	t.Helper()
	held := make([]*QueryPermit, 0, n)
	for i := 0; i < n; i++ {
		permit, err := flights.AcquireQueryPermit(context.Background(),
			execution.SlotIdentity{QueryGroup: execution.QueryGroupIdentity(fmt.Sprintf("qg-%d", i)), EvaluationTime: 60},
			execution.OperationNormal, clock.Now().Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, permit)
	}
	return held
}

// The lookback takes any permit nobody is waiting for, to the last one, and
// holds no share of its own: with 16 permits and 15 held formally it takes
// the 16th, and with every permit held it is refused as full. Its permits
// fill the pool as formal ones do.
func TestTheLookbackTakesAnyFreePermitAndNoShare(t *testing.T) {
	clock := newMutableClock(time.Unix(300, 0))
	flights := lookbackFlights(t, clock)
	lookbacks := make([]*LookbackPermit, 0, 16)
	for range 16 {
		permit, refused := flights.TryAcquireLookbackPermit()
		if permit == nil || refused != "" {
			t.Fatalf("an idle pool refused lookback permit %d: %q", len(lookbacks)+1, refused)
		}
		lookbacks = append(lookbacks, permit)
	}
	if permit, reason := flights.TryAcquireLookbackPermit(); permit != nil || reason != LookbackRefusedFull {
		t.Fatalf("a 17th permit of 16 = %v %q, want refused as full", permit, reason)
	}
	for _, permit := range lookbacks {
		permit.Release()
		permit.Release() // a second release changes nothing
	}

	normal := holdNormal(t, flights, clock, 15)
	last, reason := flights.TryAcquireLookbackPermit()
	if last == nil || reason != "" {
		t.Fatalf("with one permit free the lookback was refused: %q", reason)
	}
	if permit, reason := flights.TryAcquireLookbackPermit(); permit != nil || reason != LookbackRefusedFull {
		t.Fatalf("with every permit held the lookback = %v %q, want refused as full", permit, reason)
	}
	last.Release()
	for _, held := range normal {
		held.Release()
	}
	if inflight := flights.queryPermitSnapshot().Inflight; inflight != 0 {
		t.Fatalf("after every release %d permits are still held", inflight)
	}
}

// A formal query waiting for a permit is never overtaken: the lookback is
// refused while anyone waits, and a lookback permit released while a formal
// query waits goes to it at once.
func TestTheLookbackNeverOvertakesAWaitingQuery(t *testing.T) {
	clock := newMutableClock(time.Unix(300, 0))
	flights := lookbackFlights(t, clock)
	lookback, reason := flights.TryAcquireLookbackPermit()
	if lookback == nil {
		t.Fatalf("idle pool refused: %q", reason)
	}
	normal := holdNormal(t, flights, clock, 15) // pool full: 15 + the lookback
	granted := make(chan *QueryPermit, 1)
	go func() {
		permit, err := flights.AcquireQueryPermit(context.Background(), execution.SlotIdentity{QueryGroup: "waiting", EvaluationTime: 60},
			execution.OperationNormal, clock.Now().Add(time.Minute))
		if err != nil {
			t.Error(err)
		}
		granted <- permit
	}()
	deadline := time.Now().Add(5 * time.Second)
	for flights.QueryPermitOccupancy().Waiting["normal"] == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the formal query never queued")
		}
		time.Sleep(time.Millisecond)
	}
	if permit, reason := flights.TryAcquireLookbackPermit(); permit != nil || reason != LookbackRefusedWaiting {
		t.Fatalf("with a formal query waiting the lookback = %v %q, want refused", permit, reason)
	}
	lookback.Release()
	select {
	case permit := <-granted:
		permit.Release()
	case <-time.After(5 * time.Second):
		t.Fatal("releasing the lookback permit did not hand it to the waiting query")
	}
	for _, held := range normal {
		held.Release()
	}
}

// The lookback's permits are its own column: counted into the process's
// occupancy, never into any Operation's, and never into the formal
// acquire/queue counters the capacity is sized on.
func TestTheLookbackIsAccountedApartFromEveryOperation(t *testing.T) {
	clock := newMutableClock(time.Unix(300, 0))
	flights := lookbackFlights(t, clock)
	before := flights.QueryPermitOccupancy()
	permit, _ := flights.TryAcquireLookbackPermit()
	during := flights.QueryPermitOccupancy()
	if during.LookbackInflight != 1 || during.Inflight[execution.OperationNormal] != 0 || during.Inflight[execution.OperationProbe] != 0 {
		t.Fatalf("lookback inflight %d, per operation %v", during.LookbackInflight, during.Inflight)
	}
	clock.Advance(3 * time.Second)
	if held := flights.QueryPermitOccupancy().LookbackHeldSeconds; held < 3 {
		t.Fatalf("a held lookback permit's seconds = %v, want the elapsed 3", held)
	}
	permit.Release()
	after := flights.QueryPermitOccupancy()
	if after.LookbackInflight != 0 || after.LookbackHeldSeconds < 3 || after.HeldSeconds[execution.OperationNormal] != 0 {
		t.Fatalf("after release lookback %d/%v, normal held %v", after.LookbackInflight, after.LookbackHeldSeconds, after.HeldSeconds)
	}
	if after.Acquires != before.Acquires || after.Queued != before.Queued {
		t.Fatalf("the lookback moved the formal counters: acquires %d->%d queued %d->%d", before.Acquires, after.Acquires, before.Queued, after.Queued)
	}
	if flights.queryPermitSnapshot().Inflight != 0 {
		t.Fatal("the process total still holds the released lookback permit")
	}
}

func TestTheLookbackIsRefusedWithoutARecoveryBudget(t *testing.T) {
	if permit, reason := NewFlightCoordinator().TryAcquireLookbackPermit(); permit != nil || reason != LookbackRefusedDisabled {
		t.Fatalf("a coordinator without limits granted %v %q", permit, reason)
	}
	var none *FlightCoordinator
	if permit, reason := none.TryAcquireLookbackPermit(); permit != nil || reason != LookbackRefusedDisabled {
		t.Fatalf("a nil coordinator granted %v %q", permit, reason)
	}
}

// Room at the grant is not room for the whole read. A burst that takes the
// free permits while a lookback read runs makes a formal query wait; the
// moment it does, the lookback permit is asked to yield, and once the read
// gives it back the waiting query has it. A second waiter asks again
// without closing the channel twice, and no permit is asked while nobody
// waits.
func TestAWaitingFormalQueryMakesTheLookbackYield(t *testing.T) {
	clock := newMutableClock(time.Unix(300, 0))
	flights := lookbackFlights(t, clock)
	lookback, refused := flights.TryAcquireLookbackPermit()
	if lookback == nil || refused != "" {
		t.Fatalf("an idle pool refused the lookback: %q", refused)
	}
	normal := holdNormal(t, flights, clock, 15) // the pool of 16 is full
	select {
	case <-lookback.Yield():
		t.Fatal("the lookback was asked to yield while no formal query waited")
	default:
	}
	wait := func(name string) chan *QueryPermit {
		granted := make(chan *QueryPermit, 1)
		go func() {
			permit, err := flights.AcquireQueryPermit(context.Background(),
				execution.SlotIdentity{QueryGroup: execution.QueryGroupIdentity(name), EvaluationTime: 60},
				execution.OperationNormal, clock.Now().Add(time.Minute))
			if err != nil {
				t.Error(err)
			}
			granted <- permit
		}()
		return granted
	}
	first := wait("qg-first")
	select {
	case <-lookback.Yield():
	case <-time.After(5 * time.Second):
		t.Fatal("a formal query waits and the lookback was not asked to yield")
	}
	second := wait("qg-second")
	deadline := time.Now().Add(5 * time.Second)
	for flights.QueryPermitOccupancy().Waiting["normal"] != 2 {
		if time.Now().After(deadline) {
			t.Fatal("the second formal query never queued")
		}
		time.Sleep(time.Millisecond)
	}
	lookback.Release()
	select {
	case permit := <-first:
		permit.Release()
	case <-time.After(5 * time.Second):
		t.Fatal("the permit the lookback gave back did not reach the waiting query")
	}
	select {
	case permit := <-second:
		permit.Release()
	case <-time.After(5 * time.Second):
		t.Fatal("the second waiter was not granted")
	}
	for _, held := range normal {
		held.Release()
	}
}
