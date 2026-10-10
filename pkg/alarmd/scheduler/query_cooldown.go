package scheduler

import (
	"context"
	"hash/fnv"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

const unavailableThreshold = 3

// QueryCooldownReentryWindow is how soon after leaving the pool an entry
// counts as a re-entry. A pool that is stable has next to none: a Query Group
// leaves on evidence -- a query that answered, or a query that changed -- and
// one that comes straight back is either a backend that answers now and then
// or an exit that had no business happening.
const QueryCooldownReentryWindow = 24 * time.Hour

// The pool events, closed. entered and reentered put a Query Group in the
// pool on this process's own failed queries; restored puts it back from its
// persisted record, after a restart or a change of owner, without counting
// an entry; extended is a failed probe. recovered, query_revision_changed
// and disabled take it out, each on its own evidence: a query that
// answered, a query that is no longer the one that failed, a deployment
// that switched the pool off.
const (
	QueryCooldownEntered              = "entered"
	QueryCooldownReentered            = "reentered"
	QueryCooldownExtended             = "extended"
	QueryCooldownRestored             = "restored"
	QueryCooldownRecovered            = "recovered"
	QueryCooldownQueryRevisionChanged = "query_revision_changed"
	QueryCooldownDisabled             = "disabled"
)

// This is per owned Runner, not per frozen Slot. No history or timers; what
// outlives the Runner is its QueryCooldownRecord, when a store is attached.
type queryCooldownState struct {
	failures                   uint32
	lastSlot                   execution.SlotIdentity
	queryRevision              execution.QueryRevision
	scheduleRevision           execution.ScheduleRevision
	segmentStart               execution.EvaluationTime
	until, wakeAt, lastQueryAt time.Time
}

// queryCooldownMemory is what the pool remembers across membership: since
// when the Query Group has been in it and how it got there, and its last
// exit, which is what makes the next entry a re-entry.
type queryCooldownMemory struct {
	enteredAt  time.Time
	source     string
	exitedAt   time.Time
	exitReason string
	reentries  uint32
}

// QueryCooldownRecord is one Query Group's place in the pool as it is kept
// outside the process. Pool membership used to live in the Runner alone, so a
// restart emptied the pool and a change of owner dropped the Query Group from
// it, and both put it back through three more failed queries: in and out with
// no evidence either way. The record carries it across both. OwnerEpoch is
// the owner that wrote it, so an owner that has been replaced cannot write
// over its successor. A record whose Until is zero is not in the pool; it is
// kept for its last exit.
type QueryCooldownRecord struct {
	QueryGroup       execution.QueryGroupIdentity `json:"query_group"`
	OwnerEpoch       uint64                       `json:"owner_epoch"`
	EnteredAt        time.Time                    `json:"entered_at"`
	Until            time.Time                    `json:"until"`
	LastQueryAt      time.Time                    `json:"last_query_at"`
	Failures         uint32                       `json:"failures"`
	QueryRevision    execution.QueryRevision      `json:"query_revision,omitempty"`
	ScheduleRevision execution.ScheduleRevision   `json:"schedule_revision,omitempty"`
	SegmentStart     execution.EvaluationTime     `json:"segment_start,omitempty"`
	ExitedAt         time.Time                    `json:"exited_at"`
	ExitReason       string                       `json:"exit_reason,omitempty"`
	Reentries        uint32                       `json:"reentries,omitempty"`
}

// QueryCooldownKey is where a Query Group's record is kept under prefix. The
// store that writes it and the evidence read that shows it both build the key
// here, so the two cannot name different keys.
func QueryCooldownKey(prefix string, queryGroup execution.QueryGroupIdentity) string {
	return prefix + ":" + string(queryGroup)
}

// QueryCooldownStore keeps QueryCooldownRecords. A record that cannot be read
// is no record: the Query Group starts outside the pool and is probed as
// before, never refused. A save the store refuses because a later owner
// wrote the record is that owner's business, and nothing is retried.
type QueryCooldownStore interface {
	LoadQueryCooldown(ctx context.Context, queryGroup execution.QueryGroupIdentity) (QueryCooldownRecord, bool, error)
	SaveQueryCooldown(ctx context.Context, fence execution.OwnerFence, record QueryCooldownRecord) error
}

// WithQueryCooldownStore keeps this Runner's pool membership in store.
func (runner *Runner) WithQueryCooldownStore(store QueryCooldownStore) *Runner {
	if runner != nil {
		runner.cooldownStore = store
	}
	return runner
}

// restoreQueryCooldown reads the persisted record once, on the Runner's first
// round that holds the Query Group: a Query Group in the pool comes back in it
// with the time it entered, and one that left keeps its last exit.
func (runner *Runner) restoreQueryCooldown(ctx context.Context, fence execution.OwnerFence) {
	runner.cooldownFence = fence
	if runner.cooldownLoaded {
		return
	}
	runner.cooldownLoaded = true
	if runner.cooldownStore == nil {
		return
	}
	record, found, err := runner.cooldownStore.LoadQueryCooldown(ctx, runner.queryGroup)
	if err != nil || !found {
		return
	}
	memory := &runner.cooldownMemory
	memory.exitedAt, memory.exitReason, memory.reentries = record.ExitedAt, record.ExitReason, record.Reentries
	if record.Until.IsZero() {
		return
	}
	runner.queryCooldown = queryCooldownState{failures: record.Failures, queryRevision: record.QueryRevision,
		scheduleRevision: record.ScheduleRevision, segmentStart: record.SegmentStart,
		until: record.Until, lastQueryAt: record.LastQueryAt}
	memory.enteredAt, memory.source = record.EnteredAt, QueryCooldownRestored
	runner.emitQueryCooldown(ctx, QueryCooldownRestored)
}

// saveQueryCooldown writes the pool state as it is now. Only on a change of
// it -- an entry, an extension, an exit, a probe brought forward -- so its
// cost is the pool's transitions, not its rounds.
func (runner *Runner) saveQueryCooldown(ctx context.Context) {
	if runner.cooldownStore == nil || runner.cooldownFence.OwnerEpoch == 0 {
		return
	}
	state, memory := runner.queryCooldown, runner.cooldownMemory
	_ = runner.cooldownStore.SaveQueryCooldown(ctx, runner.cooldownFence, QueryCooldownRecord{
		QueryGroup: runner.queryGroup, OwnerEpoch: runner.cooldownFence.OwnerEpoch,
		EnteredAt: memory.enteredAt, Until: state.until, LastQueryAt: state.lastQueryAt, Failures: state.failures,
		QueryRevision: state.queryRevision, ScheduleRevision: state.scheduleRevision, SegmentStart: state.segmentStart,
		ExitedAt: memory.exitedAt, ExitReason: memory.exitReason, Reentries: memory.reentries,
	})
}

func (runner *Runner) clearQueryCooldown(ctx context.Context, event string) {
	if !runner.queryCooldown.until.IsZero() {
		runner.queryCooldown.until = time.Time{}
		runner.cooldownMemory.exitedAt, runner.cooldownMemory.exitReason = runner.now(), event
		runner.emitQueryCooldown(ctx, event)
		runner.queryCooldown = queryCooldownState{}
		runner.cooldownMemory.enteredAt, runner.cooldownMemory.source = time.Time{}, ""
		runner.saveQueryCooldown(ctx)
		return
	}
	runner.queryCooldown = queryCooldownState{}
}

func (runner *Runner) deferUnavailableQuery(ctx context.Context, slot FrozenSlot) bool {
	if !runner.flights.limits.QueryUnavailableCooldown {
		runner.clearQueryCooldown(ctx, QueryCooldownDisabled)
		return false
	}
	state := &runner.queryCooldown
	switch {
	case state.failures > 0 && state.queryRevision != slot.Contract.QueryRevision:
		// The query that failed is not the one that will run: the failures
		// were about something else.
		runner.clearQueryCooldown(ctx, QueryCooldownQueryRevisionChanged)
	case state.failures > 0 && (state.scheduleRevision != slot.Contract.ScheduleRevision ||
		state.segmentStart != slot.Contract.ScheduleSegmentStart):
		// The same query under another schedule or Segment -- another
		// strategy joined the Query Group, or a new build cut its content.
		// That is no evidence about the backend: the Query Group stays in the
		// pool and is probed once, now, and the probe decides.
		state.scheduleRevision, state.segmentStart = slot.Contract.ScheduleRevision, slot.Contract.ScheduleSegmentStart
		if !state.until.IsZero() {
			if runner.now().Before(state.until) {
				state.until = runner.now()
			}
			runner.saveQueryCooldown(ctx)
		}
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
	state := &runner.queryCooldown
	if result.QueryAvailability == execution.QueryAvailabilityUnknown {
		// A probe that proved nothing either way. The Query Group stays in
		// the pool, and the next probe waits a period instead of every Slot
		// after this one running as if the pool had let it go.
		if !state.until.IsZero() && !runner.now().Before(state.until) && intervalSeconds > 0 &&
			intervalSeconds <= int64((24*time.Hour)/time.Second) {
			state.until = runner.now().Add(time.Duration(intervalSeconds) * time.Second)
			runner.saveQueryCooldown(ctx)
		}
		return
	}
	if result.QueryAvailability == execution.QueryAvailabilityAvailable {
		state.lastQueryAt = runner.now()
		runner.clearQueryCooldown(ctx, QueryCooldownRecovered)
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
	event := QueryCooldownExtended
	if state.until.IsZero() {
		memory := &runner.cooldownMemory
		event, memory.enteredAt, memory.source = QueryCooldownEntered, runner.now(), "probe"
		if !memory.exitedAt.IsZero() && runner.now().Sub(memory.exitedAt) < QueryCooldownReentryWindow {
			event = QueryCooldownReentered
			memory.reentries++
		}
	}
	state.until = runner.now().Add(queryCooldownDelay(runner.queryGroup, time.Duration(intervalSeconds)*time.Second, state.failures))
	runner.emitQueryCooldown(ctx, event)
	runner.saveQueryCooldown(ctx)
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
	state, memory := runner.queryCooldown, runner.cooldownMemory
	result, reason := queryCooldownOutcome(event)
	runner.flights.observer.Observe(ctx, observability.Observation{
		Component: observability.ComponentScheduler, Stage: observability.StageQueryCooldown,
		Result: result, ReasonCode: reason, Direction: observability.DirectionInternal,
		Trace: observability.TraceFields{QueryGroupKey: string(runner.queryGroup)},
		QueryCooldown: &observability.QueryCooldownFacts{Event: event, Until: state.until,
			LastQueryAt: state.lastQueryAt, Failures: state.failures,
			EnteredAt: memory.enteredAt, Source: memory.source,
			LastExitAt: memory.exitedAt, LastExitReason: memory.exitReason, Reentries: memory.reentries},
	})
}

// queryCooldownOutcome is the result and reason a cooldown transition carries
// on its line. The transition used to carry neither: the line's result read
// _other and its reason reason_not_reported, while the event word sat in the
// facts. Entering or extending the cooldown is the Query Group degraded by
// its query being unavailable; leaving it on a query that answered is normal
// dispatch resumed, and the reason it resumed from travels with it; leaving
// it because the configuration changed or the policy was switched off is
// neither, and carries no reason.
func queryCooldownOutcome(event string) (observability.Result, observability.ReasonCode) {
	switch event {
	case QueryCooldownEntered, QueryCooldownReentered, QueryCooldownExtended, QueryCooldownRestored:
		return observability.ResultDegraded, observability.ReasonCode(contract.ReasonQueryUnavailable)
	case QueryCooldownRecovered:
		return observability.ResultResumed, observability.ReasonCode(contract.ReasonQueryUnavailable)
	default:
		return observability.ResultSuccess, observability.ReasonNone
	}
}
