package scheduler

import (
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// deadlineDispatchFixture is a coordinator with one free permit, recovery's
// turn next, a normal hold history of holdRef, and one normal and one
// recovery waiter queued.
func deadlineDispatchFixture(t *testing.T, holdRef time.Duration, normalLeft time.Duration) (*FlightCoordinator, *queryPermitWaiter, *queryPermitWaiter) {
	t.Helper()
	now := time.Unix(1792071805, 0) // a Slot's permit window, 5 s before its :25 deadline
	f, err := NewFlightCoordinatorWithRecovery(testRecoveryLimits(), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	for range 20 {
		f.holds.Record(now, holdRef)
	}
	normal := &queryPermitWaiter{slot: execution.SlotIdentity{QueryGroup: "normal", EvaluationTime: 1792071780},
		operation: execution.OperationNormal, deadline: now.Add(normalLeft), grant: make(chan *QueryPermit, 1)}
	recovery := &queryPermitWaiter{slot: execution.SlotIdentity{QueryGroup: "takeover", EvaluationTime: 1792071720},
		operation: execution.OperationReplay, deadline: now.Add(10 * time.Minute), grant: make(chan *QueryPermit, 1)}
	f.mu.Lock()
	f.queryInflight = f.limits.ProcessQueryPermits - 1
	f.normalWaiters = []*queryPermitWaiter{normal}
	f.recoveryWaiters = []*queryPermitWaiter{recovery}
	f.nextRecovery = true
	f.mu.Unlock()
	return f, normal, recovery
}

func granted(w *queryPermitWaiter) bool {
	select {
	case p := <-w.grant:
		return p != nil
	default:
		return false
	}
}

// A normal waiter that would miss its deadline under 1:1 alternation gets the
// free permit ahead of a replay whose deadline is far off.
func TestDispatchServesNormalNearItsDeadlineBeforeRecovery(t *testing.T) {
	f, normal, recovery := deadlineDispatchFixture(t, time.Second, 800*time.Millisecond)
	f.mu.Lock()
	f.dispatchQueryPermitsLocked()
	next := f.nextRecovery
	f.mu.Unlock()
	if !granted(normal) || granted(recovery) {
		t.Fatal("the normal waiter due within its drain time must be served before recovery")
	}
	if !next {
		t.Fatal("recovery keeps its turn for the next permit once no normal waiter is due")
	}
}

// Far from any normal deadline the 1:1 alternation is unchanged.
func TestDispatchAlternatesWhenNoNormalWaiterIsDue(t *testing.T) {
	f, normal, recovery := deadlineDispatchFixture(t, time.Second, 20*time.Second)
	f.mu.Lock()
	f.dispatchQueryPermitsLocked()
	f.mu.Unlock()
	if granted(normal) || !granted(recovery) {
		t.Fatal("with recovery's turn and no normal waiter due, recovery takes the permit")
	}
}

// Before any normal hold is known there is nothing to derive a margin from:
// the alternation is unchanged.
func TestDispatchAlternatesWithoutHoldHistory(t *testing.T) {
	f, normal, recovery := deadlineDispatchFixture(t, 0, 500*time.Millisecond)
	f.mu.Lock()
	f.holds = HourlyDurations{}
	f.dispatchQueryPermitsLocked()
	f.mu.Unlock()
	if granted(normal) || !granted(recovery) {
		t.Fatal("without a hold reference recovery keeps its turn")
	}
}

// The margin counts the normal queue ahead: with more normal waiters than
// permits, a deadline a few holds away is already due.
func TestDispatchMarginGrowsWithNormalQueue(t *testing.T) {
	f, normal, recovery := deadlineDispatchFixture(t, time.Second, 2500*time.Millisecond)
	f.mu.Lock()
	for i := range 4 {
		f.normalWaiters = append(f.normalWaiters, &queryPermitWaiter{
			slot:      execution.SlotIdentity{QueryGroup: execution.QueryGroupIdentity(rune('a' + i)), EvaluationTime: 1792071780},
			operation: execution.OperationNormal, deadline: normal.deadline.Add(time.Minute), grant: make(chan *QueryPermit, 1)})
	}
	f.dispatchQueryPermitsLocked()
	f.mu.Unlock()
	// 5 normal waiters over 2 permits drain in 3 holds; a deadline 2.5 holds
	// away is due, so the one free permit goes to the normal queue.
	if granted(recovery) {
		t.Fatal("a normal deadline within the queue's drain time must keep the permit from recovery")
	}
}
