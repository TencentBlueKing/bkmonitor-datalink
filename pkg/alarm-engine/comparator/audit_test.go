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
	"errors"
	"fmt"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarm-engine/contract"
)

func TestRunPreviewsCompleteAuditBeforeOffsetCommit(t *testing.T) {
	t.Parallel()

	run, _ := mustCoverageRun(t, testAssignments())
	inputPayload, input := testTriggerInput(t, "normal")
	key := mustPartitionKey(t, input)
	decisionPayload := testDecisionBatch(t, input, contract.DecisionOutcomeNoTrigger)
	commitStreamRecord(t, run, StreamRecord{
		Epoch: "run-1", Role: StreamInput, Topic: "input", Partition: 0, Offset: 20, Key: key, Value: inputPayload,
	})
	commitStreamRecord(t, run, StreamRecord{
		Epoch: "run-1", Role: StreamGo, Topic: "go", Partition: 0, Offset: 10, Key: key, Value: decisionPayload,
	})
	prepared, err := run.Prepare(StreamRecord{
		Epoch: "run-1", Role: StreamPython, Topic: "python", Partition: 0, Offset: 30, Key: key, Value: decisionPayload,
	})
	if err != nil {
		t.Fatalf("Prepare(python) error = %v", err)
	}
	inputID := input.DetectionOutcomes[0].InputID
	if _, _, err := run.Assess("run-1", inputID, Gates{}); !errors.Is(err, ErrRecordInFlight) {
		t.Fatalf("Assess() error = %v, want record in flight", err)
	}

	batches, err := run.PreviewAudits(prepared, Gates{StableEpoch: true})
	if err != nil || len(batches) != 1 {
		t.Fatalf("PreviewAudits() batches=%d error=%v", len(batches), err)
	}
	batch := batches[0]
	if len(batch.Audits) != 1 {
		t.Fatalf("PreviewAudit() audits=%d, want 1", len(batch.Audits))
	}
	audit := batch.Audits[0]
	if audit.InputID != inputID || audit.JoinStatus != contract.ComparisonJoinComplete ||
		audit.Eligibility != contract.ComparisonEligibilityEligible || audit.Verdict != contract.ComparisonVerdictMatch ||
		audit.Coverage.Phase != contract.ComparisonCoverageComplete {
		t.Fatalf("PreviewAudit() audit = %#v, want complete eligible match", audit)
	}
	if next, err := run.NextOffset("run-1", StreamPython, 0); !errors.Is(err, ErrRecordInFlight) || next != 0 {
		t.Fatalf("NextOffset() before commit = %d, error=%v", next, err)
	}
	if _, err := run.CommitSucceeded(prepared); err != nil {
		t.Fatalf("CommitSucceeded() error = %v", err)
	}
	if next, err := run.NextOffset("run-1", StreamPython, 0); err != nil || next != 31 {
		t.Fatalf("NextOffset() after commit = %d, error=%v, want 31", next, err)
	}
}

func TestComparisonAuditBatchesRespectCountAndEncodedByteLimits(t *testing.T) {
	t.Parallel()

	_, input := testTriggerInput(t, "normal")
	candidates := make([]comparisonAuditCandidate, 0, contract.MaxComparisonAuditItemsV1+1)
	for index := 0; index <= contract.MaxComparisonAuditItemsV1; index++ {
		sourceTime := int64(index + 1)
		recordID := fmt.Sprintf("%032x.%d", index+1, sourceTime)
		inputID, err := contract.DeriveInputID(contract.InputIdentity{
			TenantID: input.StrategyIR.TenantID, Purpose: input.StrategyIR.Purpose,
			StrategyID: input.StrategyIR.StrategyRef.StrategyID, ItemID: input.StrategyIR.StrategyRef.ItemID,
			StrategyContentSHA256: input.StrategyIR.StrategyRef.ContentSHA256, RecordID: recordID,
		})
		if err != nil {
			t.Fatalf("DeriveInputID() error = %v", err)
		}
		candidates = append(candidates, comparisonAuditCandidate{
			input: input,
			audit: contract.ComparisonAudit{
				EventKind: contract.ComparisonAuditEventSnapshot,
				InputID:   inputID, RecordID: recordID, SourceTime: sourceTime,
				JoinStatus: contract.ComparisonJoinPendingBoth, Eligibility: contract.ComparisonEligibilityNone, Verdict: contract.ComparisonVerdictNone,
				Source: contract.ComparisonSourceEvidence{Outcome: contract.OutcomeNormal, SemanticSHA256: fmt.Sprintf("%064x", index+1)},
				Coverage: contract.ComparisonCoverageEvidence{
					Phase: contract.ComparisonCoveragePending, MissingRoles: []string{contract.ComparisonRoleGo, contract.ComparisonRolePython},
					MissingAtBarrierRoles: []string{}, LateRoles: []string{},
				},
			},
		})
	}
	batches, err := buildComparisonAuditBatches(candidates)
	if err != nil {
		t.Fatalf("buildComparisonAuditBatches() error = %v", err)
	}
	total := 0
	for _, batch := range batches {
		if len(batch.Audits) == 0 || len(batch.Audits) > contract.MaxComparisonAuditItemsV1 {
			t.Fatalf("batch audits = %d", len(batch.Audits))
		}
		payload, err := contract.EncodeComparisonAuditBatch(batch)
		if err != nil || len(payload) > contract.MaxComparisonAuditBytesV1 {
			t.Fatalf("EncodeComparisonAuditBatch() bytes=%d error=%v", len(payload), err)
		}
		total += len(batch.Audits)
	}
	if total != len(candidates) || len(batches) < 2 {
		t.Fatalf("batches=%d audits=%d, want all %d candidates split", len(batches), total, len(candidates))
	}
}
