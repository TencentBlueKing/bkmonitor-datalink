package scheduler

import (
	"context"
	"errors"
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
// an entry; extended is a failed probe. recovered and query_revision_changed
// take it out, each on its own evidence: a query that answered, and a query
// that is no longer the one that failed.
const (
	QueryCooldownEntered              = "entered"
	QueryCooldownReentered            = "reentered"
	QueryCooldownExtended             = "extended"
	QueryCooldownRestored             = "restored"
	QueryCooldownRecovered            = "recovered"
	QueryCooldownQueryRevisionChanged = "query_revision_changed"
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
	// reason is why the latest failed query was unavailable.
	reason execution.ReasonCode
	// timeouts is how many of the failures were the backend not answering
	// before the deadline (QUERY_TIMEOUT), and firstTimeoutAt when the first
	// of them was: what a reader of the pool needs to say "in it since T
	// after N timeouts". Both clear with the failures.
	timeouts       uint32
	firstTimeoutAt time.Time
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
	// lastProbe is the latest execution the pool let run, kept across the
	// exit it may have caused.
	lastProbe *observability.QueryCooldownProbe
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
	Reason           execution.ReasonCode         `json:"reason,omitempty"`
	// Timeouts and FirstTimeoutAt are the failures that were timeouts and
	// when the first of them was. A record written before they existed reads
	// as none, which is what it had.
	Timeouts       uint32    `json:"timeouts,omitempty"`
	FirstTimeoutAt time.Time `json:"first_timeout_at"`
	// LastProbe is the latest execution the pool let run: what its answer
	// was, what budget it had and how long it took. A record written before
	// it existed has none.
	LastProbe *observability.QueryCooldownProbe `json:"last_probe,omitempty"`
}

// QueryCooldownKey is where a Query Group's record is kept under prefix. The
// store that writes it and the evidence read that shows it both build the key
// here, so the two cannot name different keys.
func QueryCooldownKey(prefix string, queryGroup execution.QueryGroupIdentity) string {
	return prefix + ":" + string(queryGroup)
}

// QueryCooldownStore keeps QueryCooldownRecords. A record that does not
// decode is no record (ErrQueryCooldownUndecodable): the Query Group starts
// outside the pool and is probed as before, never refused. A read that fails
// is read again on the Runner's next round, and the Runner writes nothing
// before a read succeeds. A save the store refuses because a later owner
// wrote the record is that owner's business, and nothing is retried.
type QueryCooldownStore interface {
	LoadQueryCooldown(ctx context.Context, queryGroup execution.QueryGroupIdentity) (QueryCooldownRecord, bool, error)
	SaveQueryCooldown(ctx context.Context, fence execution.OwnerFence, record QueryCooldownRecord) error
}

// ErrQueryCooldownUndecodable is a record that is there and does not decode.
// Reading it again gives the same bytes, so it is no record, and the next
// write may replace it; a read that failed is not that.
var ErrQueryCooldownUndecodable = errors.New("alarmd: query cooldown record does not decode")

// WithQueryCooldownStore keeps this Runner's pool membership in store.
func (runner *Runner) WithQueryCooldownStore(store QueryCooldownStore) *Runner {
	if runner != nil {
		runner.cooldownStore = store
	}
	return runner
}

// restoreQueryCooldown reads the persisted record on the Runner's first
// round that holds the Query Group: a Query Group in the pool comes back in it
// with the time it entered, and one that left keeps its last exit. A read
// that fails is made again on the next round, and until one succeeds nothing
// is written (saveQueryCooldown): the memory a failed read leaves is empty,
// and a write from it would replace the record under this owner's epoch -
// the entry time today, the re-entries zero, an entry counted that was not
// one - where the pool's identity is meant to survive restarts and owner
// changes. A record that does not decode is read as none, as before.
func (runner *Runner) restoreQueryCooldown(ctx context.Context, fence execution.OwnerFence) {
	runner.cooldownFence = fence
	if runner.cooldownLoaded {
		return
	}
	if runner.cooldownStore == nil {
		runner.cooldownLoaded = true
		return
	}
	record, found, err := runner.cooldownStore.LoadQueryCooldown(ctx, runner.queryGroup)
	if err != nil && !errors.Is(err, ErrQueryCooldownUndecodable) {
		return
	}
	runner.cooldownLoaded = true
	if err != nil || !found {
		return
	}
	memory := &runner.cooldownMemory
	memory.exitedAt, memory.exitReason, memory.reentries = record.ExitedAt, record.ExitReason, record.Reentries
	memory.lastProbe = record.LastProbe
	if record.Until.IsZero() {
		return
	}
	runner.queryCooldown = queryCooldownState{failures: record.Failures, queryRevision: record.QueryRevision,
		scheduleRevision: record.ScheduleRevision, segmentStart: record.SegmentStart,
		until: record.Until, lastQueryAt: record.LastQueryAt, reason: record.Reason,
		timeouts: record.Timeouts, firstTimeoutAt: record.FirstTimeoutAt}
	memory.enteredAt, memory.source = record.EnteredAt, QueryCooldownRestored
	runner.emitQueryCooldown(ctx, QueryCooldownRestored)
}

// saveQueryCooldown writes the pool state as it is now. Only on a change of
// it -- an entry, an extension, an exit, a probe brought forward -- so its
// cost is the pool's transitions, not its rounds.
func (runner *Runner) saveQueryCooldown(ctx context.Context) {
	if runner.cooldownStore == nil || runner.cooldownFence.OwnerEpoch == 0 || !runner.cooldownLoaded {
		return
	}
	state, memory := runner.queryCooldown, runner.cooldownMemory
	_ = runner.cooldownStore.SaveQueryCooldown(ctx, runner.cooldownFence, QueryCooldownRecord{
		QueryGroup: runner.queryGroup, OwnerEpoch: runner.cooldownFence.OwnerEpoch,
		EnteredAt: memory.enteredAt, Until: state.until, LastQueryAt: state.lastQueryAt, Failures: state.failures,
		QueryRevision: state.queryRevision, ScheduleRevision: state.scheduleRevision, SegmentStart: state.segmentStart,
		ExitedAt: memory.exitedAt, ExitReason: memory.exitReason, Reentries: memory.reentries, Reason: state.reason,
		Timeouts: state.timeouts, FirstTimeoutAt: state.firstTimeoutAt, LastProbe: memory.lastProbe,
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

type queryCooldownHeldKey struct{}

// withQueryCooldownHeld tells the Slot source that this Query Group's
// queries are held by the degraded pool now. A Slot due before its takeover
// is then not replayed past the distance rule: the replay's query would be
// held like every other, the Slot would be classified again each time the
// Runner woke and never run, and the pool's Slots are given up on for
// distance by design, takeover or not.
func withQueryCooldownHeld(ctx context.Context, held bool) context.Context {
	if !held {
		return ctx
	}
	return context.WithValue(ctx, queryCooldownHeldKey{}, true)
}

// queryCooldownHeld reports what withQueryCooldownHeld said; false when
// nothing did.
func queryCooldownHeld(ctx context.Context) bool {
	held, _ := ctx.Value(queryCooldownHeldKey{}).(bool)
	return held
}

// queryCooldownHolds reports whether the pool holds this Query Group's
// queries now: the cooldown has not run out. The Slot's own
// checks (deferUnavailableQuery) can still let one through -- an expired
// range, a Slot past its maintenance bound -- and none of them is a replay.
func (runner *Runner) queryCooldownHolds() bool {
	state := runner.queryCooldown
	return !state.until.IsZero() && runner.now().Before(state.until)
}

func (runner *Runner) deferUnavailableQuery(ctx context.Context, slot FrozenSlot) bool {
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

// queryCooldownLetsRun reports whether an execution starting now runs while
// the Query Group is in the pool: the pool let it through - its cooldown ran
// out, or the Slot's own checks let it run (deferUnavailableQuery) - and its
// answer decides whether the Query Group stays. Such an execution runs under
// the budget a normal round has (execution.WithNormalQueryBudget), so the
// pool is left only on an answer the Query Group's normal rounds can get too:
// a retry or replay otherwise has its whole interval from its arrival, and a
// backend slower than a normal round's budget answered it every time and was
// let out, to time out again on the normal rounds and come back.
func (runner *Runner) queryCooldownLetsRun() bool {
	return !runner.queryCooldown.until.IsZero()
}

func (runner *Runner) recordQueryAvailability(ctx context.Context, slot FrozenSlot, operation execution.Operation,
	result execution.SlotExecutionResult, intervalSeconds int64, decides bool) {
	if !result.Completed || slot.ExpiredRange != nil || slot.Recovery.Disposition == ReplayExpired {
		return
	}
	state := &runner.queryCooldown
	if decides {
		runner.cooldownMemory.lastProbe = queryCooldownProbe(runner.now(), operation, result)
	}
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
	state.reason = result.QueryUnavailableReason
	if state.failures < 32 {
		state.failures++
	}
	if state.reason == execution.ReasonCode(contract.ReasonQueryTimeout) {
		if state.timeouts == 0 {
			state.firstTimeoutAt = runner.now()
		}
		if state.timeouts < 32 {
			state.timeouts++
		}
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
	result, reason := queryCooldownOutcome(event, state.reason)
	runner.flights.observer.Observe(ctx, observability.Observation{
		Component: observability.ComponentScheduler, Stage: observability.StageQueryCooldown,
		Result: result, ReasonCode: reason, Direction: observability.DirectionInternal,
		Trace: observability.TraceFields{QueryGroupKey: string(runner.queryGroup)},
		QueryCooldown: &observability.QueryCooldownFacts{Event: event, Until: state.until,
			LastQueryAt: state.lastQueryAt, Failures: state.failures, Timeouts: state.timeouts, FirstTimeoutAt: state.firstTimeoutAt,
			EnteredAt: memory.enteredAt, Source: memory.source,
			LastExitAt: memory.exitedAt, LastExitReason: memory.exitReason, Reentries: memory.reentries,
			LastProbe: memory.lastProbe},
	})
}

// queryCooldownOutcome is the result and reason a cooldown transition carries
// on its line. The transition used to carry neither: the line's result read
// _other and its reason reason_not_reported, while the event word sat in the
// facts. Entering or extending the cooldown is the Query Group degraded by
// its query being unavailable, under the reason that query failed with;
// leaving it on a query that answered is normal dispatch resumed, and the
// reason it resumed from travels with it; leaving it because the
// configuration changed or the policy was switched off is neither, and
// carries no reason. A failure whose reason was not recorded - a record kept
// before records carried one - says so, rather than taking the backend's
// word for a failure that may have been the strategy's.
func queryCooldownOutcome(event string, failed execution.ReasonCode) (observability.Result, observability.ReasonCode) {
	if failed == "" {
		failed = execution.ReasonQueryReasonUnrecorded
	}
	switch event {
	case QueryCooldownEntered, QueryCooldownReentered, QueryCooldownExtended, QueryCooldownRestored:
		return observability.ResultDegraded, observability.ReasonCode(failed)
	case QueryCooldownRecovered:
		return observability.ResultResumed, observability.ReasonCode(failed)
	default:
		return observability.ResultSuccess, observability.ReasonNone
	}
}

// queryCooldownProbe is what one execution the pool let run proved and had.
func queryCooldownProbe(at time.Time, operation execution.Operation, result execution.SlotExecutionResult) *observability.QueryCooldownProbe {
	probe := &observability.QueryCooldownProbe{At: at, Operation: string(operation), Outcome: observability.QueryCooldownProbeUnknown}
	switch result.QueryAvailability {
	case execution.QueryAvailabilityAvailable:
		probe.Outcome = observability.QueryCooldownProbeAnswered
	case execution.QueryAvailabilityUnavailable:
		probe.Outcome = observability.QueryCooldownProbeUnavailable
	}
	if clock := result.PrimaryQueryClock; clock != nil {
		probe.Measured, probe.BudgetMillis, probe.ElapsedMillis = true, clock.BudgetMillis, clock.ElapsedMillis
	}
	return probe
}
