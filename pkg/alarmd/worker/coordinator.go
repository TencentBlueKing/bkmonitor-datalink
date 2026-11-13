// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package worker contains the single in-process phase-two execution
// coordinator. It is not a service and owns no retry queue or dependency
// implementation.
package worker

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/inputslice"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sourceliveness"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/targetplan"
)

type Ports struct {
	// EffectiveTime supplies bounded legacy facts; nil yields UNKNOWN for legacy schedules.
	EffectiveTime strategy.EffectiveTimeProvider
	// Now is the wall clock the effective time is judged at: the moment the
	// worker decides a Level's Trigger and Recovery, not the Slot's
	// evaluation time. Optional: nil is time.Now.
	Now          func() time.Time
	Finalization execution.QueryFreeFinalizationSource
	Activation   execution.PlanActivationSource
	Query        execution.QueryExecutionSource
	Sequencer    execution.SideEffectSequencer
	Evaluator    execution.Evaluator
	Admission    execution.SideEffectAdmitter
	GapGuard     execution.GapGuardStore
	// NoData is required, like every other port here. A worker without it would
	// evaluate every Plan's thresholds and none of their absence, and the only
	// sign would be no-data alerts that never fire - which is indistinguishable
	// from nothing being absent.
	NoData execution.PlanNoDataStore
	// NoDataEnds keeps the no-data end index (execution.NoDataEndIndex).
	// Optional: without it a level written to end on no data ends only while
	// its group is in the answer.
	NoDataEnds execution.NoDataEndIndex
	// SourceLiveness is asked, before a Level written to end on no data ends
	// an absent series' alert, whether the Plan's source is alive
	// (sourceliveness): not shown alive, the end is held and the entry kept.
	// The Evaluator asks the same judge for a present series. Optional: nil
	// ends on no data as configured.
	SourceLiveness sourceliveness.Judge
	// Hosts resolves which business a host belongs to, which decides both what
	// a no-data roster expects and which of its groups have left. Required for
	// the same reason: without it every declared host would read as unknown, so
	// every static target would expect nothing and no absence would ever be
	// reported - a silence that looks exactly like health.
	Hosts execution.HostBusiness
	// NoDataManualCloses is read just before a batch's no-data alerts go to
	// the sink, to drop the ones a user closed while the round ran
	// (decision-024). Optional: without it nothing is checked there, and a
	// close still stops the absence at the Plan's next Slot.
	NoDataManualCloses execution.NoDataManualCloseReader
	Events             execution.EventSink
	State              execution.StateStore
	Progress           execution.ProgressStore
	Observer           execution.Observer
	// OpenAlerts is required: a worker that evaluates without it sends every
	// RECOVERY envelope, and the trigger counts that as not_configured, which
	// on a production worker is the wiring having come apart.
	OpenAlerts execution.OpenAlertCopy
	// StateHorizon is the platform no-data tracking horizon H in force now,
	// in seconds. A series' runtime state lives at most H past its last
	// write, so one that stops appearing leaves nothing behind for longer
	// than H. Optional: nil leaves the retention's own lifetime uncapped.
	StateHorizon func() int64
	// ExecutionEvidence records and reads how far an earlier attempt at a Slot
	// got. It is the one optional port: nil keeps exactly the behaviour of
	// builds before it existed, which is that a Slot finalized after its replay
	// window reads as never evaluated. A deployment without it loses a reading,
	// not a detection.
	ExecutionEvidence execution.SlotExecutionEvidenceStore
	// Targets resolves a Plan's target plan for one Slot (decision-017). It
	// is optional the way ExecutionEvidence is, and for a safer reason: a
	// worker without it does not run target-plan Plans on no target, it
	// admits none of their records (target_plan_unresolved) and judges none
	// of their absence (SKIPPED_TARGET_SELECTOR_UNAVAILABLE), both by name.
	// Plans without a target plan never touch it.
	Targets TargetResolver
	// ScopeGroups reads the dynamic groups a Plan's target scope names, for
	// one Slot. Optional as Targets is: a worker without it admits none of
	// those conditions' records (dynamic_group_unavailable), by name. Plans
	// whose targets name no dynamic group never touch it.
	ScopeGroups ScopeGroupResolver
	// RetainedBorrower is the State cache, which holds what no Slot has
	// reserved of the retained pool and gives it back the moment a Slot
	// needs it; the coordinator lends it that room when it is built.
	// Optional: without it the pool has no borrower.
	RetainedBorrower execution.RetainedBytesBorrower
}

// ScopeGroupResolver reads one dynamic group a target scope names for one
// Slot. cmdbcache's platform group stores implement it; they read nothing
// from Redis on the Slot path but a group's first reference.
type ScopeGroupResolver interface {
	// ResolveScopeGroup answers one group of a tenant; interval is the
	// asking Plan's evaluation period, which bounds how long the group is
	// kept without being asked for.
	ResolveScopeGroup(ctx context.Context, tenant, id string, interval time.Duration) targetplan.SelectorResult
}

// TargetResolver resolves one target plan for one Slot. cmdbcache's
// resolver implements it; it reads nothing from Redis on the Slot path but
// a group's first reference.
type TargetResolver interface {
	// ResolveFor answers one plan of a tenant for one Slot, against that
	// tenant's CMDB cache; interval is the Plan's evaluation period, which
	// bounds how long its groups are kept without being asked for.
	ResolveFor(ctx context.Context, tenant string, plan *contract.TargetPlanV1, interval time.Duration) *targetplan.Resolution
}

type SlotExecutionCoordinator struct {
	ports        Ports
	budget       ProvisionalBudget
	reservations processProvisionalReservations
	// slicedInput is each Query Group's last read on this replica, for its
	// next Slot's first slices (sliced_input.go).
	slicedInput sliceTrees
	// noDataSkips is how long each Plan's no-data detection has been skipping.
	// One per process, because a streak is about rounds rather than about one
	// Slot. See no_data_skip_streak.go.
	noDataSkips noDataSkipStreaks
	// effectiveSeen is when each Plan's effective-time requirement came into
	// effect for this process, the bound below which a window hole's minute
	// is not judged against the schedule. See out_of_hours.go.
	effectiveSeen effectiveTimeSeen
	// holders is who holds the retained budget now, by Slot
	// (RetainedHolders): the pool's figure says how much, this says whose.
	holders retainedHolderRegistry
	// noDataSegment is how many fields a no-data memory's scan asks for at
	// once and how many names an absence window reads; zero is
	// execution.NoDataRosterSegmentGroups. A test sets it to cut a small
	// memory into many segments.
	noDataSegment int
}

type processProvisionalReservations struct {
	mu            sync.Mutex
	series        uint64
	retainedBytes uint64
	states        uint64
	events        uint64
	gaps          uint64
	gapFacts      uint64
}

type ProvisionalBudget struct {
	MaxSeries         uint64
	MaxRetainedBytes  uint64
	MaxStateMutations uint64
	MaxEvents         uint64
	MaxGapMutations   uint64
	// StoreMaxItems is the State store's per-call item bound. One Plan's
	// State or Gap mutations beyond it are applied in successive calls of at
	// most this size, up to execution.StateApplyMaxChunks calls, which also
	// caps what one Slot may produce (see slotBudget). Zero means the store
	// has no bound below the process budget.
	StoreMaxItems uint64
	// QueryPermits is the process's query permits (ProcessQueryPermits), what
	// the provider is asked at once across every Slot; a Slot read in slices
	// reads at most sliceReadWidth of them at once. Zero, as tests build it,
	// reads one slice ahead.
	QueryPermits int
}

// sliceReadWidth is how many slices of one Slot are read at once: the one it
// is on and the ones read ahead. Half the process's query permits at most,
// so one large Query Group never takes them all, and four at most: the
// share is split among the slices in flight (sliceReadAheadLimit), and a
// fifth would leave each too small to be worth its request. A constant, not
// a setting: what the provider can take in all is the permits' to say, and
// how many of them one Query Group uses the program knows better than an
// operator (decision-020 throughput design 3.1, way 6).
func (coordinator *SlotExecutionCoordinator) sliceReadWidth() int {
	if width := sliceReadWidthForMeasurement.Load(); width > 0 {
		return int(width)
	}
	if coordinator.budget.QueryPermits <= 0 {
		return 2
	}
	return min(4, max(1, coordinator.budget.QueryPermits/2))
}

type activationProtectionRequiredError struct {
	activationRequest execution.PlanActivationRequest
	currentFacts      execution.PlanActivationResult
	activations       execution.PlanActivationResult
	completion        execution.SlotCompletion
	// completionCause travels with the completion because the converge path
	// commits that completion rather than deriving its own. Without it the
	// whole path reported the completion and dropped the one field that says
	// which of the conditions folded into UNAVAILABLE actually happened.
	completionCause execution.CompletionAttribution
	// guardReason is the word the Guards written for activations carry:
	// classified over those Plans alone, since a Plan that is gone gets no
	// Guard and its word must not land on the Guard of one that came back.
	guardReason execution.ReasonCode
}

func (*activationProtectionRequiredError) Error() string {
	return "alarmd worker: changed activation requires Guard protection"
}

// now is the wall clock of a decision (Ports.Now).
func (coordinator *SlotExecutionCoordinator) now() time.Time {
	if coordinator.ports.Now != nil {
		return coordinator.ports.Now()
	}
	return time.Now()
}

func NewSlotExecutionCoordinator(ports Ports, budget ProvisionalBudget) (*SlotExecutionCoordinator, error) {
	if ports.Finalization == nil || ports.Activation == nil || ports.Query == nil || ports.Sequencer == nil ||
		ports.Evaluator == nil || ports.Admission == nil || ports.GapGuard == nil || ports.NoData == nil || ports.Hosts == nil ||
		ports.Events == nil || ports.State == nil || ports.Progress == nil || ports.Observer == nil ||
		ports.OpenAlerts == nil {
		return nil, errors.New("alarmd worker: all C0 execution ports are required")
	}
	if budget.MaxSeries == 0 || budget.MaxRetainedBytes == 0 || budget.MaxStateMutations == 0 ||
		budget.MaxEvents == 0 || budget.MaxGapMutations == 0 {
		return nil, errors.New("alarmd worker: positive process provisional budgets are required")
	}
	coordinator := &SlotExecutionCoordinator{ports: ports, budget: budget}
	if borrower := ports.RetainedBorrower; borrower != nil {
		borrower.BorrowRetainedWithin(func() uint64 { return RetainedRoom(coordinator) })
	}
	return coordinator, nil
}

func (coordinator *SlotExecutionCoordinator) acquireProvisional(series, retainedBytes uint64, stream *streamedExecution, phase string) error {
	coordinator.reservations.mu.Lock()
	// The bytes are the budget and are judged first; the series count is a
	// guardrail at the smallest a series can encode, which binds only when
	// the byte accounting has gone wrong. A refusal the stream's read ahead
	// may stand behind is judged again once it has yielded (yieldReadAhead).
	for {
		err := coordinator.judgeRetainedLocked(stream, retainedBytes, phase)
		if err == nil {
			break
		}
		coordinator.reservations.mu.Unlock()
		if !stream.yieldReadAhead() {
			return err
		}
		coordinator.reservations.mu.Lock()
	}
	defer coordinator.reservations.mu.Unlock()
	if series > coordinator.budget.MaxSeries-coordinator.reservations.series {
		return budgetRejection(observability.CapacityBudgetSeries, phase, coordinator.reservations.series, series, coordinator.budget.MaxSeries, stream.ownBudget(observability.CapacityBudgetSeries), stream.ownBudgetUsage(coordinator.budget))
	}
	coordinator.reservations.series += series
	coordinator.reservations.retainedBytes += retainedBytes
	return nil
}

func (coordinator *SlotExecutionCoordinator) releaseProvisional(series, retainedBytes uint64) {
	coordinator.reservations.mu.Lock()
	coordinator.reservations.series -= series
	coordinator.reservations.retainedBytes -= retainedBytes
	coordinator.reservations.mu.Unlock()
}

// judgeRetainedLocked judges retainedBytes more that stream asks to hold,
// under reservations.mu: against the stream's share first, then against the
// pool. Every byte a Query Group's Slot holds is judged here - its input, its
// State and output, its gap facts, its no-data memory and first writes, the
// frames it keeps for its writes - so a Slot within its share that the pool
// refuses is refused for what others hold (RESOURCE_POOL_HELD_BY_OTHERS),
// never for being too large. Share before pool and against the stream's own
// total, not what is free: a share measured against what happens to be free
// would mean a different thing every round. Every Slot's execution is judged
// against its share, a query-free one's included; the pool's refusal of one
// that began is named for the others holding it, and what belongs to no Slot
// - the worker's own preparation - and a query-free owner, whose usage is not
// the whole execution's, is the pool's RESOURCE_HARD_STOP.
func (coordinator *SlotExecutionCoordinator) judgeRetainedLocked(stream *streamedExecution, retainedBytes uint64, phase string) error {
	err := coordinator.judgeRetainedOnceLocked(stream, retainedBytes, phase)
	if err != nil && stream != nil {
		stream.aheadAtRefusal.Store(stream.aheadBytes.Load())
	}
	return err
}

// judgeRetainedOnceLocked is judgeRetainedLocked's judgement.
func (coordinator *SlotExecutionCoordinator) judgeRetainedOnceLocked(stream *streamedExecution, retainedBytes uint64, phase string) error {
	owned := stream != nil && stream.began
	if share := coordinator.qgShareBytes(); stream != nil && share > 0 && retainedBytes > 0 && stream.retainedTotal()+retainedBytes > share {
		return shareRejection(phase, stream.retainedTotal(), retainedBytes, share, stream.ownBudgetUsage(coordinator.budget))
	}
	if coordinator.fitsLocked(retainedBytes) {
		return nil
	}
	if owned {
		return poolHeldByOthersRejection(phase, stream.retainedTotal(), coordinator.reservations.retainedBytes, retainedBytes,
			coordinator.budget.MaxRetainedBytes, stream.ownBudgetUsage(coordinator.budget))
	}
	return budgetRejection(observability.CapacityBudgetRetainedBytes, phase, coordinator.reservations.retainedBytes, retainedBytes, coordinator.budget.MaxRetainedBytes, stream.ownBudget(observability.CapacityBudgetRetainedBytes), stream.ownBudgetUsage(coordinator.budget))
}

// tryAcquireRetained reserves bytes against the retained budget when they fit
// and reports false, reserving nothing, when they do not. For memory a Slot
// may hold or do without: it never refuses the Slot, it only declines.
func (coordinator *SlotExecutionCoordinator) tryAcquireRetained(retainedBytes uint64) bool {
	coordinator.reservations.mu.Lock()
	defer coordinator.reservations.mu.Unlock()
	if !coordinator.fitsLocked(retainedBytes) {
		return false
	}
	coordinator.reservations.retainedBytes += retainedBytes
	return true
}

// fitsLocked reports whether retainedBytes more fit the retained pool, under
// reservations.mu: what no Slot has reserved, with what the State cache
// borrowed of it given back first, as far as the bytes need it. The cache
// never stands in a Slot's way - a reservation is refused only where the pool
// has no room for it with the cache empty - and gives back nothing for one
// that would be refused anyway.
func (coordinator *SlotExecutionCoordinator) fitsLocked(retainedBytes uint64) bool {
	free := coordinator.budget.MaxRetainedBytes - coordinator.reservations.retainedBytes
	if retainedBytes > free {
		return false
	}
	if borrower := coordinator.ports.RetainedBorrower; borrower != nil {
		if lent := min(borrower.BorrowedRetainedBytes(), free); retainedBytes > free-lent {
			borrower.YieldRetainedBytes(retainedBytes - (free - lent))
		}
	}
	return true
}

// RetainedRoom is what no Slot running on coordinator has reserved of the
// retained pool: what the State cache may borrow. A function, as
// RetainedReserved is.
func RetainedRoom(coordinator *SlotExecutionCoordinator) uint64 {
	coordinator.reservations.mu.Lock()
	defer coordinator.reservations.mu.Unlock()
	return coordinator.budget.MaxRetainedBytes - coordinator.reservations.retainedBytes
}

// RetainedReserved is what the Slots running on coordinator hold reserved
// against MaxRetainedBytes at the moment of the call: the pool's usage, where
// the completion rows carry each Slot's share only after the fact. The
// reservation is checked and moved under one lock, so it is read under that
// lock as well rather than from a second copy every site that moves it would
// have to keep in step; the lock is only ever held for a comparison and an
// addition. A function rather than a method: Execute is the coordinator's
// one way in, and a reading of its pool is not a way in.
func RetainedReserved(coordinator *SlotExecutionCoordinator) uint64 {
	coordinator.reservations.mu.Lock()
	defer coordinator.reservations.mu.Unlock()
	return coordinator.reservations.retainedBytes
}

// admittedState is what admission measured for one mutation: its stored size,
// and the frame the store encoded to measure it, while the Slot holds that
// frame for the write.
type admittedState struct {
	bytes int64
	frame *execution.EncodedStateFrame
}

// frameHeaderBytes is what a kept frame holds beside its bytes: the frame
// itself, which admission hands back with them.
var frameHeaderBytes = uint64(unsafe.Sizeof(execution.EncodedStateFrame{}))

// frameRetainedBytes is what one kept frame holds of the retained budget:
// the encoded record as allocated, and the frame around it.
func frameRetainedBytes(frame *execution.EncodedStateFrame) uint64 {
	return uint64(cap(frame.Bytes)) + frameHeaderBytes
}

// heldFrames is one Plan's admitted frames kept for its write, reserved
// against the retained budget from the moment admission hands each chunk
// back until the write is over.
//
// They used to be reserved once, after the last chunk: until then a Plan's
// frames -- whole encoded records, the history the loaded state's own charge
// already covers encoded again beside it -- were held on no reservation at
// all, and with every running Slot admitting at once that was up to a quarter
// of the retained budget on a busy replica. The frame around each record was
// not counted either.
type heldFrames struct {
	coordinator *SlotExecutionCoordinator
	// owner is the Slot whose share the frames count against; its own
	// holder entry carries them, so the frames register as a holder of their
	// own only without one.
	owner *streamedExecution
	bytes uint64
	// slot and operation name whose frames these are, and since when they are
	// held, for the retained-holder reading; bytesNow is bytes for it, and
	// holding says the frames are in the coordinator's holder registry.
	slot      execution.SlotIdentity
	operation execution.Operation
	since     time.Time
	bytesNow  atomic.Uint64
	holding   bool
	// refused is set once the budget did not take a chunk: the Plan keeps
	// the frames reserved before it and lets every later one go.
	refused  bool
	released bool
}

