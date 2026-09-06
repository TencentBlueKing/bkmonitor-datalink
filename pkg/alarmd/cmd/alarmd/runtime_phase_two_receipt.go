package main

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/shadow"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// One execution owns only counters and immutable scalar proofs, never Dataset
// or State copies. QueryCalled may run concurrently; all updates share mu.
type phaseTwoReceiptFacts struct {
	mu                                           sync.Mutex
	execution                                    *phaseTwoShadowExecution
	emitter                                      *phaseTwoFinalEmitter
	request                                      execution.SlotExecutionRequest
	plans                                        map[execution.PlanIdentity]*phaseTwoPlanCoverage
	calls                                        uint64
	lost, beginKnown, priorUnfinished, committed bool
	inputKnown                                   bool
	completion                                   string
	series, records                              uint64
	resultDigest                                 string
	prefixStarted                                time.Time
	prefixKnown                                  bool
	completedAt                                  int64
	terminalRef                                  string
}
type phaseTwoPlanCoverage struct {
	due                       execution.DuePlan
	effective                 strategy.EffectiveTimeFact
	levels                    map[uint32]*contract.ShadowLevelCoverageV1
	outcomes                  contract.ShadowRecordOutcomesV1
	selected, produced, acked uint64
	ackUnknown                bool
}

func coverageZeroLevel(id uint32) *contract.ShadowLevelCoverageV1 {
	z := contract.KnownShadowCountV1(0)
	return &contract.ShadowLevelCoverageV1{LevelID: id, Selected: z, Normal: z, Abnormal: z, Recovery: z, Unavailable: z, Terminal: z, Excluded: z, PythonShortCircuited: z, PartialAcceptedAbnormal: z, SuppressedAbnormal: z, Primary: z, SiblingDiagnostic: z, LateAfterComplete: z}
}
func coverageAdd(c *contract.KnownCountV1, n uint64) {
	if c.Known && c.Value != nil {
		*c = contract.KnownShadowCountV1(*c.Value + n)
	}
}
func newPhaseTwoReceiptFacts(e *phaseTwoFinalEmitter, s *phaseTwoShadowExecution, r execution.SlotExecutionRequest) *phaseTwoReceiptFacts {
	prefix := e.takeZeroQueryPrefix(r, time.Now())
	return &phaseTwoReceiptFacts{emitter: e, execution: s, request: r, prefixKnown: !prefix.IsZero(), prefixStarted: prefix, plans: map[execution.PlanIdentity]*phaseTwoPlanCoverage{}}
}

