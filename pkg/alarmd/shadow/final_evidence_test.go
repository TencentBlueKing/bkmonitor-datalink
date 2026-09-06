// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package shadow

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

func finalTestInput(t *testing.T) GoEvidenceInput {
	t.Helper()
	b, err := os.ReadFile("../contract/testdata/go-v2/trigger_event_v1.json")
	if err != nil {
		t.Fatal(err)
	}
	event, err := contract.DecodeTriggerEventV1(b)
	if err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile("../contract/testdata/shadow-final-v1/comparison_config.json")
	if err != nil {
		t.Fatal(err)
	}
	var golden struct {
		Vectors []struct {
			Input contract.ComparisonConfigV1 `json:"canonical_input"`
		} `json:"vectors"`
	}
	if err := json.Unmarshal(b, &golden); err != nil {
		t.Fatal(err)
	}
	cfg := golden.Vectors[0].Input
	cfg.SelectionOrder = []uint32{5, 1}
	for i := range cfg.Levels {
		cfg.Levels[i].Trigger.WindowPoints = 1
		cfg.Levels[i].Trigger.RequiredAnomalies = 1
		cfg.Levels[i].Recovery.ConsecutiveWindows = 1
		cfg.Levels[i].Priority = 20
		if cfg.Levels[i].LevelID == 5 {
			cfg.Levels[i].Priority = 1
		}
	}
	cfg.Numeric.TargetUnit = "percent"
	_, digest, err := contract.CanonicalComparisonConfigV1(cfg)
	if err != nil {
		t.Fatal(err)
	}
	d := strings.Repeat("a", 64)
	return GoEvidenceInput{EpochID: "epoch", IdentityVersion: "identity-v1", ProjectionVersion: "primary-v1", Event: *event, ACK: BusinessACK{EventID: event.EventID, SemanticDigest: event.EventSemanticDigest, Confirmed: true}, Config: cfg,
		Context: contract.ShadowContextV1{ComparisonConfigDigest: digest, PlanScheduleRevision: "plan-schedule", EvaluationTime: event.EvaluationTime, SlotIdentity: "slot", SnapshotRevision: "snapshot", QueryRevision: "query", QueryGroupScheduleRevision: "qg-schedule", DuePlanSetDigest: d, EffectiveTimeRequirementDigest: d, EffectiveTimeFactDigest: d}, Completeness: contract.ShadowCompletenessV1{Input: "FULL", Readiness: "READY", History: "FULL"}}
}

