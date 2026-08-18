// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package contract

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestComparisonAuditBatchRoundTripAndPartitionKey(t *testing.T) {
	t.Parallel()

	batch := validComparisonAuditBatch(t)
	payload, err := EncodeComparisonAuditBatch(batch)
	if err != nil {
		t.Fatalf("EncodeComparisonAuditBatch() error = %v", err)
	}
	decoded, err := DecodeComparisonAuditBatch(payload)
	if err != nil {
		t.Fatalf("DecodeComparisonAuditBatch() error = %v", err)
	}
	if !reflect.DeepEqual(decoded.Audits, batch.Audits) {
		t.Fatalf("decoded audits = %#v, want %#v", decoded.Audits, batch.Audits)
	}
	inputKey, err := decodedTriggerInputForDecision(t).PartitionKey()
	if err != nil {
		t.Fatalf("input.PartitionKey() error = %v", err)
	}
	auditKey, err := decoded.PartitionKey()
	if err != nil {
		t.Fatalf("audit.PartitionKey() error = %v", err)
	}
	if !bytes.Equal(auditKey, inputKey) {
		t.Fatalf("audit partition key = %x, want %x", auditKey, inputKey)
	}
}

func TestComparisonAuditEncodesCompactDecisionEvidence(t *testing.T) {
	t.Parallel()

	payload, err := EncodeComparisonAuditBatch(validComparisonAuditBatch(t))
	if err != nil {
		t.Fatalf("EncodeComparisonAuditBatch() error = %v", err)
	}
	if bytes.Contains(payload, []byte(`"anomaly_timestamps":[`)) {
		t.Fatal("comparison audit repeated the full decision timestamp array")
	}
	for _, field := range [][]byte{[]byte(`"anomaly_timestamps_count":1`), []byte(`"anomaly_timestamps_sha256":"`)} {
		if !bytes.Contains(payload, field) {
			t.Fatalf("comparison audit is missing compact decision evidence field %s", field)
		}
	}
}

func TestBuildComparisonAuditBatchOwnsInput(t *testing.T) {
	t.Parallel()

	source := validComparisonAuditBatch(t)
	audit := source.Audits[0]
	built, err := BuildComparisonAuditBatch(
		source.PartitionHashVersion,
		source.TenantID,
		source.Purpose,
		source.StrategyRef,
		[]ComparisonAudit{audit},
	)
	if err != nil {
		t.Fatalf("BuildComparisonAuditBatch() error = %v", err)
	}
	wantLevel := *built.Audits[0].GoDecision.Decision.Level
	wantTimestampCount := built.Audits[0].GoDecision.Decision.AnomalyTimestampsCount
	wantTimestampSHA := built.Audits[0].GoDecision.Decision.AnomalyTimestampsSHA256
	audit.Coverage.MissingRoles = append(audit.Coverage.MissingRoles, ComparisonRoleGo)
	audit.GoDecision.IdentityMismatchFields = append(audit.GoDecision.IdentityMismatchFields, ComparisonDecisionIdentityGeneration)
	*audit.GoDecision.Decision.Level = 1
	audit.GoDecision.Decision.AnomalyTimestampsCount = 2
	audit.GoDecision.Decision.AnomalyTimestampsSHA256 = strings.Repeat("f", 64)
	if len(built.Audits[0].GoDecision.IdentityMismatchFields) != 0 ||
		*built.Audits[0].GoDecision.Decision.Level != wantLevel ||
		built.Audits[0].GoDecision.Decision.AnomalyTimestampsCount != wantTimestampCount ||
		built.Audits[0].GoDecision.Decision.AnomalyTimestampsSHA256 != wantTimestampSHA {
		t.Fatal("BuildComparisonAuditBatch() retained caller-owned nested state")
	}
}

func TestBuildComparisonAuditBatchDoesNotNormalizeNilArrays(t *testing.T) {
	t.Parallel()

	source := validComparisonAuditBatch(t)
	build := func(audit ComparisonAudit) error {
		_, err := BuildComparisonAuditBatch(
			source.PartitionHashVersion,
			source.TenantID,
			source.Purpose,
			source.StrategyRef,
			[]ComparisonAudit{audit},
		)
		return err
	}
	audit := source.Audits[0]
	audit.Coverage.LateRoles = nil
	if err := build(audit); err == nil {
		t.Fatal("BuildComparisonAuditBatch() normalized a nil coverage array")
	}
}