// keep reserves one admission chunk's frames as the chunk comes back, or
// lets them go when the retained budget cannot take them -- and every frame
// of the Plan after them. A frame let go is only encoded again by the write,
// as it was before frames were kept; the Slot is neither refused nor
// delayed for it, and admission is the same either way.
func (held *heldFrames) keep(chunk []admittedState) {
	var total uint64
	for _, state := range chunk {
		if state.frame != nil {
			total += frameRetainedBytes(state.frame)
		}
	}
	if total == 0 {
		return
	}
	if !held.refused && held.coordinator.holdRetained(held.owner, total, "frames") == nil {
		held.bytes += total
		held.bytesNow.Store(held.bytes)
		if !held.holding && held.owner == nil {
			held.holding, held.since = true, time.Now()
			held.coordinator.holders.add(held)
		}
		return
	}
	held.refused = true
	for index := range chunk {
		chunk[index].frame = nil
	}
}

// settle keeps the reservation to the frames the write still has: a
// mutation admission refused, or a series the output dropped, gives its
// frame's bytes back now rather than when the Plan's write is over.
func (held *heldFrames) settle(kept []admittedState) {
	if held == nil || held.released {
		return
	}
	var total uint64
	for _, state := range kept {
		if state.frame != nil {
			total += frameRetainedBytes(state.frame)
		}
	}
	if total < held.bytes {
		held.coordinator.letGoRetained(held.owner, held.bytes-total)
		held.bytes = total
		held.bytesNow.Store(total)
	}
}

// release returns the frames' reservation. Called once the write is over or
// the Plan stops short of it, and deferred beside that for the exits that
// return; only the first call releases.
func (held *heldFrames) release() {
	if held == nil || held.released {
		return
	}
	held.released = true
	held.coordinator.letGoRetained(held.owner, held.bytes)
	held.bytesNow.Store(0)
	if held.holding {
		held.coordinator.holders.remove(held)
	}
}

// Execute performs one already-scheduled attempt. normal, retry, replay and
// probe differ only by request.Operation; retry policy and queues stay outside
// this single completion path.
func (coordinator *SlotExecutionCoordinator) Execute(
	ctx context.Context,
	request execution.SlotExecutionRequest,
) (execution.SlotExecutionResult, error) {
	if coordinator == nil {
		return execution.SlotExecutionResult{}, errors.New("alarmd worker: initialized coordinator is required")
	}
	if err := ctx.Err(); err != nil {
		return execution.SlotExecutionResult{}, err
	}
	if err := request.Validate(); err != nil {
		return execution.SlotExecutionResult{}, fmt.Errorf("alarmd worker: invalid execution request: %w", err)
	}
	if request.Supplement != nil {
		// Before the evidence mark is armed: a supplement neither begins nor
		// completes its Slot, so it has nothing of either to leave behind.
		return coordinator.executeSupplement(withSlotTrace(ctx, request), request)
	}
	ctx, applied := withAppliedPlans(ctx)
	// On the way out, and only when this attempt wrote state and then could not
	// write the Slot down. Best-effort by construction: the mark is what lets a
	// later query-free completion say "this was evaluated", and failing to
	// leave it costs that completion its evidence -- it must not also change
	// what this attempt reports to the scheduler, which is about the Slot.
	defer func() { coordinator.recordExecutionEvidence(ctx, request, applied) }()
	ctx = withSlotTrace(ctx, request)
	if request.ExpiredRange != nil {
		return coordinator.executeExpiredRange(ctx, request)
	}
	beginStarted := time.Now()
	begin, err := coordinator.ports.Progress.BeginSlot(ctx, execution.ProgressBeginRequest{
		Identity:   execution.ProgressIdentity{QueryGroup: request.Contract.Slot.QueryGroup},
		OwnerFence: request.OwnerFence, Projection: request.UnfinishedProjection(), ContentScope: request.ContentScope,
	})
	// Timed whichever way it went. A BeginSlot that fails says so; one that
	// simply took twenty seconds used to say nothing at all, and an attempt
	// stuck here is indistinguishable from one stuck anywhere else between
	// slot_started and slot_completed.
	observability.ObserveSlotWait(ctx, coordinator.ports.Observer, observability.SlotWaitProgressBegin,
		observability.Operation(request.Operation), beginStarted, time.Now)
	if err != nil {
		return coordinator.progressBeginFailure(ctx, request, beginStarted, err), nil
	}
	if err := begin.Validate(); err != nil {
		return execution.SlotExecutionResult{}, fmt.Errorf("alarmd worker: invalid Progress BeginSlot result: %w", err)
	}
	if begin.Status != execution.ProgressCommitted {
		if refused := progressBeginRefused(begin.Status); refused != nil {
			return execution.SlotExecutionResult{}, refused
		}
		reason := begin.ReasonCode
		if reason == "" {
			reason = execution.ReasonCode(contract.ReasonProgressBeginRejected)
		}
		return activationRetry(reason), nil
	}
	finalizationStarted := time.Now()
	finalization, err := coordinator.ports.Finalization.ResolveFinalization(ctx, request)
	// This step reads the frozen Plan, and so the Segment's content objects.
	// It is where the twenty-two second silence in the production evidence
	// falls: after the frozen Plan was generated and before access planned a
	// query, with no line on either side of it.
	observability.ObserveSlotWait(ctx, coordinator.ports.Observer, observability.SlotWaitFinalization,
		observability.Operation(request.Operation), finalizationStarted, time.Now)
	if err != nil {
		return execution.SlotExecutionResult{}, fmt.Errorf("alarmd worker: resolve finalization: %w", err)
	}
	if err := finalization.Validate(request); err != nil {
		return execution.SlotExecutionResult{}, fmt.Errorf("alarmd worker: invalid finalization: %w", err)
	}
	if finalization.Mode == execution.FinalizationSnapshotRetry || finalization.Mode == execution.FinalizationExactSetBlocked {
		return activationRetry(finalization.ReasonCode), nil
	}
	if finalization.Mode != execution.FinalizationQueryRequired {
		return coordinator.executeQueryFreeFinalization(ctx, request, finalization)
	}
	started := time.Now()
	stream := &streamedExecution{coordinator: coordinator, request: request}
	defer stream.releaseProvisional()
	// Before the release: no read ahead outlives the Slot, and what it holds
	// goes back with the rest.
	defer stream.letGoReadAhead()
	ctx = withRetainedOwner(ctx, stream)
	queryRequest := execution.QueryExecutionRequest{
		Contract: request.Contract, Operation: request.Operation, AttemptNo: request.AttemptNo,
	}
	// One read of the whole Query Group, or one a slice when this Slot is
	// read in slices (sliced_input.go): a slice whose input passes its limit
	// while it streams is let go and split, its children read in its place.
	stream.slices = coordinator.sliceReadingFor(ctx, request)
	var completion execution.QueryExecutionCompletion
	completionCtx, cancelCompletion := ctx, context.CancelFunc(func() {})
	defer func() { cancelCompletion() }()
	read := false
	for {
		var leaf inputslice.Leaf
		if stream.slicing() {
			pending := stream.slices.pending()
			leaf = pending[0]
			stream.beginSlice(leaf, len(pending) == 1)
			queryRequest.Slice = leaf.Slice
		}
		execution.MarkStage(ctx, execution.SlotStageQuery)
		sliceStarted := time.Now()
		// The slice read ahead while the one before completed, when it is
		// this one and came back whole; read here otherwise.
		sliceCompletion, err, replayed := stream.replayReadAhead(ctx, leaf)
		if !replayed {
			sliceCompletion, err = coordinator.ports.Query.Execute(ctx, queryRequest, stream)
		}
		// The provider's own timeout comes back as a completion, UNAVAILABLE:
		// put on the slice's size it is the read out of time below, by the
		// time its query took, read ahead or here alike.
		if err == nil && stream.slicing() {
			if took, answered, timedOut := providerTimedOut(sliceCompletion); timedOut {
				if timeout := stream.providerTimeout(took, answered); timeout != nil {
					err = timeout
				} else {
					stream.slices.noteDegradedTimeout()
				}
			}
		}
		if err != nil && stream.slicing() && errors.Is(err, errSliceDimensionsGone) {
			stream.slices.forgetDimensions()
			continue
		}
		if err != nil && stream.slicing() && errors.Is(err, errSliceOverflow) {
			if refused := stream.splitSlice(leaf); refused != nil {
				refused = stream.finalSliceRefusal(refused)
				coordinator.observeQueryFailure(ctx, request.Operation, started, "execute", refused)
				return execution.SlotExecutionResult{}, fmt.Errorf("alarmd worker: query: %w", refused)
			}
			continue
		}
		if err != nil && stream.slicing() && sliceReadTimedOut(err) && !isReadinessDeferred(err) {
			if took, provider := timedOutTook(err, time.Since(sliceStarted)); provider && stream.splitsOnTimeout(ctx, took) {
				// Put on its size: let go and split as a slice past its
				// limit is, its children read in its place, once a Slot. A
				// Slot whose children complete is no timeout of its group's
				// cooldown; one whose do not is, as before.
				if refused := stream.splitSliceKept(leaf); refused == nil {
					stream.slices.timeoutSplit = true
					// Its read is no read: what the lookback captured of it
					// is taken back, its children captured in its place.
					if discarder, ok := coordinator.ports.Query.(sliceReadDiscarder); ok {
						discarder.DiscardSliceRead(request.Contract, leaf.Slice)
					}
					continue
				}
			}
			outcome := observability.InputSliceTimeoutNotSplit
			if stream.slices.timeoutSplit {
				outcome = observability.InputSliceTimeoutSplitFailed
			}
			err = &sliceTimeoutError{err: err, outcome: outcome}
		} else if err != nil && stream.slicing() && stream.slices.timeoutSplit && !errors.Is(err, errSliceOverflow) {
			// Split for its time, and failed after for another reason.
			err = &sliceTimeoutError{err: err, outcome: observability.InputSliceTimeoutSplitFailed}
		}
		if err != nil {
			if isReadinessDeferred(err) {
				// A readiness deferral is the normal pacing of a Slot whose data is
				// not in yet, and it is the single highest-volume line alarmd emits.
				// It has to say so: an empty reason on a non-success result
				// normalizes to internal_unknown (observability.NormalizeReason),
				// which made every deferral read as an unclassified internal error -
				// in the logs and in the stage counter's reason label alike.
				// And what it waits for: the query, its requirement, when it is
				// ready and on what basis.
				coordinator.emitObservation(ctx, observability.Observation{
					Component: observability.ComponentAccess, Stage: observability.StageQueryCompleted,
					Result: observability.ResultRetrying, Operation: observability.Operation(request.Operation),
					ReasonCode: observability.ReasonCode(contract.ReasonQueryNotReady),
					Duration:   time.Since(started), DurationKnown: true, ReadinessDeferral: readinessDeferralOf(err),
				})
			} else {
				coordinator.observeQueryFailure(ctx, request.Operation, started, "execute", err)
			}
			// A read that ran out of time is remembered too: one the provider
			// timed out at no less than the backend's 30 s, for the next Slot
			// of the Query Group to start in slices by that time rather than
			// read it whole to time out again; one alarmd's deadline ended at
			// what it took, put on nothing (timedOutTook).
			if stream.slices != nil && sliceReadTimedOut(err) {
				stream.slices.timedOutTook, stream.slices.timedOutByProvider = timedOutTook(err, time.Since(sliceStarted))
				stream.slices.queryMillis += stream.slices.timedOutTook
				stream.keepTimedOutSplit(leaf)
				coordinator.rememberSlotInput(request, stream, true)
			}
			return execution.SlotExecutionResult{}, fmt.Errorf("alarmd worker: query: %w", err)
		}
		if !read {
			execution.MarkQueryReturned(ctx, time.Now())
			completionCtx, cancelCompletion = shortPeriodCompletionContext(ctx, request.Operation, stream.header)
			read = true
		}
		if err := completionCtx.Err(); err != nil {
			return execution.SlotExecutionResult{}, fmt.Errorf("alarmd worker: completion deadline: %w", err)
		}
		if tooLong := sliceQueryTooLong(queryRequest.Slice, sliceCompletion); tooLong != nil {
			tooLong = stream.finalSliceRefusal(tooLong)
			coordinator.observeQueryFailure(ctx, request.Operation, started, "execute", tooLong)
			return execution.SlotExecutionResult{}, fmt.Errorf("alarmd worker: query: %w", tooLong)
		}
		if completion, err = foldSliceCompletion(completion, sliceCompletion); err != nil {
			return execution.SlotExecutionResult{}, err
		}
		if stream.slices != nil {
			// A read the provider timed out and that completed degraded took
			// the backend's limit, whatever its answer took to come back: the
			// slices after it are judged against that (timedOutForSize).
			took := slowestQueryMillis(sliceCompletion)
			if _, _, timedOut := providerTimedOut(sliceCompletion); timedOut {
				took = max(took, 2*sliceQueryTargetMillis)
			}
			stream.slices.queryMillis += took
			stream.slices.sliceMillis = append(stream.slices.sliceMillis, took)
			stream.slices.noteLeafRead(leaf, took)
			stream.slices.noteSliceCompletion(sliceCompletion)
		}
		// The next slice is read while this one completes, up to half of what
		// the share leaves beside this slice (sliceLimit): its decode is off
		// the Slot's path (slice_read_ahead.go).
		if width := coordinator.sliceReadWidth(); stream.slicing() && width > 1 && !sliceReadAheadOff.Load() {
			if pending := stream.slices.pending(); len(pending) > 1 {
				stream.startSliceReadAheads(ctx, queryRequest, pending, width)
			}
		}
		if err := stream.complete(completionCtx, sliceCompletion); err != nil {
			coordinator.observeQueryFailure(completionCtx, request.Operation, started, "stream_complete", err)
			// Not "invalid query result": the query is only one of the things
			// this step does, and the failure that brought 163 Slots down in a
			// day was a state read of ours timing out, with the query fine.
			return execution.SlotExecutionResult{}, fmt.Errorf("alarmd worker: complete Slot: %w", err)
		}
		if !stream.slicing() || stream.slices.landSliceRead(leaf) {
			break
		}
	}
	ctx = completionCtx
	if stream.slicing() && stream.slices.everySliceTargetMissing() {
		// The provider answered the Query Group's target missing in every
		// slice: nothing of its size was read, and slices kept from reads
		// before - their shape, cut, count and the time they took - would
		// only send the next probe as many requests, each answered the same.
		// The next Slot reads it whole.
		coordinator.slicedInput.forget(request.Contract.Slot.QueryGroup)
	} else {
		coordinator.rememberSlotInput(request, stream, false)
	}
	// The Slot's result is the evaluation's; the query line reports the
	// physical queries' own beside it (observeQueryCompleted).
	queryResult, queryReason := provisionalResult(stream.evaluated)
	coordinator.observeQueryCompleted(ctx, request.Operation, started, completion, stream.evaluated, &stream.summary)
	// Whether a physical query of the round answered short, by the rule the
	// query line reports it by, for the completion line's lost-round facts.
	physicalLoss, _ := physicalQueryFailure(completion)
	if stream.summary.planCount() == 0 {
		// The timing travels with the usage here for the same reason it does at
		// the other exit: this Slot ran. A round that produced no Plan result
		// still waited for its records and still read State, and reporting the
		// budgets it used beside an empty clock would read as a Slot that was
		// never measured rather than one that found nothing to decide.
		return execution.SlotExecutionResult{Result: queryResult, ReasonCode: queryReason,
			Usage: stream.budgetUsage(), Timing: stream.timing(), InPlaceWait: completion.InPlaceWait,
			PhysicalQueryLoss: physicalLoss}, nil
	}

	var result execution.SlotExecutionResult
	leaveApply := stream.enter(slotPhaseApply)
	if stream.windows.started {
		// Cut into windows while it was evaluated: the earlier windows and
		// the head are written, the last window and the tail are left.
		result, err = coordinator.finishWindowedSlot(ctx, stream)
	} else {
		err = coordinator.ports.Sequencer.Sequence(ctx, sequencingScope(stream.header, stream.stateItems, stream.gapItems), func(sequenceCtx context.Context) error {
			var executeErr error
			result, executeErr = coordinator.finalizeSummarized(
				withUnseenPlans(withNoDataEnds(withNoDataAbsenceStarts(sequenceCtx, stream.noDataAbsenceStarts), stream.noDataEndRound()), stream.unseenPlans()),
				request, stream.header, stream.bindings, stream.state, stream.gaps, stream.evaluated, &stream.summary,
				stream.noDataMutations, stream.queryEvidence, stream.seriesCensus, stream.targetSummaries(),
			)
			return executeErr
		})
	}
	leaveApply()
	if err != nil {
		var protection *activationProtectionRequiredError
		if errors.As(err, &protection) {
			result, convergeErr := coordinator.convergeNormalActivation(ctx, request, *protection)
			if convergeErr != nil {
				return execution.SlotExecutionResult{}, fmt.Errorf("alarmd worker: converge changed activation: %w", convergeErr)
			}
			result.InPlaceWait = completion.InPlaceWait
			result.PhysicalQueryLoss = physicalLoss
			result.Superseded = stream.superseded
			// This attempt ran, and may have written windows and slices
			// before a Plan turned out to need protecting - a first-seen
			// Plan a later slice found had run (7.5) is one: the row says
			// what it used and how it read, the slices it wrote as
			// first-seen among it, which no later attempt sees again.
			result.Usage = stream.budgetUsage()
			result.Timing = stream.timing()
			return result, nil
		}
		return execution.SlotExecutionResult{}, fmt.Errorf("alarmd worker: execute frozen Slot: %w", err)
	}
	// Filled at the one exit that has both the finished result and the stream
	// that produced it. A Slot that ends any other way has no usage to report
	// rather than a usage of zero, and the two must not arrive as one number.
	result.Usage = stream.budgetUsage()
	result.Timing = stream.timing()
	result.InPlaceWait = completion.InPlaceWait
	result.PhysicalQueryLoss = physicalLoss
	// What the attempt skipped as superseded, whatever it then committed:
	// the completion line names it, and the read hold counts it as an
	// attempt overtaken by newer state.
	result.Superseded = stream.superseded
	return result, nil
}

