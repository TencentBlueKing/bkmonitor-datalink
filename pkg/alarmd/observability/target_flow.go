package observability

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
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

type TargetFlowConfig struct {
	QueryGroups []string `yaml:"query_groups"`
}

func (c TargetFlowConfig) Validate() error {
	if len(c.QueryGroups) > TargetFlowMaxGroups {
		return errors.New("target flow: too many query groups")
	}
	seen := make(map[string]bool, len(c.QueryGroups))
	for _, q := range c.QueryGroups {
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
	Decision              string `json:"decision,omitempty"`
	BusinessID            string `json:"business_id,omitempty"`
	TenantID              string `json:"tenant_id,omitempty"`
	NextSlot              int64  `json:"next_slot,omitempty"`
	LastFullSlot          int64  `json:"last_full_slot,omitempty"`
	QueuedAtMS            int64  `json:"queued_at_ms,omitempty"`
	QueueWaitNS           int64  `json:"queue_wait_ns,omitempty"`
	Evaluations           uint64 `json:"evaluations_observed"`
	ReadyAtMS             int64  `json:"ready_at_ms,omitempty"`
	Completion            string `json:"completion,omitempty"`
	Attempted             bool   `json:"attempted"`
	Completed             bool   `json:"completed"`
	PlansTruncated        bool   `json:"plans_truncated,omitempty"`
}
type targetFlowRecord struct {
	SlotIdentityKnown bool            `json:"slot_identity_known"`
	Diagnostic        bool            `json:"target_flow"`
	Component         string          `json:"component"`
	Time              int64           `json:"time_unix_ms"`
	Stage             string          `json:"stage"`
	Result            string          `json:"result,omitempty"`
	Reason            string          `json:"reason_code,omitempty"`
	QueryGroup        string          `json:"query_group_key"`
	Strategy          string          `json:"strategy_id,omitempty"`
	Snapshot          string          `json:"snapshot_revision,omitempty"`
	QueryRevision     string          `json:"query_revision,omitempty"`
	ScheduleRevision  string          `json:"schedule_revision,omitempty"`
	SegmentStart      int64           `json:"schedule_segment_start,omitempty"`
	Slot              int64           `json:"evaluation_time,omitempty"`
	Owner             string          `json:"owner_id,omitempty"`
	OwnerEpoch        uint64          `json:"owner_epoch,omitempty"`
	DurationNS        int64           `json:"duration_ns,omitempty"`
	Facts             TargetFlowFacts `json:"facts"`
	Dropped           uint64          `json:"dropped_total"`
	QueueSuppressed   uint64          `json:"queue_suppressed_total,omitempty"`
	RecordLimit       int             `json:"record_limit"`
	ByteLimit         int             `json:"byte_limit"`
}
type TargetFlow struct {
	sequence        atomic.Uint64
	logger          *Logger
	groups          map[string]struct{}
	now             func() time.Time
	mu              sync.Mutex
	start           time.Time
	records, bytes  int
	dropped         uint64
	marked          bool
	queueSeen       map[string]uint8 // Only selected QGs; three fixed rejection reasons per window.
	queueSuppressed uint64
}

func NewTargetFlow(logger *Logger, c TargetFlowConfig) (*TargetFlow, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if len(c.QueryGroups) == 0 {
		return nil, nil
	}
	if logger == nil || logger.writer == nil {
		return nil, errors.New("target flow: logger required")
	}
	groups := make(map[string]struct{}, len(c.QueryGroups))
	for _, q := range c.QueryGroups {
		groups[q] = struct{}{}
	}
	return &TargetFlow{logger: logger, groups: groups, now: time.Now, queueSeen: make(map[string]uint8, len(groups))}, nil
}
func (f *TargetFlow) Selected(q string) bool {
	if f == nil {
		return false
	}
	_, ok := f.groups[q]
	return ok
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
func (f *TargetFlow) Observe(ctx context.Context, o Observation) {
	if f == nil {
		return
	}
	if o.Stage == StageStatePreflight || o.Stage == StageStateApplied {
		if ctx != nil {
			if v, ok := ctx.Value(targetFlowContextKey{}).(*targetFlowContext); ok && (o.Trace.QueryGroupKey == "" || o.Trace.QueryGroupKey == v.queryGroup) {
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
			if v, ok := ctx.Value(targetFlowContextKey{}).(*targetFlowContext); ok && (o.Trace.QueryGroupKey == "" || o.Trace.QueryGroupKey == v.queryGroup) {
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
	default:
		return
	}
	trace := mergeTraceFields(o.Trace, TraceFieldsFromContext(ctx))
	if !f.Selected(trace.QueryGroupKey) {
		return
	}
	o = NormalizeObservation(o)
	facts := TargetFlowFacts{}
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
	for _, s := range []string{stage, result, reason, t.QueryGroupKey, t.StrategyID, t.SnapshotRevision, t.QueryRevision, t.ScheduleRevision, t.OwnerID, facts.Decision, facts.BusinessID, facts.TenantID, facts.Completion, facts.FailureStage, facts.FailureCategory, facts.FailureCode} {
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
		f.marked = false
		for qg := range f.queueSeen {
			delete(f.queueSeen, qg)
		}
	}
	if stage == "queue_skipped" {
		var bit uint8
		switch facts.Decision {
		case "normal_queue_full":
			bit = 1
		case "delayed_queue_full":
			bit = 2
		case "delayed_queue_evicted":
			bit = 4
		}
		if bit != 0 {
			if f.queueSeen[t.QueryGroupKey]&bit != 0 {
				f.queueSuppressed++
				return
			}
			f.queueSeen[t.QueryGroupKey] |= bit
		}
	}
	if f.records >= TargetFlowMaxRecords-1 || f.bytes >= TargetFlowMaxBytes-targetFlowMarkerReserve {
		f.dropLocked(now)
		return
	}
	record := targetFlowRecord{SlotIdentityKnown: t.EvaluationTime > 0 && t.QueryRevision != "" && t.SnapshotRevision != "" && t.ScheduleRevision != "", Diagnostic: true, Component: "runtime", Time: now.UnixMilli(), Stage: stage, Result: result, Reason: reason, QueryGroup: t.QueryGroupKey, Strategy: t.StrategyID, Snapshot: t.SnapshotRevision, QueryRevision: t.QueryRevision, ScheduleRevision: t.ScheduleRevision, SegmentStart: t.ScheduleSegmentStart, Slot: t.EvaluationTime, Owner: t.OwnerID, OwnerEpoch: t.OwnerEpoch, DurationNS: int64(d), Facts: facts, Dropped: f.dropped, RecordLimit: TargetFlowMaxRecords, ByteLimit: TargetFlowMaxBytes}
	record.QueueSuppressed = f.queueSuppressed
	wire, _ := json.Marshal(record)
	wire = append(wire, '\n')
	if len(wire) > targetFlowMaxRecordBytes || f.records >= TargetFlowMaxRecords-1 || f.bytes+len(wire) > TargetFlowMaxBytes-targetFlowMarkerReserve {
		f.dropLocked(now)
		return
	}
	f.records++
	f.bytes += len(wire)
	_, _ = f.logger.writer.Write(wire)
}
func (f *TargetFlow) drop() {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.now()
	if f.start.IsZero() || now.Sub(f.start) >= time.Minute || now.Before(f.start) {
		f.start = now
		f.records = 0
		f.bytes = 0
		f.marked = false
		for qg := range f.queueSeen {
			delete(f.queueSeen, qg)
		}
	}
	f.dropLocked(now)
}
func (f *TargetFlow) dropLocked(now time.Time) {
	f.dropped++
	if f.marked {
		return
	}
	f.marked = true
	marker, _ := json.Marshal(struct {
		Time            int64  `json:"time_unix_ms"`
		Stage           string `json:"stage"`
		Dropped         uint64 `json:"dropped_total"`
		QueueSuppressed uint64 `json:"queue_suppressed_total,omitempty"`
		RecordLimit     int    `json:"record_limit"`
		ByteLimit       int    `json:"byte_limit"`
	}{now.UnixMilli(), "target_flow_dropped", f.dropped, f.queueSuppressed, TargetFlowMaxRecords, TargetFlowMaxBytes})
	marker = append(marker, '\n')
	if f.records < TargetFlowMaxRecords && f.bytes+len(marker) <= TargetFlowMaxBytes {
		f.records++
		f.bytes += len(marker)
		_, _ = f.logger.writer.Write(marker)
	}
}

// Record emits only selected QG lifecycle facts outside a Runner invocation.
func (f *TargetFlow) Record(stage, q string, facts TargetFlowFacts) {
	if f.Selected(q) {
		f.emit(stage, "", "", TraceFields{QueryGroupKey: q}, facts, 0)
	}
}