func TestComparisonAuditIDIsStableForReplayAndChangesWithSemantics(t *testing.T) {
	t.Parallel()

	audit := validComparisonAuditBatch(t).Audits[0]
	first, err := DeriveComparisonAuditID(audit)
	if err != nil {
		t.Fatalf("DeriveComparisonAuditID() error = %v", err)
	}
	const wantAuditID = "2fc41395686ae7eeb5adeed6150d452114023e87bcb2fffc665fe554035d3d12"
	if first != wantAuditID {
		t.Fatalf("audit_id = %q, want %q", first, wantAuditID)
	}
	audit.AuditID = strings.Repeat("f", 64)
	replayed, err := DeriveComparisonAuditID(audit)
	if err != nil {
		t.Fatalf("DeriveComparisonAuditID(replay) error = %v", err)
	}
	if replayed != first {
		t.Fatalf("replay audit_id = %q, want %q", replayed, first)
	}
	audit.Verdict = ComparisonVerdictHardDiff
	audit.PythonDecision.SemanticSHA256 = strings.Repeat("e", 64)
	drifted, err := DeriveComparisonAuditID(audit)
	if err != nil {
		t.Fatalf("DeriveComparisonAuditID(drift) error = %v", err)
	}
	if drifted == first {
		t.Fatal("semantic drift reused the prior audit_id")
	}
}

func TestComparisonAuditRejectsIncoherentStates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*ComparisonAudit)
	}{
		{name: "pending input is not authoritative audit", mutate: func(a *ComparisonAudit) { a.JoinStatus = ComparisonJoinPendingInput }},
		{name: "complete missing Python", mutate: func(a *ComparisonAudit) { a.PythonDecision = nil }},
		{name: "eligible without verdict", mutate: func(a *ComparisonAudit) { a.Verdict = ComparisonVerdictNone }},
		{name: "hard diff while excluded", mutate: func(a *ComparisonAudit) {
			a.Eligibility = ComparisonEligibilityWarmup
			a.Verdict = ComparisonVerdictHardDiff
		}},
		{name: "conflict without flag", mutate: func(a *ComparisonAudit) {
			a.JoinStatus = ComparisonJoinConflict
			a.Verdict = ComparisonVerdictNone
			a.Eligibility = ComparisonEligibilityNone
		}},
		{name: "invalid without flag", mutate: func(a *ComparisonAudit) {
			a.JoinStatus = ComparisonJoinInvalid
			a.Verdict = ComparisonVerdictNone
			a.Eligibility = ComparisonEligibilityNone
		}},
		{name: "Go conflict without evidence", mutate: func(a *ComparisonAudit) {
			a.GoConflict = true
			a.GoDecision = nil
			a.JoinStatus = ComparisonJoinConflict
			a.Verdict = ComparisonVerdictNone
			a.Eligibility = ComparisonEligibilityNone
			a.Coverage.MissingRoles = []string{ComparisonRoleGo}
			a.Coverage.Phase = ComparisonCoveragePending
		}},
		{name: "complete coverage with missing role", mutate: func(a *ComparisonAudit) { a.Coverage.MissingRoles = []string{ComparisonRoleGo} }},
		{name: "decision evidence contradicts coverage", mutate: func(a *ComparisonAudit) {
			a.Coverage.Phase = ComparisonCoveragePending
			a.Coverage.MissingRoles = []string{ComparisonRoleGo}
		}},
		{name: "unfrozen barrier with late role", mutate: func(a *ComparisonAudit) {
			a.Coverage.BarrierFrozen = false
			a.Coverage.LateRoles = []string{ComparisonRoleGo}
			a.Eligibility = ComparisonEligibilityCoverageGap
			a.Verdict = ComparisonVerdictNone
		}},
		{name: "sticky late takes precedence over warmup", mutate: func(a *ComparisonAudit) {
			a.Coverage.BarrierFrozen = true
			a.Coverage.LateRoles = []string{ComparisonRoleGo}
			a.Eligibility = ComparisonEligibilityWarmup
			a.Verdict = ComparisonVerdictNone
		}},
		{name: "frozen barrier cannot remain pending", mutate: func(a *ComparisonAudit) {
			a.GoDecision = nil
			a.PythonDecision = nil
			a.JoinStatus = ComparisonJoinPendingBoth
			a.Eligibility = ComparisonEligibilityNone
			a.Verdict = ComparisonVerdictNone
			a.Coverage = ComparisonCoverageEvidence{
				Phase: ComparisonCoveragePending, BarrierFrozen: true,
				MissingRoles:          []string{ComparisonRoleGo, ComparisonRolePython},
				MissingAtBarrierRoles: []string{}, LateRoles: []string{},
			}
		}},
		{name: "barrier evidence requires missing phase", mutate: func(a *ComparisonAudit) {
			a.GoDecision = nil
			a.JoinStatus = ComparisonJoinPendingGo
			a.Eligibility = ComparisonEligibilityNone
			a.Verdict = ComparisonVerdictNone
			a.Coverage = ComparisonCoverageEvidence{
				Phase: ComparisonCoverageOverdue, BarrierFrozen: true,
				MissingRoles: []string{ComparisonRoleGo}, MissingAtBarrierRoles: []string{ComparisonRoleGo}, LateRoles: []string{},
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			audit := validComparisonAuditBatch(t).Audits[0]
			test.mutate(&audit)
			if _, err := DeriveComparisonAuditID(audit); err == nil {
				t.Fatal("DeriveComparisonAuditID() accepted an incoherent assessment audit")
			}
		})
	}
}

