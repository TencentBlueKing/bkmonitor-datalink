package shadow

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// GoFrozenEvidenceInputV2 carries facts already captured by the execution and
// successful business sink. The builder performs no lookup or ACK operation.
type GoFrozenEvidenceInputV2 struct {
	EpochID              string
	IdentityVersion      string
	ProjectionVersion    string
	Event                contract.TriggerEventV1
	ACK                  BusinessACK
	Context              contract.ShadowContextV1
	Completeness         contract.ShadowCompletenessV1
	Due                  execution.DuePlan
	Requirements         []execution.DataRequirement
	Queries              map[execution.LogicalQueryRef]execution.QueryPlanFacts
	Frozen               execution.FrozenExecutionContractRef
	PrimaryEffectiveTime strategy.EffectiveTimeFact
}

// BuildGoFinalEvidenceV2 connects the real frozen scalar Threshold/ALWAYS
// adapter to an ACKed native event. Unsupported shapes remain explicit errors.
func BuildGoFinalEvidenceV2(input GoFrozenEvidenceInputV2, maxBytes int) (*contract.FinalResultEvidenceV1, error) {
	encoded, err := EncodeGoFinalEvidenceV2(input, maxBytes)
	if err != nil {
		return nil, err
	}
	cloned, err := contract.DecodeShadowResultRecordV1(encoded.CopyBytes(), maxBytes)
	if err != nil {
		return nil, err
	}
	return cloned.Evidence, nil
}

// EncodeGoFinalEvidenceV2 keeps runtime publishing on the official codec path
// without decoding an envelope merely to encode it again for the queue.
func EncodeGoFinalEvidenceV2(input GoFrozenEvidenceInputV2, maxBytes int) (contract.EncodedFinalResultV1, error) {
	e, err := buildGoFinalEvidenceV2(input)
	if err != nil {
		return contract.EncodedFinalResultV1{}, err
	}
	return contract.EncodeImmutableFinalResultV1(e, maxBytes)
}