func TestFinalEvidenceNativeACKAndIsolation(t *testing.T) {
	input := finalTestInput(t)
	evidence, err := BuildGoFinalEvidence(input, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Primary.LevelID != 5 || evidence.Primary.Values["value"] != "50.1" || len(evidence.Native.LevelResults) != 2 || evidence.GoEvent.EventID != input.Event.EventID {
		t.Fatal("lost native fact or dynamic primary")
	}
	input.Event.LevelResults[0].Result = "changed"
	input.Event.RecordRef.Dimensions["host"] = json.RawMessage(`"changed"`)
	if evidence.Native.LevelResults[0].Result == "changed" || string(evidence.GoEvent.RecordRef.Dimensions["host"]) == `"changed"` {
		t.Fatal("retained caller-owned data")
	}
	for _, test := range []struct {
		name   string
		change func(*GoEvidenceInput)
	}{
		{"not ACKed", func(i *GoEvidenceInput) { i.ACK.Confirmed = false }},
		{"other event ACK", func(i *GoEvidenceInput) { i.ACK.EventID = "other" }},
		{"config drift", func(i *GoEvidenceInput) { i.Context.ComparisonConfigDigest = strings.Repeat("b", 64) }},
		{"missing sibling closure", func(i *GoEvidenceInput) { i.Config.Levels = i.Config.Levels[:1] }},
		{"window mismatch", func(i *GoEvidenceInput) {
			i.Config.Levels[0].Trigger.WindowPoints = 2
			_, i.Context.ComparisonConfigDigest, _ = contract.CanonicalComparisonConfigV1(i.Config)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			i := finalTestInput(t)
			test.change(&i)
			if _, err := BuildGoFinalEvidence(i, 1<<20); err == nil {
				t.Fatal("invalid evidence accepted")
			}
		})
	}
	if _, err := BuildGoFinalEvidence(finalTestInput(t), 16); err == nil {
		t.Fatal("byte bound ignored")
	}
	// A sink ACK remains a true fact even if a later State write failed. No
	// State/Progress success is invented by this builder or its output schema.
	if !evidence.Delivery.BusinessACK {
		t.Fatal("business ACK lost")
	}
}

func TestFinalEvidenceIdentityAndDigestSeparation(t *testing.T) {
	input := finalTestInput(t)
	first, err := BuildGoFinalEvidence(input, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	input.Event.Trace.ExecutionID = "another-execution"
	input.EpochID = "another-epoch"
	input.Context.SlotIdentity = "another-runtime-reference"
	second, err := BuildGoFinalEvidence(input, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if first.Subject != second.Subject || first.Primary.Digest != second.Primary.Digest || first.GoEvent.EventID != second.GoEvent.EventID {
		t.Fatal("execution or Epoch contaminated business comparison identity")
	}
	// Sibling semantic change must change config closure without changing the
	// projection of an unchanged primary. It does not change native Event IDs.
	input.Config.Levels[0].Detectors[0].Threshold = "999"
	_, input.Context.ComparisonConfigDigest, err = contract.CanonicalComparisonConfigV1(input.Config)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := BuildGoFinalEvidence(input, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Context.ComparisonConfigDigest == first.Context.ComparisonConfigDigest || changed.Primary.Digest != first.Primary.Digest || changed.Subject != first.Subject {
		t.Fatal("config, primary and subject were conflated")
	}
	bad := *first
	bad.Delivery.FinalAdmission = "SUPPRESSED"
	if err := contract.ValidateFinalResultEvidenceV1(&bad); err == nil {
		t.Fatal("suppressed object became final evidence")
	}
	bad = *first
	bad.Chain = contract.ShadowPython
	bad.GoEvent = nil
	if err := contract.ValidateFinalResultEvidenceV1(&bad); err == nil {
		t.Fatal("Python synthetic siblings accepted")
	}
}

func TestFinalEvidenceDifferentPrimaryAndRecovery(t *testing.T) {
	input := finalTestInput(t)
	first, err := BuildGoFinalEvidence(input, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	// Same frozen config, same subject, a different actual final primary.
	input.Event.LevelResults[0].Result = contract.LevelResultAbnormal
	input.Event.LevelResults[0].DecisionWindow.Trigger.ObservedAnomalies = 1
	input.Event.LevelResults[1].Result = contract.LevelResultNormal
	input.Event.LevelResults[1].DecisionWindow.Trigger.ObservedAnomalies = 0
	rebuild := func(kind string) {
		g := input.Event
		event, buildErr := contract.BuildTriggerEventV1(contract.TriggerEventBuildInputV1{EventKind: kind, TenantID: g.TenantID, BusinessID: g.BusinessID, PlanRef: g.PlanRef, RecordRef: g.RecordRef, Observed: g.Observed, LevelResults: g.LevelResults, EvaluationTime: g.EvaluationTime, DetectPlanFingerprint: g.DetectPlanFingerprint, TriggerStateFingerprint: g.TriggerStateFingerprint, ExecutionID: g.Trace.ExecutionID, MaxEvidenceBytes: 1 << 20})
		if buildErr != nil {
			t.Fatal(buildErr)
		}
		input.Event = *event
		input.ACK = BusinessACK{EventID: event.EventID, SemanticDigest: event.EventSemanticDigest, Confirmed: true}
	}
	rebuild(contract.TriggerEventAbnormal)
	other, err := BuildGoFinalEvidence(input, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if first.Subject != other.Subject || first.Context.ComparisonConfigDigest != other.Context.ComparisonConfigDigest || first.Primary.Digest == other.Primary.Digest {
		t.Fatal("primary change escaped common subject/config")
	}
	left, err := ProjectTriggerEventV1(ChainPython, *first.GoEvent)
	if err != nil {
		t.Fatal(err)
	}
	right, err := ProjectTriggerEventV1(ChainGo, *other.GoEvent)
	if err != nil {
		t.Fatal(err)
	}
	comparison, err := CompareAbnormal(left, right)
	if err != nil || comparison.Reason != ReasonPrimaryLevelDiff {
		t.Fatal("primary mismatch was not a direct difference", err)
	}
	input = finalTestInput(t)
	for i := range input.Event.LevelResults {
		l := &input.Event.LevelResults[i]
		l.Result = contract.LevelResultNormal
		l.DetectEvidence.DetectionResult = "NORMAL"
		l.DecisionWindow.Trigger.ObservedAnomalies = 0
		l.DecisionWindow.Recovery.ObservedConsecutiveMisses = 0
	}
	input.Event.LevelResults[1].Result = contract.LevelResultRecovery
	input.Event.LevelResults[1].DecisionWindow.Recovery.ObservedConsecutiveMisses = 1
	rebuild(contract.TriggerEventRecovery)
	recovered, err := BuildGoFinalEvidence(input, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.ResultKind != contract.TriggerEventRecovery {
		t.Fatal("Recovery lost")
	}
	input.Completeness.Input = "PARTIAL_ACCEPTED"
	if _, err := BuildGoFinalEvidence(input, 1<<20); err == nil {
		t.Fatal("partial Recovery accepted")
	}
}

func offlineTestManifest() contract.ValidationEpochManifestV1 {
	return contract.ValidationEpochManifestV1{Schema: contract.Schema{Name: contract.ValidationEpochManifestSchemaV1, Major: 1}, RequiredFeatures: []string{}, EpochID: "epoch", StartedAt: 1725000000, EligibleFrom: 1725000000, ExpectedEnd: 1725001000,
		Target: contract.ShadowTargetScopeV1{TenantID: "default", BusinessID: "2", DataType: "SERIES", Capabilities: []string{"Threshold"}, ExcludedCapabilities: []string{}}, Python: contract.ShadowSourceVersionV1{Commit: strings.Repeat("a", 40), Image: "python@sha256:example", Schema: "python-final-v1"}, Go: contract.ShadowSourceVersionV1{Commit: strings.Repeat("b", 40), Image: "go@sha256:example", Schema: "go-final-v1"}, StrategyObservation: "observation", StrategyPublication: "publication", ComparisonVersion: "comparison-v1", IdentityVersion: "identity-v1", PythonTopic: contract.ShadowTopicV1{Name: "python-shadow", ConsumerGroup: "comparison", Partitions: 1}, GoTopic: contract.ShadowTopicV1{Name: "go-shadow", ConsumerGroup: "comparison", Partitions: 1}, AuditTopic: contract.ShadowTopicV1{Name: "audit-shadow", ConsumerGroup: "audit", Partitions: 1}, Limits: contract.ShadowResourceLimitsV1{MaxEntries: 10, MaxRetainedBytes: 1024, MaxAgeSeconds: 60, MaxQueueEntries: 10, MaxQueueBytes: 1024, MaxAuditInflight: 1, MaxMessageBytes: 1024}, GracePolicyRevision: "grace-v1", RuntimeConfigDigests: []string{strings.Repeat("c", 64)}, KnownExclusionReasons: []string{"FILTERED_NORMAL_RESULT_UNOBSERVABLE"}}
}

func TestOfflineExecutableProjectionPath(t *testing.T) {
	in := OfflineInput{Manifest: offlineTestManifest(), GoEvents: []GoEvidenceInput{finalTestInput(t)}, Receipts: []contract.ChainCoverageReceiptV1{}}
	payload, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeOfflineInput(payload, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	out, err := ValidateOfflineInput(decoded, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != "CONTRACTS_VALID_NOT_EPOCH_VERDICT" || len(out.Evidence) != 1 {
		t.Fatal("wrong offline scope")
	}
	if err := contract.ValidateShadowExclusionV1(&in.Manifest, "invented"); err == nil {
		t.Fatal("unfrozen exclusion accepted")
	}
	in.GoEvents[0].EpochID = "other"
	if _, err := ValidateOfflineInput(in, 1<<20); err == nil {
		t.Fatal("cross-Epoch input accepted")
	}
	m := offlineTestManifest()
	m.RuntimeConfigDigests = nil
	if err := contract.ValidateValidationEpochManifestV1(&m); err == nil {
		t.Fatal("runtime configuration identity omitted")
	}
	m = offlineTestManifest()
	m.GoTopic.Name = m.PythonTopic.Name
	if err := contract.ValidateValidationEpochManifestV1(&m); err == nil {
		t.Fatal("shared topic accepted")
	}
	m = offlineTestManifest()
	m.Limits.MaxQueueBytes = 0
	if err := contract.ValidateValidationEpochManifestV1(&m); err == nil {
		t.Fatal("missing memory bound accepted")
	}
}