func TestComparisonAuditBatchRejectsWireIdentityDrift(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*ComparisonAuditBatch)
	}{
		{name: "nil required features", mutate: func(batch *ComparisonAuditBatch) { batch.RequiredFeatures = nil }},
		{name: "wrong input identity", mutate: func(batch *ComparisonAuditBatch) {
			batch.Audits[0].InputID = strings.Repeat("a", 64)
			batch.Audits[0].AuditID, _ = DeriveComparisonAuditID(batch.Audits[0])
		}},
		{name: "unacknowledged Go batch identity drift", mutate: func(batch *ComparisonAuditBatch) {
			batch.Audits[0].GoDecision.BatchIdentitySHA256 = strings.Repeat("e", 64)
			batch.Audits[0].AuditID, _ = DeriveComparisonAuditID(batch.Audits[0])
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			batch := validComparisonAuditBatch(t)
			test.mutate(batch)
			if err := batch.Validate(); err == nil {
				t.Fatal("Validate() accepted wire identity drift")
			}
		})
	}
}

func TestComparisonAuditRejectsInvalidSideThatContradictsItsOwnInputIdentity(t *testing.T) {
	t.Parallel()

	batch := validComparisonAuditBatch(t)
	audit := &batch.Audits[0]
	audit.GoDecision.BatchIdentitySHA256 = strings.Repeat("e", 64)
	audit.GoInvalid = true
	audit.GoDecision.InvalidReasonCode = ComparisonDecisionInvalidLevel
	audit.JoinStatus = ComparisonJoinInvalid
	audit.Eligibility = ComparisonEligibilityNone
	audit.Verdict = ComparisonVerdictNone
	audit.AuditID, _ = DeriveComparisonAuditID(*audit)
	if err := batch.Validate(); err == nil {
		t.Fatal("Validate() accepted a decision that contradicts its own batch identity")
	}
}

func TestComparisonAuditPreservesInvalidSideBatchIdentity(t *testing.T) {
	t.Parallel()

	batch := validComparisonAuditBatch(t)
	audit := &batch.Audits[0]
	audit.GoDecision.BatchIdentitySHA256 = strings.Repeat("e", 64)
	audit.GoDecision.InvalidReasonCode = ComparisonDecisionInvalidBatchIdentity
	audit.GoDecision.IdentityMismatchFields = []string{ComparisonDecisionIdentityGeneration}
	audit.GoInvalid = true
	audit.JoinStatus = ComparisonJoinInvalid
	audit.Eligibility = ComparisonEligibilityNone
	audit.Verdict = ComparisonVerdictNone
	audit.AuditID, _ = DeriveComparisonAuditID(*audit)
	if err := batch.Validate(); err != nil {
		t.Fatalf("Validate(invalid side identity) error = %v", err)
	}
}