// withSlotTrace puts the Slot's coordinates on every line this execution
// emits.
func withSlotTrace(ctx context.Context, request execution.SlotExecutionRequest) context.Context {
	return observability.ContextWithTraceFields(ctx, observability.TraceFields{
		QueryGroupKey:        string(request.Contract.Slot.QueryGroup),
		SnapshotRevision:     string(request.Contract.SnapshotRevision),
		QueryRevision:        string(request.Contract.QueryRevision),
		ScheduleRevision:     string(request.Contract.ScheduleRevision),
		ScheduleSegmentStart: int64(request.Contract.ScheduleSegmentStart),
		DuePlanSetDigest:     string(request.Contract.DuePlanSetDigest),
		OwnerID:              request.OwnerFence.OwnerID, OwnerEpoch: request.OwnerFence.OwnerEpoch,
		EvaluationTime: int64(request.Contract.Slot.EvaluationTime),
	})
}

// timing is where this execution's wall clock went, for the completion row.
// Beside the usage rather than in it: see execution.SlotTiming.
func (stream *streamedExecution) timing() execution.SlotTiming {
	if stream == nil {
		return execution.SlotTiming{}
	}
	if !stream.clockSince.IsZero() {
		stream.chargeClock()
	}
	return execution.SlotTiming{
		Slot:         stream.slotMillis(),
		Input:        stream.spentMillis(slotPhaseInput),
		Preflight:    stream.spentMillis(slotPhasePreflight),
		Evaluate:     stream.spentMillis(slotPhaseEvaluate),
		Apply:        stream.spentMillis(slotPhaseApply),
		RecordIDs:    stream.spentMillis(slotPhaseRecordIDs),
		Cut:          stream.spentMillis(slotPhaseCut),
		Merge:        stream.spentMillis(slotPhaseMerge),
		Series:       stream.spentMillis(slotPhaseSeries),
		NoDataRead:   stream.spentMillis(slotPhaseNoDataRead),
		NoDataWrite:  stream.spentMillis(slotPhaseNoDataWrite),
		Unattributed: stream.spentMillis(slotPhaseOther),
	}
}

// budgetUsage is what this execution took of each budget, for the completion
// row. It reads the same counters the rejection path reports, so a completion
// and a refusal of the same Slot describe the same quantities. The retained
// bytes are the Slot's peak, and each phase's share of them at that peak, so
// the four still add up: a Slot applied window by window gave bytes back as
// its windows landed, and what it held at its end is not what capacity is
// sized from (workerCostSource, retainedPeaks).
func (stream *streamedExecution) budgetUsage() execution.SlotBudgetUsage {
	if stream == nil {
		return execution.SlotBudgetUsage{}
	}
	budget := stream.coordinator.budget
	shapes := stream.sliceShapeOutcomes()
	return execution.SlotBudgetUsage{
		StateMutations: stream.effectsSpent.states + stream.effects.states, GapMutations: stream.effectsSpent.gaps + stream.effects.gaps,
		Events: stream.effectsSpent.events + stream.effects.events, EventsWithoutMessage: stream.summary.withoutMessage,
		RetainedBytes: stream.peakRetained, Series: stream.series,
		DependencySeriesDiscarded: stream.dependencyDiscarded,
		InputSlices:               stream.slicesRead(), InputSlicesDiscarded: stream.slicesDiscarded(),
		FirstSeenUnseenSlices: stream.unseenSlices(), FirstSeenUnseenSeries: stream.unseenSeries(),
		InputSlicesReadAhead: stream.readAheadEnds[readAheadReplayed], InputSlicesReadAheadOtherLeaf: stream.readAheadEnds[readAheadOtherLeaf],
		InputSlicesReadAheadYielded: stream.readAheadEnds[readAheadYielded], InputSlicesReadAheadFailed: stream.readAheadEnds[readAheadFailed],
		InputSliceTimeouts:                  stream.sliceTimeoutsSplit(),
		InputSliceShapeStarted:              shapes[0],
		InputSliceShapeKept:                 shapes[1],
		InputSliceShapeMerged:               shapes[2],
		InputSliceShapeVoidedRevision:       shapes[3],
		InputSliceShapeVoidedDimensions:     shapes[4],
		InputSliceShapeVoidedMaxLeaves:      shapes[5],
		InputSliceShapeRefusedNotALeaf:      shapes[6],
		InputSliceShapeRefusedNotAPartition: shapes[7],
		InputSliceShapeRefusedMaxLeaves:     shapes[8],
		InputSliceShapeEvictedIdle:          shapes[9],
		InputSliceCutOn:                     stream.sliceCutFrom(),
		InputSliceDimension:                 stream.sliceDimension(),
		InputSlicesDeferred:                 stream.slicesDeferred(),
		InputSliceStartDiscarded:            stream.sliceStartDiscarded(),
		NoDataStagesAheadCarried:            stream.stageAheadEnds[stageAheadCarried], NoDataStagesAheadDiffers: stream.stageAheadEnds[stageAheadDiffers],
		NoDataStagesAheadDiscarded: stream.stageAheadEnds[stageAheadDiscarded], NoDataStagesAheadStopped: stream.stageAheadEnds[stageAheadStopped],
		NoDataStageAheadDiffersKeptOtherwise: stream.stageAheadDiffers[0], NoDataStageAheadDiffersCountMismatch: stream.stageAheadDiffers[1],
		NoDataStageAheadDiffersPresentAsOf: stream.stageAheadDiffers[2],
		NoDataGroupsStagedAhead:            stream.groupsStagedAhead,
		RetainedInputBytes:                 stream.peakByPhase[retainPhaseInput],
		RetainedGapBytes:                   stream.peakByPhase[retainPhaseGap],
		RetainedOutputBytes:                stream.peakByPhase[retainPhaseOutput],
		RetainedStateBytes:                 stream.peakByPhase[retainPhaseState],
		StateMutationsLimit:                budget.MaxStateMutations, GapMutationsLimit: budget.MaxGapMutations,
		EventsLimit: budget.MaxEvents, RetainedBytesLimit: budget.MaxRetainedBytes, SeriesLimit: budget.MaxSeries,
		RetainedShareBytes: stream.coordinator.qgShareBytes(),
		Windows:            uint64(stream.windows.count), WindowCeilingWindows: stream.windows.ceilingWindows,
		WindowCeilingRaisedWindows: stream.windows.raisedWindows,
	}
}

// readinessDeferralOf is what a deferred Slot waits for, as its error names
// it; nil when it names nothing.
func readinessDeferralOf(err error) *observability.ReadinessDeferralFacts {
	var deferred interface {
		ReadinessDeferral() *observability.ReadinessDeferralFacts
	}
	if !errors.As(err, &deferred) {
		return nil
	}
	return deferred.ReadinessDeferral()
}

func isReadinessDeferred(err error) bool {
	var deferred interface{ ReadinessReadyAt() time.Time }
	return errors.As(err, &deferred) && !deferred.ReadinessReadyAt().IsZero()
}

func (coordinator *SlotExecutionCoordinator) executeQueryFreeFinalization(
	ctx context.Context,
	request execution.SlotExecutionRequest,
	finalization execution.QueryFreeFinalization,
) (execution.SlotExecutionResult, error) {
	owner := &streamedExecution{coordinator: coordinator, request: request}
	defer owner.releaseProvisional()
	if err := owner.retainTargets(ctx, len(finalization.Targets.Plans), finalization.Targets.Plans); err != nil {
		return execution.SlotExecutionResult{}, err
	}
	plans := append([]execution.PlanKey(nil), finalization.Targets.Plans...)
	sort.Slice(plans, func(left, right int) bool { return execution.LessPlanKey(plans[left], plans[right]) })
	activationRequest := execution.PlanActivationRequest{Contract: request.Contract, Plans: plans}
	guardFacts, err := coordinator.loadActivations(ctx, activationRequest)
	if err != nil {
		return activationRetry(execution.ReasonCode(contract.ReasonActivationReadFailed)), nil
	}

	var result execution.SlotExecutionResult
	var changedActivations *execution.PlanActivationResult
	err = coordinator.ports.Sequencer.Sequence(ctx, activatedPlanSequencingScope(request.Contract.Slot, guardFacts), func(sequenceCtx context.Context) error {
		if _, err := coordinator.ensureActivatedPlanGaps(
			sequenceCtx,
			request,
			finalization.ReasonCode,
			guardFacts,
			// Both query-free modes may reuse protection that is already
			// sufficient. Gating this on the mode made a Slot with a marker
			// from an earlier streaming attempt unfinishable: a GAP_SKIPPED
			// finalization compared the two digests, found them different --
			// legitimately, because a streaming attempt and a query-free
			// finalization write different content for the same Slot -- and
			// refused, on every round, for as long as the marker stood.
			//
			// The mode was never what made reuse safe. queryFreeGapAlreadyProtects
			// is, and it checks sufficiency directly: same Plan, same
			// generation, same ApplyVersion, same schedule revision, a
			// Plan-wide scope whose protection is at least as strong as the
			// requested protection. The same Slot must not reset observations
			// already committed by an earlier attempt.
			true,
		); err != nil {
			return err
		}
		progressFacts, err := coordinator.loadActivations(sequenceCtx, activationRequest)
		if err != nil {
			result = activationRetry(execution.ReasonCode(contract.ReasonActivationReadFailed))
			return nil
		}
		if !guardFacts.SameSelections(progressFacts) {
			changed := changedSelectedActivations(guardFacts, progressFacts)
			changedActivations = &changed
			result = activationRetry(execution.ReasonCode(contract.ReasonConfigDrift))
			return nil
		}
		if err := coordinator.admitActivatedPlans(sequenceCtx, request, progressFacts); err != nil {
			return err
		}
		completionKind := execution.CompletionSnapshotUnavailable
		if finalization.Mode == execution.FinalizationGapSkipped {
			completionKind = execution.CompletionGapSkipped
		}
		// What an earlier attempt at this Slot got to, read here because this
		// is the last moment anything knows the frozen due set and the first
		// moment the completion is being built. Both query-free modes ask: each
		// of them can be the second half of an attempt that evaluated, alerted
		// and then failed to write the Slot down.
		evidence := coordinator.readExecutionEvidence(sequenceCtx, execution.PlanIdentitiesOf(plans), request.Contract.Slot)
		result, err = coordinator.commitProgress(sequenceCtx, request, execution.SlotCompletion{
			Contract: request.Contract, Kind: completionKind,
			Result: observability.ResultDegraded, ReasonCode: finalization.ReasonCode,
			Evidence: evidence,
		}, execution.CompletionAttribution{Cause: finalization.Cause, Reason: finalization.ReasonCode})
		return err
	})
	if err != nil {
		return execution.SlotExecutionResult{}, fmt.Errorf("alarmd worker: finalize query-free Slot: %w", err)
	}
	if changedActivations != nil {
		if err := coordinator.protectActivatedPlanGaps(
			ctx,
			request,
			finalization.ReasonCode,
			*changedActivations,
			// The same, for the activations that changed underneath this
			// round. Reuse is decided by whether the protection is sufficient,
			// not by which query-free mode asked.
			true,
		); err != nil {
			return execution.SlotExecutionResult{}, fmt.Errorf("alarmd worker: protect changed query-free activation: %w", err)
		}
	}
	return result, nil
}

func (coordinator *SlotExecutionCoordinator) loadActivations(
	ctx context.Context,
	request execution.PlanActivationRequest,
) (execution.PlanActivationResult, error) {
	result, err := coordinator.ports.Activation.LoadActivations(ctx, request)
	if err == nil {
		err = result.Validate(request)
	}
	return result, err
}

func duePlanActivationRequest(
	contractRef execution.FrozenExecutionContractRef,
	duePlans []execution.DuePlan,
) execution.PlanActivationRequest {
	plans := make([]execution.PlanKey, len(duePlans))
	for index, plan := range duePlans {
		plans[index] = plan.Key()
	}
	sort.Slice(plans, func(left, right int) bool { return execution.LessPlanKey(plans[left], plans[right]) })
	return execution.PlanActivationRequest{Contract: contractRef, Plans: plans}
}

// progressBeginFailure observes a BeginSlot error with its text under the Slot
// coordinates already attached to ctx and keeps the Slot retrying. A
// deterministic cause (persisted-fact, validation or identity error) is
// reported as PROGRESS_BEGIN_FAILED; a transport or context failure keeps the
// retryable PROGRESS_BEGIN_REJECTED. Both reasons are observation-only.
func (coordinator *SlotExecutionCoordinator) progressBeginFailure(
	ctx context.Context,
	request execution.SlotExecutionRequest,
	started time.Time,
	err error,
) execution.SlotExecutionResult {
	reason := execution.ReasonCode(contract.ReasonProgressBeginFailed)
	var deterministic interface{ DeterministicControlFact() }
	var transport net.Error
	if !errors.As(err, &deterministic) &&
		(errors.As(err, &transport) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		reason = execution.ReasonCode(contract.ReasonProgressBeginRejected)
	}
	coordinator.observe(ctx, observability.ComponentProgress, observability.StageProgressCommitted,
		request.Operation, started, "", observability.ReasonCode(reason), err)
	return activationRetry(reason)
}

func activationRetry(reason execution.ReasonCode) execution.SlotExecutionResult {
	return execution.SlotExecutionResult{Completed: false, Result: observability.ResultRetrying, ReasonCode: reason}
}

func changedSelectedActivations(
	before execution.PlanActivationResult,
	after execution.PlanActivationResult,
) execution.PlanActivationResult {
	changed := execution.PlanActivationResult{Contract: after.Contract}
	for _, fact := range after.Facts {
		previous, found := before.Find(fact.Key())
		if found && previous.Equal(fact) {
			continue
		}
		if fact.Selection != execution.ActivationNone {
			changed.Facts = append(changed.Facts, fact)
		}
	}
	return changed
}

// activationChange is what kind of change the activated Plans made under a
// Slot that was frozen with them: an edit, the Plan coming back, or the Plan
// gone. It decides which word the Slot's partial-gap completion carries.
//
// The three are told apart from the same comparison changedDuePlanActivations
// makes. A due Plan whose activation now names the same identity, schedule
// revision and state generation was not edited: it left the active set and
// came back, which shows either as the activation epoch moved or as the
// activation asking for its warming to restart (ForceWarming) with nothing
// else changed. The restart alone does not say which: the control plane
// forces warming on a Plan re-entering the activation and on a Plan whose
// state generation was edited, and a Slot frozen after the edit carries the
// new generation on both sides of the comparison. What tells them apart is
// what the store holds of the generation: a marker for it, or state or
// no-data memory of it this Slot read from before it (ranBefore), says it has
// run before and is coming back; a generation with neither is new, which is
// an edit. The state is the evidence a Plan from a deployment's first
// activation has - never forced to warm up, it never wrote a marker. A due Plan the activation does not name, or names as unselected,
// is gone. Anything else - a revision or a generation that moved - is an
// edit. An edit outranks the other two when a Slot carries several due
// Plans, because it is the one a reader has to go and look at; gone outranks
// returned, because it is the one that leaves a stretch behind.
//
// Read against the PLAN_NOT_ACTIVE skip the cursor records for the Slots in
// between (scheduler.ErrNoPlanDueInSegment): that skip says why the Slots
// before this one are missing and why they are not replayed; this says why
// the Slot they are missing up to is not a configuration drift. The two are
// one story, told at the two Slots it has.
type activationChange int

const (
	activationChangeUnchanged activationChange = iota
	activationChangeReactivated
	activationChangeNotActive
	activationChangeDrift
)

func classifyActivationChange(
	duePlans []execution.DuePlan,
	activations execution.PlanActivationResult,
	loadedGaps execution.GapLoadResult,
	unseen map[execution.PlanKey]struct{},
) activationChange {
	change := activationChangeUnchanged
	for _, due := range duePlans {
		fact, found := activations.Find(due.Key())
		var this activationChange
		samePlan := found && fact.Selection != execution.ActivationNone &&
			fact.Selected.Identity == due.Identity &&
			fact.Selected.StateGeneration == due.StateGeneration &&
			fact.Selected.ScheduleRevision == due.ScheduleRevision
		recorded, written := changeWrittenAtOwnVersion(activations.Contract, due, loadedGaps)
		switch {
		case !found || fact.Selection == execution.ActivationNone:
			this = activationChangeNotActive
		case written:
			// An earlier attempt of this Slot already wrote the Plan's marker
			// under its word: the word stands. Classified again, the marker
			// itself would change the answer - a new generation's own marker
			// reads as one that ran before, and a returning Plan's older
			// marker is gone under it - and the same marker at the same
			// version under another word is a conflict on every retry.
			this = recorded
		case samePlan && fact.Selected.StateApplyEpoch == due.StateApplyEpoch && !fact.Selected.ForceWarming:
			this = activationChangeUnchanged
		case samePlan && fact.Selected.StateApplyEpoch != due.StateApplyEpoch:
			this = activationChangeReactivated
		case samePlan && ranBefore(loadedGaps, unseen, due):
			// Warming restarted on a generation that has run: the Plan ran
			// under it before and is back.
			this = activationChangeReactivated
		default:
			this = activationChangeDrift
		}
		if this > change {
			change = this
		}
	}
	return change
}

