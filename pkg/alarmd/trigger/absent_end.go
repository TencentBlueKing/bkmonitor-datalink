// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package trigger

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strconv"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// AbsentNoDataEndV1 is a series no data ends while it is absent from the
// answer: the levels written to end on no data (strategy.RecoveryPlan
// .MissingData) whose alerts are open and whose last real point is
// (R + T + 1) periods old. Its identity is what the series had when it was
// last present - the worker keeps it while the alert is open - because there
// is no record to take it from now.
type AbsentNoDataEndV1 struct {
	TenantID, BusinessID string
	Plan                 *strategy.CompiledPlan
	// DimensionIdentityDigest and Dimensions are the series' identity and
	// alert dimensions as its last record had them; DedupeMD5 its alert's
	// fingerprint.
	DimensionIdentityDigest string
	Dimensions              map[string]json.RawMessage
	DedupeMD5               string
	// SourceTime is the period the round judged, which the series has no
	// point in; EvaluationTime the round.
	SourceTime, EvaluationTime int64
	ExecutionID                string
	LevelIDs                   []uint32
	MaxEvidenceBytes           int
	// AttributedBusinessID is the business a global business Plan attributed
	// the series to while it reported (execution.NoDataEndEntry), which the
	// end is filed under as the alert was; empty files it under BusinessID.
	AttributedBusinessID string
}

// emptyAnomalyDigest is the anomaly digest of a window with no anomaly in it,
// as the history summary derives it: nothing hashed.
var emptyAnomalyDigest = sha256.Sum256(nil)

// BuildAbsentNoDataEndV1 is the RECOVERY of an absent series no data ended:
// each named level ended by no data (contract.RecoveryEndedByNoData), with
// its ending, a decision window at the judged period that saw no anomaly and
// no point, and no detect evidence; the record is the series' identity at
// that period, marked absent (contract.TriggerRecordRefV1.Absent).
func BuildAbsentNoDataEndV1(input AbsentNoDataEndV1) (*contract.TriggerEventV1, error) {
	if input.Plan == nil || len(input.LevelIDs) == 0 {
		return nil, errors.New("alarmd trigger: an absent no-data end needs a Plan and its levels")
	}
	ref := input.Plan.StrategyRef()
	strategyID, strategyErr := strconv.ParseInt(ref.StrategyID, 10, 64)
	businessID, businessErr := strconv.ParseInt(input.BusinessID, 10, 64)
	if ref.SnapshotRevision <= 0 || strategyErr != nil || businessErr != nil || ref.TenantID != input.TenantID {
		return nil, errors.New("alarmd trigger: an absent no-data end needs a frozen strategy identity")
	}
	recordID, err := contract.DeriveRecordIDV2(input.DimensionIdentityDigest, input.SourceTime)
	if err != nil {
		return nil, err
	}
	var results []contract.LevelResultV1
	for _, level := range input.Plan.Levels().All() {
		definition := level.Definition()
		if !slices.Contains(input.LevelIDs, definition.LevelID) {
			continue
		}
		triggerPlan, recoveryPlan := level.Trigger(), level.Recovery()
		if recoveryPlan.MissingData != contract.RecoveryMissingDataEvaluateAsZero {
			return nil, errors.New("alarmd trigger: an absent no-data end for a level not written to end on no data")
		}
		start, ok := windowStartV2(input.SourceTime, triggerPlan.WindowSize, triggerPlan.StepSeconds)
		if !ok {
			return nil, errors.New("alarmd trigger: an absent no-data end's window does not fit in time")
		}
		results = append(results, contract.LevelResultV1{
			LevelID: definition.LevelID, LevelCode: definition.LevelCode, Priority: definition.Priority,
			Result: contract.LevelResultRecovery, LevelTriggerFingerprint: level.Fingerprints().Trigger,
			DecisionWindow: contract.DecisionWindowV1{
				Type: decisionWindowTypeV1, Version: 1, SourceTime: input.SourceTime,
				Trigger: contract.TriggerWindowEvidenceV1{WindowStart: start, WindowEnd: input.SourceTime,
					WindowSize: triggerPlan.WindowSize, RequiredAnomalies: triggerPlan.RequiredAnomalies},
				Recovery: contract.RecoveryWindowEvidenceV1{Enabled: recoveryPlan.Enabled,
					RequiredConsecutiveWindows: recoveryPlan.ConsecutiveWindows, OldestWindowStart: start,
					End: recoveryPlan.End, EndedBy: contract.RecoveryEndedByNoData},
				// Not FULL: the window holds no point of the series at all.
				HistoryCompleteness: string(HistoryGapped),
				WindowEvidence:      contract.WindowEvidenceV1{AnomalyTimestampsDigest: hex.EncodeToString(emptyAnomalyDigest[:])},
			},
		})
	}
	if len(results) != len(input.LevelIDs) {
		return nil, errors.New("alarmd trigger: an absent no-data end names a level the Plan does not have")
	}
	fingerprints := input.Plan.Fingerprints()
	dimensions := input.Dimensions
	if dimensions == nil {
		dimensions = map[string]json.RawMessage{}
	}
	event, err := contract.BuildTriggerEventV1(contract.TriggerEventBuildInputV1{
		StrategyRef: &contract.StrategySnapshotRef{TenantID: ref.TenantID, BusinessID: businessID, StrategyID: strategyID, Revision: ref.SnapshotRevision},
		DedupeMD5:   input.DedupeMD5, EventKind: contract.TriggerEventRecovery,
		TenantID: input.TenantID, BusinessID: input.BusinessID, PlanRef: input.Plan.PlanRef(),
		RecordRef: contract.TriggerRecordRefV1{RecordID: recordID, SourceTime: input.SourceTime,
			DimensionIdentityDigest: input.DimensionIdentityDigest, Dimensions: dimensions, Absent: true},
		Observed:     contract.TriggerObservedV1{Values: map[string]json.RawMessage{}},
		LevelResults: results, EvaluationTime: input.EvaluationTime,
		DetectPlanFingerprint: fingerprints.Detect, TriggerStateFingerprint: fingerprints.Trigger,
		ExecutionID: input.ExecutionID, MaxEvidenceBytes: input.MaxEvidenceBytes,
	})
	if err != nil {
		return nil, err
	}
	// Set beside the built event, as the evaluator sets it on every other
	// event of a global business Plan: it is where the output files the
	// alert, not part of the event's identity.
	event.AttributedBusinessID = input.AttributedBusinessID
	return event, nil
}
