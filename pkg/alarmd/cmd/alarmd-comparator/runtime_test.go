// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func TestComparisonResultLedgerCountsOneVerdictPerInput(t *testing.T) {
	t.Parallel()

	ledger := mustLedger(t, 4)
	matched := verdictAudit("input-1", contract.ComparisonVerdictMatch)

	if got := ledger.admit(matched); len(got) != 1 || got[0] != metric.CompareMatch {
		t.Fatalf("first match = %v, want one match", got)
	}
	if got := ledger.admit(matched); len(got) != 0 {
		t.Fatalf("replayed match = %v, want no additional count", got)
	}
	if got := ledger.admit(verdictAudit("input-2", contract.ComparisonVerdictHardDiff)); len(got) != 1 || got[0] != metric.CompareMismatch {
		t.Fatalf("hard diff = %v, want one mismatch", got)
	}
}

func TestComparisonResultLedgerCountsAVerdictThatActuallyChanges(t *testing.T) {
	t.Parallel()

	ledger := mustLedger(t, 4)
	if got := ledger.admit(verdictAudit("input-1", contract.ComparisonVerdictMatch)); len(got) != 1 {
		t.Fatalf("first match = %v, want one match", got)
	}
	if got := ledger.admit(verdictAudit("input-1", contract.ComparisonVerdictHardDiff)); len(got) != 1 || got[0] != metric.CompareMismatch {
		t.Fatalf("changed verdict = %v, want the divergence counted", got)
	}
	if got := ledger.admit(verdictAudit("input-1", contract.ComparisonVerdictHardDiff)); len(got) != 0 {
		t.Fatalf("repeated hard diff = %v, want no additional count", got)
	}
}

func TestComparisonResultLedgerCountsEachMissingRoleOnceAsBarriersFreeze(t *testing.T) {
	t.Parallel()

	ledger := mustLedger(t, 4)
	goMissing := missingAudit("input-1", contract.ComparisonRoleGo)
	bothMissing := missingAudit("input-1", contract.ComparisonRoleGo, contract.ComparisonRolePython)

	if got := ledger.admit(goMissing); len(got) != 1 || got[0] != metric.CompareMissingGo {
		t.Fatalf("first missing role = %v, want one missing Go", got)
	}
	if got := ledger.admit(bothMissing); len(got) != 1 || got[0] != metric.CompareMissingPython {
		t.Fatalf("progressive barrier = %v, want only the newly missing Python role", got)
	}
	if got := ledger.admit(bothMissing); len(got) != 0 {
		t.Fatalf("replayed missing roles = %v, want no additional count", got)
	}
}

func TestComparisonResultLedgerIgnoresCoverageGapsWithoutABarrier(t *testing.T) {
	t.Parallel()

	ledger := mustLedger(t, 4)
	pending := contract.ComparisonAudit{
		InputID: "input-1",
		Verdict: contract.ComparisonVerdictNone,
		Coverage: contract.ComparisonCoverageEvidence{
			Phase:        contract.ComparisonCoveragePending,
			MissingRoles: []string{contract.ComparisonRoleGo},
		},
	}
	if got := ledger.admit(pending); len(got) != 0 {
		t.Fatalf("pending coverage = %v, want no comparison count", got)
	}
}

func TestRecordingComparisonAuditSinkKeepsARollingDeduplicationWindow(t *testing.T) {
	t.Parallel()

	published := 0
	sink := mustRecordingSink(t, 1, func(*contract.ComparisonAuditBatch) error {
		published++
		return nil
	})
	first := &contract.ComparisonAuditBatch{Audits: []contract.ComparisonAudit{verdictAudit("input-1", contract.ComparisonVerdictMatch)}}
	if err := sink.WriteBatch(context.Background(), first); err != nil {
		t.Fatalf("WriteBatch(first) error = %v", err)
	}
	second := &contract.ComparisonAuditBatch{Audits: []contract.ComparisonAudit{verdictAudit("input-2", contract.ComparisonVerdictMatch)}}
	if err := sink.WriteBatch(context.Background(), second); err != nil {
		t.Fatalf("WriteBatch(second) error = %v", err)
	}
	if published != 2 {
		t.Fatalf("published batches = %d, want both batches", published)
	}
	if _, ok := sink.ledger.counted["input-1"]; ok {
		t.Fatal("oldest input remained in the rolling deduplication window")
	}
	if _, ok := sink.ledger.counted["input-2"]; !ok {
		t.Fatal("newest input was not retained in the rolling deduplication window")
	}
}