// changeWrittenAtOwnVersion is the change a due Plan's marker at this Slot's
// own ApplyVersion was written under, when it was written under one of the
// two activation-change words. Any other marker at the version - the
// satisfied marker of a Plan judged new, or one a query-free completion
// wrote - says nothing about the activation and is classified as before.
func changeWrittenAtOwnVersion(
	contractRef execution.FrozenExecutionContractRef,
	due execution.DuePlan,
	loadedGaps execution.GapLoadResult,
) (activationChange, bool) {
	marker, found := loadedGaps.Find(due.GapIdentity())
	if !found || marker.Status != execution.GapFound || !atOwnVersion(contractRef, due.StateApplyEpoch, marker) {
		return activationChangeUnchanged, false
	}
	for _, scope := range marker.Scopes {
		switch scope.ReasonCode {
		case execution.ReasonCode(contract.ReasonPlanReactivated):
			return activationChangeReactivated, true
		case execution.ReasonCode(contract.ReasonConfigDrift):
			return activationChangeDrift, true
		}
	}
	return activationChangeUnchanged, false
}

// guardReasonFor is the word the Guards written for the given activations
// carry: the change classified over the Plans those activations name, which
// are the ones getting a Guard. A Slot's completion word is the max over
// every due Plan and can be PLAN_NOT_ACTIVE for a Plan that is gone, but a
// gone Plan gets no Guard, and that word on the Guard of a Plan that came
// back would misname it - and it is not a Guard scope word at all, so the
// fold would rank it first and the page has no words for it.
func guardReasonFor(
	duePlans []execution.DuePlan,
	activations execution.PlanActivationResult,
	loadedGaps execution.GapLoadResult,
	unseen map[execution.PlanKey]struct{},
) execution.ReasonCode {
	guarded := make([]execution.DuePlan, 0, len(duePlans))
	for _, due := range duePlans {
		if fact, found := activations.Find(due.Key()); found && fact.Selection != execution.ActivationNone {
			guarded = append(guarded, due)
		}
	}
	return classifyActivationChange(guarded, activations, loadedGaps, unseen).reason()
}

// generationSeenBefore reports whether the loaded Guards hold a marker for
// the due Plan's state generation: a generation that has run before.
func generationSeenBefore(loadedGaps execution.GapLoadResult, due execution.DuePlan) bool {
	marker, found := loadedGaps.Find(due.GapIdentity())
	return found && (marker.Status == execution.GapFound || marker.Status == execution.GapClearedTombstone)
}

// ranBefore reports whether the due Plan has run under its state generation
// before this Slot: the store holds a marker for the generation, or this Slot
// read state or no-data memory of it written before the Slot - the Plans not
// in unseen (unseenPlans). unseen is nil where the Slot read neither, as one
// completed without its query, and the marker alone then says.
func ranBefore(loadedGaps execution.GapLoadResult, unseen map[execution.PlanKey]struct{}, due execution.DuePlan) bool {
	if generationSeenBefore(loadedGaps, due) {
		return true
	}
	if unseen == nil {
		return false
	}
	_, nothingBefore := unseen[due.Key()]
	return !nothingBefore
}

// reason is the completion reason the change is reported under, CONFIG_DRIFT
// for an edit and for a change this Slot cannot classify.
func (change activationChange) reason() execution.ReasonCode {
	switch change {
	case activationChangeReactivated:
		return execution.ReasonCode(contract.ReasonPlanReactivated)
	case activationChangeNotActive:
		return execution.ReasonCode(contract.ReasonPlanNotActive)
	default:
		return execution.ReasonCode(contract.ReasonConfigDrift)
	}
}

func (change activationChange) cause() execution.CompletionCause {
	switch change {
	case activationChangeReactivated:
		return execution.CausePlanReactivated
	case activationChangeNotActive:
		return execution.CausePlanNotActive
	default:
		return execution.CauseConfigDrift
	}
}

func changedDuePlanActivations(
	duePlans []execution.DuePlan,
	activations execution.PlanActivationResult,
) (map[execution.PlanKey]struct{}, execution.PlanActivationResult) {
	changedPlans := make(map[execution.PlanKey]struct{})
	changedSelected := execution.PlanActivationResult{Contract: activations.Contract}
	for _, due := range duePlans {
		fact, found := activations.Find(due.Key())
		if found && fact.Selection != execution.ActivationNone &&
			fact.Selected.Identity == due.Identity &&
			fact.Selected.StateGeneration == due.StateGeneration &&
			fact.Selected.StateApplyEpoch == due.StateApplyEpoch &&
			fact.Selected.ScheduleRevision == due.ScheduleRevision {
			continue
		}
		changedPlans[due.Key()] = struct{}{}
		if found && fact.Selection != execution.ActivationNone {
			changedSelected.Facts = append(changedSelected.Facts, fact)
		}
	}
	return changedPlans, changedSelected
}

func (coordinator *SlotExecutionCoordinator) admitActivatedPlans(
	ctx context.Context,
	request execution.SlotExecutionRequest,
	activations execution.PlanActivationResult,
) error {
	facts := append([]execution.PlanActivationFact(nil), activations.Facts...)
	sort.Slice(facts, func(left, right int) bool { return lessPlanIdentity(facts[left].Plan, facts[right].Plan) })
	for _, fact := range facts {
		if fact.Selection == execution.ActivationNone {
			continue
		}
		if err := coordinator.admitPlan(ctx, request, fact.Key(), fact.Selected.StateApplyEpoch); err != nil {
			return err
		}
	}
	return nil
}

func (coordinator *SlotExecutionCoordinator) admitDuePlans(
	ctx context.Context,
	request execution.SlotExecutionRequest,
	duePlans []execution.DuePlan,
) error {
	plans := append([]execution.DuePlan(nil), duePlans...)
	sort.Slice(plans, func(left, right int) bool { return lessPlanIdentity(plans[left].Identity, plans[right].Identity) })
	for _, plan := range plans {
		if err := coordinator.admit(ctx, request, plan); err != nil {
			return err
		}
	}
	return nil
}

// admitDuePlanActivations is admitDuePlans without the owner fence, for the
// admission before the Progress commit, whose script runs the fence itself.
func (coordinator *SlotExecutionCoordinator) admitDuePlanActivations(
	ctx context.Context,
	request execution.SlotExecutionRequest,
	duePlans []execution.DuePlan,
) error {
	plans := append([]execution.DuePlan(nil), duePlans...)
	sort.Slice(plans, func(left, right int) bool { return lessPlanIdentity(plans[left].Identity, plans[right].Identity) })
	for _, plan := range plans {
		started := time.Now()
		result, err := coordinator.ports.Admission.CheckActivation(ctx, execution.SideEffectAdmissionRequest{
			Contract: request.Contract, Plan: plan.Key(), StateApplyEpoch: plan.StateApplyEpoch, OwnerFence: request.OwnerFence,
		})
		if err == nil {
			if validateErr := result.Validate(); validateErr != nil {
				err = validateErr
			} else if !result.Admitted {
				err = fmt.Errorf("admission denied: %s", result.ReasonCode)
			}
		}
		coordinator.observe(ctx, observability.ComponentState, observability.StagePlanActivationChecked, request.Operation, started, "", result.ReasonCode, err)
		if err != nil {
			return fmt.Errorf("alarmd worker: side-effect admission: %w", err)
		}
	}
	return nil
}

func (coordinator *SlotExecutionCoordinator) protectActivatedPlanGaps(
	ctx context.Context,
	request execution.SlotExecutionRequest,
	reason execution.ReasonCode,
	activations execution.PlanActivationResult,
	reuseCommittedQueryFreeStatement bool,
) error {
	return coordinator.ports.Sequencer.Sequence(
		ctx,
		activatedPlanSequencingScope(request.Contract.Slot, activations),
		func(sequenceCtx context.Context) error {
			_, err := coordinator.ensureActivatedPlanGaps(
				sequenceCtx,
				request,
				reason,
				activations,
				reuseCommittedQueryFreeStatement,
			)
			return err
		},
	)
}

func (coordinator *SlotExecutionCoordinator) convergeNormalActivation(
	ctx context.Context,
	request execution.SlotExecutionRequest,
	protection activationProtectionRequiredError,
) (execution.SlotExecutionResult, error) {
	// The words the protection was raised under travel with it: the retry
	// says the Slot's word, the Guard the converge writes says the word for
	// the Plans getting a Guard, and the completion it commits is the one it
	// was handed. The two differ only on a Slot where one Plan is gone and
	// another came back.
	reason := protection.completion.ReasonCode
	if reason == "" {
		reason = execution.ReasonCode(contract.ReasonConfigDrift)
	}
	guardReason := protection.guardReason
	if guardReason == "" {
		guardReason = reason
	}
	result := activationRetry(reason)
	var changedActivations *execution.PlanActivationResult
	err := coordinator.ports.Sequencer.Sequence(
		ctx,
		activatedPlanSequencingScope(request.Contract.Slot, protection.activations),
		func(sequenceCtx context.Context) error {
			alreadyProtected, err := coordinator.ensureActivatedPlanGaps(
				sequenceCtx,
				request,
				guardReason,
				protection.activations,
				false,
			)
			if err != nil || !alreadyProtected {
				return err
			}
			progressFacts, err := coordinator.loadActivations(sequenceCtx, protection.activationRequest)
			if err != nil {
				result = activationRetry(execution.ReasonCode(contract.ReasonActivationReadFailed))
				return nil
			}
			if !protection.currentFacts.SameSelections(progressFacts) {
				changed := changedSelectedActivations(protection.currentFacts, progressFacts)
				changedActivations = &changed
				return nil
			}
			if err := coordinator.admitActivatedPlans(sequenceCtx, request, progressFacts); err != nil {
				return err
			}
			result, err = coordinator.commitProgress(
				sequenceCtx, request, protection.completion, protection.completionCause)
			return err
		},
	)
	if err != nil {
		return execution.SlotExecutionResult{}, err
	}
	if changedActivations != nil {
		if err := coordinator.protectActivatedPlanGaps(
			ctx,
			request,
			execution.ReasonCode(contract.ReasonConfigDrift),
			*changedActivations,
			false,
		); err != nil {
			return execution.SlotExecutionResult{}, err
		}
	}
	return result, nil
}

func (coordinator *SlotExecutionCoordinator) ensureActivatedPlanGaps(
	ctx context.Context,
	request execution.SlotExecutionRequest,
	reason execution.ReasonCode,
	activations execution.PlanActivationResult,
	reuseCommittedQueryFreeStatement bool,
) (bool, error) {
	owner := &streamedExecution{coordinator: coordinator, request: request}
	defer owner.releaseProvisional()
	if err := owner.retainTargets(ctx, len(activations.Facts), activations); err != nil {
		return false, err
	}
	items := make([]execution.PlanGapLoadItem, 0, len(activations.Facts))
	for _, fact := range activations.Facts {
		if fact.Selection == execution.ActivationNone {
			continue
		}
		version, err := execution.BuildApplyVersion(request.Contract, fact.Selected.StateApplyEpoch)
		if err != nil {
			return false, fmt.Errorf("alarmd worker: build activated Plan ApplyVersion: %w", err)
		}
		items = append(items, execution.PlanGapLoadItem{
			Identity:     fact.GapIdentity(),
			ApplyVersion: version, ScheduleRevision: fact.Selected.ScheduleRevision,
		})
	}
	if len(items) == 0 {
		return true, nil
	}
	sort.Slice(items, func(left, right int) bool {
		return lessPlanIdentity(items[left].Identity.Plan, items[right].Identity.Plan)
	})
	if err := coordinator.admitActivatedPlans(ctx, request, activations); err != nil {
		return false, err
	}
	started := time.Now()
	loadRequest := execution.GapLoadRequest{Contract: request.Contract, Items: items}
	loaded, err := owner.loadGapFacts(ctx, loadRequest)
	if err == nil {
		err = execution.ValidateGapLoad(loadRequest, loaded)
	}
	gapResult, gapReason := summarizeGapLoad(loaded)
	coordinator.observeWithCounts(ctx, observability.ComponentState, observability.StageGapLoaded, request.Operation, started,
		gapResult, gapReason, observability.Counts{Keys: int64(len(loaded.Items))}, err)
	if err != nil {
		return false, fmt.Errorf("alarmd worker: activated Plan gap preflight: %w", err)
	}

	selected := make(map[execution.PlanKey]execution.ActivatedPlan, len(activations.Facts))
	for _, fact := range activations.Facts {
		if fact.Selection != execution.ActivationNone {
			selected[fact.Key()] = fact.Selected
		}
	}
	mutations := make([]execution.PlanGapMutation, 0, len(items))
	requireAlready := make(map[execution.PlanGapIdentity]struct{})
	superseded := 0
	defer func() { coordinator.observeSupersededGaps(ctx, request.Operation, superseded) }()
	for _, item := range items {
		marker, found := loaded.Find(item.Identity)
		if !found {
			return false, errors.New("alarmd worker: activated Plan gap marker is absent from validated result")
		}
		kind, err := ensureGappedKind(marker)
		if err != nil {
			return false, err
		}
		plan := selected[execution.PlanKeyOf(item.Identity.Plan, item.Identity.Shard)]
		// Query-free finalization cannot replace a statement already committed
		// by this Slot, including a tombstone left by successful gap recovery.
		if reuseCommittedQueryFreeStatement && sameSlotGapCommitted(marker, item, plan) {
			continue
		}
		mutation, err := execution.BuildPlanGapMutation(execution.PlanGapMutation{
			Identity: item.Identity, ExpectedMarkerRevision: marker.MarkerRevision,
			ApplyVersion: item.ApplyVersion, ScheduleRevision: item.ScheduleRevision,
			Scopes: []execution.GapScopeMutation{{
				Scope: execution.GapScope{}, Kind: kind,
				ReasonCode: reason, RequiredFullSlots: plan.RequiredFullSlots,
			}},
		})
		if err != nil {
			return false, fmt.Errorf("alarmd worker: build activated Plan gap mutation: %w", err)
		}
		if marker.Status == execution.GapFound || marker.Status == execution.GapClearedTombstone {
			switch execution.CompareApplyVersion(marker.PersistedApplyVersion, mutation.ApplyVersion) {
			case execution.ApplyVersionPersistedNewer:
				// A later Slot's marker already holds the Plan at least as
				// far as this Slot would: it is enough, as a forced warming
				// takes a marker at or past its activation
				// (unsatisfiedForcedWarmingActivations), and the expired
				// replay, skipped range or warming this Slot is finishing
				// completes on it instead of retrying against a marker that
				// will never be older.
				if execution.SupersededBy(marker.PersistedApplyVersion, mutation.ApplyVersion) {
					superseded++
					continue
				}
				// Any other newer marker -- written under an activation this
				// Slot has not reached, or by another Slot of the same moment
				// -- is refused as the store refuses it, by name: it read
				// internal_unknown, and an expired replay it held up was
				// retried with nothing to say why.
				return false, fmt.Errorf("alarmd worker: %w", &GapApplyRefusal{
					Stage: "activated Plan gap marker is newer than the Slot", Status: execution.GapGuardStale,
					Site: GapSiteBeforeEvents, Plan: item.Identity.Plan, StateGeneration: item.Identity.StateGeneration,
					ExpectedRevision: marker.MarkerRevision})
			case execution.ApplyVersionEqual:
				if marker.Status == execution.GapFound && marker.PersistedMutationDigest == mutation.MutationDigest {
					requireAlready[item.Identity] = struct{}{}
					break
				}
				return false, newGapGuardConflict(marker, item, mutation, reason, plan)
			}
		}
		if err := owner.retainGapMutation(ctx, mutation); err != nil {
			return false, err
		}
		mutations = append(mutations, mutation)
	}
	if len(mutations) == 0 {
		return true, nil
	}
	return coordinator.applyActivatedPlanGaps(ctx, request.Operation, request.Contract, mutations, requireAlready)
}

// sameSlotGapCommitted fences reuse with the selected identity and versions.
// A tombstone is this Slot's conclusion too, not active protection to strengthen.
func sameSlotGapCommitted(marker execution.GapGuardSnapshot, item execution.PlanGapLoadItem, plan execution.ActivatedPlan) bool {
	return (marker.Status == execution.GapFound || marker.Status == execution.GapClearedTombstone) && marker.Identity == item.Identity &&
		plan.Identity == item.Identity.Plan && plan.StateGeneration == item.Identity.StateGeneration &&
		plan.StateApplyEpoch == item.ApplyVersion.StateApplyEpoch && plan.ScheduleRevision == item.ScheduleRevision &&
		execution.CompareApplyVersion(marker.PersistedApplyVersion, item.ApplyVersion) == execution.ApplyVersionEqual &&
		marker.LastScheduleRevision == item.ScheduleRevision
}

func ensureGappedKind(marker execution.GapGuardSnapshot) (execution.GapMutationKind, error) {
	switch marker.Status {
	case execution.GapMissing, execution.GapClearedTombstone:
		return execution.GapOpen, nil
	case execution.GapFound:
		for _, scope := range marker.Scopes {
			if !scope.Scope.HasLevel {
				return execution.GapStrengthen, nil
			}
		}
		return execution.GapOpen, nil
	case execution.GapUnavailable, execution.GapTerminal:
		return "", fmt.Errorf("alarmd worker: activated Plan gap marker is not writable: %s", marker.Status)
	default:
		return "", fmt.Errorf("alarmd worker: unknown activated Plan gap marker: %s", marker.Status)
	}
}

