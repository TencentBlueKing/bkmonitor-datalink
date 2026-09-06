package shadow

import (
	"encoding/json"
	"errors"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"reflect"
)

// PreparedFrozenEvidence is call-local immutable configuration, not execution
// authority. Its unexported payload is produced only from actual frozen inputs.
// Every result still verifies its own ACK, native Event and effective-time fact.
type PreparedFrozenEvidence struct {
	due            execution.DuePlan
	frozen         execution.FrozenExecutionContractRef
	v2             *contract.ComparisonConfigV2
	v3             *contract.ComparisonConfigV3
	digest         string
	businessConfig json.RawMessage
	businessDigest string
	common         finalComparisonFacts
}

func PrepareFrozenEvidence(due execution.DuePlan, requirements []execution.DataRequirement, queries map[execution.LogicalQueryRef]execution.QueryPlanFacts, frozen execution.FrozenExecutionContractRef) (*PreparedFrozenEvidence, error) {
	if err := frozen.Validate(); err != nil {
		return nil, err
	}
	p := &PreparedFrozenEvidence{due: due, frozen: frozen}
	p.due.PartialCapabilities = append([]execution.LevelPartialCapability(nil), due.PartialCapabilities...)
	if due.PartialCapabilities != nil && p.due.PartialCapabilities == nil {
		p.due.PartialCapabilities = []execution.LevelPartialCapability{}
	}
	for i := range p.due.PartialCapabilities {
		if proof := p.due.PartialCapabilities[i].Proof; proof != nil {
			copy := *proof
			p.due.PartialCapabilities[i].Proof = &copy
		}
	}
	c, err := BuildFrozenComparisonConfigV2(due, requirements, queries)
	if err == nil {
		p.v2 = &c
		p.common = finalComparisonFacts{c.Levels, c.Numeric, c.SelectionMappingVersion}
		_, p.digest, err = contract.CanonicalComparisonConfigV2(c)
	} else {
		if !errors.Is(err, ErrFrozenConfigUnsupported) {
			return nil, err
		}
		v, err3 := BuildFrozenComparisonConfigV3(due, requirements, queries)
		if err3 != nil {
			return nil, err3
		}
		p.v3 = &v
		p.common = finalComparisonFacts{v.Levels, v.Numeric, v.SelectionMappingVersion}
		_, p.digest, err = contract.CanonicalComparisonConfigV3(v)
	}
	if err != nil {
		return nil, err
	}
	if p.v3 != nil {
		p.businessConfig, p.businessDigest, err = contract.CanonicalBusinessConfigV2(*p.v3)
	} else {
		p.businessConfig, p.businessDigest, err = contract.CanonicalBusinessConfigV1(*p.v2)
	}
	if err != nil {
		return nil, err
	}
	return p, nil
}
func (p *PreparedFrozenEvidence) Digest() string { return p.digest }
func (p *PreparedFrozenEvidence) WindowSeconds() uint32 {
	if p.v3 != nil {
		return p.v3.Schedule.WindowSeconds
	}
	return p.v2.Schedule.WindowSeconds
}
func (p *PreparedFrozenEvidence) final(input GoFrozenEvidenceInputV2) (*contract.FinalResultEvidenceV1, error) {
	if p == nil || (p.v2 == nil && p.v3 == nil) || !reflect.DeepEqual(p.due, input.Due) || p.due.CompiledPlan != input.Due.CompiledPlan || !reflect.DeepEqual(p.frozen, input.Frozen) {
		return nil, errors.New("shadow prepared frozen binding")
	}
	return buildGoFinalEvidenceWithConfig(input, p.common, p.digest)
}
func (p *PreparedFrozenEvidence) EncodeFinal(input GoFrozenEvidenceInputV2, limit int) (contract.EncodedFinalResultV1, error) {
	e, err := p.final(input)
	if err != nil {
		return contract.EncodedFinalResultV1{}, err
	}
	return contract.EncodeImmutableFinalResultV1(e, limit)
}
func (p *PreparedFrozenEvidence) Business(input GoFrozenEvidenceInputV2, limit int) (*contract.BusinessAbnormalV1, error) {
	if input.Event.EventKind != contract.TriggerEventAbnormal {
		return nil, errors.New("business comparison excludes Recovery")
	}
	e, err := p.final(input)
	if err != nil {
		return nil, err
	}
	if _, err = contract.EncodeImmutableFinalResultV1(e, limit); err != nil {
		return nil, err
	}
	return buildBusinessReference(input, e, append(json.RawMessage(nil), p.businessConfig...), p.businessDigest, limit)
}

func (p *PreparedFrozenEvidence) EncodeCoverage(receipt contract.ChainCoverageReceiptV1, completed *int64, limit int) (contract.EncodedGoCoverageV1, error) {
	if p == nil || (p.v2 == nil && p.v3 == nil) {
		return contract.EncodedGoCoverageV1{}, errors.New("shadow prepared configuration missing")
	}
	version, err := execution.BuildApplyVersion(p.frozen, p.due.StateApplyEpoch)
	if err != nil {
		return contract.EncodedGoCoverageV1{}, err
	}
	ctx := receipt.Context
	if receipt.TenantID != p.due.Identity.TenantID || receipt.BusinessID != p.due.Identity.BusinessID || receipt.StrategyID != p.due.Identity.StrategyID || ctx.ComparisonConfigDigest != p.digest || ctx.PlanScheduleRevision != string(p.due.ScheduleRevision) || ctx.EvaluationTime != int64(p.frozen.Slot.EvaluationTime) || ctx.SlotIdentity != string(version.SlotDigest) || ctx.SnapshotRevision != string(p.frozen.SnapshotRevision) || ctx.QueryRevision != string(p.frozen.QueryRevision) || ctx.QueryGroupScheduleRevision != string(p.frozen.ScheduleRevision) || ctx.ScheduleSegmentStart != int64(p.frozen.ScheduleSegmentStart) || ctx.DuePlanSetDigest != string(p.frozen.DuePlanSetDigest) {
		return contract.EncodedGoCoverageV1{}, errors.New("shadow prepared receipt binding")
	}
	if p.v3 != nil {
		return contract.EncodeGoCoverageEnvelopeV2(contract.GoCoverageEnvelopeV2{EpochID: receipt.EpochID, Receipt: receipt, Config: *p.v3, CompletedAt: completed}, limit)
	}
	return contract.EncodeGoCoverageEnvelopeV1(contract.GoCoverageEnvelopeV1{EpochID: receipt.EpochID, Receipt: receipt, Config: *p.v2, CompletedAt: completed}, limit)
}