func buildGoFinalEvidenceV2(input GoFrozenEvidenceInputV2) (*contract.FinalResultEvidenceV1, error) {
	if !input.ACK.Confirmed || input.ACK.EventID != input.Event.EventID || input.ACK.SemanticDigest != input.Event.EventSemanticDigest {
		return nil, errors.New("alarmd shadow: ACK does not identify this event")
	}
	projected, err := ProjectTriggerEventV1(ChainGo, input.Event)
	if err != nil {
		return nil, err
	}
	cfg, err := BuildFrozenComparisonConfigV2(input.Due, input.Requirements, input.Queries)
	if err != nil {
		return nil, err
	}
	_, configDigest, err := contract.CanonicalComparisonConfigV2(cfg)
	if err != nil {
		return nil, err
	}
	plan, event, ctx, frozen := input.Due.CompiledPlan, input.Event, input.Context, input.Frozen
	version, err := execution.BuildApplyVersion(frozen, input.Due.StateApplyEpoch)
	if err != nil {
		return nil, err
	}
	if event.TenantID != input.Due.Identity.TenantID || event.BusinessID != input.Due.Identity.BusinessID || event.PlanRef != plan.PlanRef() || event.DetectPlanFingerprint != plan.Fingerprints().Detect || event.TriggerStateFingerprint != plan.Fingerprints().Trigger {
		return nil, errors.New("alarmd shadow: native event and frozen Plan disagree")
	}
	if ctx.ComparisonConfigDigest != configDigest || ctx.PlanScheduleRevision != string(input.Due.ScheduleRevision) || ctx.EvaluationTime != event.EvaluationTime || ctx.EvaluationTime != int64(frozen.Slot.EvaluationTime) || ctx.SlotIdentity != string(version.SlotDigest) || ctx.SnapshotRevision != string(frozen.SnapshotRevision) || ctx.QueryRevision != string(frozen.QueryRevision) || ctx.QueryGroupScheduleRevision != string(frozen.ScheduleRevision) || ctx.ScheduleSegmentStart != int64(frozen.ScheduleSegmentStart) || ctx.DuePlanSetDigest != string(frozen.DuePlanSetDigest) {
		return nil, errors.New("alarmd shadow: frozen execution context mismatch")
	}
	if len(cfg.Levels) != len(event.LevelResults) {
		return nil, errors.New("alarmd shadow: incomplete Level config closure")
	}
	var primary contract.LevelResultV1
	var configLevel contract.ShadowLevelConfigV2
	for _, native := range event.LevelResults {
		found := false
		for _, compiled := range plan.Levels() {
			if compiled.Definition().LevelID == native.LevelID && compiled.Fingerprints().Trigger == native.LevelTriggerFingerprint {
				found = true
			}
		}
		if !found {
			return nil, errors.New("alarmd shadow: native Level fingerprint mismatch")
		}
		for _, level := range cfg.Levels {
			if level.LevelID != native.LevelID {
				continue
			}
			if native.Priority != level.Priority || native.DecisionWindow.Trigger.WindowSize != level.Trigger.WindowPoints || native.DecisionWindow.Trigger.RequiredAnomalies != level.Trigger.RequiredAnomalies || native.DecisionWindow.Recovery.Enabled != level.Recovery.Enabled || native.DecisionWindow.Recovery.RequiredConsecutiveWindows != level.Recovery.ConsecutiveWindows {
				return nil, errors.New("alarmd shadow: native window and config disagree")
			}
			if native.LevelID == projected.Primary.LevelID {
				primary, configLevel = native, level
			}
		}
	}
	fact := input.PrimaryEffectiveTime
	for _, level := range plan.Levels() {
		if level.Definition().LevelID == primary.LevelID && (fact.RequirementDigest() != level.EffectiveTimeRequirementDigest() || ctx.EffectiveTimeRequirementDigest != fact.RequirementDigest()) {
			return nil, errors.New("alarmd shadow: effective time requirement mismatch")
		}
	}
	if fact.FactRevision() == "" || fact.Status() != strategy.EffectiveTimeActive || ctx.EffectiveTimeFactDigest != fact.FactDigest() || event.EvaluationTime < fact.ValidFrom() || event.EvaluationTime >= fact.ValidUntil() || primary.DetectEvidence.EffectiveTimeStatus != fact.Status() {
		return nil, errors.New("alarmd shadow: actual effective time fact mismatch")
	}
	raw := strings.TrimSpace(string(primary.DetectEvidence.NormalizedValue))
	if strings.HasPrefix(raw, "\"") {
		if err := json.Unmarshal(primary.DetectEvidence.NormalizedValue, &raw); err != nil {
			return nil, err
		}
	}
	value, err := contract.NormalizeShadowDecimalV1(raw)
	if err != nil {
		return nil, err
	}
	detectDigest, err := contract.DeriveCanonicalDigestV2("shadow-comparable-detect-v2", configLevel.Detectors)
	if err != nil {
		return nil, err
	}
	triggerDigest, err := contract.DeriveCanonicalDigestV2("shadow-comparable-trigger-v2", struct {
		Trigger  contract.ShadowTriggerConfigV2  `json:"trigger"`
		Recovery contract.ShadowRecoveryConfigV2 `json:"recovery"`
	}{configLevel.Trigger, configLevel.Recovery})
	if err != nil {
		return nil, err
	}
	e := &contract.FinalResultEvidenceV1{
		Schema: contract.Schema{Name: contract.FinalResultEvidenceSchemaV1, Major: 1}, RequiredFeatures: []string{}, RecordType: contract.ShadowFinalResult,
		EpochID: input.EpochID, Chain: contract.ShadowGo, ResultKind: event.EventKind,
		Subject: contract.ShadowSubjectV1{IdentityVersion: input.IdentityVersion, TenantID: event.TenantID, BusinessID: event.BusinessID, StrategyID: event.PlanRef.StrategyID, DimensionIdentityDigest: event.RecordRef.DimensionIdentityDigest, SourceTime: event.RecordRef.SourceTime},
		Native:  contract.ShadowNativeRefV1{EventID: event.EventID, SemanticDigest: event.EventSemanticDigest, LevelResults: event.LevelResults},
		Primary: contract.PrimaryComparableProjectionV1{Version: input.ProjectionVersion, SelectionMappingVersion: cfg.SelectionMappingVersion, LevelID: primary.LevelID, Priority: primary.Priority, Result: primary.Result, Values: map[string]string{"value": value}, Unit: cfg.Numeric.TargetUnit, WindowStart: primary.DecisionWindow.Trigger.WindowStart, WindowEnd: primary.DecisionWindow.Trigger.WindowEnd, DetectConfigDigest: detectDigest, TriggerConfigDigest: triggerDigest},
		Context: ctx, Completeness: input.Completeness, Delivery: contract.ShadowDeliveryV1{FinalAdmission: "PRODUCED", BusinessACK: true, ReasonCode: "BUSINESS_ACK_CONFIRMED"}, GoEvent: &event,
	}
	e.Primary.Digest, err = contract.ShadowProjectionDigestV1(e.Primary)
	if err != nil {
		return nil, err
	}
	e.Native.EvidenceDigest, err = contract.ShadowNativeDigestV1(e.Native)
	if err != nil {
		return nil, err
	}
	return e, nil
}