func TestComparisonAuditDecoderNegotiatesStrictEnvelope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(map[string]any)
		wantErr bool
	}{
		{name: "unknown major", mutate: func(d map[string]any) { d["schema"].(map[string]any)["major"] = 2 }, wantErr: true},
		{name: "unknown feature", mutate: func(d map[string]any) { d["required_features"] = []string{"future-required"} }, wantErr: true},
		{name: "minor zero unknown field", mutate: func(d map[string]any) { d["future_optional"] = true }, wantErr: true},
		{name: "higher minor optional field", mutate: func(d map[string]any) { d["schema"].(map[string]any)["minor"] = 1; d["future_optional"] = true }},
		{name: "nested unknown field", mutate: func(d map[string]any) { d["audits"].([]any)[0].(map[string]any)["future_optional"] = true }, wantErr: true},
		{name: "case collision", mutate: func(d map[string]any) { d["Audits"] = d["audits"] }, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := validComparisonAuditDocument(t)
			test.mutate(document)
			payload, err := json.Marshal(document)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}
			_, err = DecodeComparisonAuditBatch(payload)
			if (err != nil) != test.wantErr {
				t.Fatalf("DecodeComparisonAuditBatch() error = %v, wantErr=%v", err, test.wantErr)
			}
		})
	}

	payload, err := EncodeComparisonAuditBatch(validComparisonAuditBatch(t))
	if err != nil {
		t.Fatalf("EncodeComparisonAuditBatch() error = %v", err)
	}
	duplicate := bytes.Replace(payload, []byte(`"required_features":[]`), []byte(`"required_features":[],"required_features":[]`), 1)
	if _, err := DecodeComparisonAuditBatch(duplicate); err == nil {
		t.Fatal("DecodeComparisonAuditBatch() accepted a duplicate field")
	}
	explicitNullError := bytes.Replace(payload, []byte(`"outcome":"ANOMALOUS"`), []byte(`"outcome":"ANOMALOUS","error_code":null`), 1)
	if _, err := DecodeComparisonAuditBatch(explicitNullError); err == nil {
		t.Fatal("DecodeComparisonAuditBatch() accepted an explicit null source error_code")
	}

	noTrigger := validComparisonAuditBatch(t)
	for _, evidence := range []*ComparisonDecisionEvidence{noTrigger.Audits[0].GoDecision, noTrigger.Audits[0].PythonDecision} {
		evidence.Decision.Outcome = DecisionOutcomeNoTrigger
		evidence.Decision.ReasonCode = DecisionReasonTriggerConditionNotMet
		evidence.Decision.Level = nil
		evidence.Decision.AnomalyTimestampsCount = 0
		evidence.Decision.AnomalyTimestampsSHA256 = comparisonDecisionTimestampsSHA256(nil)
	}
	noTrigger.Audits[0].AuditID, _ = DeriveComparisonAuditID(noTrigger.Audits[0])
	noTriggerPayload, err := EncodeComparisonAuditBatch(noTrigger)
	if err != nil {
		t.Fatalf("EncodeComparisonAuditBatch(no trigger) error = %v", err)
	}
	explicitNullLevel := bytes.Replace(
		noTriggerPayload,
		[]byte(`"reason_code":"TRIGGER_CONDITION_NOT_MET"`),
		[]byte(`"reason_code":"TRIGGER_CONDITION_NOT_MET","level":null`),
		1,
	)
	if _, err := DecodeComparisonAuditBatch(explicitNullLevel); err == nil {
		t.Fatal("DecodeComparisonAuditBatch() accepted an explicit null decision level")
	}
	explicitNullInvalidReason := bytes.Replace(
		noTriggerPayload,
		[]byte(`"identity_mismatch_fields":[]`),
		[]byte(`"invalid_reason_code":null,"identity_mismatch_fields":[]`),
		1,
	)
	if _, err := DecodeComparisonAuditBatch(explicitNullInvalidReason); err == nil {
		t.Fatal("DecodeComparisonAuditBatch() accepted an explicit null invalid_reason_code")
	}
}

func TestComparisonAuditDecoderRejectsNonCanonicalEmptyTimestampDigest(t *testing.T) {
	t.Parallel()

	nonCanonicalDigest := validComparisonAuditBatch(t)
	for _, evidence := range []*ComparisonDecisionEvidence{
		nonCanonicalDigest.Audits[0].GoDecision,
		nonCanonicalDigest.Audits[0].PythonDecision,
	} {
		evidence.Decision.Outcome = DecisionOutcomeNoTrigger
		evidence.Decision.ReasonCode = DecisionReasonTriggerConditionNotMet
		evidence.Decision.Level = nil
		evidence.Decision.AnomalyTimestampsCount = 0
		evidence.Decision.AnomalyTimestampsSHA256 = strings.Repeat("f", 64)
	}
	nonCanonicalDigest.Audits[0].AuditID = deriveComparisonAuditIDUncheckedForTest(t, nonCanonicalDigest.Audits[0])
	nonCanonicalPayload, err := json.Marshal(nonCanonicalDigest)
	if err != nil {
		t.Fatalf("json.Marshal(non-canonical digest) error = %v", err)
	}
	if _, err := DecodeComparisonAuditBatch(nonCanonicalPayload); err == nil {
		t.Fatal("DecodeComparisonAuditBatch() accepted a non-canonical empty timestamp digest")
	}
}

