// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package comparator

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// PreviewAudits builds authoritative post-record audit snapshots while the
// prepared record is still hidden behind the input offset commit boundary.
func (r *Run) PreviewAudits(prepared Prepared, gates Gates) ([]*contract.ComparisonAuditBatch, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.requireValidLocked(); err != nil {
		return nil, err
	}
	if !r.matchesInflightLocked(prepared) {
		return nil, r.invalidateLocked(fmt.Errorf("comparator: prepared record mismatch"))
	}
	if r.coverageTimeout <= 0 {
		return nil, r.invalidateLocked(fmt.Errorf("comparator: coverage timeout is not configured"))
	}

	inputIDs := make([]string, 0, len(r.inflight.coverage))
	for inputID := range r.inflight.coverage {
		inputIDs = append(inputIDs, inputID)
	}
	sort.Strings(inputIDs)
	candidates := make([]comparisonAuditCandidate, 0, len(inputIDs))
	if gates.EpochStartSourceTime == nil && r.epochStartTime != nil {
		epochStart := *r.epochStartTime
		gates.EpochStartSourceTime = &epochStart
	}
	for _, inputID := range inputIDs {
		coverageItem := r.inflight.coverage[inputID]
		coverage := r.coverageSnapshotLocked(inputID, coverageItem, r.inflight.observedAt)
		entryGates := gates
		entryGates.CoverageComplete = coverageEntryComparable(coverageItem)
		observation, ok, err := r.joiner.auditObservation(r.epoch, inputID, entryGates)
		if err != nil {
			return nil, r.invalidateLocked(fmt.Errorf("comparator: preview audit: %w", err))
		}
		if !ok {
			continue
		}
		if !terminalAudit(observation.assessment, coverage) {
			continue
		}
		audit, err := buildComparisonAudit(observation, coverage)
		if err != nil {
			return nil, r.invalidateLocked(fmt.Errorf("comparator: preview audit: %w", err))
		}
		candidates = append(candidates, comparisonAuditCandidate{source: observation.source, audit: audit})
	}
	batches, err := buildComparisonAuditBatches(candidates)
	if err != nil {
		return nil, r.invalidateLocked(fmt.Errorf("comparator: build audit batches: %w", err))
	}
	r.inflight.auditsPrepared = true
	return batches, nil
}

// AuditBatches snapshots already committed inputs after a transport-owned
// coverage barrier changes their externally visible state.
func (r *Run) AuditBatches(epoch string, inputIDs []string, gates Gates) ([]*contract.ComparisonAuditBatch, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.requireEpochAndCommittedLocked(epoch); err != nil {
		return nil, err
	}
	now, err := r.nowLocked()
	if err != nil {
		return nil, r.invalidateLocked(err)
	}
	ownedIDs := append([]string(nil), inputIDs...)
	sort.Strings(ownedIDs)
	candidates := make([]comparisonAuditCandidate, 0, len(ownedIDs))
	if gates.EpochStartSourceTime == nil && r.epochStartTime != nil {
		epochStart := *r.epochStartTime
		gates.EpochStartSourceTime = &epochStart
	}
	for _, inputID := range ownedIDs {
		coverageItem := r.coverage[inputID]
		if coverageItem == nil {
			return nil, r.invalidateLocked(fmt.Errorf("comparator: audit coverage input is unknown"))
		}
		coverage := r.coverageSnapshotLocked(inputID, coverageItem, now)
		entryGates := gates
		entryGates.CoverageComplete = coverageEntryComparable(coverageItem)
		observation, ok, err := r.joiner.auditObservation(r.epoch, inputID, entryGates)
		if err != nil {
			return nil, r.invalidateLocked(fmt.Errorf("comparator: audit snapshot: %w", err))
		}
		if !ok {
			return nil, r.invalidateLocked(fmt.Errorf("comparator: audit source is missing"))
		}
		audit, err := buildComparisonAudit(observation, coverage)
		if err != nil {
			return nil, r.invalidateLocked(fmt.Errorf("comparator: audit snapshot: %w", err))
		}
		if !terminalAudit(observation.assessment, coverage) {
			continue
		}
		candidates = append(candidates, comparisonAuditCandidate{source: observation.source, audit: audit})
	}
	batches, err := buildComparisonAuditBatches(candidates)
	if err != nil {
		return nil, r.invalidateLocked(fmt.Errorf("comparator: build audit batches: %w", err))
	}
	return batches, nil
}

