package observability

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Fixed diagnostic limits are independent of all execution and ordinary log budgets.
const TargetFlowMaxGroups = 32
const TargetFlowMaxRecords = 4096
const TargetFlowMaxBytes = 4 << 20
const targetFlowMaxRecordBytes = 4096
const targetFlowMarkerReserve = 1024

// Keep one quarter of the diagnostic budget for completion facts. At the
// supported 10-second minimum cadence, 32 selected QGs produce 192 Slots per
// minute; the standard five-record completion chain fits inside this reserve.
const targetFlowCriticalReserveRecords = 1024
const targetFlowCriticalReserveBytes = 1 << 20

// validateSelection bounds what may be observed at once. The limit is the
// diagnostic budget: the per-minute record and byte budgets are shared across
// everything selected, so the more objects are observed the less of each one's
// lifecycle fits.
func validateSelection(queryGroups []string) error {
	if len(queryGroups) > TargetFlowMaxGroups {
		return errors.New("target flow: too many query groups")
	}
	seen := make(map[string]bool, len(queryGroups))
	for _, q := range queryGroups {
		b, e := hex.DecodeString(q)
		if e != nil || len(b) != 32 || seen[q] {
			return errors.New("target flow: query groups must be unique SHA256 identities")
		}
		seen[q] = true
	}
	return nil
}

// TargetFlowFacts contains bounded lifecycle facts, never input or per-point values.
type TargetFlowFacts struct {
	RangeFirst            int64  `json:"range_first,omitempty"`
	RangeLast             int64  `json:"range_last,omitempty"`
	RangeCount            uint32 `json:"range_count,omitempty"`
	RangeDigest           string `json:"range_digest,omitempty"`
	StatePreflightCalls   uint64 `json:"state_preflight_calls_observed"`
	StatePreflightKeys    uint64 `json:"state_preflight_keys_observed"`
	StatePreflightNS      int64  `json:"state_preflight_ns_observed"`
	StateApplyCalls       uint64 `json:"state_apply_calls_observed"`
	StateApplyKeys        uint64 `json:"state_apply_keys_observed"`
	StateApplyNS          int64  `json:"state_apply_ns_observed"`
	EvaluationNS          int64  `json:"evaluation_ns_observed"`
	RunID                 uint64 `json:"run_id,omitempty"`
	FailureStage          string `json:"failure_stage,omitempty"`
	FailureCategory       string `json:"failure_category,omitempty"`
	FailureCode           string `json:"failure_code,omitempty"`
	ExecutionOutcomeKnown bool   `json:"execution_outcome_known"`
	// Capacity facts of a resource_hard rejection: the budget kind, the phase
	// of the failed reservation and the counts the Coordinator logged. A
	// per-Slot cap rejection (phase slot_output) has no shared usage; its
	// own_used is the total the Slot would have reached.
	CapacityBudget     string  `json:"capacity_budget,omitempty"`
	CapacityPhase      string  `json:"capacity_phase,omitempty"`
	CapacityOwnUsed    *uint64 `json:"capacity_own_used,omitempty"`
	CapacitySharedUsed uint64  `json:"capacity_shared_used,omitempty"`
	CapacityRequested  uint64  `json:"capacity_requested,omitempty"`
	CapacityLimit      uint64  `json:"capacity_limit,omitempty"`
	Decision           string  `json:"decision,omitempty"`
	BusinessID         string  `json:"business_id,omitempty"`
	TenantID           string  `json:"tenant_id,omitempty"`
	NextSlot           int64   `json:"next_slot,omitempty"`
	LastFullSlot       int64   `json:"last_full_slot,omitempty"`
	QueuedAtMS         int64   `json:"queued_at_ms,omitempty"`
	QueueWaitNS        int64   `json:"queue_wait_ns,omitempty"`
	Evaluations        uint64  `json:"evaluations_observed"`
	ReadyAtMS          int64   `json:"ready_at_ms,omitempty"`
	Completion         string  `json:"completion,omitempty"`
	Attempted          bool    `json:"attempted"`
	Completed          bool    `json:"completed"`
	PlansTruncated     bool    `json:"plans_truncated,omitempty"`
}
type targetFlowRecord struct {
	SlotIdentityKnown bool   `json:"slot_identity_known"`
	Diagnostic        bool   `json:"target_flow"`
	Component         string `json:"component"`
	// Process identifies the process that wrote this record, because run_id
	// alone does not identify a round. The sequence behind run_id is per
	// process and starts again at one, while the records outlive the process
	// that wrote them -- they are kept per object, under a retention longer
	// than a window, and every replica writes an object's records to the same
	// place. So after a restart, or after the object moves to another replica,
	// two unrelated rounds carry the same run_id, and anything grouping by it
	// alone folds them into one.
	Process          string          `json:"process_id"`
	Time             int64           `json:"time_unix_ms"`
	Stage            string          `json:"stage"`
	Result           string          `json:"result,omitempty"`
	Reason           string          `json:"reason_code,omitempty"`
	QueryGroup       string          `json:"query_group_key"`
	Strategy         string          `json:"strategy_id,omitempty"`
	Snapshot         string          `json:"snapshot_revision,omitempty"`
	QueryRevision    string          `json:"query_revision,omitempty"`
	ScheduleRevision string          `json:"schedule_revision,omitempty"`
	SegmentStart     int64           `json:"schedule_segment_start,omitempty"`
	Slot             int64           `json:"evaluation_time,omitempty"`
	Owner            string          `json:"owner_id,omitempty"`
	OwnerEpoch       uint64          `json:"owner_epoch,omitempty"`
	DurationNS       int64           `json:"duration_ns,omitempty"`
	Facts            TargetFlowFacts `json:"facts"`
	Dropped          uint64          `json:"dropped_total"`
	QueueSuppressed  uint64          `json:"queue_suppressed_total,omitempty"`
	RecordLimit      int             `json:"record_limit"`
	ByteLimit        int             `json:"byte_limit"`
}