func deriveComparisonAuditIDUncheckedForTest(t *testing.T, audit ComparisonAudit) string {
	t.Helper()
	audit.AuditID = ""
	payload, err := json.Marshal(audit)
	if err != nil {
		t.Fatalf("json.Marshal(audit) error = %v", err)
	}
	digest := sha256.New()
	for _, field := range [][]byte{[]byte(comparisonAuditIDVersionV1), payload} {
		var prefix [4]byte
		binary.BigEndian.PutUint32(prefix[:], uint32(len(field)))
		_, _ = digest.Write(prefix[:])
		_, _ = digest.Write(field)
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func TestComparisonAuditBatchItemAndByteLimits(t *testing.T) {
	t.Parallel()

	batch := validComparisonAuditBatch(t)
	base := batch.Audits[0]
	batch.Audits = make([]ComparisonAudit, MaxComparisonAuditItemsV1)
	for index := range batch.Audits {
		audit := base
		goEvidence, pythonEvidence := *base.GoDecision, *base.PythonDecision
		audit.GoDecision, audit.PythonDecision = &goEvidence, &pythonEvidence
		sourceTime := int64(1000 + index)
		recordID := fmt.Sprintf("%s.%d", strings.Repeat("c", 32), sourceTime)
		inputID, err := DeriveInputID(InputIdentity{
			TenantID: batch.TenantID, Purpose: batch.Purpose, StrategyID: batch.StrategyRef.StrategyID,
			ItemID: batch.StrategyRef.ItemID, StrategyContentSHA256: batch.StrategyRef.ContentSHA256, RecordID: recordID,
		})
		if err != nil {
			t.Fatalf("DeriveInputID() error = %v", err)
		}
		audit.InputID, audit.RecordID, audit.SourceTime = inputID, recordID, sourceTime
		audit.GoDecision.Decision.InputID, audit.GoDecision.Decision.RecordID = inputID, recordID
		audit.GoDecision.Decision.DecisionID, _ = DeriveTriggerDecisionID(inputID)
		audit.GoDecision.Decision.AnomalyTimestampsCount = 1
		audit.GoDecision.Decision.AnomalyTimestampsSHA256 = comparisonDecisionTimestampsSHA256([]int64{sourceTime})
		audit.PythonDecision.Decision = audit.GoDecision.Decision
		audit.AuditID, _ = DeriveComparisonAuditID(audit)
		batch.Audits[index] = audit
	}
	if err := batch.Validate(); err != nil {
		t.Fatalf("Validate(500) error = %v", err)
	}
	batch.Audits = append(batch.Audits, batch.Audits[0])
	if err := batch.Validate(); err == nil {
		t.Fatal("Validate() accepted 501 audits")
	}

	exact := validComparisonAuditBatch(t)
	baseline, err := EncodeComparisonAuditBatch(exact)
	if err != nil {
		t.Fatalf("EncodeComparisonAuditBatch(baseline) error = %v", err)
	}
	exact.StrategyRef.Generation += strings.Repeat("x", MaxComparisonAuditBytesV1-len(baseline))
	refreshComparisonAuditBatchIdentity(t, exact)
	atLimit, err := EncodeComparisonAuditBatch(exact)
	if err != nil {
		t.Fatalf("EncodeComparisonAuditBatch(exact limit) error = %v", err)
	}
	if len(atLimit) != MaxComparisonAuditBytesV1 {
		t.Fatalf("encoded bytes = %d, want %d", len(atLimit), MaxComparisonAuditBytesV1)
	}
	if _, err := DecodeComparisonAuditBatch(atLimit); err != nil {
		t.Fatalf("DecodeComparisonAuditBatch(exact limit) error = %v", err)
	}
	exact.StrategyRef.Generation += "x"
	refreshComparisonAuditBatchIdentity(t, exact)
	if _, err := EncodeComparisonAuditBatch(exact); err == nil {
		t.Fatal("EncodeComparisonAuditBatch() accepted payload above byte limit")
	}
}

func validComparisonAuditBatch(t *testing.T) *ComparisonAuditBatch {
	t.Helper()
	input := decodedTriggerInputForDecision(t)
	source := input.DetectionOutcomes[0]
	decision := newTriggerDecision(
		t, source, DecisionOutcomeTrigger, DecisionReasonTriggerConditionMet, intPointer(3), []int64{source.Record.SourceTime},
	)
	identity := ComparisonDecisionBatchIdentity{
		PartitionHashVersion: input.PartitionHashVersion,
		TenantID:             input.StrategyIR.TenantID,
		Purpose:              input.StrategyIR.Purpose,
		StrategyRef:          input.StrategyIR.StrategyRef,
		DecisionAlgorithm:    DecisionAlgorithmV1,
	}
	identitySHA256, err := DeriveComparisonDecisionBatchIdentitySHA256(identity)
	if err != nil {
		t.Fatalf("DeriveComparisonDecisionBatchIdentitySHA256() error = %v", err)
	}
	audit := ComparisonAudit{
		EventKind:   ComparisonAuditEventSnapshot,
		InputID:     source.InputID,
		RecordID:    source.Record.RecordID,
		SourceTime:  source.Record.SourceTime,
		JoinStatus:  ComparisonJoinComplete,
		Eligibility: ComparisonEligibilityEligible,
		Verdict:     ComparisonVerdictMatch,
		Source: ComparisonSourceEvidence{
			Outcome: source.Outcome, SemanticSHA256: strings.Repeat("b", 64),
		},
		GoDecision: &ComparisonDecisionEvidence{
			BatchIdentitySHA256: identitySHA256, Decision: mustSummarizeTriggerDecision(t, decision),
			IdentityMismatchFields: []string{}, SemanticSHA256: strings.Repeat("d", 64),
		},
		PythonDecision: &ComparisonDecisionEvidence{
			BatchIdentitySHA256: identitySHA256, Decision: mustSummarizeTriggerDecision(t, decision),
			IdentityMismatchFields: []string{}, SemanticSHA256: strings.Repeat("d", 64),
		},
		Coverage: ComparisonCoverageEvidence{Phase: ComparisonCoverageComplete, MissingRoles: []string{}, MissingAtBarrierRoles: []string{}, LateRoles: []string{}},
	}
	batch, err := BuildComparisonAuditBatch(
		input.PartitionHashVersion,
		input.StrategyIR.TenantID,
		input.StrategyIR.Purpose,
		input.StrategyIR.StrategyRef,
		[]ComparisonAudit{audit},
	)
	if err != nil {
		t.Fatalf("BuildComparisonAuditBatch() error = %v", err)
	}
	return batch
}

func mustSummarizeTriggerDecision(t *testing.T, decision TriggerDecision) ComparisonDecisionSummary {
	t.Helper()
	summary, err := SummarizeTriggerDecision(decision)
	if err != nil {
		t.Fatalf("SummarizeTriggerDecision() error = %v", err)
	}
	return summary
}

func refreshComparisonAuditBatchIdentity(t *testing.T, batch *ComparisonAuditBatch) {
	t.Helper()
	identitySHA256, err := DeriveComparisonDecisionBatchIdentitySHA256(ComparisonDecisionBatchIdentity{
		PartitionHashVersion: batch.PartitionHashVersion,
		TenantID:             batch.TenantID,
		Purpose:              batch.Purpose,
		StrategyRef:          batch.StrategyRef,
		DecisionAlgorithm:    DecisionAlgorithmV1,
	})
	if err != nil {
		t.Fatalf("DeriveComparisonDecisionBatchIdentitySHA256() error = %v", err)
	}
	for index := range batch.Audits {
		audit := &batch.Audits[index]
		if audit.GoDecision != nil {
			audit.GoDecision.BatchIdentitySHA256 = identitySHA256
		}
		if audit.PythonDecision != nil {
			audit.PythonDecision.BatchIdentitySHA256 = identitySHA256
		}
		audit.AuditID, err = DeriveComparisonAuditID(*audit)
		if err != nil {
			t.Fatalf("DeriveComparisonAuditID() error = %v", err)
		}
	}
}

func validComparisonAuditDocument(t *testing.T) map[string]any {
	t.Helper()
	payload, err := json.Marshal(validComparisonAuditBatch(t))
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var document map[string]any
	if err := json.Unmarshal(payload, &document); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	return document
}