func (coordinator *SlotExecutionCoordinator) applyActivatedPlanGaps(
	ctx context.Context,
	operation execution.Operation,
	contractRef execution.FrozenExecutionContractRef,
	items []execution.PlanGapMutation,
	requireAlready map[execution.PlanGapIdentity]struct{},
	extensions ...map[execution.PlanGapIdentity]*observability.GapExtensionFacts,
) (bool, error) {
	allAlready := len(items) > 0
	superseded := 0
	// These markers are written from the activation record alone, which
	// carries no retention: the request says so, and the store writes them
	// for its ceiling, which outlives any Plan's next round.
	err := coordinator.applyGapChunks(ctx, operation, contractRef, items, execution.GenerationRetention{Unknown: true}, "activated Plan gap guard",
		func(item execution.GapGuardApplyItemResult) error {
			// A later Slot wrote the marker after this one read it: enough,
			// by the rule ensureActivatedPlanGaps applies to what it read.
			if item.Status == execution.GapGuardSuperseded {
				superseded++
				return nil
			}
			if item.Status != execution.GapGuardAlreadyApplied {
				allAlready = false
			}
			if _, redo := requireAlready[item.Identity]; redo && item.Status != execution.GapGuardAlreadyApplied {
				return fmt.Errorf("activated Plan gap redo did not converge: %s", item.Status)
			}
			if item.Status != execution.GapGuardApplied && item.Status != execution.GapGuardAlreadyApplied {
				return &GapApplyRefusal{Stage: "activated Plan gap guard did not complete", Status: item.Status,
					Site: GapSiteBeforeEvents, Plan: item.Identity.Plan, StateGeneration: item.Identity.StateGeneration}
			}
			return nil
		}, extensions...)
	coordinator.observeSupersededGaps(ctx, operation, superseded)
	if err != nil {
		return false, fmt.Errorf("alarmd worker: apply activated Plan gap guard: %w", err)
	}
	return allAlready, nil
}

// applyGapChunks writes Plan gap mutations in Store-sized chunks in slice
// order and validates every receipt; accept decides which item statuses
// complete an item. The chunk loop stops at the first failed chunk, so a
// later chunk is never sent after an earlier one failed.
func (coordinator *SlotExecutionCoordinator) applyGapChunks(
	ctx context.Context,
	operation execution.Operation,
	contractRef execution.FrozenExecutionContractRef,
	items []execution.PlanGapMutation,
	retention execution.GenerationRetention,
	subject string,
	accept func(execution.GapGuardApplyItemResult) error,
	extensionMaps ...map[execution.PlanGapIdentity]*observability.GapExtensionFacts,
) error {
	started := time.Now()
	var totals applyTotals
	return forEachChunk(ctx, len(items), coordinator.applyChunkItems(coordinator.budget.MaxGapMutations), func(chunk applyChunk) error {
		chunkItems := items[chunk.start:chunk.end]
		var extensions []*observability.GapExtensionFacts
		if len(extensionMaps) > 0 {
			for _, item := range chunkItems {
				if facts := extensionMaps[0][item.Identity]; facts != nil {
					extensions = append(extensions, facts)
				}
			}
		}
		// Gap marks are written for the output and State that follow them,
		// and the store takes them by contract, not by owner fence: a
		// declined execution writing them would be a second writer of the
		// markers the next holder is writing for the same Slot.
		if err := execution.EnterCommit(ctx, execution.SlotStageState); err != nil {
			return err
		}
		chunkStarted := time.Now()
		result, err := coordinator.ports.GapGuard.ApplyGap(ctx, execution.GapGuardApplyRequest{Contract: contractRef, Items: chunkItems, Retention: retention})
		var reason execution.ReasonCode
		if err == nil {
			if err = result.Validate(); err == nil {
				expected := make([]execution.PlanGapIdentity, len(chunkItems))
				actual := make([]execution.PlanGapIdentity, len(result.Items))
				for index, item := range chunkItems {
					expected[index] = item.Identity
				}
				for index, item := range result.Items {
					actual[index] = item.Identity
					reason = item.ReasonCode
					if err = accept(item); err != nil {
						// The revision this Slot expected is on the mutation,
						// not on the store's answer, so it is filled in here
						// rather than in each accept: a refusal that says only
						// which status happened sends a reader looking for a
						// second writer with nothing to identify it by.
						err = withExpectedMarkerRevision(err, chunkItems)
						break
					}
				}
				if err == nil {
					err = validateIdentitySet(expected, actual, subject)
				}
			}
		}
		totals.keys += int64(len(chunkItems))
		coordinator.observeChunk(ctx, observability.StageGapGuardCommitted, operation, chunkStarted, started, "", reason,
			chunk, totals, observability.Counts{}, err, nil, nil, "", 0, extensions...)
		return err
	})
}

func activatedPlanSequencingScope(slot execution.SlotIdentity, activations execution.PlanActivationResult) execution.SequencingScope {
	scope := execution.SequencingScope{Slot: slot}
	for _, fact := range activations.Facts {
		if fact.Selection != execution.ActivationNone {
			scope.GapKeys = append(scope.GapKeys, fact.GapIdentity())
		}
	}
	sort.Slice(scope.GapKeys, func(left, right int) bool {
		return lessPlanIdentity(scope.GapKeys[left].Plan, scope.GapKeys[right].Plan)
	})
	return scope
}

func (coordinator *SlotExecutionCoordinator) finalizePrepared(
	ctx context.Context,
	request execution.SlotExecutionRequest,
	header execution.InternalExecutionHeader,
	bindings []execution.NamedInputBinding,
	loadedState execution.StatePreflightResult,
	evaluated execution.EvaluationResult,
) (execution.SlotExecutionResult, error) {
	return coordinator.finalizePreparedWithGaps(
		ctx, request, header, bindings, loadedState, execution.GapLoadResult{}, evaluated, nil,
		queryAvailabilityEvidence{},
		// The census a caller with no stream can state: the loaded views are
		// the series it read, and it meant to evaluate exactly those.
		seriesCensus{Due: len(loadedState.Items), Read: len(loadedState.Items)},
		nil,
	)
}

func (coordinator *SlotExecutionCoordinator) finalizePreparedWithGaps(
	ctx context.Context,
	request execution.SlotExecutionRequest,
	header execution.InternalExecutionHeader,
	bindings []execution.NamedInputBinding,
	loadedState execution.StatePreflightResult,
	loadedGaps execution.GapLoadResult,
	evaluated execution.EvaluationResult,
	noDataMemory []execution.PlanNoDataMutation,
	query queryAvailabilityEvidence,
	census seriesCensus,
	targets []execution.TargetResolutionSummary,
) (execution.SlotExecutionResult, error) {
	summary := summarizeEvaluation(evaluated)
	return coordinator.finalizeSummarized(ctx, request, header, bindings, loadedState, loadedGaps, evaluated, &summary,
		noDataMemory, query, census, targets)
}

// finalizeSummarized writes a Slot's results and commits its Progress. Each
// Plan's State and events are written from evaluated; everything read of the
// Slot as a whole - the completion, its coverage, the no-data end updates -
// is read from summary, which a Slot builds as its results merge and a
// caller with the whole result in hand builds from it.
func (coordinator *SlotExecutionCoordinator) finalizeSummarized(
	ctx context.Context,
	request execution.SlotExecutionRequest,
	header execution.InternalExecutionHeader,
	bindings []execution.NamedInputBinding,
	loadedState execution.StatePreflightResult,
	loadedGaps execution.GapLoadResult,
	evaluated execution.EvaluationResult,
	summary *slotEvaluationSummary,
	noDataMemory []execution.PlanNoDataMutation,
	query queryAvailabilityEvidence,
	census seriesCensus,
	targets []execution.TargetResolutionSummary,
) (execution.SlotExecutionResult, error) {
	apply, read := coordinator.beginSlotApply(ctx, request, header, loadedGaps, unseenPlansFrom(ctx))
	if !read {
		return activationRetry(execution.ReasonCode(contract.ReasonActivationReadFailed)), nil
	}
	stateIndex := indexStatePreflight(loadedState)
	for _, planResult := range sortedPlanResults(evaluated) {
		due, ok := duePlan(header.DuePlans, planResult.Plan)
		if !ok {
			return execution.SlotExecutionResult{}, errors.New("alarmd worker: evaluated plan is not due")
		}
		if apply.changed(due) {
			apply.withheldNoData[planResult.Plan] = struct{}{}
			continue
		}
		if err := coordinator.admit(ctx, request, due); err != nil {
			return execution.SlotExecutionResult{}, err
		}
		gapRetention, err := apply.planGapsBefore(ctx, due, &planResult)
		if err != nil {
			return execution.SlotExecutionResult{}, err
		}
		output, err := apply.applyPlanState(ctx, due, planResult.StateResults, loadedState, stateIndex)
		if err != nil {
			return execution.SlotExecutionResult{}, err
		}
		if output == planOutputWithheld {
			continue
		}
		if err := apply.planGapAfter(ctx, planResult.GuardAfterState, gapRetention); err != nil {
			return execution.SlotExecutionResult{}, err
		}
		apply.noteRetryPending(planResult)
	}
	census.Written += apply.written
	return apply.complete(ctx, bindings, summary, noDataMemory, evaluated.Result, evaluated.ReasonCode, query, census, targets)
}

// unsatisfiedForcedWarmingActivations is the Plans this Slot forces to warm
// up: forced by their activation and not yet holding a marker at or past it.
// A WARMING marker at this Slot's own ApplyVersion is one an earlier attempt
// of this same Slot wrote, not one an earlier Slot did: the Plan is still the
// one this Slot skips, or its retry would advance that marker at the same
// version and conflict. A marker older or newer than the Slot is read as
// before - a newer one is a later Slot's after Progress moved back, and is
// enough (the store answers it Superseded).
func unsatisfiedForcedWarmingActivations(
	activations execution.PlanActivationResult,
	loaded execution.GapLoadResult,
	changedPlans map[execution.PlanKey]struct{},
) execution.PlanActivationResult {
	result := execution.PlanActivationResult{Contract: activations.Contract}
	for _, fact := range activations.Facts {
		if fact.Selection == execution.ActivationNone || !fact.Selected.ForceWarming {
			continue
		}
		if _, changed := changedPlans[fact.Key()]; changed {
			continue
		}
		marker, found := loaded.Find(fact.GapIdentity())
		if found && (marker.Status == execution.GapFound || marker.Status == execution.GapClearedTombstone) &&
			marker.PersistedApplyVersion.StateApplyEpoch >= fact.Selected.StateApplyEpoch &&
			!(marker.Status == execution.GapFound && atOwnVersion(activations.Contract, fact.Selected.StateApplyEpoch, marker)) {
			continue
		}
		result.Facts = append(result.Facts, fact)
	}
	return result
}

// atOwnVersion reports whether a gap marker was written at the ApplyVersion
// the contract's Slot writes a Plan of the given epoch at: by an earlier
// attempt of this same Slot.
func atOwnVersion(contractRef execution.FrozenExecutionContractRef, epoch execution.StateApplyEpoch, marker execution.GapGuardSnapshot) bool {
	version, err := execution.BuildApplyVersion(contractRef, epoch)
	return err == nil && execution.CompareApplyVersion(marker.PersistedApplyVersion, version) == execution.ApplyVersionEqual
}

// completionCause is observation only: it says which of the conditions that
// share the UNAVAILABLE completion kind actually happened. It is a separate
// parameter rather than a field on the completion because the completion is
// persisted and its reason decides how consecutive gaps fold in a Progress gap
// summary.
func (coordinator *SlotExecutionCoordinator) commitProgress(
	ctx context.Context,
	request execution.SlotExecutionRequest,
	completion execution.SlotCompletion,
	completionCause execution.CompletionAttribution,
) (execution.SlotExecutionResult, error) {
	if err := execution.EnterCommit(ctx, execution.SlotStageProgress); err != nil {
		return execution.SlotExecutionResult{}, err
	}
	if request.ExpiredRange != nil {
		return coordinator.commitExpiredRange(ctx, request, completion, completionCause)
	}
	started := time.Now()
	progressRequest := execution.ProgressCommitRequest{
		Identity:   execution.ProgressIdentity{QueryGroup: request.Contract.Slot.QueryGroup},
		OwnerFence: request.OwnerFence, ExpectedNextSlot: request.ExpectedNextSlot, Completion: completion,
		Projection: request.UnfinishedProjection(), ContentScope: request.ContentScope,
	}
	if err := progressRequest.Validate(); err != nil {
		return execution.SlotExecutionResult{}, fmt.Errorf("alarmd worker: invalid progress commit: %w", err)
	}
	progress, err := coordinator.ports.Progress.CommitProgress(ctx, progressRequest)
	if err == nil {
		err = progress.Validate()
	}
	if err != nil {
		coordinator.observe(ctx, observability.ComponentProgress, observability.StageProgressCommitted, request.Operation, started, "", "", err)
		return execution.SlotExecutionResult{}, fmt.Errorf("alarmd worker: commit progress: %w", err)
	}
	if progress.Status != execution.ProgressCommitted {
		err = progressNotCommitted(progress.Status)
		reason := progress.ReasonCode
		if refusal, refused := ownership.RefusalReason(err); refused {
			reason = execution.ReasonCode(refusal)
		}
		coordinator.observe(ctx, observability.ComponentProgress, observability.StageProgressCommitted, request.Operation, started, "", reason, err)
		return execution.SlotExecutionResult{}, fmt.Errorf("alarmd worker: %w", err)
	}
	// The Slot is written down. Whatever this attempt applied is now recorded
	// where it belongs, so it must leave no mark behind: the mark answers a
	// question only an attempt that never got here can raise.
	appliedPlansFrom(ctx).progressCommitted()
	observationResult := observability.Result(observability.ResultSuccess)
	observationReason := progress.ReasonCode
	if completion.Kind == execution.CompletionGapSkipped || completion.Kind == execution.CompletionSnapshotUnavailable {
		observationResult = observability.ResultDegraded
		observationReason = completion.ReasonCode
	}
	coordinator.observeCommittedProgress(ctx, request.Operation, started, observationResult, observationReason,
		string(completion.Kind), string(completionCause.Cause), string(completionCause.Reason), completionCause.SlotCause().CarriedReason(),
		completionScopeFacts(completionCause), completionCause.Coverage, completion.Evidence, completion.Primary)
	committed := execution.SlotExecutionResult{Completed: true, CompletionKind: completion.Kind, Result: completion.Result, ReasonCode: completion.ReasonCode,
		CompletionCause: completionCause.SlotCause()}
	if completion.Kind == execution.CompletionGapSkipped || completion.Kind == execution.CompletionSnapshotUnavailable {
		committed.QueryFreeCause = queryFreeCause(completionCause.Cause)
	}
	return committed, nil
}

// progressNotCommitted is the error for a Progress commit the store did not
// take. A stale owner is the handover's refusal -- the Query Group moved
// while the Slot ran and the fence the commit carried is no longer the
// owner's -- and wraps the ownership word, so the Slot names it as the
// handover does; the other statuses keep theirs.
func progressNotCommitted(status execution.ProgressCommitStatus) error {
	switch status {
	case execution.ProgressStaleOwner:
		return fmt.Errorf("progress not committed: %s: %w", status, ownership.ErrStaleFence)
	case execution.ProgressContentMoved:
		return fmt.Errorf("progress not committed: %s: %w", status, ownership.ErrContentScopeMoved)
	}
	return fmt.Errorf("progress not committed: %s", status)
}

// progressBeginRefused is the error for a BeginSlot or BeginRange the owner
// fence refused, nil for every other status. Nothing of the Slot was written,
// so the Runner reads it as the attempt's ownership rejection rather than as
// an execution that ran and failed.
func progressBeginRefused(status execution.ProgressCommitStatus) error {
	switch status {
	case execution.ProgressStaleOwner:
		return &execution.SlotBeginRefusedError{Status: status, Err: ownership.ErrStaleFence}
	case execution.ProgressContentMoved:
		return &execution.SlotBeginRefusedError{Status: status, Err: ownership.ErrContentScopeMoved}
	}
	return nil
}

func (coordinator *SlotExecutionCoordinator) admit(
	ctx context.Context,
	request execution.SlotExecutionRequest,
	plan execution.DuePlan,
) error {
	return coordinator.admitPlan(ctx, request, plan.Key(), plan.StateApplyEpoch)
}

func (coordinator *SlotExecutionCoordinator) admitPlan(
	ctx context.Context,
	request execution.SlotExecutionRequest,
	plan execution.PlanKey,
	epoch execution.StateApplyEpoch,
) error {
	started := time.Now()
	result, err := coordinator.ports.Admission.Check(ctx, execution.SideEffectAdmissionRequest{
		Contract: request.Contract, Plan: plan, StateApplyEpoch: epoch, OwnerFence: request.OwnerFence,
	})
	if err == nil {
		if validateErr := result.Validate(); validateErr != nil {
			err = validateErr
		} else if !result.Admitted {
			err = fmt.Errorf("admission denied: %s", result.ReasonCode)
		}
	}
	// A fence refusal comes back as the store's typed error with no reason
	// on the result; without naming it here the admission line -- and the
	// fence_checked line relayed from it -- said internal_unknown for a
	// stale fence, a Query Group assigned elsewhere and a moved scope alike.
	reason := result.ReasonCode
	if named, ok := ownership.RefusalReason(err); ok && reason == "" {
		reason = execution.ReasonCode(named)
	}
	coordinator.observe(ctx, observability.ComponentState, observability.StageSideEffectAdmission, request.Operation, started, "", reason, err)
	if err != nil {
		return fmt.Errorf("alarmd worker: side-effect admission: %w", err)
	}
	return nil
}

// observeGapScheduleRestart names the recovery of a Plan gap marker that was
// left behind by a Plan schedule change: the loaded marker still carries the
// schedule revision it was written under, this Slot warms or clears it under
// the current one, and the store restarts the warmup count of every scope
// whose revision moved. It is observation only: the Slot result, the gap
// mutation and Progress are unchanged, and an operator reading the Query
// Group's flow can tell this restart from an ordinary warmup Slot.
func (coordinator *SlotExecutionCoordinator) observeGapScheduleRestart(
	ctx context.Context,
	operation execution.Operation,
	loaded execution.GapLoadResult,
	mutations []execution.PlanGapMutation,
) {
	for _, mutation := range mutations {
		marker, found := loaded.Find(mutation.Identity)
		if !found || marker.Status != execution.GapFound ||
			marker.LastScheduleRevision == "" || marker.LastScheduleRevision == mutation.ScheduleRevision {
			continue
		}
		coordinator.observeWithCounts(ctx, observability.ComponentState, observability.StageGapGuardCommitted,
			operation, time.Now(), observability.ResultResumed, observability.ReasonNone,
			observability.Counts{Keys: int64(len(mutation.Scopes))}, nil)
	}
}

