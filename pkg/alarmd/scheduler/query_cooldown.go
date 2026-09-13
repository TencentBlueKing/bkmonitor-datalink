package scheduler

import (
	"context"
	"hash/fnv"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

const unavailableThreshold = 3

// This is per owned Runner, not per frozen Slot. No history, timers or I/O.
type queryCooldownState struct {
	failures                   uint32
	lastSlot                   execution.SlotIdentity
	queryRevision              execution.QueryRevision
	scheduleRevision           execution.ScheduleRevision
	segmentStart               execution.EvaluationTime
	until, wakeAt, lastQueryAt time.Time
}

func (runner *Runner) clearQueryCooldown(ctx context.Context, event string) {
	if !runner.queryCooldown.until.IsZero() {
		runner.queryCooldown.until = time.Time{}
		runner.emitQueryCooldown(ctx, event)
	}
	runner.queryCooldown = queryCooldownState{}
}

func (runner *Runner) deferUnavailableQuery(ctx context.Context, slot FrozenSlot) bool {
	if !runner.flights.limits.QueryUnavailableCooldown {
		runner.clearQueryCooldown(ctx, "disabled")
		return false
	}
	state := &runner.queryCooldown
	if state.failures > 0 && (state.queryRevision != slot.Contract.QueryRevision ||
		state.scheduleRevision != slot.Contract.ScheduleRevision ||
		state.segmentStart != slot.Contract.ScheduleSegmentStart) {
		runner.clearQueryCooldown(ctx, "config_changed")
	}
	if state.until.IsZero() || !runner.now().Before(state.until) ||
		slot.ExpiredRange != nil || slot.Recovery.Disposition == ReplayExpired ||
		runner.attempt != nil {
		return false
	}
	// Unknown deadlines fail open to execution; never invent a maintenance bound.
	if slot.RecoveryUntilUnixMilli <= runner.now().UnixMilli() {
		return false
	}
	state.wakeAt = state.until
	if deadline := time.UnixMilli(slot.RecoveryUntilUnixMilli); deadline.Before(state.wakeAt) {
		state.wakeAt = deadline
	}
	if recheck := slot.Recovery.RecheckAtUnixMilli; recheck > runner.now().UnixMilli() {
		if at := time.UnixMilli(recheck); at.Before(state.wakeAt) {
			state.wakeAt = at
		}
	}
	return true
}

func (runner *Runner) recordQueryAvailability(ctx context.Context, slot FrozenSlot, result execution.SlotExecutionResult, intervalSeconds int64) {
	if !runner.flights.limits.QueryUnavailableCooldown || !result.Completed ||
		slot.ExpiredRange != nil || slot.Recovery.Disposition == ReplayExpired {
		return
	}
	if result.QueryAvailability == execution.QueryAvailabilityUnknown {
		return
	}
	state := &runner.queryCooldown
	if result.QueryAvailability == execution.QueryAvailabilityAvailable {
		state.lastQueryAt = runner.now()
		runner.clearQueryCooldown(ctx, "recovered")
		return
	}
	if result.QueryAvailability != execution.QueryAvailabilityUnavailable || state.lastSlot == slot.Contract.Slot {
		return
	}
	state.lastSlot = slot.Contract.Slot
	state.queryRevision = slot.Contract.QueryRevision
	state.scheduleRevision = slot.Contract.ScheduleRevision
	state.segmentStart = slot.Contract.ScheduleSegmentStart
	state.lastQueryAt = runner.now()
	if state.failures < 32 {
		state.failures++
	}
	// A missing or extreme interval is not permission to guess a cooldown.
	if state.failures < unavailableThreshold || intervalSeconds <= 0 ||
		intervalSeconds > int64((24*time.Hour)/time.Second) {
		return
	}
	event := "extended"
	if state.until.IsZero() {
		event = "entered"
	}
	state.until = runner.now().Add(queryCooldownDelay(runner.queryGroup, time.Duration(intervalSeconds)*time.Second, state.failures))
	runner.emitQueryCooldown(ctx, event)
}

func queryCooldownDelay(qg execution.QueryGroupIdentity, period time.Duration, failures uint32) time.Duration {
	base := 2 * period
	capDelay := 5 * time.Minute
	if base > capDelay {
		capDelay = base
	}
	delay := base
	for i := uint32(unavailableThreshold); i < failures && delay < capDelay; i++ {
		if delay > capDelay/2 {
			delay = capDelay
		} else {
			delay *= 2
		}
	}
	// Spread a synchronized population over at least its natural period.
	// A small percentage jitter would recreate the same permit-pool burst.
	spread := delay / 2
	if spread < period {
		spread = period
	}
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(qg))
	_, _ = hash.Write([]byte{byte(failures)})
	return delay - time.Duration(hash.Sum64()%uint64(spread))
}

func (runner *Runner) emitQueryCooldown(ctx context.Context, event string) {
	if runner.flights.observer == nil {
		return
	}
	defer func() { _ = recover() }()
	state := runner.queryCooldown
	runner.flights.observer.Observe(ctx, observability.Observation{
		Component: observability.ComponentScheduler, Stage: observability.StageQueryCooldown,
		Trace: observability.TraceFields{QueryGroupKey: string(runner.queryGroup)},
		QueryCooldown: &observability.QueryCooldownFacts{Event: event, Until: state.until,
			LastQueryAt: state.lastQueryAt, Failures: state.failures},
	})
}