type comparisonAuditCandidate struct {
	source *sourceObservation
	audit  contract.ComparisonAudit
}

type comparisonAuditIdentity struct {
	partitionHashVersion string
	tenantID             string
	purpose              string
	strategyID           string
	itemID               string
	generation           string
	contentSHA256        string
}

type comparisonAuditGroup struct {
	source *sourceObservation
	audits []contract.ComparisonAudit
}

func buildComparisonAuditBatches(candidates []comparisonAuditCandidate) ([]*contract.ComparisonAuditBatch, error) {
	groups := make(map[comparisonAuditIdentity]*comparisonAuditGroup)
	order := make([]comparisonAuditIdentity, 0)
	for _, candidate := range candidates {
		if candidate.source == nil || candidate.source.strategy == nil {
			return nil, fmt.Errorf("authoritative DetectInput is required")
		}
		strategy := candidate.source.strategy
		identity := comparisonAuditIdentity{
			partitionHashVersion: candidate.source.partitionHashVersion,
			tenantID:             strategy.TenantID, purpose: strategy.Purpose,
			strategyID: strategy.StrategyRef.StrategyID, itemID: strategy.StrategyRef.ItemID,
			generation: strategy.StrategyRef.Generation, contentSHA256: strategy.StrategyRef.ContentSHA256,
		}
		group := groups[identity]
		if group == nil {
			group = &comparisonAuditGroup{source: candidate.source}
			groups[identity] = group
			order = append(order, identity)
		}
		group.audits = append(group.audits, candidate.audit)
	}
	batches := make([]*contract.ComparisonAuditBatch, 0)
	for _, identity := range order {
		group := groups[identity]
		for start := 0; start < len(group.audits); start += contract.MaxComparisonAuditItemsV1 {
			end := start + contract.MaxComparisonAuditItemsV1
			if end > len(group.audits) {
				end = len(group.audits)
			}
			var err error
			batches, err = appendComparisonAuditChunks(batches, group.source, group.audits[start:end])
			if err != nil {
				return nil, err
			}
		}
	}
	return batches, nil
}

func appendComparisonAuditChunks(
	batches []*contract.ComparisonAuditBatch,
	source *sourceObservation,
	audits []contract.ComparisonAudit,
) ([]*contract.ComparisonAuditBatch, error) {
	batch, err := buildComparisonAuditBatch(source, audits)
	if err == nil {
		_, err = contract.EncodeComparisonAuditBatch(batch)
	}
	if err == nil {
		return append(batches, batch), nil
	}
	if len(audits) <= 1 {
		return nil, err
	}
	middle := len(audits) / 2
	batches, leftErr := appendComparisonAuditChunks(batches, source, audits[:middle])
	if leftErr != nil {
		return nil, leftErr
	}
	return appendComparisonAuditChunks(batches, source, audits[middle:])
}

func buildComparisonAuditBatch(source *sourceObservation, audits []contract.ComparisonAudit) (*contract.ComparisonAuditBatch, error) {
	strategy := source.strategy
	return contract.BuildComparisonAuditBatch(
		source.partitionHashVersion,
		strategy.TenantID,
		strategy.Purpose,
		strategy.StrategyRef,
		audits,
	)
}

func buildComparisonAudit(observation auditObservation, coverage CoverageSnapshot) (contract.ComparisonAudit, error) {
	sourceEvidence := contract.ComparisonSourceEvidence{
		Kind: contract.ComparisonSourceDetectInput, SemanticSHA256: hex.EncodeToString(observation.sourceFingerprint[:]),
	}
	if observation.source.outcome != nil {
		sourceEvidence.Kind = ""
		sourceEvidence.Outcome = observation.source.outcome.Outcome
		if len(observation.source.outcome.ErrorCode) != 0 {
			if err := json.Unmarshal(observation.source.outcome.ErrorCode, &sourceEvidence.ErrorCode); err != nil {
				return contract.ComparisonAudit{}, err
			}
		}
	}
	goDecision, err := comparisonDecisionEvidence(observation.goDecision, observation.source, observation.assessment.GoInvalid)
	if err != nil {
		return contract.ComparisonAudit{}, err
	}
	pythonDecision, err := comparisonDecisionEvidence(observation.pythonDecision, observation.source, observation.assessment.PythonInvalid)
	if err != nil {
		return contract.ComparisonAudit{}, err
	}
	return contract.ComparisonAudit{
		EventKind:      contract.ComparisonAuditEventSnapshot,
		InputID:        observation.source.inputID,
		RecordID:       observation.source.recordID,
		SourceTime:     observation.source.sourceTime,
		JoinStatus:     comparisonJoin(observation.assessment.Join),
		Eligibility:    comparisonEligibility(observation.assessment.Eligibility),
		Verdict:        comparisonVerdict(observation.assessment.Verdict),
		Source:         sourceEvidence,
		GoDecision:     goDecision,
		PythonDecision: pythonDecision,
		Coverage:       comparisonCoverage(coverage),
		SourceConflict: observation.assessment.SourceConflict,
		GoConflict:     observation.assessment.GoConflict,
		PythonConflict: observation.assessment.PythonConflict,
		GoInvalid:      observation.assessment.GoInvalid,
		PythonInvalid:  observation.assessment.PythonInvalid,
	}, nil
}