func (f *phaseTwoReceiptFacts) capture() *execution.SlotCoverageCapture {
	return &execution.SlotCoverageCapture{
		Lost:              func() { f.mu.Lock(); f.lost = true; f.mu.Unlock() },
		BeginCommitted:    func(prior bool) { f.mu.Lock(); f.beginKnown = true; f.priorUnfinished = prior; f.mu.Unlock() },
		PriorStateApplied: func() { f.mu.Lock(); f.lost = true; f.mu.Unlock() },
		QueryCalled:       func(execution.QueryAttempt) { f.mu.Lock(); f.calls++; f.mu.Unlock() },
		Prepared:          f.prepared, InputCompleted: f.input, Evaluated: f.evaluated, OutputWritten: f.output,
		ProgressCommitted: func(r execution.ProgressCommitRequest, out execution.ProgressCommitResult) {
			f.mu.Lock()
			defer f.mu.Unlock()
			if out.Status == execution.ProgressCommitted && !f.committed {
				f.committed = true
				f.completedAt = time.Now().UnixMilli()
				f.terminalRef, _ = contract.DeriveCanonicalDigestV2("go-progress-terminal-proof-v1", r)
			}
		},
	}
}
func (f *phaseTwoReceiptFacts) prepared(h execution.InternalExecutionHeader, effective map[execution.ConsumerRef]strategy.EffectiveTimeFact) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if int64(h.Contract.Slot.EvaluationTime) < f.emitter.manifest.EligibleFrom || int64(h.Contract.Slot.EvaluationTime) >= f.emitter.manifest.ExpectedEnd {
		return
	}
	if h.Contract != f.request.Contract || uint64(len(h.DuePlans)) > f.emitter.manifest.Limits.MaxEntries {
		f.lost = true
		return
	}
	for _, due := range h.DuePlans {
		if due.Identity.TenantID != f.emitter.manifest.Target.TenantID || due.Identity.BusinessID != f.emitter.manifest.Target.BusinessID {
			continue
		}
		z := contract.KnownShadowCountV1(0)
		p := &phaseTwoPlanCoverage{due: due, levels: map[uint32]*contract.ShadowLevelCoverageV1{}, outcomes: contract.ShadowRecordOutcomesV1{PrimaryAbnormal: z, PrimaryRecovery: z, NoEvent: z, Excluded: z, Unavailable: z, Terminal: z}}
		for _, l := range due.CompiledPlan.Levels() {
			id := l.Definition().LevelID
			p.levels[id] = coverageZeroLevel(id)
			fact := effective[execution.ConsumerRef{Plan: due.Identity, LevelID: id, HasLevel: true}]
			if p.effective.FactDigest() == "" {
				p.effective = fact
			} else if p.effective.FactDigest() != fact.FactDigest() {
				f.lost = true
			}
		}
		f.plans[due.Identity] = p
	}
}
func (f *phaseTwoReceiptFacts) input(in execution.QueryExecutionCompletion) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inputKnown = in.AllRequiredCompleted
	f.completion = "FULL"
	// Physical delivery facts have already passed stream.complete validation.
	type proof struct {
		Query    execution.PhysicalQueryDigest
		Ref      execution.ProviderResultRef
		Delivery execution.SeriesDelivery
		Quality  execution.Completeness
		State    execution.DataState
	}
	proofs := make([]proof, 0, len(in.PhysicalQueries))
	for _, q := range in.PhysicalQueries {
		f.series += q.Delivery.Series
		f.records += q.Delivery.Records
		proofs = append(proofs, proof{q.PhysicalQuery, q.Ref, q.Delivery, q.Completeness, q.DataState})
		if q.Completeness == execution.CompletenessUnavailable {
			f.completion = "UNAVAILABLE"
		} else if q.Completeness == execution.CompletenessPartial && f.completion == "FULL" {
			f.completion = "PARTIAL"
		}
	}
	sort.Slice(proofs, func(i, j int) bool { return proofs[i].Query < proofs[j].Query })
	f.resultDigest, _ = contract.DeriveCanonicalDigestV2("go-validated-delivery-proof-v1", proofs)
}
func (f *phaseTwoReceiptFacts) evaluated(_ execution.EvaluationRequest, out execution.EvaluationResult) {
	f.mu.Lock()
	defer f.mu.Unlock()
	type anchor struct {
		Series execution.SeriesIdentityDigest
		Record execution.RecordAnchor
	}
	for _, r := range out.Plans {
		p := f.plans[r.Plan]
		if p == nil {
			continue
		}
		// Evaluator is called per Plan/series, bounded by its existing record limit.
		records := map[anchor]string{}
		for _, l := range r.LevelOutcomes {
			level := p.levels[l.LevelID]
			if level == nil {
				f.lost = true
				continue
			}
			coverageAdd(&level.Selected, 1)
			key := anchor{l.SeriesIdentityDigest, l.Record}
			if _, ok := records[key]; !ok {
				records[key] = "NO_EVENT"
			}
			switch l.Outcome {
			case execution.LevelOutcomeNormal:
				coverageAdd(&level.Normal, 1)
			case execution.LevelOutcomeAbnormal:
				coverageAdd(&level.Abnormal, 1)
				coverageAdd(&level.SuppressedAbnormal, 1)
				if len(l.PartialProofs) > 0 {
					coverageAdd(&level.PartialAcceptedAbnormal, 1)
				}
			case execution.LevelOutcomeRecovery:
				coverageAdd(&level.Recovery, 1)
			case execution.LevelOutcomeUnknown:
				coverageAdd(&level.Unavailable, 1)
				records[key] = "UNAVAILABLE"
			case execution.LevelOutcomeTerminal:
				coverageAdd(&level.Terminal, 1)
				records[key] = "TERMINAL"
			default:
				f.lost = true
			}
		}
		for _, state := range r.StateResults {
			for _, event := range state.Events {
				key := anchor{execution.SeriesIdentityDigest(event.RecordRef.DimensionIdentityDigest), execution.RecordAnchor{RecordID: event.RecordRef.RecordID, SourceTime: event.RecordRef.SourceTime}}
				if _, ok := records[key]; !ok {
					f.lost = true
					continue
				}
				records[key] = event.EventKind
				for _, sibling := range event.LevelResults {
					if sibling.LevelID != event.PrimaryLevelID {
						if diagnostic := p.levels[sibling.LevelID]; diagnostic != nil {
							coverageAdd(&diagnostic.SiblingDiagnostic, 1)
						}
					}
				}
				level := p.levels[event.PrimaryLevelID]
				if level != nil {
					coverageAdd(&level.Primary, 1)
					if event.EventKind == contract.TriggerEventAbnormal && level.SuppressedAbnormal.Value != nil && *level.SuppressedAbnormal.Value > 0 {
						level.SuppressedAbnormal = contract.KnownShadowCountV1(*level.SuppressedAbnormal.Value - 1)
					}
				}
			}
		}
		p.selected += uint64(len(records))
		for _, kind := range records {
			switch kind {
			case contract.TriggerEventAbnormal:
				coverageAdd(&p.outcomes.PrimaryAbnormal, 1)
			case contract.TriggerEventRecovery:
				coverageAdd(&p.outcomes.PrimaryRecovery, 1)
			case "UNAVAILABLE":
				coverageAdd(&p.outcomes.Unavailable, 1)
			case "TERMINAL":
				coverageAdd(&p.outcomes.Terminal, 1)
			default:
				coverageAdd(&p.outcomes.NoEvent, 1)
			}
		}
	}
}
func (f *phaseTwoReceiptFacts) output(events []contract.TriggerEventV1, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, event := range events {
		p := f.plans[execution.PlanIdentity{TenantID: event.TenantID, BusinessID: event.BusinessID, StrategyID: event.PlanRef.StrategyID}]
		if p == nil {
			continue
		}
		p.produced++
		if err == nil {
			p.acked++
		} else {
			p.ackUnknown = true
		}
	}
}
func (f *phaseTwoReceiptFacts) emit(_ context.Context, result execution.SlotExecutionResult, runErr error) {
	defer func() {
		// A faulty optional publisher cannot affect Slot execution. Its own
		// coverage counters, not the FinalEvidence stages, own submission facts.
		_ = recover()
	}()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.emitter.finishZeroQueryPrefix(f, time.Now(), result.Completed)
	publisher, ok := f.emitter.publisher.(interface {
		TryEnqueueCoverageReceipt(contract.EncodedGoCoverageV1) bool
	})
	if !ok {
		return
	}
	k := contract.KnownShadowCountV1
	for _, p := range f.plans {
		cfg, err := shadow.BuildFrozenComparisonConfigV2(p.due, f.execution.frozen.Requirements, f.execution.frozen.QueryFacts)
		if err != nil {
			publisher.TryEnqueueCoverageReceipt(contract.EncodedGoCoverageV1{})
			continue
		}
		_, digest, err := contract.CanonicalComparisonConfigV2(cfg)
		if err != nil {
			publisher.TryEnqueueCoverageReceipt(contract.EncodedGoCoverageV1{})
			continue
		}
		version, err := execution.BuildApplyVersion(f.request.Contract, p.due.StateApplyEpoch)
		if err != nil {
			publisher.TryEnqueueCoverageReceipt(contract.EncodedGoCoverageV1{})
			continue
		}
		c := contract.ShadowContextV1{ComparisonConfigDigest: digest, PlanScheduleRevision: string(p.due.ScheduleRevision), EvaluationTime: int64(f.request.Contract.Slot.EvaluationTime), SlotIdentity: string(version.SlotDigest), SnapshotRevision: string(f.request.Contract.SnapshotRevision), QueryRevision: string(f.request.Contract.QueryRevision), QueryGroupScheduleRevision: string(f.request.Contract.ScheduleRevision), ScheduleSegmentStart: int64(f.request.Contract.ScheduleSegmentStart), DuePlanSetDigest: string(f.request.Contract.DuePlanSetDigest), EffectiveTimeRequirementDigest: p.effective.RequirementDigest(), EffectiveTimeFactDigest: p.effective.FactDigest()}
		executionRef, _ := contract.DeriveCanonicalDigestV2("go-slot-execution-observation-v1", f.request)
		r := contract.ChainCoverageReceiptV1{EpochID: f.emitter.manifest.EpochID, Chain: contract.ShadowGo, TenantID: p.due.Identity.TenantID, BusinessID: p.due.Identity.BusinessID, StrategyID: p.due.Identity.StrategyID, Context: c, Input: contract.ShadowInputCoverageV1{QueryAttempts: contract.ShadowAttemptObservationV1{ExecutionRef: executionRef, CurrentExecution: k(f.calls)}, Completion: f.completion, Series: k(f.series), Records: k(f.records), SelectedPlanRecords: k(p.selected), SourceWindow: contract.SourceWindowV2{FromTime: c.EvaluationTime - int64(cfg.Schedule.WindowSeconds), UntilTime: c.EvaluationTime}, ResultBytesDigest: f.resultDigest}, Records: p.outcomes, PhysicalProduced: k(p.produced), PhysicalACKed: k(p.acked), TerminalFact: f.committed, TerminalFactRef: f.terminalRef, GapReasons: []string{}, ReasonCounts: []contract.ReasonCountV1{}}
		if r.Input.Completion == "" {
			r.Input.Completion = "UNAVAILABLE"
		}
		ids := make([]int, 0, len(p.levels))
		for id := range p.levels {
			ids = append(ids, int(id))
		}
		sort.Ints(ids)
		for _, id := range ids {
			r.Levels = append(r.Levels, *p.levels[uint32(id)])
		}
		complete := !f.lost && f.beginKnown && (!f.priorUnfinished || f.prefixKnown) && f.inputKnown && f.completion == "FULL" && f.committed && result.Completed && runErr == nil && !p.ackUnknown
		if !f.inputKnown {
			r.Input.Series = contract.KnownCountV1{}
			r.Input.Records = contract.KnownCountV1{}
			r.Input.SelectedPlanRecords = contract.KnownCountV1{}
			r.Records = contract.ShadowRecordOutcomesV1{}
			for i, l := range r.Levels {
				r.Levels[i] = contract.ShadowLevelCoverageV1{LevelID: l.LevelID}
			}
			r.PhysicalProduced = contract.KnownCountV1{}
			r.PhysicalACKed = contract.KnownCountV1{}
		}
		if complete {
			r.Input.QueryAttempts.LogicalSlot = k(f.calls)
			r.Input.QueryAttempts.ObservedFromSlotStart = true
			r.Input.QueryAttempts.ContinuousThroughTerminal = true
			r.CoverageComplete = true
		} else {
			r.GapReasons = []string{"EXECUTION_COVERAGE_INCOMPLETE"}
			if p.ackUnknown {
				r.PhysicalACKed = contract.KnownCountV1{}
			}
			if f.lost {
				r.Records = contract.ShadowRecordOutcomesV1{}
				r.PhysicalProduced = contract.KnownCountV1{}
				r.PhysicalACKed = contract.KnownCountV1{}
				r.Input.SelectedPlanRecords = contract.KnownCountV1{}
			}
		}
		receipt, err := shadow.BuildCoverageReceipt(r, int(f.emitter.manifest.Limits.MaxMessageBytes))
		if err != nil {
			publisher.TryEnqueueCoverageReceipt(contract.EncodedGoCoverageV1{})
			continue
		}
		envelope := contract.GoCoverageEnvelopeV1{EpochID: r.EpochID, Receipt: *receipt, Config: cfg}
		if f.committed && f.completedAt > 0 {
			at := f.completedAt
			envelope.CompletedAt = &at
		}
		wire, err := contract.EncodeGoCoverageEnvelopeV1(envelope, int(f.emitter.manifest.Limits.MaxMessageBytes))
		if err != nil {
			publisher.TryEnqueueCoverageReceipt(contract.EncodedGoCoverageV1{})
			continue
		}
		publisher.TryEnqueueCoverageReceipt(wire)
	}
}

