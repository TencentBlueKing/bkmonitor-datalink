package observability

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

// These are schema bounds, not deployment capacity defaults.
const SeriesSampleMaxBytes = 4096
const SeriesSampleMaxLevels = 2

// encoding/json stages a whole record before Write. The whitelist has fewer
// than 64 string fields (including both levels), each at most 128 bytes. JSON
// escaping expands each input byte at most sixfold: strings + keys + scalar
// fields fit in 64 KiB. Reserve twice that for buffer growth/allocator rounding,
// for every potentially concurrent encoder, even though normal records fit in
// the 4 KiB payload. This is a schema bound, not a deployment budget.
const SeriesSampleEncodingScratchBytes = 128 << 10

// SeriesSampleLimits is an explicit allocation from the process observation
// budget. The runtime derives it from effective resources; zero disables it.
// Lifecycle diagnostics keep their existing allocation separately.
type SeriesSampleLimits struct {
	RecordsPerMinute int `json:"records_per_minute"`
	BytesPerMinute   int `json:"bytes_per_minute"`
	QueueCapacity    int `json:"queue_capacity"`
}

type SeriesSampleSelection struct {
	QueryGroup           string    `json:"query_group"`
	WindowID             string    `json:"window_id"`
	OpenedAt             time.Time `json:"opened_at"`
	ExpiresAt            time.Time `json:"expires_at"`
	TenantID             string    `json:"tenant_id"`
	BusinessID           string    `json:"business_id"`
	StrategyID           string    `json:"strategy_id"`
	StateGeneration      string    `json:"state_generation"`
	PlanScheduleRevision string    `json:"plan_schedule_revision"`
	// Empty selects and pins the first successfully reserved series in this
	// window. It is deliberately not representative or exhaustive discovery.
	SeriesDigest string `json:"series_digest,omitempty"`
	SeriesKind   string `json:"series_kind,omitempty"`
	LevelID      uint32 `json:"level_id,omitempty"`
}

func (s SeriesSampleSelection) Validate() error {
	if s.QueryGroup == "" || s.WindowID == "" || s.TenantID == "" || s.BusinessID == "" || s.StrategyID == "" || s.StateGeneration == "" || s.PlanScheduleRevision == "" || s.OpenedAt.IsZero() || !s.ExpiresAt.After(s.OpenedAt) {
		return errors.New("series sample: fixed Plan identity, version and window lifetime are required")
	}
	if s.SeriesKind != "" && s.SeriesKind != "NO_DATA" {
		return errors.New("series sample: invalid series kind")
	}
	for _, v := range []string{s.QueryGroup, s.WindowID, s.TenantID, s.BusinessID, s.StrategyID, s.StateGeneration, s.PlanScheduleRevision, s.SeriesDigest} {
		if len(v) > 128 {
			return errors.New("series sample: identity exceeds schema bound")
		}
	}
	return nil
}

type SeriesSampleCandidate struct {
	QueryGroup, TenantID, BusinessID, StrategyID                    string
	StateGeneration, PlanScheduleRevision, SeriesDigest, SeriesKind string
	Slot                                                            int64
}

// SeriesSample contains only copied scalar facts. Missing decision evidence is
// represented explicitly, not as a zero count. No state view, raw input, query,
// dimensions, detector configuration or event body crosses this boundary.
type SeriesSample struct {
	Kind                 string              `json:"kind"`
	WindowID             string              `json:"window_id"`
	OpenedAt             int64               `json:"opened_at_unix_ms"`
	ProcessID            string              `json:"process_id"`
	RunID                uint64              `json:"run_id"`
	ExecutionID          string              `json:"execution_id"`
	QueryGroup           string              `json:"query_group"`
	TenantID             string              `json:"tenant_id"`
	BusinessID           string              `json:"business_id"`
	StrategyID           string              `json:"strategy_id"`
	SnapshotRevision     string              `json:"snapshot_revision"`
	QueryRevision        string              `json:"query_revision"`
	ScheduleRevision     string              `json:"schedule_revision"`
	PlanScheduleRevision string              `json:"plan_schedule_revision"`
	StateGeneration      string              `json:"state_generation"`
	StateStatus          string              `json:"state_status"`
	Slot                 int64               `json:"slot"`
	SeriesDigest         string              `json:"series_digest"`
	SeriesKind           string              `json:"series_kind"`
	Coverage             string              `json:"coverage"`
	RecordID             string              `json:"record_id"`
	SourceTime           int64               `json:"source_time"`
	Provisional          bool                `json:"evaluation_provisional"`
	RecoveryHeld         bool                `json:"recovery_held"`
	RecoveryCause        string              `json:"recovery_cause,omitempty"`
	OpenAlertGate        string              `json:"open_alert_gate,omitempty"`
	EventID              string              `json:"event_id,omitempty"`
	Levels               []SeriesSampleLevel `json:"levels"`
}