func comparisonDecisionEvidence(
	observation *decisionObservation,
	source *sourceObservation,
	invalidSide bool,
) (*contract.ComparisonDecisionEvidence, error) {
	if observation == nil {
		return nil, nil
	}
	summary, err := contract.SummarizeTriggerDecision(observation.decision)
	if err != nil {
		return nil, err
	}
	identity := contract.ComparisonDecisionBatchIdentity{
		PartitionHashVersion: observation.batch.PartitionHashVersion,
		TenantID:             observation.batch.TenantID,
		Purpose:              observation.batch.Purpose,
		StrategyRef:          observation.batch.StrategyRef,
		DecisionAlgorithm:    observation.batch.DecisionAlgorithm,
	}
	identitySHA256, err := contract.DeriveComparisonDecisionBatchIdentitySHA256(identity)
	if err != nil {
		return nil, err
	}
	invalidReason := ""
	mismatchFields := []string{}
	if invalidSide {
		invalidReason, mismatchFields, err = comparisonDecisionInvalidEvidence(source, observation)
		if err != nil {
			return nil, err
		}
	}
	return &contract.ComparisonDecisionEvidence{
		BatchIdentitySHA256:    identitySHA256,
		Decision:               summary,
		InvalidReasonCode:      invalidReason,
		IdentityMismatchFields: mismatchFields,
		SemanticSHA256:         hex.EncodeToString(observation.fingerprint[:]),
	}, nil
}

func comparisonDecisionInvalidEvidence(
	source *sourceObservation,
	observation *decisionObservation,
) (string, []string, error) {
	if source == nil || source.strategy == nil || observation == nil {
		return "", nil, fmt.Errorf("comparator: invalid decision evidence requires authoritative input and decision")
	}
	want := decisionBatchIdentity{
		PartitionHashVersion: source.partitionHashVersion,
		TenantID:             source.strategy.TenantID,
		Purpose:              source.strategy.Purpose,
		StrategyRef:          source.strategy.StrategyRef,
		DecisionAlgorithm:    contract.DecisionAlgorithmV1,
	}
	mismatchFields := comparisonDecisionIdentityMismatchFields(want, observation.batch)
	if len(mismatchFields) != 0 {
		return contract.ComparisonDecisionInvalidBatchIdentity, mismatchFields, nil
	}
	err := validateDecisionAgainstInput(source, observation)
	if err == nil {
		return "", nil, fmt.Errorf("comparator: decision marked invalid without contradictory evidence")
	}
	var validationErr *contract.ValidationError
	if !errors.As(err, &validationErr) {
		return contract.ComparisonDecisionInvalidOther, []string{}, nil
	}
	switch validationErr.Field {
	case "trigger_decision.input_id", "trigger_decision.record_id":
		return contract.ComparisonDecisionInvalidRecordIdentity, []string{}, nil
	case "trigger_decision_batch.decisions.level":
		return contract.ComparisonDecisionInvalidLevel, []string{}, nil
	case "trigger_decision_batch.decisions.anomaly_timestamps":
		return contract.ComparisonDecisionInvalidAnomalyTimestamps, []string{}, nil
	case "trigger_decision_batch.decisions":
		return contract.ComparisonDecisionInvalidOutcomeReason, []string{}, nil
	default:
		return contract.ComparisonDecisionInvalidOther, []string{}, nil
	}
}