// Only a zero-Query prefix is retained. It never drives scheduling/retries or
// stores business results; each QG has at most one bounded provenance entry.
type phaseTwoZeroQueryPrefix struct {
	Contract execution.FrozenExecutionContractRef
	Fence    execution.OwnerFence
	Started  time.Time
}

func (e *phaseTwoFinalEmitter) takeZeroQueryPrefix(r execution.SlotExecutionRequest, now time.Time) time.Time {
	e.zeroPrefixMu.Lock()
	defer e.zeroPrefixMu.Unlock()
	p, ok := e.zeroPrefixes[r.Contract.Slot.QueryGroup]
	delete(e.zeroPrefixes, r.Contract.Slot.QueryGroup)
	if ok && p.Contract == r.Contract && p.Fence == r.OwnerFence && now.Sub(p.Started) >= 0 && now.Sub(p.Started) <= time.Duration(e.manifest.Limits.MaxAgeSeconds)*time.Second {
		return p.Started
	}
	return time.Time{}
}
func (e *phaseTwoFinalEmitter) finishZeroQueryPrefix(f *phaseTwoReceiptFacts, now time.Time, completed bool) {
	e.zeroPrefixMu.Lock()
	defer e.zeroPrefixMu.Unlock()
	key := f.request.Contract.Slot.QueryGroup
	delete(e.zeroPrefixes, key)
	if completed || f.calls != 0 || f.lost || !f.beginKnown || (f.priorUnfinished && !f.prefixKnown) {
		return
	}
	if uint64(len(e.zeroPrefixes)) >= e.manifest.Limits.MaxEntries {
		return
	}
	if e.zeroPrefixes == nil {
		e.zeroPrefixes = map[execution.QueryGroupIdentity]phaseTwoZeroQueryPrefix{}
	}
	if f.prefixKnown {
		now = f.prefixStarted
	}
	e.zeroPrefixes[key] = phaseTwoZeroQueryPrefix{f.request.Contract, f.request.OwnerFence, now}
}
