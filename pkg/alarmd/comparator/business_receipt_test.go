package comparator

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

func businessReceiptFixture(t *testing.T, produced bool) (*contract.BusinessAbnormalV1, contract.ComparisonConfigV2, *contract.ChainCoverageReceiptV1) {
	t.Helper()
	ref, err := contract.DecodeBusinessAbnormalV1(businessFixture(t), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	_ = json.Unmarshal(ref.Config, &object)
	object["schema_version"] = "comparison-config-v2"
	for _, l := range object["levels"].([]any) {
		l.(map[string]any)["recovery"] = map[string]any{"enabled": false, "consecutive_windows": 0, "mode": "CONTINUOUS_TRIGGER_MISS", "input_requirement": "DATA_DRIVEN"}
	}
	wire, _ := json.Marshal(object)
	var config contract.ComparisonConfigV2
	_ = json.Unmarshal(wire, &config)
	_, digest, err := contract.CanonicalComparisonConfigV2(config)
	if err != nil {
		t.Fatal(err)
	}
	k := contract.KnownShadowCountV1
	zero, one := k(0), k(1)
	d := strings.Repeat("a", 64)
	ctx := contract.ShadowContextV1{ComparisonConfigDigest: digest, PlanScheduleRevision: "plan", EvaluationTime: 180, SlotIdentity: "slot", SnapshotRevision: "snapshot", QueryRevision: "query", QueryGroupScheduleRevision: "schedule", DuePlanSetDigest: d, EffectiveTimeRequirementDigest: d, EffectiveTimeFactDigest: d}
	r := &contract.ChainCoverageReceiptV1{Schema: contract.Schema{Name: contract.ChainCoverageReceiptSchemaV1, Major: 1}, RequiredFeatures: []string{}, RecordType: contract.ShadowCoverage, EpochID: "epoch", Chain: contract.ShadowGo, TenantID: ref.Subject.TenantID, BusinessID: ref.Subject.BusinessID, StrategyID: ref.Subject.StrategyID, Context: ctx,
		Input:   contract.ShadowInputCoverageV1{QueryAttempts: contract.ShadowAttemptObservationV1{ExecutionRef: "execution", CurrentExecution: one, LogicalSlot: one, ObservedFromSlotStart: true, ContinuousThroughTerminal: true}, Completion: "FULL", Series: one, Records: one, SelectedPlanRecords: one, SourceWindow: contract.SourceWindowV2{FromTime: 120, UntilTime: 180}, ResultBytesDigest: d},
		Records: contract.ShadowRecordOutcomesV1{PrimaryAbnormal: zero, PrimaryRecovery: zero, NoEvent: one, Excluded: zero, Unavailable: zero, Terminal: zero},
		Levels:  []contract.ShadowLevelCoverageV1{{LevelID: 1, Selected: one, Normal: one, Abnormal: zero, Recovery: zero, Unavailable: zero, Terminal: zero, Excluded: zero, PythonShortCircuited: zero, PartialAcceptedAbnormal: zero, SuppressedAbnormal: zero, Primary: zero, SiblingDiagnostic: zero, LateAfterComplete: zero}}, PhysicalProduced: zero, PhysicalACKed: zero, TerminalFact: true, TerminalFactRef: "actual-progress", CoverageComplete: true, GapReasons: []string{}, ReasonCounts: []contract.ReasonCountV1{}}
	if produced {
		r.Records.PrimaryAbnormal = one
		r.Records.NoEvent = zero
		r.Levels[0].Abnormal = one
		r.Levels[0].Normal = zero
		r.Levels[0].Primary = one
		r.PhysicalProduced = one
		r.PhysicalACKed = one
	}
	if err = contract.ValidateChainCoverageReceiptV1(r); err != nil {
		t.Fatal(err)
	}
	return ref, config, r
}

func TestBusinessReceiptCannotHideMissingEvidenceAsMissingGo(t *testing.T) {
	for _, produced := range []bool{false, true} {
		ref, config, receipt := businessReceiptFixture(t, produced)
		p := BusinessPartition{contract.ShadowPython, "native", 0}
		g := BusinessPartition{contract.ShadowGo, "shadow", 0}
		r, err := NewBusinessRun("epoch", []BusinessRange{{p, 0, 1}, {g, 0, 0}}, BusinessLimits{10, 1 << 20, 1 << 18, 20, time.Hour})
		if err != nil {
			t.Fatal(err)
		}
		if err = r.Observe(time.Unix(200, 0), BusinessOffset{p, 0, "raw"}, businessFixture(t)); err != nil {
			t.Fatal(err)
		}
		if err = r.BindGoReceipt(ref.Subject, receipt, config, time.Unix(201, 0)); err != nil {
			t.Fatal(err)
		}
		audits, err := r.Finalize(time.Unix(203, 0), time.Unix(202, 0), time.Second)
		if err != nil || len(audits) != 1 {
			t.Fatal(err, audits)
		}
		if produced {
			if audits[0].Reason != "GO_EVIDENCE_COVERAGE_GAP" || audits[0].Verdict != "" {
				t.Fatal("actual Go ACK disappeared into PYTHON_ONLY", audits)
			}
		} else if audits[0].Verdict != "PYTHON_ONLY" {
			t.Fatal("proven missing Go not hard failure", audits)
		}
		receipt.EpochID = "different"
		if err = r.BindGoReceipt(ref.Subject, receipt, config, time.Unix(201, 0)); err == nil {
			t.Fatal("foreign Epoch receipt accepted")
		}
	}
}