func terminalAudit(_ Assessment, coverage CoverageSnapshot) bool {
	return coverage.Phase == CoverageComplete || coverage.Phase == CoverageMissingAtBarrier
}

func comparisonDecisionIdentityMismatchFields(want, got decisionBatchIdentity) []string {
	fields := make([]string, 0, 8)
	if want.PartitionHashVersion != got.PartitionHashVersion {
		fields = append(fields, contract.ComparisonDecisionIdentityPartitionHashVersion)
	}
	if want.TenantID != got.TenantID {
		fields = append(fields, contract.ComparisonDecisionIdentityTenantID)
	}
	if want.Purpose != got.Purpose {
		fields = append(fields, contract.ComparisonDecisionIdentityPurpose)
	}
	if want.StrategyRef.StrategyID != got.StrategyRef.StrategyID {
		fields = append(fields, contract.ComparisonDecisionIdentityStrategyID)
	}
	if want.StrategyRef.ItemID != got.StrategyRef.ItemID {
		fields = append(fields, contract.ComparisonDecisionIdentityItemID)
	}
	if want.StrategyRef.Generation != got.StrategyRef.Generation {
		fields = append(fields, contract.ComparisonDecisionIdentityGeneration)
	}
	if want.StrategyRef.ContentSHA256 != got.StrategyRef.ContentSHA256 {
		fields = append(fields, contract.ComparisonDecisionIdentityContentSHA256)
	}
	if want.DecisionAlgorithm != got.DecisionAlgorithm {
		fields = append(fields, contract.ComparisonDecisionIdentityAlgorithm)
	}
	sort.Strings(fields)
	return fields
}

func comparisonCoverage(snapshot CoverageSnapshot) contract.ComparisonCoverageEvidence {
	return contract.ComparisonCoverageEvidence{
		Phase:                 comparisonCoveragePhase(snapshot.Phase),
		BarrierFrozen:         snapshot.BarrierFrozen,
		MissingRoles:          comparisonRoles(snapshot.MissingRoles),
		MissingAtBarrierRoles: comparisonRoles(snapshot.MissingAtBarrierRoles),
		LateRoles:             comparisonRoles(snapshot.LateRoles),
	}
}

func comparisonRoles(roles []StreamRole) []string {
	result := make([]string, 0, len(roles))
	for _, role := range roles {
		switch role {
		case StreamGo:
			result = append(result, contract.ComparisonRoleGo)
		case StreamPython:
			result = append(result, contract.ComparisonRolePython)
		}
	}
	sort.Strings(result)
	return result
}

func comparisonJoin(value JoinStatus) string {
	return map[JoinStatus]string{
		JoinPendingInput: contract.ComparisonJoinPendingInput, JoinPendingBoth: contract.ComparisonJoinPendingBoth,
		JoinPendingGo: contract.ComparisonJoinPendingGo, JoinPendingPython: contract.ComparisonJoinPendingPython,
		JoinComplete: contract.ComparisonJoinComplete, JoinConflict: contract.ComparisonJoinConflict,
		JoinInvalid: contract.ComparisonJoinInvalid,
	}[value]
}

func comparisonEligibility(value Eligibility) string {
	return map[Eligibility]string{
		EligibilityNone: contract.ComparisonEligibilityNone, EligibilityEligible: contract.ComparisonEligibilityEligible,
		EligibilityWarmup: contract.ComparisonEligibilityWarmup, EligibilityCoverageGap: contract.ComparisonEligibilityCoverageGap,
		EligibilitySourceError: contract.ComparisonEligibilitySourceError, EligibilityUnsupported: contract.ComparisonEligibilityUnsupported,
		EligibilityEpochUnstable: contract.ComparisonEligibilityEpochUnstable,
	}[value]
}

func comparisonVerdict(value Verdict) string {
	return map[Verdict]string{
		VerdictNone: contract.ComparisonVerdictNone, VerdictMatch: contract.ComparisonVerdictMatch,
		VerdictHardDiff: contract.ComparisonVerdictHardDiff,
	}[value]
}

func comparisonCoveragePhase(value CoveragePhase) string {
	return map[CoveragePhase]string{
		CoveragePending: contract.ComparisonCoveragePending, CoverageOverdue: contract.ComparisonCoverageOverdue,
		CoverageMissingAtBarrier: contract.ComparisonCoverageMissingAtBarrier, CoverageComplete: contract.ComparisonCoverageComplete,
	}[value]
}