type SeriesSampleLevel struct {
	LevelID                 uint32  `json:"level_id"`
	DetectResult            string  `json:"detect_result,omitempty"`
	DetectReason            string  `json:"detect_reason,omitempty"`
	PredicateFingerprint    string  `json:"predicate_fingerprint,omitempty"`
	MatchedAlgorithmOrdinal *uint32 `json:"matched_algorithm_ordinal,omitempty"`
	MatchedGroupOrdinal     *uint32 `json:"matched_group_ordinal,omitempty"`
	NormalizedScalar        string  `json:"normalized_scalar,omitempty"`
	ScalarStatus            string  `json:"scalar_status"`
	InputCompletion         string  `json:"input_completion"`
	HistoryCompleteness     string  `json:"history_completeness,omitempty"`
	HistoryValid            uint32  `json:"history_valid"`
	HistoryRequired         uint32  `json:"history_required"`
	HistoryStart            int64   `json:"history_start"`
	HistoryEnd              int64   `json:"history_end"`
	HistoryForced           bool    `json:"history_forced"`
	Fresh                   bool    `json:"fresh"`
	EffectiveStatus         string  `json:"effective_status,omitempty"`
	DecisionStatus          string  `json:"decision_status"`
	TriggerRequired         uint32  `json:"trigger_required"`
	TriggerWindow           uint32  `json:"trigger_window"`
	TriggerObserved         *uint32 `json:"trigger_observed,omitempty"`
	RecoveryEnabled         bool    `json:"recovery_enabled"`
	RecoveryRequired        uint32  `json:"recovery_required"`
	RecoveryObserved        *uint32 `json:"recovery_observed,omitempty"`
	Outcome                 string  `json:"outcome"`
	Reason                  string  `json:"reason"`
	StateDisposition        string  `json:"state_disposition"`
}

type SeriesSampleHealth struct {
	Scope         string `json:"scope"`
	Selected      uint64 `json:"selected"`
	Recorded      uint64 `json:"recorded"`
	BudgetDropped uint64 `json:"budget_dropped"`
	QueueDropped  uint64 `json:"queue_dropped"`
	Oversize      uint64 `json:"oversize"`
	Expired       uint64 `json:"expired"`
}

type sampleSelection struct {
	SeriesSampleSelection
	pinnedDigest string
	lastSlot     int64
	hasSlot      bool
}
type sampleSelections map[string]*sampleSelection

type SeriesSampler struct {
	limits                                                                  SeriesSampleLimits
	selected                                                                atomic.Pointer[sampleSelections]
	mu                                                                      sync.Mutex // short budget/selection bookkeeping only; never encoding or I/O
	start                                                                   time.Time
	records, bytes                                                          int
	pool                                                                    chan *SeriesSampleReservation
	queue                                                                   chan *SeriesSampleReservation
	now                                                                     func() time.Time
	process                                                                 string
	selectedCount, recorded, budgetDropped, queueDropped, oversize, expired atomic.Uint64
}

// SeriesSampleBufferBytes includes the fixed payload, owned schema storage and
// both queue pointer slots. Allocator/runtime overhead is covered conservatively.
func SeriesSampleBufferBytes() int {
	return int(unsafe.Sizeof(SeriesSampleReservation{})) + SeriesSampleEncodingScratchBytes + 256
}

func NewSeriesSampler(limits SeriesSampleLimits) (*SeriesSampler, error) {
	if limits.RecordsPerMinute <= 0 || limits.BytesPerMinute < SeriesSampleMaxBytes || limits.QueueCapacity <= 0 {
		return nil, errors.New("series sample: positive resource-derived record, byte and queue budgets are required")
	}
	if limits.QueueCapacity > limits.RecordsPerMinute {
		return nil, errors.New("series sample: queue allocation exceeds record budget")
	}
	s := &SeriesSampler{limits: limits, pool: make(chan *SeriesSampleReservation, limits.QueueCapacity), queue: make(chan *SeriesSampleReservation, limits.QueueCapacity), now: time.Now, process: newProcessIdentity()}
	for i := 0; i < limits.QueueCapacity; i++ {
		s.pool <- &SeriesSampleReservation{sampler: s}
	}
	return s, nil
}