// targetFlowSelection is the set of query groups whose lifecycle is recorded.
// It is swapped whole rather than mutated, because Selected is read on the
// dispatch path for every round and must not take a lock there.
type targetFlowSelection map[string]struct{}

type TargetFlow struct {
	sequence atomic.Uint64
	// process tells this process's records apart from those an earlier one left
	// behind. It cannot be the replica name: a container restarted in place
	// keeps that name, and the sequence behind run_id starts over anyway.
	process         string
	logger          *Logger
	groups          atomic.Pointer[targetFlowSelection]
	now             func() time.Time
	mu              sync.Mutex
	start           time.Time
	queueSeen       map[string]uint8 // Only selected QGs; three fixed rejection reasons per window.
	queueSuppressed uint64
	records, bytes  int
	dropped         uint64
	windowDropped   uint64
	nextDropMarker  uint64
	// sink receives the same record the log line carries, so a window's output
	// can be read back where the window was opened. It is stored atomically and
	// called outside the lock: this is a diagnostic path, and a slow or broken
	// sink must not be able to hold up the pipeline whose facts it describes.
	sink atomic.Pointer[func(queryGroup string, record []byte)]
}

// SetSink installs where a selected object's records go besides the log.
//
// Writing only to the log means the window's output leaves through a different
// door than the one it was opened at: whoever opened it has to know to go and
// search a log index, with no link and no hint. A sink the page can read back
// closes that loop. Nil removes it.
func (f *TargetFlow) SetSink(sink func(queryGroup string, record []byte)) {
	if f == nil {
		return
	}
	if sink == nil {
		f.sink.Store(nil)
		return
	}
	f.sink.Store(&sink)
}

