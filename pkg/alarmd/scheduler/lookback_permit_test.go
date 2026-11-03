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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
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
	deadline := testwait.Deadline(t)
	for flights.QueryPermitOccupancy().Waiting["normal"] == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the formal query never queued")
		}
		time.Sleep(time.Millisecond) // fixed-wait: the poll interval of a condition wait bounded by the test deadline
	}
	if permit, reason := flights.TryAcquireLookbackPermit(); permit != nil || reason != LookbackRefusedWaiting {
		t.Fatalf("with a formal query waiting the lookback = %v %q, want refused", permit, reason)
	}
	lookback.Release()
	select {
	case permit := <-granted:
		permit.Release()
	case <-testwait.Expired(t):
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
	case <-testwait.Expired(t):
		t.Fatal("a formal query waits and the lookback was not asked to yield")
	}
	second := wait("qg-second")
	deadline := testwait.Deadline(t)
	for flights.QueryPermitOccupancy().Waiting["normal"] != 2 {
		if time.Now().After(deadline) {
			t.Fatal("the second formal query never queued")
		}
		time.Sleep(time.Millisecond) // fixed-wait: the poll interval of a condition wait bounded by the test deadline
	}
	lookback.Release()
	select {
	case permit := <-first:
		permit.Release()
	case <-testwait.Expired(t):
		t.Fatal("the permit the lookback gave back did not reach the waiting query")
	}
	select {
	case permit := <-second:
		permit.Release()
	case <-testwait.Expired(t):
		t.Fatal("the second waiter was not granted")
	}
	for _, held := range normal {
		held.Release()
	}
}

// With the permits at the memory's share the lookback can hold the whole
// pool while nothing else runs - 32 process permits, 10 recovery, what a
// 1 GiB container derives - and one formal query that has to wait asks every
// lookback permit to yield, not just one; the first given back is the
// query's.
func TestEveryLookbackYieldsToOneWaitingQueryAtTheMemoryShare(t *testing.T) {
	clock := newMutableClock(time.Unix(300, 0))
	limits := testRecoveryLimits()
	limits.ProcessQueryPermits, limits.RecoveryQueryPermits, limits.ReadyQueueCapacity = 32, 10, 64
	flights, err := NewFlightCoordinatorWithRecovery(limits, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	lookbacks := make([]*LookbackPermit, 0, 32)
	for range 32 {
		permit, refused := flights.TryAcquireLookbackPermit()
		if permit == nil {
			t.Fatalf("an idle pool of 32 refused lookback permit %d: %q", len(lookbacks)+1, refused)
		}
		lookbacks = append(lookbacks, permit)
	}
	if permit, refused := flights.TryAcquireLookbackPermit(); permit != nil || refused != LookbackRefusedFull {
		t.Fatalf("a 33rd lookback permit: %v %q, want refused as full", permit, refused)
	}
	granted := make(chan *QueryPermit, 1)
	go func() {
		permit, err := flights.AcquireQueryPermit(context.Background(),
			execution.SlotIdentity{QueryGroup: "qg-formal", EvaluationTime: 60},
			execution.OperationNormal, clock.Now().Add(time.Minute))
		if err != nil {
			t.Error(err)
		}
		granted <- permit
	}()
	for index, lookback := range lookbacks {
		select {
		case <-lookback.Yield():
		case <-testwait.Expired(t):
			t.Fatalf("a formal query waits and lookback permit %d of 32 was not asked to yield", index+1)
		}
	}
	lookbacks[17].Release()
	select {
	case permit := <-granted:
		permit.Release()
	case <-testwait.Expired(t):
		t.Fatal("the permit a lookback gave back did not reach the waiting query")
	}
	for _, lookback := range lookbacks {
		lookback.Release()
	}
	if occupancy := flights.QueryPermitOccupancy(); occupancy.LookbackInflight != 0 || occupancy.Budget != 32 {
		t.Fatalf("after every release %d lookback permits held of %d", occupancy.LookbackInflight, occupancy.Budget)
	}
}

// The occupancy's peak is the most permits held at once in the last minute,
// the lookback's included: it stays at the burst after the permits go back,
// a permit held from before the minute still counts, and a minute after the
// burst it is what is held then.
func TestThePermitPeakIsTheMostHeldInTheLastMinute(t *testing.T) {
	clock := newMutableClock(time.Unix(300, 0))
	flights := lookbackFlights(t, clock)
	normal := holdNormal(t, flights, clock, 5)
	if got := flights.QueryPermitOccupancy().PeakHeld; got != 5 {
		t.Fatalf("five formal permits held, peak %d, want 5", got)
	}
	normal[4].Release()
	if got := flights.QueryPermitOccupancy().PeakHeld; got != 5 {
		t.Fatalf("one of five formal permits given back, peak %d, want the five", got)
	}
	normal[4] = holdNormal(t, flights, clock, 1)[0]
	lookback, _ := flights.TryAcquireLookbackPermit()
	if lookback == nil {
		t.Fatal("an idle pool refused the lookback")
	}
	if got := flights.QueryPermitOccupancy().PeakHeld; got != 6 {
		t.Fatalf("five formal permits and a lookback held, peak %d, want 6", got)
	}
	for _, permit := range normal[1:] {
		permit.Release()
	}
	lookback.Release()
	clock.Advance(30 * time.Second)
	if got := flights.QueryPermitOccupancy().PeakHeld; got != 6 {
		t.Fatalf("30 s after the burst the peak is %d, want the burst's 6", got)
	}
	clock.Advance(31 * time.Second)
	if got := flights.QueryPermitOccupancy().PeakHeld; got != 1 {
		t.Fatalf("a minute after the burst, one permit still held from before it: peak %d, want 1", got)
	}
	normal[0].Release()
	if got := flights.QueryPermitOccupancy().PeakHeld; got != 0 {
		t.Fatalf("nothing held and nothing granted in the minute: peak %d, want 0", got)
	}
}

// The window is the minute before the read, the read's own second included:
// a value 59 s old counts, one 60 s old does not. A second ahead of the read
// - a wall clock stepped back after it was observed - is not counted, or it
// would hold the peak until real time caught up with it.
func TestSecondMaxKeepsExactlyTheLastMinute(t *testing.T) {
	var peak SecondMax
	at := time.Unix(1_000_000, 0)
	peak.Observe(at, 7)
	if got := peak.Max(at); got != 7 {
		t.Fatalf("read in the observed second: %d, want 7", got)
	}
	if got := peak.Max(at.Add(59 * time.Second)); got != 7 {
		t.Fatalf("59 s later: %d, want 7", got)
	}
	if got := peak.Max(at.Add(60 * time.Second)); got != 0 {
		t.Fatalf("60 s later: %d, want 0", got)
	}
	if got := peak.Max(at.Add(-time.Second)); got != 0 {
		t.Fatalf("read a second before the observation (clock stepped back): %d, want 0", got)
	}
}