// Select replaces the control snapshot. A failed control read need not call
// Select: each entry expires locally even while that read remains unavailable.
// Repeated snapshots preserve the pinned digest and Slot guard of the same window.
func (s *SeriesSampler) Select(selections []SeriesSampleSelection) error {
	if s == nil {
		return nil
	}
	if len(selections) > TargetFlowMaxGroups {
		return errors.New("series sample: too many windows")
	}
	next := make(sampleSelections, len(selections))
	for _, selection := range selections {
		if err := selection.Validate(); err != nil {
			return err
		}
		if _, exists := next[selection.QueryGroup]; exists {
			return errors.New("series sample: duplicate query group")
		}
		next[selection.QueryGroup] = &sampleSelection{SeriesSampleSelection: selection}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if previous := s.selected.Load(); previous != nil {
		for q, v := range next {
			if old := (*previous)[q]; old != nil && old.SeriesSampleSelection == v.SeriesSampleSelection {
				next[q] = old
			}
		}
	}
	s.selected.Store(&next)
	return nil
}

// TryReserve precedes every sample-only copy and encode. Exhausted, sibling,
// closed and expired calls allocate nothing and never touch a writer.
func (s *SeriesSampler) TryReserve(ctx context.Context, c SeriesSampleCandidate) *SeriesSampleReservation {
	if s == nil {
		return nil
	}
	selections := s.selected.Load()
	if selections == nil {
		return nil
	}
	v := (*selections)[c.QueryGroup]
	if v == nil || v.TenantID != c.TenantID || v.BusinessID != c.BusinessID || v.StrategyID != c.StrategyID || v.StateGeneration != c.StateGeneration || v.PlanScheduleRevision != c.PlanScheduleRevision || v.SeriesKind != c.SeriesKind || (v.SeriesDigest != "" && v.SeriesDigest != c.SeriesDigest) {
		return nil
	}
	now := s.now()
	if !now.Before(v.ExpiresAt) {
		s.expired.Add(1)
		return nil
	}
	if c.SeriesDigest == "" || len(c.SeriesDigest) > 128 {
		s.oversize.Add(1)
		return nil
	}
	s.mu.Lock()
	if selections != s.selected.Load() || (v.pinnedDigest != "" && v.pinnedDigest != c.SeriesDigest) || (v.hasSlot && c.Slot <= v.lastSlot) {
		s.mu.Unlock()
		return nil
	}
	if s.start.IsZero() || now.Sub(s.start) >= time.Minute || now.Before(s.start) {
		s.start = now
		s.records = 0
		s.bytes = 0
	}
	if s.records >= s.limits.RecordsPerMinute || s.bytes > s.limits.BytesPerMinute-SeriesSampleMaxBytes {
		s.budgetDropped.Add(1)
		s.mu.Unlock()
		return nil
	}
	var r *SeriesSampleReservation
	select {
	case r = <-s.pool:
	default:
		s.queueDropped.Add(1)
		s.mu.Unlock()
		return nil
	}
	s.records++
	s.bytes += SeriesSampleMaxBytes
	v.pinnedDigest = c.SeriesDigest
	v.lastSlot = c.Slot
	v.hasSlot = true
	r.period = s.start
	r.levelID = v.LevelID
	s.mu.Unlock()
	r.Sample = SeriesSample{Kind: "series_sample", WindowID: v.WindowID, OpenedAt: v.OpenedAt.UnixMilli(), ProcessID: s.process, QueryGroup: c.QueryGroup, TenantID: c.TenantID, BusinessID: c.BusinessID, StrategyID: c.StrategyID, StateGeneration: c.StateGeneration, PlanScheduleRevision: c.PlanScheduleRevision, Slot: c.Slot, SeriesDigest: c.SeriesDigest, SeriesKind: c.SeriesKind, Provisional: true, Coverage: "explicit_series_first_record"}
	if v.SeriesDigest == "" {
		r.Sample.Coverage = "first_seen_series_first_record"
	}
	if ctx != nil {
		if flow, ok := ctx.Value(targetFlowContextKey{}).(*targetFlowContext); ok && flow.queryGroup == c.QueryGroup {
			r.Sample.ProcessID = flow.flow.process
			r.Sample.RunID = flow.runID
		}
	}
	r.Sample.Levels = r.levels[:0]
	s.selectedCount.Add(1)
	return r
}

type SeriesSampleReservation struct {
	Sample     SeriesSample
	levels     [SeriesSampleMaxLevels]SeriesSampleLevel
	sampler    *SeriesSampler
	period     time.Time
	levelID    uint32
	wire       [SeriesSampleMaxBytes]byte
	size       int
	queryGroup string
}

func (r *SeriesSampleReservation) AddLevel(id uint32) *SeriesSampleLevel {
	if r == nil || (r.levelID != 0 && r.levelID != id) || len(r.Sample.Levels) == SeriesSampleMaxLevels {
		return nil
	}
	n := len(r.Sample.Levels)
	r.Sample.Levels = r.levels[:n+1]
	r.levels[n] = SeriesSampleLevel{LevelID: id, ScalarStatus: "not_available", InputCompletion: "UNKNOWN", DecisionStatus: "not_evaluated"}
	return &r.levels[n]
}
func (r *SeriesSampleReservation) Level(id uint32) *SeriesSampleLevel {
	if r != nil {
		for i := range r.Sample.Levels {
			if r.Sample.Levels[i].LevelID == id {
				return &r.Sample.Levels[i]
			}
		}
	}
	return nil
}

// Write is a bounded encoder destination. Encoder input is a fixed whitelist
// with bounded scalar strings; oversize is dropped, never truncated into JSON.
func (r *SeriesSampleReservation) Write(p []byte) (int, error) {
	if len(p) > len(r.wire)-r.size {
		return 0, errors.New("series sample: encoded record exceeds schema bound")
	}
	n := copy(r.wire[r.size:], p)
	r.size += n
	return n, nil
}

func (r *SeriesSampleReservation) Commit() {
	if r == nil {
		return
	}
	if !r.bounded() {
		r.sampler.oversize.Add(1)
		r.Cancel()
		return
	}
	if err := json.NewEncoder(r).Encode(&r.Sample); err != nil {
		r.sampler.oversize.Add(1)
		// Encoding already spent CPU. Keep the attempt's record token while
		// refunding bytes that will never be written; otherwise repeated
		// oversize records could bypass the CPU-derived attempt allowance.
		r.sampler.mu.Lock()
		if r.period == r.sampler.start {
			r.sampler.bytes -= SeriesSampleMaxBytes
		}
		r.sampler.mu.Unlock()
		r.Release()
		return
	}
	r.queryGroup = r.Sample.QueryGroup
	r.Sample = SeriesSample{}
	r.levels = [SeriesSampleMaxLevels]SeriesSampleLevel{}
	// Every queued/in-flight record owns one pool slot, so the reserved queue
	// position cannot be stolen by any producer after TryReserve succeeds.
	r.sampler.queue <- r
	r.sampler.recorded.Add(1)
}

func (r *SeriesSampleReservation) bounded() bool {
	s := &r.Sample
	if len(s.Levels) == 0 || len(s.Levels) > SeriesSampleMaxLevels {
		return false
	}
	for _, v := range []string{s.Kind, s.Coverage, s.WindowID, s.ProcessID, s.ExecutionID, s.QueryGroup, s.TenantID, s.BusinessID, s.StrategyID, s.SnapshotRevision, s.QueryRevision, s.ScheduleRevision, s.PlanScheduleRevision, s.StateGeneration, s.StateStatus, s.SeriesDigest, s.SeriesKind, s.RecordID, s.RecoveryCause, s.OpenAlertGate, s.EventID} {
		if len(v) > 128 {
			return false
		}
	}
	for _, l := range s.Levels {
		for _, v := range []string{l.DetectResult, l.DetectReason, l.PredicateFingerprint, l.NormalizedScalar, l.ScalarStatus, l.InputCompletion, l.HistoryCompleteness, l.EffectiveStatus, l.DecisionStatus, l.Outcome, l.Reason, l.StateDisposition} {
			if len(v) > 128 {
				return false
			}
		}
	}
	return len(s.Levels) > 0 && len(s.Levels) <= SeriesSampleMaxLevels
}

func (r *SeriesSampleReservation) Cancel() {
	if r == nil {
		return
	}
	s := r.sampler
	s.mu.Lock()
	if r.period == s.start {
		s.records--
		s.bytes -= SeriesSampleMaxBytes
	}
	s.mu.Unlock()
	r.Release()
}
func (r *SeriesSampleReservation) QueryGroup() string { return r.queryGroup }
func (r *SeriesSampleReservation) Bytes() []byte      { return r.wire[:r.size] }

// Release is called once by the shared diagnostic writer after its attempt.
func (r *SeriesSampleReservation) Release() {
	s := r.sampler
	r.Sample = SeriesSample{}
	r.levels = [SeriesSampleMaxLevels]SeriesSampleLevel{}
	r.size = 0
	r.queryGroup = ""
	s.pool <- r
}
func (s *SeriesSampler) Records() <-chan *SeriesSampleReservation {
	if s == nil {
		return nil
	}
	return s.queue
}
func (s *SeriesSampler) Limits() SeriesSampleLimits {
	if s == nil {
		return SeriesSampleLimits{}
	}
	return s.limits
}
func (s *SeriesSampler) Health() SeriesSampleHealth {
	if s == nil {
		return SeriesSampleHealth{Scope: "process_cumulative"}
	}
	return SeriesSampleHealth{Scope: "process_cumulative", Selected: s.selectedCount.Load(), Recorded: s.recorded.Load(), BudgetDropped: s.budgetDropped.Load(), QueueDropped: s.queueDropped.Load(), Oversize: s.oversize.Load(), Expired: s.expired.Load()}
}