func (coordinator *SlotExecutionCoordinator) applyGap(
	ctx context.Context,
	operation execution.Operation,
	contractRef execution.FrozenExecutionContractRef,
	items []execution.PlanGapMutation,
	retention execution.GenerationRetention,
	site string,
) error {
	superseded := 0
	err := coordinator.applyGapChunks(ctx, operation, contractRef, items, retention, "gap guard",
		func(item execution.GapGuardApplyItemResult) error {
			// A later Slot's marker is left as it is (superseded_series.go);
			// the Plan's events and State go on as they would have.
			if item.Status == execution.GapGuardSuperseded {
				superseded++
				return nil
			}
			if item.Status != execution.GapGuardApplied && item.Status != execution.GapGuardAlreadyApplied {
				return &GapApplyRefusal{Stage: "gap guard did not complete", Status: item.Status, Site: site,
					Plan: item.Identity.Plan, StateGeneration: item.Identity.StateGeneration}
			}
			return nil
		})
	// Counted whether or not a later chunk failed: what was skipped was
	// skipped.
	coordinator.observeSupersededGaps(ctx, operation, superseded)
	if err != nil {
		return fmt.Errorf("alarmd worker: apply gap guard: %w", err)
	}
	return nil
}

// withoutMessage is the events the evaluation decided and did not keep,
// because the sink would have taken them and sent nothing. The sink is still
// asked - with what is left, even nothing - so its admission against the lease
// is where it was; and the line counts them as the sink used to: in the
// batch's events, formats and kinds, and among the events without a message.
func (coordinator *SlotExecutionCoordinator) writeEvents(
	ctx context.Context,
	operation execution.Operation,
	plan execution.PlanIdentity,
	events []contract.TriggerEventV1,
	withoutMessage []execution.EventWithoutMessage,
) error {
	if len(events) == 0 && len(withoutMessage) == 0 {
		return nil
	}
	if err := execution.EnterCommit(ctx, execution.SlotStageOutput); err != nil {
		return err
	}
	ctx = observability.ContextWithTraceFields(ctx, observability.TraceFields{StrategyID: plan.StrategyID, BusinessID: plan.BusinessID})
	// The sink's own count of what it handed the broker, for the line: a
	// batch the protocol has no message for is a success that wrote nothing.
	ctx, outputWrite := observability.ContextWithOutputWriteReport(ctx)
	started := time.Now()
	err := coordinator.ports.Events.WriteBatch(ctx, events)
	reason := execution.ReasonCode(observability.ReasonNone)
	var rejection *observability.OutputRejectionFacts
	if err != nil {
		reason = execution.ReasonCode(observability.ReasonInternalUnknown)
		if isRetryableOutputDependency(err) {
			reason = execution.ReasonCode(contract.ReasonOutputACKUnknown)
		}
		// The sink's own refusal is named by the sink: the reason word is the
		// observation's reason, and the sentence travels as facts beside it
		// rather than being read back out of the error chain.
		if named, rejected := outputRejectionReason(err); rejected {
			reason = named
			rejection = &observability.OutputRejectionFacts{Reason: string(named)}
			var detailed outputRejectionDetail
			if errors.As(err, &detailed) {
				rejection.Detail = detailed.OutputRejectionDetail()
			}
		}
		if deferral, deferred := outputDeferralReason(err); deferred {
			reason = deferral
		}
	}
	// How many of the batch went out as each wire format, from the word each
	// event carries beside it. On the line whether the write succeeded or
	// not: a batch the broker refused still says what it was.
	formats := observability.OutputWireFormatCounts{}
	kinds := observability.OutputEventKindCounts{}
	for _, event := range events {
		format := event.WireFormat
		if format == "" {
			// An event with no word is a real state and gets the fold's
			// name rather than an empty key.
			format = observability.WireFormatOther
		}
		formats[format]++
		kinds[observability.OutputEventKindKey{Format: format, EventKind: event.EventKind}]++
	}
	for _, dropped := range withoutMessage {
		formats[dropped.Format]++
		kinds[observability.OutputEventKindKey{Format: dropped.Format, EventKind: dropped.EventKind}]++
	}
	written := outputWrite()
	if written != nil && len(withoutMessage) != 0 {
		// Only onto a report the sink made: a batch refused before the sink
		// counted anything said nothing about these either.
		written = withoutMessageReported(written, withoutMessage)
	}
	// The kind the sink's error says it is, by its type: which of the
	// failures that share a reason this one was. Empty on a success.
	failureKind := ""
	if err != nil {
		failureKind = observability.OutputFailureKindOf(err)
		// A refusal the sink named is the client's by what it is, whether or
		// not its error states a kind: the fleet reads a refusal code under
		// no kind as unknown.
		if rejection != nil && failureKind == observability.OutputFailureUnknown {
			failureKind = observability.OutputFailureClientRejected
		}
	}
	coordinator.emitObservation(ctx, observability.Observation{
		Component: observability.ComponentOutput, Stage: observability.StageEventACKed,
		Operation:  observability.Operation(operation),
		ReasonCode: observability.ReasonCode(reason), Duration: time.Since(started), DurationKnown: true,
		Counts: observability.Counts{Events: int64(len(events) + len(withoutMessage))}, Err: err, OutputRejection: rejection,
		OutputFailureKind: failureKind,
		OutputWrite:       written, OutputWireFormats: formats, OutputEventKinds: kinds,
		NoDataEmission: noDataEmissionOf(events, err),
	})
	acked := events
	if notWritten, partial := outputNotWritten(err); partial {
		// The rest of the batch was written and acknowledged: the copy
		// hears of those, and of none of the events that were not written.
		acked = make([]contract.TriggerEventV1, 0, len(events))
		for _, event := range events {
			if _, skipped := notWritten[event.EventID]; !skipped {
				acked = append(acked, event)
			}
		}
	} else if err != nil {
		return fmt.Errorf("alarmd worker: acknowledge events: %w", err)
	}
	// Only after the ACK: a batch the sink did not take opened nothing at
	// the consumer, and the copy must not say it did. The constructor
	// requires the port; the guard is for tests that build the struct.
	if coordinator.ports.OpenAlerts != nil && len(acked) > 0 {
		coordinator.ports.OpenAlerts.Acknowledged(acked)
		coordinator.observeNewOpens(ctx, operation, plan)
	}
	if err != nil {
		return fmt.Errorf("alarmd worker: acknowledge events: %w", err)
	}
	return nil
}

// outputNotWritten reports whether the sink wrote the batch except for some
// of its events (kafka.OutputPartiallyRejectedError), and which: the events
// it refused and the ones it withheld beside them. It is checked before the
// whole-batch rejection, which the partial one also names.
func outputNotWritten(err error) (map[string]struct{}, bool) {
	if err == nil {
		return nil, false
	}
	var partial interface{ OutputNotWrittenEventIDs() []string }
	if !errors.As(err, &partial) || partial == nil {
		return nil, false
	}
	ids := partial.OutputNotWrittenEventIDs()
	notWritten := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		notWritten[id] = struct{}{}
	}
	return notWritten, true
}

// withoutSeriesNotWritten is the accepted mutations, and their sizes, less
// the series any of whose events was not written. The sink withholds every
// event of such a series, so a series left with one of its events written
// is a sink that broke that promise: its State would miss an alert the
// consumer holds, and that is refused here rather than applied.
func withoutSeriesNotWritten(
	accepted []execution.StateMutation,
	acceptedBytes []admittedState,
	events map[execution.StateKeyIdentity][]contract.TriggerEventV1,
	notWritten map[string]struct{},
) ([]execution.StateMutation, []admittedState, error) {
	kept := make([]execution.StateMutation, 0, len(accepted))
	keptBytes := make([]admittedState, 0, len(acceptedBytes))
	for index, mutation := range accepted {
		held, sent := 0, 0
		for _, event := range events[mutation.Identity] {
			if _, skipped := notWritten[event.EventID]; skipped {
				held++
			} else {
				sent++
			}
		}
		if held == 0 {
			kept = append(kept, mutation)
			keptBytes = append(keptBytes, acceptedBytes[index])
			continue
		}
		if sent != 0 {
			return nil, nil, fmt.Errorf("alarmd worker: output wrote %d events of a series whose other %d it did not write", sent, held)
		}
	}
	return kept, keptBytes, nil
}

// outputsOf is what the accepted mutations' series decided to send: their
// events, and the identities of the events they did not keep. Only accepted
// ones - a series whose mutation the store refused sends nothing, and counts
// nothing either.
func outputsOf(
	accepted []execution.StateMutation,
	events map[execution.StateKeyIdentity][]contract.TriggerEventV1,
	withoutMessage map[execution.StateKeyIdentity][]execution.EventWithoutMessage,
) ([]contract.TriggerEventV1, []execution.EventWithoutMessage) {
	sent := make([]contract.TriggerEventV1, 0)
	var dropped []execution.EventWithoutMessage
	for _, mutation := range accepted {
		sent = append(sent, events[mutation.Identity]...)
		dropped = append(dropped, withoutMessage[mutation.Identity]...)
	}
	return sent, dropped
}

// withoutMessageReported adds the events the evaluation did not keep to the
// sink's report of the batch, as the sink counted them when it received them:
// no message, by format and kind.
func withoutMessageReported(facts *observability.OutputWriteFacts, withoutMessage []execution.EventWithoutMessage) *observability.OutputWriteFacts {
	type key struct{ format, kind string }
	counts := make(map[key]int64, 1)
	for _, bucket := range facts.WithoutMessageBy {
		counts[key{bucket.Format, bucket.EventKind}] += bucket.Events
	}
	for _, dropped := range withoutMessage {
		counts[key{dropped.Format, dropped.EventKind}]++
	}
	merged := *facts
	merged.WithoutMessage += int64(len(withoutMessage))
	merged.WithoutMessageBy = make([]observability.OutputWithoutMessage, 0, len(counts))
	for k, n := range counts {
		merged.WithoutMessageBy = append(merged.WithoutMessageBy, observability.OutputWithoutMessage{Format: k.format, EventKind: k.kind, Events: n})
	}
	sort.Slice(merged.WithoutMessageBy, func(i, j int) bool {
		if merged.WithoutMessageBy[i].Format != merged.WithoutMessageBy[j].Format {
			return merged.WithoutMessageBy[i].Format < merged.WithoutMessageBy[j].Format
		}
		return merged.WithoutMessageBy[i].EventKind < merged.WithoutMessageBy[j].EventKind
	})
	return &merged
}

// outputRejectionDetail is the sentence the sink's refusal carries beside its
// reason word -- the converter's or the client's, without identity. Declared
// here rather than imported so the coordinator names the shape it reads and
// not the package that produces it, the way outputRejectionReason does.
type outputRejectionDetail interface {
	OutputRejectionDetail() string
}

func isRetryableOutputDependency(err error) bool {
	if err == nil {
		return false
	}
	var dependencyErr interface{ RetryableOutputDependency() }
	return errors.As(err, &dependencyErr) && dependencyErr != nil
}

// outputDeferralReason reports whether the sink declined to start the batch
// because the Slot's lease has less life left than the batch needs
// (kafka.OutputDeferredError), and the reason it names. Retryable by
// construction -- the next renewal changes the answer -- but not an unknown
// acknowledgement: no broker was asked.
func outputDeferralReason(err error) (execution.ReasonCode, bool) {
	if err == nil {
		return "", false
	}
	var deferral interface{ OutputDeferralReason() string }
	if !errors.As(err, &deferral) || deferral == nil || deferral.OutputDeferralReason() == "" {
		return "", false
	}
	return execution.ReasonCode(deferral.OutputDeferralReason()), true
}

// outputRejectionReason reports whether the sink refused to write the events
// on its own account -- the converter would not represent them, or the client
// refused them before any broker -- and the reason it names for the Slot's
// completion (kafka.OutputRejectedError). Such an error is checked before the
// dependency marker on purpose: it never carries that marker, and a sink that
// gave it both would have a reader retry a refusal.
func outputRejectionReason(err error) (execution.ReasonCode, bool) {
	if err == nil {
		return "", false
	}
	var rejection interface{ OutputRejectionReason() string }
	if !errors.As(err, &rejection) || rejection == nil || rejection.OutputRejectionReason() == "" {
		return "", false
	}
	return execution.ReasonCode(rejection.OutputRejectionReason()), true
}

// admitState admits one Plan's mutations in Store-sized chunks. It returns the
// deterministic rejections by identity and, aligned with mutations, the
// encoded size the store measured for each admitted mutation and the frame it
// measured, when the store kept one and the retained budget took it; and the
// reservation those frames are held under, which the caller releases. An
// admission that does not complete -- a chunk that fails, a context that ends
// between chunks -- releases every frame it had reserved before it returns.
func (coordinator *SlotExecutionCoordinator) admitState(
	ctx context.Context,
	operation execution.Operation,
	contractRef execution.FrozenExecutionContractRef,
	retention []execution.StateRetentionRequirement,
	horizon int64,
	mutations []execution.StateMutation,
) (map[execution.StateKeyIdentity]execution.ReasonCode, []admittedState, *heldFrames, error) {
	started := time.Now()
	deterministic := make(map[execution.StateKeyIdentity]execution.ReasonCode)
	encodedBytes := make([]admittedState, len(mutations))
	held := &heldFrames{coordinator: coordinator, owner: retainedOwnerFrom(ctx), slot: contractRef.Slot, operation: operation}
	var totals applyTotals
	err := forEachChunk(ctx, len(mutations), coordinator.applyChunkItems(coordinator.budget.MaxStateMutations), func(chunk applyChunk) error {
		chunkItems := mutations[chunk.start:chunk.end]
		chunkStarted := time.Now()
		result, err := coordinator.ports.State.AdmitRuntime(ctx, execution.StateApplyRequest{
			Contract: contractRef, Retention: retention, Items: chunkItems, HorizonSeconds: horizon,
		})
		var reason execution.ReasonCode
		var chunkBytes int64
		rejected := 0
		chunkLegacyIDs := 0
		var refusalRules []string
		var refusalText string
		if err == nil {
			if err = result.Validate(); err == nil {
				actual := make([]execution.StateKeyIdentity, len(result.Items))
				for index, item := range result.Items {
					actual[index] = item.Identity
				}
				err = validateIdentitySet(stateIdentities(chunkItems), actual, "state admission")
			}
			if err == nil {
				reason = firstStateAdmissionFailureReason(result.Items)
				position := make(map[execution.StateKeyIdentity]int, len(chunkItems))
				for index, mutation := range chunkItems {
					position[mutation.Identity] = chunk.start + index
				}
				for _, item := range result.Items {
					switch item.Status {
					case execution.StateAdmissionAccepted:
						encodedBytes[position[item.Identity]] = admittedState{bytes: int64(item.EncodedBytes), frame: item.Frame}
						chunkBytes += int64(item.EncodedBytes)
						chunkLegacyIDs += item.LegacyRecordIDs
					case execution.StateAdmissionDeterministicInvalid:
						deterministic[item.Identity] = item.ReasonCode
						refusalRules = addRefusalRule(refusalRules, item.RefusalRule)
						// The first sentence of the chunk: a Plan refused
						// whole says the same one for every mutation.
						if refusalText == "" {
							refusalText = item.RefusalText
						}
						rejected++
					default:
						err = fmt.Errorf("state admission did not complete: %s", item.Status)
					}
				}
				if err == nil {
					// Reserved as the chunk comes back, not after the last
					// one: the frames are held from here on.
					held.keep(encodedBytes[chunk.start:chunk.end])
				}
			}
		}
		observationResult := observability.Result(observability.ResultSuccess)
		if rejected > 0 {
			observationResult = observability.ResultTerminal
		}
		totals.keys += int64(len(chunkItems))
		totals.bytes += chunkBytes
		coordinator.observeChunk(ctx, observability.StageStateAdmission, operation, chunkStarted, started, observationResult, reason,
			chunk, totals, observability.Counts{Keys: int64(len(chunkItems)), StateBytes: chunkBytes}, err, nil, refusalRules,
			observability.SanitizeErrorText(refusalText), chunkLegacyIDs)
		return err
	})
	if err != nil {
		held.release()
		return nil, nil, nil, fmt.Errorf("alarmd worker: state admission: %w", err)
	}
	return deterministic, encodedBytes, held, nil
}