// NewTargetFlow builds a flow that observes nothing until a window is opened.
//
// There is no selection to pass in. What gets observed is decided while the
// process runs, by windows that expire on their own; a selection fixed at
// startup could only be changed by a release, and a choice made during one
// investigation then outlives it with nobody able to say what it was for.
func NewTargetFlow(logger *Logger) (*TargetFlow, error) {
	if logger == nil || logger.writer == nil {
		return nil, errors.New("target flow: logger required")
	}
	flow := &TargetFlow{
		logger: logger, now: time.Now, process: newProcessIdentity(),
		queueSeen: make(map[string]uint8, TargetFlowMaxGroups),
	}
	flow.selectGroups(nil)
	return flow, nil
}

// newProcessIdentity returns something short that no other process is likely to
// produce. It is a label, never a key: it only has to make two processes'
// records distinguishable in the same list, so a failed read of the random
// source falls back to the start time rather than failing construction and
// taking the whole diagnostic path down with it.
func newProcessIdentity() string {
	var raw [4]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(raw[:])
}

func (f *TargetFlow) selectGroups(queryGroups []string) {
	selection := make(targetFlowSelection, len(queryGroups))
	for _, queryGroup := range queryGroups {
		selection[queryGroup] = struct{}{}
	}
	f.groups.Store(&selection)
}

// Select replaces the observed set. It is how a window opened at runtime takes
// effect: replicas read the window on their reconcile tick and call this, so a
// window needs no restart to start and no release to stop.
//
// The whole set is replaced rather than added to, because the set is the answer
// to "what is being observed right now" and a merge would make a closed window
// depend on every replica having seen the close.
func (f *TargetFlow) Select(queryGroups []string) error {
	if f == nil {
		return errors.New("target flow: no flow to select on")
	}
	if err := validateSelection(queryGroups); err != nil {
		return err
	}
	f.selectGroups(queryGroups)
	return nil
}

func (f *TargetFlow) Selected(q string) bool {
	if f == nil {
		return false
	}
	selection := f.groups.Load()
	if selection == nil {
		return false
	}
	_, ok := (*selection)[q]
	return ok
}

// SelectionSize reports how many query groups are observed, so the budget the
// selection shares can be reported alongside what it was spent on.
func (f *TargetFlow) SelectionSize() int {
	if f == nil {
		return 0
	}
	selection := f.groups.Load()
	if selection == nil {
		return 0
	}
	return len(*selection)
}

type targetFlowContextKey struct{}
type targetFlowContext struct {
	preflightCalls atomic.Uint64
	preflightKeys  atomic.Uint64
	preflightNS    atomic.Int64
	applyCalls     atomic.Uint64
	applyKeys      atomic.Uint64
	applyNS        atomic.Int64
	flow           *TargetFlow
	queryGroup     string
	evaluations    atomic.Uint64
	evaluationNS   atomic.Int64
	runID          uint64
}

func (f *TargetFlow) Context(ctx context.Context, q string) context.Context {
	if !f.Selected(q) {
		return ctx
	}
	return context.WithValue(ctx, targetFlowContextKey{}, &targetFlowContext{flow: f, queryGroup: q, runID: f.sequence.Add(1)})
}
func TargetFlowEnabled(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	_, ok := ctx.Value(targetFlowContextKey{}).(*targetFlowContext)
	return ok
}
func EmitTargetFlow(ctx context.Context, stage string, trace TraceFields, facts TargetFlowFacts) {
	if ctx == nil {
		return
	}
	v, ok := ctx.Value(targetFlowContextKey{}).(*targetFlowContext)
	if !ok {
		return
	}
	if trace.QueryGroupKey == "" {
		trace.QueryGroupKey = v.queryGroup
	}
	if trace.QueryGroupKey != v.queryGroup {
		return
	}
	facts.RunID = v.runID
	if stage == "runner_decision" {
		facts.StatePreflightCalls = v.preflightCalls.Load()
		facts.StatePreflightKeys = v.preflightKeys.Load()
		facts.StatePreflightNS = v.preflightNS.Load()
		facts.StateApplyCalls = v.applyCalls.Load()
		facts.StateApplyKeys = v.applyKeys.Load()
		facts.StateApplyNS = v.applyNS.Load()
		facts.Evaluations = v.evaluations.Load()
		facts.EvaluationNS = v.evaluationNS.Load()
	}
	v.flow.emit(stage, "", "", trace, facts, 0)
}

