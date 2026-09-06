package main

import (
	"context"
	"errors"
	"io"
	"os"
	"slices"
	"sync"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/access"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/shadow"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

type phaseTwoShadowProfileKey struct{}
type phaseTwoShadowExecutionKey struct{}

type phaseTwoFinalPublisher interface {
	TryEnqueueEncodedFinalEvidence(contract.EncodedFinalResultV1) bool
	Shutdown(context.Context) enginekafka.ReceiptDrainResult
}

type phaseTwoFinalEmitter struct {
	manifest     contract.ValidationEpochManifestV1
	publisher    phaseTwoFinalPublisher
	observer     observability.Observer
	maxEvents    uint64
	zeroPrefixMu sync.Mutex
	zeroPrefixes map[execution.QueryGroupIdentity]phaseTwoZeroQueryPrefix
}

// Only fixed-size facts and references to already retained immutable strings
// survive Evaluate. No dataset, State or event payload is copied into this map.
type phaseTwoEventFacts struct {
	semanticDigest string
	plan           execution.PlanIdentity
	effective      strategy.EffectiveTimeFact
	completeness   contract.ShadowCompletenessV1
}

type phaseTwoShadowExecution struct {
	comparisons map[execution.PlanIdentity]*shadow.PreparedFrozenEvidence
	frozen      access.FrozenPlan
	contract    execution.FrozenExecutionContractRef
	events      map[string]phaseTwoEventFacts
}

func loadPhaseTwoShadowManifest(ctx context.Context, cfg config.Config) (*contract.ValidationEpochManifestV1, error) {
	if cfg.PhaseTwo.ShadowManifestPath == "" {
		return nil, nil
	}
	f, err := os.Open(cfg.PhaseTwo.ShadowManifestPath)
	if err != nil {
		return nil, errors.New("phase-two shadow: cannot read Epoch manifest")
	}
	defer f.Close()
	bound := cfg.Kafka.TriggerEvent.MaxMessageBytes
	b, err := io.ReadAll(io.LimitReader(f, int64(bound)+1))
	if err != nil {
		return nil, errors.New("phase-two shadow: cannot read Epoch manifest")
	}
	m, err := contract.DecodeValidationEpochManifestV1(b, bound)
	if err != nil {
		return nil, err
	}
	profile, ok := ctx.Value(phaseTwoShadowProfileKey{}).(observability.RuntimeConfigFacts)
	if !ok || !slices.Contains(m.RuntimeConfigDigests, profile.Digest) || m.Go.Commit != commit || m.Go.Schema != "go-final-v1" || (m.ComparisonVersion != "comparison-v1" && m.ComparisonVersion != "python-business-kafka-v1") {
		return nil, errors.New("phase-two shadow: manifest differs from actual runtime or supported versions")
	}
	if m.Limits.MaxQueueEntries > uint64(cfg.ReceiptQueue.MaxQueuedMessages) || m.Limits.MaxQueueBytes > uint64(cfg.ReceiptQueue.MaxQueuedBytes) || m.Limits.MaxMessageBytes > uint64(bound) || m.Limits.MaxMessageBytes > m.Limits.MaxQueueBytes {
		return nil, errors.New("phase-two shadow: manifest exceeds configured publisher bounds")
	}
	// Broker credentials remain in the existing Kafka config, never the manifest.
	if m.GoTopic.Name == cfg.Kafka.TriggerEvent.Topic {
		return nil, errors.New("phase-two shadow: final evidence must not replace native Event output")
	}
	return m, nil
}

func (e *phaseTwoFinalEmitter) observe(ctx context.Context, stage observability.Stage, result observability.Result) {
	e.observeCount(ctx, stage, result, 1)
}

func (e *phaseTwoFinalEmitter) observeCount(ctx context.Context, stage observability.Stage, result observability.Result, count uint64) {
	defer func() { _ = recover() }()
	if e.observer != nil {
		e.observer.Observe(ctx, observability.Observation{Component: observability.ComponentCoverage, Stage: stage, Result: result, Counts: observability.Counts{Events: int64(count)}})
	}
}

type phaseTwoShadowExecutor struct {
	emitter *phaseTwoFinalEmitter
	next    scheduler.Executor
}

func (w phaseTwoShadowExecutor) Execute(ctx context.Context, r execution.SlotExecutionRequest) (execution.SlotExecutionResult, error) {
	// A persisted expired range is one query-free operation, not a completed
	// ordinary Slot. Its logical count is reported by the range observer.
	if r.ExpiredRange != nil {
		return w.next.Execute(ctx, r)
	}
	s := &phaseTwoShadowExecution{contract: r.Contract, events: make(map[string]phaseTwoEventFacts)}
	ctx = context.WithValue(ctx, phaseTwoShadowExecutionKey{}, s)
	if w.emitter == nil || w.emitter.manifest.ComparisonVersion != "python-business-kafka-v1" {
		return w.next.Execute(ctx, r)
	}
	facts := newPhaseTwoReceiptFacts(w.emitter, s, r)
	ctx = execution.WithSlotCoverageCapture(ctx, facts.capture())
	result, err := w.next.Execute(ctx, r)
	facts.emit(ctx, result, err)
	return result, err
}

type phaseTwoShadowResolver struct{ next access.FrozenPlanSource }

func (w phaseTwoShadowResolver) ResolveFrozenPlan(ctx context.Context, ref execution.FrozenExecutionContractRef) (access.FrozenPlan, error) {
	f, err := w.next.ResolveFrozenPlan(ctx, ref)
	if err == nil {
		if s, ok := ctx.Value(phaseTwoShadowExecutionKey{}).(*phaseTwoShadowExecution); ok && s.contract == ref {
			s.frozen = f
		}
	}
	return f, err
}

type phaseTwoShadowEvaluator struct {
	next    execution.Evaluator
	emitter *phaseTwoFinalEmitter
}

func (w phaseTwoShadowEvaluator) Evaluate(ctx context.Context, r execution.EvaluationRequest) (execution.EvaluationResult, error) {
	out, err := w.next.Evaluate(ctx, r)
	if err == nil {
		execution.CaptureSlotCoverage(ctx, func(c *execution.SlotCoverageCapture) {
			if c.Evaluated != nil {
				c.Evaluated(r, out)
			}
		})
		w.capture(ctx, r, out)
	}
	return out, err
}

func (w phaseTwoShadowEvaluator) capture(ctx context.Context, r execution.EvaluationRequest, out execution.EvaluationResult) {
	defer func() {
		if recover() != nil {
			w.emitter.observe(ctx, observability.StageFinalEvidenceDropped, observability.ResultFailed)
		}
	}()
	s, ok := ctx.Value(phaseTwoShadowExecutionKey{}).(*phaseTwoShadowExecution)
	if !ok || s.contract != r.Header.Contract || out.Contract != r.Header.Contract {
		return
	}
	for _, plan := range out.Plans {
		for _, state := range plan.StateResults {
			for _, event := range state.Events {
				if event.TenantID != w.emitter.manifest.Target.TenantID || event.BusinessID != w.emitter.manifest.Target.BusinessID || event.EvaluationTime < w.emitter.manifest.EligibleFrom || event.EvaluationTime >= w.emitter.manifest.ExpectedEnd {
					continue
				}
				if uint64(len(s.events)) >= w.emitter.maxEvents {
					w.emitter.observe(ctx, observability.StageFinalEvidenceDropped, observability.ResultFailed)
					continue
				}
				// Only this actual Plan/Level/series input may establish completeness.
				consumer := execution.ConsumerRef{Plan: plan.Plan, LevelID: event.PrimaryLevelID, HasLevel: true}
				var effective strategy.EffectiveTimeFact
				for _, f := range r.Header.EffectiveTimeFacts {
					if f.Consumer == consumer && string(f.SeriesIdentity) == event.RecordRef.DimensionIdentityDigest {
						effective = f.Fact
					}
				}
				inputStatus := ""
				for _, input := range r.Inputs {
					if input.Consumer != consumer || string(input.SeriesIdentity) != event.RecordRef.DimensionIdentityDigest {
						continue
					}
					if len(input.Inputs) == 0 {
						continue
					}
					inputStatus = "FULL"
					for _, b := range input.Inputs {
						if b.Completeness == execution.CompletenessFull && b.Disposition == execution.AccessAvailable {
							continue
						}
						if b.Completeness == execution.CompletenessPartial && b.Disposition == execution.AccessDegraded && b.PartialEvidence != nil {
							inputStatus = "PARTIAL_ACCEPTED"
							continue
						}
						inputStatus = ""
						break
					}
				}
				if inputStatus == "" {
					w.emitter.observe(ctx, observability.StageFinalEvidenceDropped, observability.ResultFailed)
					continue
				}
				history := ""
				for _, level := range event.LevelResults {
					if level.LevelID == event.PrimaryLevelID {
						history = level.DecisionWindow.HistoryCompleteness
					}
				}
				if history == "" {
					continue
				}
				facts := phaseTwoEventFacts{semanticDigest: event.EventSemanticDigest, plan: plan.Plan, effective: effective, completeness: contract.ShadowCompletenessV1{Input: inputStatus, Readiness: "READY", History: history}}
				if old, exists := s.events[event.EventID]; exists && old != facts {
					s.events[event.EventID] = phaseTwoEventFacts{} // Keep a bounded collision tombstone for this execution.
					w.emitter.observe(ctx, observability.StageFinalEvidenceDropped, observability.ResultFailed)
					continue
				}
				s.events[event.EventID] = facts
			}
		}
	}
}

func (e *phaseTwoFinalEmitter) shutdown(ctx context.Context) {
	defer func() {
		if recover() != nil {
			e.observe(ctx, observability.StageFinalEvidenceDropped, observability.ResultFailed)
		}
	}()
	if e.publisher != nil {
		e.publisher.Shutdown(ctx)
	}
}

type phaseTwoShadowEventSink struct {
	productionPhaseTwoEventSink
	emitter *phaseTwoFinalEmitter
}

func (w phaseTwoShadowEventSink) WriteBatch(ctx context.Context, events []contract.TriggerEventV1) error {
	if err := w.productionPhaseTwoEventSink.WriteBatch(ctx, events); err != nil {
		return err
	}
	// The sink interface supplies no partial-ACK list. A non-nil batch error
	// therefore never produces a fabricated successful evidence subset.
	w.emitter.emitACKed(ctx, events)
	return nil
}

func (e *phaseTwoFinalEmitter) emitACKed(ctx context.Context, events []contract.TriggerEventV1) {
	defer func() {
		if recover() != nil {
			e.observe(ctx, observability.StageFinalEvidenceDropped, observability.ResultFailed)
		}
	}()
	s, ok := ctx.Value(phaseTwoShadowExecutionKey{}).(*phaseTwoShadowExecution)
	if !ok {
		return
	}
	for _, event := range events {
		if event.TenantID != e.manifest.Target.TenantID || event.BusinessID != e.manifest.Target.BusinessID || event.EvaluationTime < e.manifest.EligibleFrom || event.EvaluationTime >= e.manifest.ExpectedEnd {
			continue
		}
		facts, found := s.events[event.EventID]
		if !found || facts.semanticDigest != event.EventSemanticDigest {
			e.observe(ctx, observability.StageFinalEvidenceDropped, observability.ResultFailed)
			continue
		}
		delete(s.events, event.EventID)
		var due execution.DuePlan
		for _, d := range s.frozen.DuePlans {
			if d.Identity == facts.plan {
				due = d
			}
		}
		prepared, err := s.comparison(due)
		if err != nil {
			e.observe(ctx, observability.StageFinalEvidenceDropped, observability.ResultFailed)
			continue
		}
		digest := prepared.Digest()
		version, err := execution.BuildApplyVersion(s.contract, due.StateApplyEpoch)
		if err != nil {
			e.observe(ctx, observability.StageFinalEvidenceDropped, observability.ResultFailed)
			continue
		}
		c := contract.ShadowContextV1{ComparisonConfigDigest: digest, PlanScheduleRevision: string(due.ScheduleRevision), EvaluationTime: event.EvaluationTime, SlotIdentity: string(version.SlotDigest), SnapshotRevision: string(s.contract.SnapshotRevision), QueryRevision: string(s.contract.QueryRevision), QueryGroupScheduleRevision: string(s.contract.ScheduleRevision), ScheduleSegmentStart: int64(s.contract.ScheduleSegmentStart), DuePlanSetDigest: string(s.contract.DuePlanSetDigest), EffectiveTimeRequirementDigest: facts.effective.RequirementDigest(), EffectiveTimeFactDigest: facts.effective.FactDigest()}
		input := shadow.GoFrozenEvidenceInputV2{EpochID: e.manifest.EpochID, IdentityVersion: e.manifest.IdentityVersion, ProjectionVersion: "primary-v1", Event: event, ACK: shadow.BusinessACK{Confirmed: true, EventID: event.EventID, SemanticDigest: event.EventSemanticDigest}, Context: c, Completeness: facts.completeness, Due: due, Requirements: s.frozen.Requirements, Queries: s.frozen.QueryFacts, Frozen: s.contract, PrimaryEffectiveTime: facts.effective}
		if e.manifest.ComparisonVersion == "python-business-kafka-v1" && event.EventKind == contract.TriggerEventAbnormal {
			e.emitBusiness(ctx, input, prepared)
			continue
		}
		evidence, err := prepared.EncodeFinal(input, int(e.manifest.Limits.MaxMessageBytes))
		if err != nil || e.publisher == nil {
			e.observe(ctx, observability.StageFinalEvidenceDropped, observability.ResultFailed)
			continue
		}
		if e.publisher.TryEnqueueEncodedFinalEvidence(evidence) {
			e.observe(ctx, observability.StageFinalEvidenceQueued, observability.ResultSuccess)
		}
	}
}

// The new business profile uses the existing actual ACK capture and queue.
// Recovery continues on its existing single-chain final evidence schema.
func (e *phaseTwoFinalEmitter) emitBusiness(ctx context.Context, input shadow.GoFrozenEvidenceInputV2, prepared *shadow.PreparedFrozenEvidence) {
	r, err := prepared.Business(input, int(e.manifest.Limits.MaxMessageBytes))
	if err != nil {
		e.observe(ctx, observability.StageFinalEvidenceDropped, observability.ResultFailed)
		return
	}
	wire, err := contract.EncodeGoBusinessAbnormalV1(contract.GoBusinessAbnormalV1{Schema: "go-business-abnormal-v1", RecordType: "BUSINESS_ABNORMAL", EpochID: input.EpochID, Context: input.Context, Reference: *r}, int(e.manifest.Limits.MaxMessageBytes))
	publisher, ok := e.publisher.(interface {
		TryEnqueueBusinessAbnormal(contract.EncodedBusinessAbnormalV1) bool
	})
	if err != nil || !ok {
		e.observe(ctx, observability.StageFinalEvidenceDropped, observability.ResultFailed)
		return
	}
	if publisher.TryEnqueueBusinessAbnormal(wire) {
		e.observe(ctx, observability.StageFinalEvidenceQueued, observability.ResultSuccess)
	}
}

func (s *phaseTwoShadowExecution) comparison(due execution.DuePlan) (*shadow.PreparedFrozenEvidence, error) {
	if p := s.comparisons[due.Identity]; p != nil {
		return p, nil
	}
	p, err := shadow.PrepareFrozenEvidence(due, s.frozen.Requirements, s.frozen.QueryFacts, s.contract)
	if err != nil {
		return nil, err
	}
	if s.comparisons == nil {
		s.comparisons = make(map[execution.PlanIdentity]*shadow.PreparedFrozenEvidence)
	}
	s.comparisons[due.Identity] = p
	return p, nil
}