// applyState writes the accepted mutations of one Plan in Store-sized chunks
// in slice order. When the State store can fence writes and the Slot carries
// a valid owner fence, the fence is verified inside every write so a stale
// owner cannot advance State after admission; the fence instant is taken per
// chunk so a lease that expired while an earlier chunk was written is not
// carried past its deadline. A stale fence surfaces as the ownership error,
// the same as a failed admission. A failed chunk stops the loop: the keys of
// earlier chunks stay written, later chunks are not sent, and the Plan
// neither advances State nor commits Progress until the Slot is re-run, when
// the written keys read back ALREADY_APPLIED. encodedBytes, aligned with
// mutations, feeds the observation and hands the store the frames admission
// kept, and may be nil.
func (coordinator *SlotExecutionCoordinator) applyState(
	ctx context.Context,
	operation execution.Operation,
	contractRef execution.FrozenExecutionContractRef,
	fence execution.OwnerFence,
	contentScope string,
	retention []execution.StateRetentionRequirement,
	horizon int64,
	mutations []execution.StateMutation,
	encodedBytes []admittedState,
) (map[execution.StateKeyIdentity]execution.ReasonCode, error) {
	if err := execution.EnterCommit(ctx, execution.SlotStageState); err != nil {
		return nil, err
	}
	started := time.Now()
	fenced, ok := coordinator.ports.State.(execution.FencedStateStore)
	useFence := ok && fence.Validate(contractRef) == nil
	deterministic := make(map[execution.StateKeyIdentity]execution.ReasonCode)
	var totals applyTotals
	var alreadyApplied observability.StateAlreadyAppliedFacts
	// A Plan is applied by this attempt when this attempt wrote every one of
	// its keys, and not before. Its keys go in chunks; a chunk that fails
	// stops the loop with the earlier chunks written and the later ones never
	// sent, and a Plan noted as applied off its first chunk would have its
	// Slot finalized as evaluated while the series of its unsent keys sit one
	// Slot behind. So the keys are counted as they land, and the Plan is
	// decided once, when the loop is over, from the count against the whole
	// list: any key failed, refused, already there or never sent leaves the
	// count short, and the Plan is recorded as not this attempt's.
	landed := make(map[execution.PlanIdentity]int, 1)
	expected := make(map[execution.PlanIdentity]int, 1)
	for _, mutation := range mutations {
		expected[mutation.Identity.Plan]++
	}
	defer func() {
		recorder := appliedPlansFrom(ctx)
		// A Slot written window by window counts a Plan across its windows
		// and decides it once its last one is written (decideLanding); a
		// window short of its keys already leaves the Plan incomplete.
		landing := planLandingFrom(ctx)
		for plan, keys := range expected {
			if landing != nil {
				landing.add(plan, landed[plan], keys)
				if landed[plan] != keys {
					recorder.recordIncomplete(plan)
				}
				continue
			}
			if landed[plan] == keys {
				recorder.recordApplied(plan)
			} else {
				recorder.recordIncomplete(plan)
			}
		}
	}()
	err := forEachChunk(ctx, len(mutations), coordinator.applyChunkItems(coordinator.budget.MaxStateMutations), func(chunk applyChunk) error {
		chunkItems := mutations[chunk.start:chunk.end]
		expectedRevisions := make(map[execution.StateKeyIdentity]uint64, len(chunkItems))
		for _, mutation := range chunkItems {
			expectedRevisions[mutation.Identity] = mutation.ExpectedBlobRevision
		}
		applyRequest := execution.StateApplyRequest{Contract: contractRef, Retention: retention, Items: chunkItems, HorizonSeconds: horizon}
		if len(encodedBytes) == len(mutations) {
			frames := make([]*execution.EncodedStateFrame, 0, len(chunkItems))
			for _, admitted := range encodedBytes[chunk.start:chunk.end] {
				frames = append(frames, admitted.frame)
			}
			applyRequest.Frames = frames
		}
		chunkStarted := time.Now()
		var result execution.StateApplyResult
		var err error
		if useFence {
			result, err = fenced.ApplyRuntimeFenced(ctx, applyRequest, execution.StateApplyFence{Fence: fence, ContentScope: contentScope})
		} else {
			result, err = coordinator.ports.State.ApplyRuntime(ctx, applyRequest)
		}
		var reason execution.ReasonCode
		var conflicts observability.StateVersionConflictFacts
		var applyRefusalRules []string
		chunkLegacyIDs := 0
		rejected := 0
		if err == nil {
			if err = result.Validate(); err == nil {
				actual := make([]execution.StateKeyIdentity, len(result.Items))
				for index, item := range result.Items {
					actual[index] = item.Identity
				}
				err = validateIdentitySet(stateIdentities(chunkItems), actual, "state apply")
			}
			if err == nil {
				reason = firstStateApplyFailureReason(result.Items)
				var refusal *StateConflictError
				for _, item := range result.Items {
					switch item.Status {
					case execution.StateApplied:
						// One of this Plan's keys is in the store. Counted here
						// because this is the only place that knows; the Plan
						// is decided from the count when every chunk has run.
						landed[item.Identity.Plan]++
						chunkLegacyIDs += item.LegacyRecordIDs
					case execution.StateApplyAlreadyApplied:
						// Written by an earlier attempt at this Slot, so not
						// counted for this one: that attempt left its own mark
						// if it got the Plan whole, and a Plan with any key it
						// did not write is not one this attempt can vouch for.
						// The store says how it decided; a store that does not
						// is read as stable, so a missing kind cannot pose as
						// the one reading this family exists to catch.
						kind := observability.StateAlreadyAppliedKind(item.AlreadyApplied)
						if kind == "" {
							kind = observability.StateAlreadyAppliedStable
						}
						alreadyApplied.Record(observability.StateAlreadyAppliedAtApply, kind,
							string(item.Identity.SeriesIdentityDigest), expectedRevisions[item.Identity], item.StoredBlobRevision)
					case execution.StateApplyDeterministicInvalid:
						deterministic[item.Identity] = item.ReasonCode
						applyRefusalRules = addRefusalRule(applyRefusalRules, item.RefusalRule)
						rejected++
					case execution.StateApplyStale, execution.StateApplyVersionConflict:
						// Every refused item is counted by the comparison
						// that refused it; the error carries the first one's
						// values. A store that does not say which comparison
						// counts as other, so a missing kind cannot pose as
						// the one a reader would act on.
						named := &StateConflictError{Stage: "state apply did not complete", Status: string(item.Status), RepeatedKey: item.RepeatedKey}
						if item.Status == execution.StateApplyVersionConflict {
							named.Kind, named.ExpectedRevision, named.StoredRevision, named.VersionComparison =
								item.VersionConflict, expectedRevisions[item.Identity], item.StoredBlobRevision, item.StoredVersionComparison
							conflicts.Record(observability.StateAlreadyAppliedAtApply, observability.StateVersionConflictKind(item.VersionConflict),
								string(item.Identity.SeriesIdentityDigest), expectedRevisions[item.Identity], item.StoredBlobRevision,
								string(item.StoredVersionComparison), item.RepeatedKey)
						}
						if refusal == nil {
							refusal = named
						}
					default:
						err = fmt.Errorf("state apply did not complete: %s", item.Status)
					}
				}
				if err == nil && refusal != nil {
					err = refusal
				}
			}
		}
		var chunkBytes int64
		if len(encodedBytes) == len(mutations) {
			for _, admitted := range encodedBytes[chunk.start:chunk.end] {
				chunkBytes += admitted.bytes
			}
		}
		observationResult := observability.Result(observability.ResultSuccess)
		if rejected > 0 {
			observationResult = observability.ResultTerminal
		}
		// A version refusal is a typed error with a name of its own. The
		// item's reason is empty for it -- the store names the status, not a
		// reason -- so without this the chunk's line normalised to
		// internal_unknown beside an error_type that already said which
		// refusal it was, while the terminal line for the same round named it.
		if named, ok := StateConflictReason(err); ok {
			reason = named
		}
		// A fenced write the store refused is the store's typed error too: a
		// stale fence, or a content scope that moved under the write. Named
		// the same way, so the three numbers a scope move is verified by --
		// old scope written before it took effect, old scope refused after,
		// new scope written -- are readable from this line's reason.
		if named, ok := ownership.RefusalReason(err); ok {
			reason = execution.ReasonCode(named)
		}
		totals.keys += int64(len(chunkItems))
		totals.bytes += chunkBytes
		var conflictFacts *observability.StateVersionConflictFacts
		if !conflicts.Empty() {
			conflictFacts = &conflicts
		}
		coordinator.observeChunk(ctx, observability.StageStateApplied, operation, chunkStarted, started, observationResult, reason,
			chunk, totals, observability.Counts{Keys: int64(len(chunkItems)), StateBytes: chunkBytes}, err, conflictFacts,
			applyRefusalRules, "", chunkLegacyIDs)
		return err
	})
	if !alreadyApplied.Empty() {
		coordinator.emitAlreadyApplied(ctx, operation, alreadyApplied)
	}
	if err != nil {
		return nil, fmt.Errorf("alarmd worker: apply state: %w", err)
	}
	return deterministic, nil
}

// emitAlreadyApplied publishes one Slot's ALREADY_APPLIED decisions from one
// site. It is published before the apply error is returned, so a chunk that
// met its own landed write and a later chunk that failed are both on the
// record for the round the retry follows.
func (coordinator *SlotExecutionCoordinator) emitAlreadyApplied(ctx context.Context, operation execution.Operation, facts observability.StateAlreadyAppliedFacts) {
	coordinator.emitObservation(ctx, observability.Observation{
		Component: observability.ComponentState, Stage: observability.StageMutationCompared,
		Result: observability.ResultSuccess, Operation: observability.Operation(operation),
		ReasonCode:          observability.ReasonNone,
		StateAlreadyApplied: &facts,
	})
}

func firstStateAdmissionFailureReason(items []execution.StateAdmissionItemResult) execution.ReasonCode {
	ordered := append([]execution.StateAdmissionItemResult(nil), items...)
	sort.Slice(ordered, func(left, right int) bool {
		if ordered[left].Identity != ordered[right].Identity {
			return lessStateIdentity(ordered[left].Identity, ordered[right].Identity)
		}
		return ordered[left].Status < ordered[right].Status
	})
	for _, item := range ordered {
		if item.Status != execution.StateAdmissionAccepted {
			return item.ReasonCode
		}
	}
	return observability.ReasonNone
}

func firstStateApplyFailureReason(items []execution.StateApplyItemResult) execution.ReasonCode {
	ordered := append([]execution.StateApplyItemResult(nil), items...)
	sort.Slice(ordered, func(left, right int) bool {
		if ordered[left].Identity != ordered[right].Identity {
			return lessStateIdentity(ordered[left].Identity, ordered[right].Identity)
		}
		return ordered[left].Status < ordered[right].Status
	})
	for _, item := range ordered {
		if item.Status != execution.StateApplied && item.Status != execution.StateApplyAlreadyApplied {
			return item.ReasonCode
		}
	}
	return observability.ReasonNone
}

func (coordinator *SlotExecutionCoordinator) observe(
	ctx context.Context,
	component observability.Component,
	stage observability.Stage,
	operation execution.Operation,
	started time.Time,
	result observability.Result,
	reason observability.ReasonCode,
	err error,
) {
	coordinator.observeWithCounts(ctx, component, stage, operation, started, result, reason, observability.Counts{}, err)
}

func (coordinator *SlotExecutionCoordinator) observeWithCounts(
	ctx context.Context,
	component observability.Component,
	stage observability.Stage,
	operation execution.Operation,
	started time.Time,
	result observability.Result,
	reason observability.ReasonCode,
	counts observability.Counts,
	err error,
) {
	coordinator.emitObservation(ctx, observability.Observation{
		Component: component, Stage: stage, Result: result,
		Operation:  observability.Operation(operation),
		ReasonCode: reason, Duration: time.Since(started), DurationKnown: true, Counts: counts, Err: err,
	})
}

// emitObservation applies the shared result and reason defaults of internal
// stage observations and hands the observation to the Observer; an observer
// panic never fails the Slot.
// observeOutputUnapplied writes the one line a Slot leaves when the broker
// acknowledged its events and the ownership store then refused their State:
// the Slot stops, Progress stays, and the next owner redoes the Slot from
// Progress and sends the same events again. A refusal with no event before it
// sent nothing and is not this; nor is a State write that failed for any other
// reason, which the same owner retries.
func (coordinator *SlotExecutionCoordinator) observeOutputUnapplied(
	ctx context.Context,
	operation execution.Operation,
	accepted []execution.StateMutation,
	eventsByState map[execution.StateKeyIdentity][]contract.TriggerEventV1,
	withoutMessageByState map[execution.StateKeyIdentity][]execution.EventWithoutMessage,
	err error,
) {
	refusal, refused := ownership.RefusalReason(err)
	if !refused {
		return
	}
	sent, _ := outputsOf(accepted, eventsByState, withoutMessageByState)
	if len(sent) == 0 {
		return
	}
	coordinator.emitObservation(ctx, observability.Observation{
		Component: observability.ComponentState, Stage: observability.StageOutputUnapplied,
		Result: observability.ResultFailed, Operation: observability.Operation(operation),
		ReasonCode: observability.ReasonCode(refusal),
		Counts:     observability.Counts{Events: int64(len(sent))}, Err: err,
	})
}

func (coordinator *SlotExecutionCoordinator) emitObservation(ctx context.Context, observation observability.Observation) {
	if observation.Err != nil {
		observation.Result = observability.Result(observability.ResultFailed)
		if observation.ReasonCode == "" || observation.ReasonCode == observability.ReasonNone {
			observation.ReasonCode = observability.ReasonInternalUnknown
		}
		// Any stage of a Slot stopped by its context's cancellation from
		// above -- the process stopping or the Query Group leaving -- says so.
		if observation.ReasonCode == observability.ReasonInternalUnknown && observability.CancelledFromAbove(ctx, observation.Err) {
			observation.ReasonCode = observability.ReasonSlotCancelled
		}
	} else {
		if observation.Result == "" {
			observation.Result = observability.ResultSuccess
		}
		if observation.ReasonCode == "" {
			observation.ReasonCode = observability.ReasonNone
		}
	}
	defer func() { _ = recover() }()
	coordinator.ports.Observer.Observe(ctx, observation)
}

func (coordinator *SlotExecutionCoordinator) observeCapacityRejection(
	ctx context.Context,
	operation execution.Operation,
	budget observability.CapacityBudget,
	err error,
) {
	observation := observability.Observation{
		Component: observability.ComponentResource, Stage: observability.StageResourceHard,
		Result: observability.ResultPaused, Operation: observability.Operation(operation),
		ReasonCode:     observability.ReasonCode(contract.ReasonResourceHardStop),
		CapacityBudget: budget, Err: err,
	}
	var exceeded *provisionalBudgetExceededError
	if errors.As(err, &exceeded) {
		observation.CapacityRejection = exceeded.facts
		switch {
		case exceeded.others:
			// Within its share; the rest of the pool is others'. The facts
			// carry its own, the pool's and the others' bytes.
			observation.ReasonCode = observability.ReasonCode(contract.ReasonResourcePoolHeldByOthers)
		case exceeded.share:
			// This object is over the share any one of them may hold. Not a
			// pause either, and not the same action as a Slot over the
			// per-Slot cap: the strategy has to be sharded.
			observation.ReasonCode = observability.ReasonCode(contract.ReasonQGBudgetShareExceeded)
		case exceeded.slot:
			// The Slot itself is too large for this process: not a pause that
			// resumes when shared capacity frees up.
			observation.ReasonCode = observability.ReasonCode(contract.ReasonSlotBudgetExceeded)
		}
	}
	defer func() { _ = recover() }()
	coordinator.ports.Observer.Observe(ctx, observation)
}

// stateHorizon is how long this Plan's series keep their runtime state past
// their last write: the Plan's own no-data horizon when it has one frozen -
// the strategy's, or the platform's at compile time - else the platform's
// horizon in force now. Zero when the worker was given no horizon at all.
func (coordinator *SlotExecutionCoordinator) stateHorizon(due execution.DuePlan) int64 {
	if due.CompiledPlan != nil {
		if noData := due.CompiledPlan.NoData(); noData != nil && noData.TrackingHorizonSeconds > 0 {
			return noData.TrackingHorizonSeconds
		}
	}
	if coordinator.ports.StateHorizon == nil {
		return 0
	}
	return max(coordinator.ports.StateHorizon(), 0)
}

// The share is the pool less what the other Query Groups of a replica hold
// beside the largest, split between the large ones one replica holds at once
// (sizing.md, the share):
//
//   - largeQueryGroupsPerWorker: of 842 Query Groups read from a production
//     deployment at 0.6.4882 over three hours of 11-10 (a lower bound; the
//     completion lines are sampled), one holds 25% of the largest's 677 MiB
//     or more, so no replica held two at once. The largest is 677 MiB, the
//     next 84, the 99th percentile 25.
//   - otherGroupsPoolDivisor: the pool's quarter for the rest, 256 MiB on 4
//     GiB. The readings above leave room for an eighth - the most the other
//     groups held beside the largest was 102 MiB - but a replica taking the
//     largest group over held about 840 MiB of live heap with almost nothing
//     reserved, not yet explained. The quarter stays until that is found, so a share
//     raised on the live heap is not stacked on heap the pool does not see.
const (
	largeQueryGroupsPerWorker = 1
	otherGroupsPoolDivisor    = 4
)

// qgShareBytes is the most of the retained-byte pool one Query Group's Slot may
// hold: three quarters of it on a replica that holds one large group at once.
//
// A share exists because acquireEffects otherwise only asks whether the pool
// has room, so one object may legitimately take all of it and every other Query
// Group on the replica starves. Placement spreads large objects across
// replicas, which makes that less likely; it does not stop one object from
// filling the replica it lands on. On a 4 GiB replica the share is 768 MiB;
// the largest Query Group measured, 95,760 series, reserved 628 MiB in a Slot
// whose State held three points a series, about 729 MiB with the five its
// trigger window keeps (sizing.md). A strategy that still does not fit is
// refused by name rather than let fill the pool, and an object nearing its
// share is reported before it arrives (fleet's RETAINED_SHARE_APPROACHING).
func (coordinator *SlotExecutionCoordinator) qgShareBytes() uint64 {
	pool := coordinator.budget.MaxRetainedBytes
	return (pool - pool/otherGroupsPoolDivisor) / largeQueryGroupsPerWorker
}

func summarizeStateLoad(result execution.StatePreflightResult) (observability.Result, observability.ReasonCode) {
	var degraded observability.ReasonCode
	for _, item := range result.Items {
		switch item.Status {
		case execution.StateDeterministicInvalid:
			return observability.ResultTerminal, item.ReasonCode
		case execution.StateRetryableIO:
			if degraded == "" {
				degraded = item.ReasonCode
			}
		}
	}
	if degraded != "" {
		return observability.ResultDegraded, degraded
	}
	return observability.ResultSuccess, observability.ReasonNone
}

func summarizeGapLoad(result execution.GapLoadResult) (observability.Result, observability.ReasonCode) {
	var degraded observability.ReasonCode
	for _, item := range result.Items {
		switch item.Status {
		case execution.GapTerminal:
			return observability.ResultTerminal, item.ReasonCode
		case execution.GapUnavailable:
			if degraded == "" {
				degraded = item.ReasonCode
			}
		}
	}
	if degraded != "" {
		return observability.ResultDegraded, degraded
	}
	return observability.ResultSuccess, observability.ReasonNone
}