// Observe records the facts a selected round accumulates.
//
// The counter branches below check that this flow is the one that created the
// context they are about to add to. Without that check any flow reachable from
// the observer chain adds to any round's counters, and two flows in one process
// double every count -- which is now possible, because a deployment that selects
// nothing still has a flow standing by for a window to be opened on it.
func (f *TargetFlow) Observe(ctx context.Context, o Observation) {
	if f == nil {
		return
	}
	if o.Stage == StageStatePreflight || o.Stage == StageStateApplied {
		if ctx != nil {
			if v, ok := ctx.Value(targetFlowContextKey{}).(*targetFlowContext); ok && v.flow == f && (o.Trace.QueryGroupKey == "" || o.Trace.QueryGroupKey == v.queryGroup) {
				calls, keys, ns := &v.preflightCalls, &v.preflightKeys, &v.preflightNS
				if o.Stage == StageStateApplied {
					calls, keys, ns = &v.applyCalls, &v.applyKeys, &v.applyNS
				}
				calls.Add(1)
				if o.Counts.Keys > 0 {
					keys.Add(uint64(o.Counts.Keys))
				}
				if o.Duration > 0 {
					ns.Add(int64(o.Duration))
				}
			}
		}
		return
	}
	if o.Stage == StageEvaluationCompleted {
		if ctx != nil {
			if v, ok := ctx.Value(targetFlowContextKey{}).(*targetFlowContext); ok && v.flow == f && (o.Trace.QueryGroupKey == "" || o.Trace.QueryGroupKey == v.queryGroup) {
				v.evaluations.Add(1)
				if o.Duration > 0 {
					v.evaluationNS.Add(int64(o.Duration))
				}
			}
		}
		return
	}
	switch o.Stage {
	case StageAssignmentAcquired, StageAssignmentLost, StageTakeoverStarted, StageTakeoverCompleted, StageScheduleDue, StageSlotStarted, StageSlotCompleted, StageQueryCompleted, StageProgressCommitted, StageRunnerCompleted, StageSlotSourceCompleted:
	case StageResourceHard:
		// Only a capacity rejection names the Query Group it stopped; process
		// level resource stops carry no Slot coordinates and stay in the
		// runtime log.
		if NormalizeObservation(o).CapacityRejection == nil {
			return
		}
	default:
		return
	}
	trace := mergeTraceFields(o.Trace, TraceFieldsFromContext(ctx))
	if !f.Selected(trace.QueryGroupKey) {
		return
	}
	o = NormalizeObservation(o)
	facts := TargetFlowFacts{}
	if r := o.CapacityRejection; r != nil {
		facts.CapacityBudget, facts.CapacityPhase = string(o.CapacityBudget), r.Phase
		facts.CapacityOwnUsed, facts.CapacitySharedUsed, facts.CapacityRequested, facts.CapacityLimit = r.OwnUsed, r.SharedUsed, r.Requested, r.Limit
	}
	if ValidExecuteOutcome(o.ExecuteOutcome) {
		facts.Decision = o.ExecuteOutcome
	}
	if ValidProgressCompletionKind(o.ProgressCompletionKind) {
		facts.Completion = o.ProgressCompletionKind
		facts.ExecutionOutcomeKnown = true
		facts.Completed = true
	}
	if ctx != nil {
		if v, ok := ctx.Value(targetFlowContextKey{}).(*targetFlowContext); ok {
			facts.RunID = v.runID
		}
	}
	if o.QueryFailure != nil {
		facts.FailureStage = o.QueryFailure.Stage
		facts.FailureCategory = o.QueryFailure.Category
		facts.FailureCode = o.QueryFailure.Code
	}
	f.emit(string(o.Stage), string(o.Result), string(o.ReasonCode), trace, facts, o.Duration)
}
func (f *TargetFlow) emit(stage, result, reason string, t TraceFields, facts TargetFlowFacts, d time.Duration) {
	defer func() { _ = recover() }() // diagnostics never change ACK, lease or execution returns
	if !f.Selected(t.QueryGroupKey) {
		return
	}
	// Reject oversize identity fields rather than serializing arbitrary caller payloads.
	for _, s := range []string{stage, result, reason, t.QueryGroupKey, t.StrategyID, t.SnapshotRevision, t.QueryRevision, t.ScheduleRevision, t.OwnerID, facts.Decision, facts.BusinessID, facts.TenantID, facts.Completion, facts.FailureStage, facts.FailureCategory, facts.FailureCode, facts.CapacityBudget, facts.CapacityPhase} {
		if len(s) > 128 {
			f.drop()
			return
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.now()
	if f.start.IsZero() || now.Sub(f.start) >= time.Minute || now.Before(f.start) {
		f.start = now
		f.records = 0
		f.bytes = 0
		f.windowDropped = 0
		f.nextDropMarker = 1
		for qg := range f.queueSeen {
			delete(f.queueSeen, qg)
		}
	}
	if stage == "queue_skipped" && f.suppressQueueLocked(t.QueryGroupKey, facts.Decision) {
		return
	}
	critical := targetFlowCritical(stage, facts)
	if !critical && f.records >= TargetFlowMaxRecords-1-targetFlowCriticalReserveRecords {
		f.dropLocked(now, "critical_record_reserve")
		return
	}
	if !critical && f.bytes >= TargetFlowMaxBytes-targetFlowMarkerReserve-targetFlowCriticalReserveBytes {
		f.dropLocked(now, "critical_byte_reserve")
		return
	}
	if f.records >= TargetFlowMaxRecords-1 {
		f.dropLocked(now, "record_limit")
		return
	}
	if f.bytes >= TargetFlowMaxBytes-targetFlowMarkerReserve {
		f.dropLocked(now, "byte_limit")
		return
	}
	record := targetFlowRecord{SlotIdentityKnown: t.EvaluationTime > 0 && t.QueryRevision != "" && t.SnapshotRevision != "" && t.ScheduleRevision != "", Diagnostic: true, Component: "runtime", Process: f.process, Time: now.UnixMilli(), Stage: stage, Result: result, Reason: reason, QueryGroup: t.QueryGroupKey, Strategy: t.StrategyID, Snapshot: t.SnapshotRevision, QueryRevision: t.QueryRevision, ScheduleRevision: t.ScheduleRevision, SegmentStart: t.ScheduleSegmentStart, Slot: t.EvaluationTime, Owner: t.OwnerID, OwnerEpoch: t.OwnerEpoch, DurationNS: int64(d), Facts: facts, Dropped: f.dropped, RecordLimit: TargetFlowMaxRecords, ByteLimit: TargetFlowMaxBytes}
	record.QueueSuppressed = f.queueSuppressed
	wire, _ := json.Marshal(record)
	wire = append(wire, '\n')
	if len(wire) > targetFlowMaxRecordBytes {
		f.dropLocked(now, "record_oversize")
		return
	}
	if !critical && f.records >= TargetFlowMaxRecords-1-targetFlowCriticalReserveRecords {
		f.dropLocked(now, "critical_record_reserve")
		return
	}
	if !critical && f.bytes+len(wire) > TargetFlowMaxBytes-targetFlowMarkerReserve-targetFlowCriticalReserveBytes {
		f.dropLocked(now, "critical_byte_reserve")
		return
	}
	if f.records >= TargetFlowMaxRecords-1 {
		f.dropLocked(now, "record_limit")
		return
	}
	if f.bytes+len(wire) > TargetFlowMaxBytes-targetFlowMarkerReserve {
		f.dropLocked(now, "byte_limit")
		return
	}
	f.records++
	f.bytes += len(wire)
	_, _ = f.logger.writer.Write(wire)
	if sink := f.sink.Load(); sink != nil {
		// The record is already serialized and is not touched again, so the
		// sink may keep it. A panic in a diagnostic sink must not escape into
		// the caller's execution path.
		func() {
			defer func() { _ = recover() }()
			(*sink)(t.QueryGroupKey, wire)
		}()
	}
}

func (f *TargetFlow) suppressQueueLocked(queryGroup, decision string) bool {
	var bit uint8
	switch decision {
	case "normal_queue_full":
		bit = 1
	case "delayed_queue_full":
		bit = 2
	case "delayed_queue_evicted":
		bit = 4
	}
	if bit == 0 {
		return false
	}
	if f.queueSeen[queryGroup]&bit != 0 {
		f.queueSuppressed++
		return true
	}
	f.queueSeen[queryGroup] |= bit
	return false
}

func (f *TargetFlow) drop() {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.now()
	if f.start.IsZero() || now.Sub(f.start) >= time.Minute || now.Before(f.start) {
		f.start = now
		f.records = 0
		f.bytes = 0
		f.windowDropped = 0
		f.nextDropMarker = 1
		for qg := range f.queueSeen {
			delete(f.queueSeen, qg)
		}
	}
	f.dropLocked(now, "identity_oversize")
}
func (f *TargetFlow) dropLocked(now time.Time, reason string) {
	f.dropped++
	f.windowDropped++
	if f.nextDropMarker == 0 {
		f.nextDropMarker = 1
	}
	if f.windowDropped < f.nextDropMarker {
		return
	}
	if f.nextDropMarker <= ^uint64(0)/2 {
		f.nextDropMarker *= 2
	}
	marker, _ := json.Marshal(struct {
		Time                int64  `json:"time_unix_ms"`
		Stage               string `json:"stage"`
		DropReason          string `json:"drop_reason"`
		Dropped             uint64 `json:"dropped_total"`
		WindowDropped       uint64 `json:"window_dropped"`
		QueueSuppressed     uint64 `json:"queue_suppressed_total,omitempty"`
		SelectedQueryGroups int    `json:"selected_query_groups"`
		WrittenRecords      int    `json:"written_records"`
		WrittenBytes        int    `json:"written_bytes"`
		RecordLimit         int    `json:"record_limit"`
		ByteLimit           int    `json:"byte_limit"`
	}{now.UnixMilli(), "target_flow_dropped", reason, f.dropped, f.windowDropped, f.queueSuppressed, f.SelectionSize(), f.records, f.bytes, TargetFlowMaxRecords, TargetFlowMaxBytes})
	marker = append(marker, '\n')
	if f.records < TargetFlowMaxRecords && f.bytes+len(marker) <= TargetFlowMaxBytes {
		f.records++
		f.bytes += len(marker)
		_, _ = f.logger.writer.Write(marker)
	}
}

func targetFlowCritical(stage string, facts TargetFlowFacts) bool {
	switch stage {
	case string(StageQueryCompleted), string(StageProgressCommitted), string(StageSlotCompleted), string(StageRunnerCompleted), string(StageSlotSourceCompleted), string(StageResourceHard), "execution_outcome", "runner_return", "expired_range_returned":
		return true
	case "runner_decision":
		return facts.ExecutionOutcomeKnown || facts.Completed
	default:
		return false
	}
}

// Record emits only selected QG lifecycle facts outside a Runner invocation.
func (f *TargetFlow) Record(stage, q string, facts TargetFlowFacts) {
	if f.Selected(q) {
		f.emit(stage, "", "", TraceFields{QueryGroupKey: q}, facts, 0)
	}
}
