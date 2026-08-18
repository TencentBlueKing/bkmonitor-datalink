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
	"fmt"
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarm-engine/contract"
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
		audit, err := buildComparisonAudit(observation, coverage)
		if err != nil {
			return nil, r.invalidateLocked(fmt.Errorf("comparator: preview audit: %w", err))
		}
		candidates = append(candidates, comparisonAuditCandidate{input: observation.input, audit: audit})
	}
	batches, err := buildComparisonAuditBatches(candidates)
	if err != nil {
		return nil, r.invalidateLocked(fmt.Errorf("comparator: build audit batches: %w", err))
	}
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
		candidates = append(candidates, comparisonAuditCandidate{input: observation.input, audit: audit})
	}
	batches, err := buildComparisonAuditBatches(candidates)
	if err != nil {
		return nil, r.invalidateLocked(fmt.Errorf("comparator: build audit batches: %w", err))
	}
	return batches, nil
}

type comparisonAuditCandidate struct {
	input *contract.TriggerInput
	audit contract.ComparisonAudit
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
	input  *contract.TriggerInput
	audits []contract.ComparisonAudit
}

func buildComparisonAuditBatches(candidates []comparisonAuditCandidate) ([]*contract.ComparisonAuditBatch, error) {
	groups := make(map[comparisonAuditIdentity]*comparisonAuditGroup)
	order := make([]comparisonAuditIdentity, 0)
	for _, candidate := range candidates {
		if candidate.input == nil {
			return nil, fmt.Errorf("authoritative TriggerInput is required")
		}
		strategy := candidate.input.StrategyIR
		identity := comparisonAuditIdentity{
			partitionHashVersion: candidate.input.PartitionHashVersion,
			tenantID:             strategy.TenantID, purpose: strategy.Purpose,
			strategyID: strategy.StrategyRef.StrategyID, itemID: strategy.StrategyRef.ItemID,
			generation: strategy.StrategyRef.Generation, contentSHA256: strategy.StrategyRef.ContentSHA256,
		}
		group := groups[identity]
		if group == nil {
			group = &comparisonAuditGroup{input: candidate.input}
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
			batches, err = appendComparisonAuditChunks(batches, group.input, group.audits[start:end])
			if err != nil {
				return nil, err
			}
		}
	}
	return batches, nil
}

func appendComparisonAuditChunks(
	batches []*contract.ComparisonAuditBatch,
	input *contract.TriggerInput,
	audits []contract.ComparisonAudit,
) ([]*contract.ComparisonAuditBatch, error) {
	batch, err := buildComparisonAuditBatch(input, audits)
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
	batches, leftErr := appendComparisonAuditChunks(batches, input, audits[:middle])
	if leftErr != nil {
		return nil, leftErr
	}
	return appendComparisonAuditChunks(batches, input, audits[middle:])
}

func buildComparisonAuditBatch(input *contract.TriggerInput, audits []contract.ComparisonAudit) (*contract.ComparisonAuditBatch, error) {
	strategy := input.StrategyIR
	return contract.BuildComparisonAuditBatch(
		input.PartitionHashVersion,
		strategy.TenantID,
		strategy.Purpose,
		strategy.StrategyRef,
		audits,
	)
}

func buildComparisonAudit(observation auditObservation, coverage CoverageSnapshot) (contract.ComparisonAudit, error) {
	errorCode := ""
	if len(observation.outcome.ErrorCode) != 0 {
		if err := json.Unmarshal(observation.outcome.ErrorCode, &errorCode); err != nil {
			return contract.ComparisonAudit{}, err
		}
	}
	return contract.ComparisonAudit{
		EventKind:   contract.ComparisonAuditEventSnapshot,
		InputID:     observation.outcome.InputID,
		RecordID:    observation.outcome.Record.RecordID,
		SourceTime:  observation.outcome.Record.SourceTime,
		JoinStatus:  comparisonJoin(observation.assessment.Join),
		Eligibility: comparisonEligibility(observation.assessment.Eligibility),
		Verdict:     comparisonVerdict(observation.assessment.Verdict),
		Source: contract.ComparisonSourceEvidence{
			Outcome: observation.outcome.Outcome, ErrorCode: errorCode,
			SemanticSHA256: hex.EncodeToString(observation.sourceFingerprint[:]),
		},
		GoDecision:     comparisonDecisionEvidence(observation.goDecision),
		PythonDecision: comparisonDecisionEvidence(observation.pythonDecision),
		Coverage:       comparisonCoverage(coverage),
		SourceConflict: observation.assessment.SourceConflict,
		GoConflict:     observation.assessment.GoConflict,
		PythonConflict: observation.assessment.PythonConflict,
		GoInvalid:      observation.assessment.GoInvalid,
		PythonInvalid:  observation.assessment.PythonInvalid,
	}, nil
}

func comparisonDecisionEvidence(observation *decisionObservation) *contract.ComparisonDecisionEvidence {
	if observation == nil {
		return nil
	}
	return &contract.ComparisonDecisionEvidence{
		BatchIdentity: contract.ComparisonDecisionBatchIdentity{
			PartitionHashVersion: observation.batch.PartitionHashVersion,
			TenantID:             observation.batch.TenantID,
			Purpose:              observation.batch.Purpose,
			StrategyRef:          observation.batch.StrategyRef,
			DecisionAlgorithm:    observation.batch.DecisionAlgorithm,
		},
		Decision:       cloneDecision(observation.decision),
		SemanticSHA256: hex.EncodeToString(observation.fingerprint[:]),
	}
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