func sequencingScope(
	header execution.InternalExecutionHeader,
	stateItems []execution.StatePreflightItem,
	gapItems []execution.PlanGapLoadItem,
) execution.SequencingScope {
	states := make([]execution.StateKeyIdentity, 0, len(stateItems))
	for _, item := range stateItems {
		states = append(states, item.Identity)
	}
	gaps := make([]execution.PlanGapIdentity, 0, len(gapItems))
	for _, item := range gapItems {
		gaps = append(gaps, item.Identity)
	}
	sort.Slice(states, func(left, right int) bool { return lessStateIdentity(states[left], states[right]) })
	sort.Slice(gaps, func(left, right int) bool {
		if gaps[left].Plan != gaps[right].Plan {
			return lessPlanIdentity(gaps[left].Plan, gaps[right].Plan)
		}
		return gaps[left].StateGeneration < gaps[right].StateGeneration
	})
	return execution.SequencingScope{Slot: header.Contract.Slot, StateKeys: states, GapKeys: gaps}
}

// generationRetentionOf is what the gap markers and no-data memory of these
// due Plans live for when this Slot writes them: each Plan's own state
// retention, the one their loads renew them to.
func generationRetentionOf(dues ...execution.DuePlan) (execution.GenerationRetention, error) {
	byPlan := make(map[execution.PlanIdentity][]execution.StateRetentionRequirement, len(dues))
	for _, due := range dues {
		retention, err := execution.DeriveStateRetentionRequirement(due.CompiledPlan)
		if err != nil {
			return execution.GenerationRetention{}, fmt.Errorf("alarmd worker: %w", err)
		}
		byPlan[due.Identity] = retention
	}
	return execution.GenerationRetention{ByPlan: byPlan}, nil
}

func duePlan(plans []execution.DuePlan, identity execution.PlanIdentity) (execution.DuePlan, bool) {
	for _, plan := range plans {
		if plan.Identity == identity {
			return plan, true
		}
	}
	return execution.DuePlan{}, false
}

func validateIdentitySet[T comparable](expected, actual []T, subject string) error {
	if len(actual) != len(expected) {
		return fmt.Errorf("%s result cardinality mismatch", subject)
	}
	wanted := make(map[T]struct{}, len(expected))
	for _, identity := range expected {
		if _, duplicate := wanted[identity]; duplicate {
			return fmt.Errorf("%s request contains a duplicate identity", subject)
		}
		wanted[identity] = struct{}{}
	}
	seen := make(map[T]struct{}, len(actual))
	for _, identity := range actual {
		if _, ok := wanted[identity]; !ok {
			return fmt.Errorf("%s result contains an unknown identity", subject)
		}
		if _, duplicate := seen[identity]; duplicate {
			return fmt.Errorf("%s result contains a duplicate identity", subject)
		}
		seen[identity] = struct{}{}
	}
	return nil
}

func lessPlanIdentity(left, right execution.PlanIdentity) bool {
	if left.TenantID != right.TenantID {
		return left.TenantID < right.TenantID
	}
	if left.BusinessID != right.BusinessID {
		return left.BusinessID < right.BusinessID
	}
	return left.StrategyID < right.StrategyID
}

func lessStateIdentity(left, right execution.StateKeyIdentity) bool {
	if left.Plan != right.Plan {
		return lessPlanIdentity(left.Plan, right.Plan)
	}
	if left.StateGeneration != right.StateGeneration {
		return left.StateGeneration < right.StateGeneration
	}
	return left.SeriesIdentityDigest < right.SeriesIdentityDigest
}

func sortTriggerEvents(events []contract.TriggerEventV1) {
	sort.Slice(events, func(left, right int) bool {
		leftEvent, rightEvent := events[left], events[right]
		leftPlan := execution.PlanIdentity{TenantID: leftEvent.TenantID, BusinessID: leftEvent.BusinessID, StrategyID: leftEvent.PlanRef.StrategyID}
		rightPlan := execution.PlanIdentity{TenantID: rightEvent.TenantID, BusinessID: rightEvent.BusinessID, StrategyID: rightEvent.PlanRef.StrategyID}
		if leftPlan != rightPlan {
			return lessPlanIdentity(leftPlan, rightPlan)
		}
		if leftEvent.RecordRef.DimensionIdentityDigest != rightEvent.RecordRef.DimensionIdentityDigest {
			return leftEvent.RecordRef.DimensionIdentityDigest < rightEvent.RecordRef.DimensionIdentityDigest
		}
		if leftEvent.RecordRef.SourceTime != rightEvent.RecordRef.SourceTime {
			return leftEvent.RecordRef.SourceTime < rightEvent.RecordRef.SourceTime
		}
		if leftEvent.RecordRef.RecordID != rightEvent.RecordRef.RecordID {
			return leftEvent.RecordRef.RecordID < rightEvent.RecordRef.RecordID
		}
		return leftEvent.EventID < rightEvent.EventID
	})
}

// indexStatePreflight is call-local and preserves Find's first-match behavior.
// Values refer to the already retained views; no history or level data is copied.
func indexStatePreflight(result execution.StatePreflightResult) map[execution.StateKeyIdentity]int {
	index := make(map[execution.StateKeyIdentity]int, len(result.Items))
	for position := range result.Items {
		identity := result.Items[position].Identity
		if _, exists := index[identity]; !exists {
			index[identity] = position
		}
	}
	return index
}

// Called only after this invocation received and validated ProgressCommitted.
func (coordinator *SlotExecutionCoordinator) observeCommittedProgress(ctx context.Context, operation execution.Operation, started time.Time, result observability.Result, reason observability.ReasonCode, kind, cause, causeReason string, carried *observability.CarriedReasonFacts, scope *observability.CompletionScopeFacts, coverage execution.HistoryCoverage, evidence *execution.ExecutionEvidence, primary *execution.PrimaryInputFact) {
	if reason == "" {
		reason = observability.ReasonNone
	}
	defer func() { _ = recover() }()
	coverageFacts := historyCoverageFacts(coverage)
	// What held the Slot before it was given up, on the completion that gives
	// it up. The Runner names the holder on every round and the runtime's
	// completion line carried it; this line, which is the one the fleet reads
	// the round from, did not, so the fleet's record of every skipped Slot
	// said what happened and never what held it -- and a Slot the query
	// cooldown held until it fell past the replay range read as this
	// deployment giving detection up for capacity.
	var heldBy *observability.HeldByFacts
	if kind == string(execution.CompletionGapSkipped) {
		heldBy = observability.HeldByFromContext(ctx)
	}
	coordinator.ports.Observer.Observe(ctx, observability.Observation{
		Component: observability.ComponentProgress, Stage: observability.StageProgressCommitted,
		Operation: observability.Operation(operation),
		Result:    result, ReasonCode: reason, Duration: time.Since(started), DurationKnown: true, ProgressCompletionKind: kind,
		ProgressCompletionCause: cause, ProgressCompletionReason: causeReason, ProgressCompletionScope: scope,
		CarriedReason:   carried,
		HistoryCoverage: coverageFacts, ExecutionEvidence: executionEvidenceFacts(evidence),
		PrimaryInput: primaryInputFacts(primary),
		HeldBy:       heldBy,
	})
}

// completionScopeFacts is where a completion's cause was found, for the
// completion's observation; nil when there is no cause or no scope.
func completionScopeFacts(attribution execution.CompletionAttribution) *observability.CompletionScopeFacts {
	scope := attribution.Scope
	if attribution.Cause == "" || !scope.HasPlan && scope.PhysicalQuery == "" {
		return nil
	}
	return &observability.CompletionScopeFacts{TenantID: scope.Plan.TenantID, BusinessID: scope.Plan.BusinessID,
		StrategyID: scope.Plan.StrategyID, LevelID: scope.LevelID, HasLevel: scope.HasLevel, PhysicalQuery: string(scope.PhysicalQuery)}
}

// executionEvidenceFacts carries what an earlier attempt got to onto the
// completion's observation, in the same hand-copied shape as the coverage
// counts above and for the same reason: a field computed where the decision is
// made and dropped on the way out is this codebase's most frequent defect.
func executionEvidenceFacts(evidence *execution.ExecutionEvidence) *observability.ExecutionEvidenceFacts {
	if evidence == nil {
		return nil
	}
	return &observability.ExecutionEvidenceFacts{
		Kind: string(evidence.Kind), PlansApplied: evidence.PlansApplied, PlansTotal: evidence.PlansTotal,
	}
}

// historyCoverageFacts carries the Slot's window counts onto the observation.
//
// A named function rather than a literal inside the observer call, because this
// is a hand-written copy between two structs that have to hold the same numbers,
// and this codebase's most frequent defect by a wide margin is exactly that: a
// field that is computed where the decision is made, used there, and dropped on
// one of the handoffs on the way out. Inline, nothing could execute this
// translation on its own, so a field added to one side and forgotten on the
// other was invisible -- the page would render a deployment it could not have
// observed, with nothing on it disagreeing.
//
// Levels == 0 yields nil on purpose. A Slot that summarised no window at all and
// a Slot whose windows were all complete both report zero short, and they are
// opposite statements; absence is how the first one stays sayable.
func historyCoverageFacts(coverage execution.HistoryCoverage) *observability.HistoryCoverageFacts {
	// A Slot that summarised no window still has something to report when it
	// knows why. Gating on Levels alone made the extreme case -- every series
	// resumed, or none of their State loadable -- report no coverage at all,
	// which is the one reading that most needed to be visible.
	if coverage.Levels == 0 && coverage.Resumed == 0 && coverage.Constrained == 0 && coverage.Superseded == 0 {
		return nil
	}
	facts := &observability.HistoryCoverageFacts{
		Levels: coverage.Levels, Short: coverage.Short, Empty: coverage.Empty,
		Resumed: coverage.Resumed, Constrained: coverage.Constrained, Superseded: coverage.Superseded,
		WorstValid: coverage.WorstValid, WorstRequired: coverage.WorstRequired,
		Guarded: coverage.Guarded,
		Fresh:   coverage.Fresh, ShortFresh: coverage.ShortFresh,
		Abnormal: coverage.Abnormal, AbnormalOnIncomplete: coverage.AbnormalOnIncomplete,
		Unusable: coverage.Unusable, UnusableReason: coverage.UnusableReason,
		MissingMinutes:          append([]int64(nil), coverage.MissingMinutes...),
		MissingMinutesTruncated: coverage.MissingMinutesTruncated,
		ShortUnusable:           coverage.ShortUnusable,
		End:                     coverage.End,
		WindowStart:             coverage.WindowStart,
	}
	if len(facts.MissingMinutes) == 0 {
		facts.MissingMinutes = nil
	}
	for _, window := range coverage.Windows {
		facts.Windows = append(facts.Windows, observability.HistoryWindowFact{
			Strategy: window.Plan.StrategyID, Business: window.Plan.BusinessID,
			Series: string(window.Series), Level: window.LevelID, Valid: window.Valid, Required: window.Required, End: window.End,
			Missing: append([]int64(nil), window.Missing...), MissingTotal: window.MissingTotal,
			Unusable: append([]int64(nil), window.Unusable...), UnusableTotal: window.UnusableTotal,
			Inactive: append([]int64(nil), window.Inactive...),
			Guarded:  window.Guarded, GuardReason: string(window.GuardReason), Fresh: window.Fresh,
		})
	}
	return facts
}

// primaryInputFacts carries what the completion recorded about the PRIMARY
// query onto its observation, by the same hand-copy and for the same reason
// as the coverage above. Nil when the completion carries no primary: a Slot
// skipped or expired without a query has nothing to say about the data.
func primaryInputFacts(primary *execution.PrimaryInputFact) *observability.PrimaryInputFacts {
	if primary == nil {
		return nil
	}
	return &observability.PrimaryInputFacts{Completeness: string(primary.Completeness), DataState: string(primary.DataState),
		EmptiedByTarget: primary.EmptiedByTarget, QuietWhenEmpty: primary.QuietWhenEmpty, UnfilledWhenEmpty: primary.UnfilledWhenEmpty,
		FilteredWhenEmpty: primary.FilteredWhenEmpty, ValueFiltered: primary.ValueFiltered, WitnessData: string(primary.WitnessData),
		Identity: identityFacts(primary.Identity)}
}

// identityFacts restates a round's identity evidence for the observation.
func identityFacts(evidence *execution.IdentityEvidence) *observability.IdentityFacts {
	if evidence == nil {
		return nil
	}
	facts := &observability.IdentityFacts{Withheld: evidence.Withheld, OutsideTarget: evidence.OutsideTarget, KeyMissing: evidence.KeyMissing}
	for _, sample := range evidence.Samples {
		fields := make([]observability.IdentityField, 0, len(sample.Fields))
		for _, field := range sample.Fields {
			fields = append(fields, observability.IdentityField{Name: field.Name, Expected: field.Expected, Actual: field.Actual,
				Present: field.Present, Canonical: field.Canonical})
		}
		facts.Samples = append(facts.Samples, observability.IdentitySample{Fields: fields})
	}
	return facts
}

// configDriftCompletion builds the completion of a Slot whose activations moved
// under it, and keeps it honest about its PRIMARY.
//
// Drift is reported as a partial gap, which is the right shape when the query
// itself answered. It is not when the PRIMARY was unavailable: PARTIAL says
// "some of it arrived", and the completion contract exists precisely to stop
// that from hiding "none of it did". The contract does catch the pair - but at
// progress commit, so the Slot is never recorded and runs again the next
// minute, indefinitely. The pair has to not be produced rather than be
// rejected after the fact.
//
// Both kinds carry the same result and the same reason class, so drift stays
// the reason either way; only the claim about the data changes.
//
// There is one constructor because there were two producers: the Plan drift
// branch and the activation-selection race, the second an inline literal that
// a fix to the first does not reach. Two places that must agree about an
// invariant will eventually stop agreeing.
// The cause is returned beside the kind rather than derived by the caller for
// the reason DeriveCompletionAttribution derives both in one traversal: two
// functions that must agree about the same Slot will eventually disagree, and
// then the page explains a completion that did not happen. Here the two answers come from the one fact this
// constructor already reads.
//
// With a usable primary the Slot completes as COMPLETED_WITH_PARTIAL_GAP and
// names CONFIG_DRIFT as its cause. That kind used to reach the commit line
// with no cause at all, listed beside the partial gaps a provider caused;
// the two call for opposite responses, since an edited strategy needs nobody
// and clears on the next Slot.
func configDriftCompletion(
	contractRef execution.FrozenExecutionContractRef,
	primary *execution.PrimaryInputFact,
	bindings []execution.NamedInputBinding,
	targets []execution.TargetResolutionSummary,
) (execution.SlotCompletion, execution.CompletionAttribution) {
	return activationChangeCompletion(contractRef, primary, activationChangeDrift, bindings, targets)
}

// activationChangeCompletion is configDriftCompletion with the change named:
// the same completion under CONFIG_DRIFT, PLAN_REACTIVATED or PLAN_NOT_ACTIVE,
// which are one shape with three reasons. The unavailable-primary rule is the
// same for all three.
//
// The attribution names the reason beside the cause, as a derived completion
// does: the change's own word, or, when the primary input was unavailable,
// the primary binding's reason and scope (execution.PrimaryBindingAttribution).
// The completion's ReasonCode stays the change's word: it is persisted and
// decides how consecutive gaps fold into a Progress gap summary. The targets
// the Slot resolved at Begin are the completion's whatever changed, as
// LastCompletion reports them after a restart; every caller has resolved
// them by the time it builds this completion.
func activationChangeCompletion(
	contractRef execution.FrozenExecutionContractRef,
	primary *execution.PrimaryInputFact,
	change activationChange,
	bindings []execution.NamedInputBinding,
	targets []execution.TargetResolutionSummary,
) (execution.SlotCompletion, execution.CompletionAttribution) {
	kind := execution.CompletionPartialGap
	attribution := execution.CompletionAttribution{Cause: change.cause(), Reason: change.reason()}
	if primary != nil && primary.Completeness == execution.CompletenessUnavailable {
		kind = execution.CompletionUnavailable
		attribution.Cause = execution.CausePrimaryInputUnavailable
		attribution.Reason, attribution.Scope = execution.PrimaryBindingAttribution(bindings, execution.CompletenessUnavailable)
	}
	return execution.SlotCompletion{
		Contract: contractRef, Kind: kind, Primary: primary, TargetResolutions: targets,
		Result: observability.ResultDegraded, ReasonCode: change.reason(),
	}, attribution
}

// storedStateLabel names the stored state a write-reuse comparison was made
// against. The two steady states a deployment actually spends its rounds in --
// a series that keeps recovering and one that never leaves history warming --
// are both written every round and are separated here, because a single rate
// over both describes neither.
func storedStateLabel(status execution.StateLoadStatus) observability.StateWriteReuseStored {
	switch status {
	case execution.StateFoundReady:
		return observability.StateWriteReuseStoredReady
	case execution.StateFoundWarming:
		return observability.StateWriteReuseStoredWarming
	case execution.StateFoundGapped:
		return observability.StateWriteReuseStoredGapped
	case execution.StateMissingWarming:
		return observability.StateWriteReuseStoredMissing
	default:
		return observability.StateWriteReuseStoredOther
	}
}

func gapExtensionFacts(marker execution.GapGuardSnapshot, mutation execution.PlanGapMutation) *observability.GapExtensionFacts {
	facts := &observability.GapExtensionFacts{StrategyID: mutation.Identity.Plan.StrategyID, MarkerRevision: marker.MarkerRevision}
	for _, scope := range marker.Scopes {
		facts.Persisted = append(facts.Persisted, observability.GapScopeFacts{LevelID: scope.Scope.LevelID, HasLevel: scope.Scope.HasLevel,
			Status: string(scope.Status), Reason: string(scope.ReasonCode), Required: scope.RequiredFullSlots, Observed: scope.ObservedFullSlots})
	}
	for _, scope := range mutation.Scopes {
		facts.Proposed = append(facts.Proposed, observability.GapScopeFacts{LevelID: scope.Scope.LevelID, HasLevel: scope.Scope.HasLevel,
			Kind: string(scope.Kind), Status: string(execution.GapStatusGapped), Reason: string(scope.ReasonCode), Required: scope.RequiredFullSlots})
	}
	return facts
}