func TestRecordingComparisonAuditSinkDoesNotCountAFailedPublish(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	want := errors.New("audit sink unavailable")
	sink, err := newRecordingComparisonAuditSinkWithLogger(
		metric.NewRecorder(metric.BuildInfo{}),
		stubAuditSink(func(*contract.ComparisonAuditBatch) error { return want }),
		4,
		observability.New(observability.ComponentComparator, &output),
	)
	if err != nil {
		t.Fatalf("newRecordingComparisonAuditSinkWithLogger() error = %v", err)
	}
	batch := &contract.ComparisonAuditBatch{Audits: []contract.ComparisonAudit{verdictAudit("input-1", contract.ComparisonVerdictMatch)}}

	if err := sink.WriteBatch(context.Background(), batch); !errors.Is(err, want) {
		t.Fatalf("WriteBatch() error = %v, want %v", err, want)
	}
	if len(sink.ledger.counted) != 0 {
		t.Fatalf("ledger = %#v, want no state for an unpublished audit", sink.ledger.counted)
	}
	if output.Len() != 0 {
		t.Fatalf("log after failed ACK = %q, want empty", output.String())
	}
}

func TestRecordingComparisonAuditSinkLogsACKAndAggregatedResults(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	sink, err := newRecordingComparisonAuditSinkWithLogger(
		metric.NewRecorder(metric.BuildInfo{}),
		stubAuditSink(func(*contract.ComparisonAuditBatch) error { return nil }),
		10,
		observability.New(observability.ComponentComparator, &output),
	)
	if err != nil {
		t.Fatalf("newRecordingComparisonAuditSinkWithLogger() error = %v", err)
	}
	batch := &contract.ComparisonAuditBatch{Audits: []contract.ComparisonAudit{
		verdictAudit("input-secret-1", contract.ComparisonVerdictMatch),
		verdictAudit("input-secret-2", contract.ComparisonVerdictHardDiff),
		missingAudit("input-secret-3", contract.ComparisonRoleGo, contract.ComparisonRolePython),
	}}
	if err := sink.WriteBatch(context.Background(), batch); err != nil {
		t.Fatalf("WriteBatch() error = %v", err)
	}
	logOutput := output.String()
	for _, want := range []string{
		`"stage":"comparison_audit_ack"`, `"result":"broker_ack"`, `"records":3`,
		`"match":1`, `"mismatch":1`, `"missing_go":1`, `"missing_python":1`,
	} {
		if !strings.Contains(logOutput, want) {
			t.Fatalf("log = %q, want %q", logOutput, want)
		}
	}
	if strings.Contains(logOutput, "input-secret") {
		t.Fatalf("log leaked dynamic input ID: %q", logOutput)
	}
	if strings.Count(logOutput, "\n") != 1 {
		t.Fatalf("log lines = %q, want one aggregate event for the batch", logOutput)
	}
	if strings.Contains(logOutput, `"result":"success"`) {
		t.Fatalf("ACK log = %q, must not use shutdown success result", logOutput)
	}
}

func mustLedger(t *testing.T, capacity int) *comparisonResultLedger {
	t.Helper()
	ledger, err := newComparisonResultLedger(capacity)
	if err != nil {
		t.Fatalf("newComparisonResultLedger() error = %v", err)
	}
	return ledger
}

func mustRecordingSink(
	t *testing.T,
	capacity int,
	write func(*contract.ComparisonAuditBatch) error,
) *recordingComparisonAuditSink {
	t.Helper()
	sink, err := newRecordingComparisonAuditSink(metric.NewRecorder(metric.BuildInfo{}), stubAuditSink(write), capacity)
	if err != nil {
		t.Fatalf("newRecordingComparisonAuditSink() error = %v", err)
	}
	return sink
}

type stubAuditSink func(*contract.ComparisonAuditBatch) error

func (s stubAuditSink) WriteBatch(_ context.Context, batch *contract.ComparisonAuditBatch) error {
	return s(batch)
}

func verdictAudit(inputID, verdict string) contract.ComparisonAudit {
	return contract.ComparisonAudit{
		InputID:  inputID,
		Verdict:  verdict,
		Coverage: contract.ComparisonCoverageEvidence{Phase: contract.ComparisonCoverageComplete},
	}
}

func missingAudit(inputID string, roles ...string) contract.ComparisonAudit {
	return contract.ComparisonAudit{
		InputID: inputID,
		Verdict: contract.ComparisonVerdictNone,
		Coverage: contract.ComparisonCoverageEvidence{
			Phase:                 contract.ComparisonCoverageMissingAtBarrier,
			MissingRoles:          roles,
			MissingAtBarrierRoles: roles,
		},
	}
}
