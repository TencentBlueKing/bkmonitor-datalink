package shadow_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/comparator"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/detect"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/shadow"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

func procPortFrozenInput(t *testing.T, units ...string) (execution.DuePlan, []execution.DataRequirement, map[execution.LogicalQueryRef]execution.QueryPlanFacts) {
	t.Helper()
	document, err := os.ReadFile("../contract/testdata/proc-port-v1/strategy.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(units) != 0 {
		var source map[string]any
		if err := json.Unmarshal(document, &source); err != nil {
			t.Fatal(err)
		}
		item := source["items"].([]any)[0].(map[string]any)
		item["unit"] = units[0]
		item["query_configs"].([]any)[0].(map[string]any)["unit"] = units[0]
		document, err = json.Marshal(source)
		if err != nil {
			t.Fatal(err)
		}
	}
	access := true
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("route-v1", "UTC", controlplane.LegacyQueryRuntimeFacts{AccessBKData: &access, BKDataCMDBLevelTables: []string{}, SystemDiskFilter: controlplane.LegacyRuntimeFilterFact{}, SystemNetworkFilter: controlplane.LegacyRuntimeFilterFact{}})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{Strategies: []controlplane.SourceStrategy{{SourceID: "103", Document: json.RawMessage(document), Identity: controlplane.SourceIdentity{TenantID: "default", BusinessID: "2", SpaceScope: "bkcc__2"}}}, Planner: planner})
	if err != nil || len(catalog.QueryGroups) != 1 {
		t.Fatalf("catalog=%+v err=%v", catalog, err)
	}
	frozen := catalog.QueryGroups[0].Plans[0]
	compiler, err := strategy.NewCompiler(strategy.NewDefaultAlgorithmCompilerRegistry(), config.Default().CompilerLimits())
	if err != nil {
		t.Fatal(err)
	}
	result, err := compiler.Compile(context.Background(), strategy.CompileRequest{Plan: frozen.Plan, DatasetContract: catalog.QueryGroups[0].QueryPlan.Normalization.DatasetContract, StateSemantics: strategy.StateSemantics{StateSchemaVersion: "window-state-v1", CodecSemanticsVersion: "window-state-codec-v1", IdentitySchemaDigest: strings.Repeat("3", 64), SourceTimeSemanticsVersion: "source-time-seconds-v1", HistoryCellSemanticsVersion: "detect-history-cell-v1"}})
	if err != nil {
		t.Fatal(err)
	}
	plan, ok := result.Plan()
	if !ok || len(result.LevelTerminals()) != 0 {
		t.Fatalf("compile %+v %+v", result.PlanTerminal(), result.LevelTerminals())
	}
	due := execution.DuePlan{Identity: frozen.Identity, CompiledPlan: plan, ScheduleSpec: frozen.ScheduleSpec, ScheduleRevision: frozen.ScheduleRevision, CompletionDeadlineUnixMilli: 180000}
	var requirements []execution.DataRequirement
	for _, template := range frozen.RequirementTemplates {
		requirements = append(requirements, template.Bind(execution.DataRequirementConsumer{Consumer: execution.ConsumerRef{Plan: due.Identity}, ConsumerDeadlineUnixMilli: 180000, DownstreamExecutionReserveMilliSec: 5000}))
	}
	return due, requirements, frozen.QueryPlans
}

func TestProcPortFrozenRealCompiler(t *testing.T) {
	due, requirements, queries := procPortFrozenInput(t)
	c, err := shadow.BuildFrozenComparisonConfigV2(due, requirements, queries)
	if err != nil {
		t.Fatalf("%v projection=%+v requirements=%+v", err, due.CompiledPlan.Projection(), requirements)
	}
	wire, _, err := contract.CanonicalComparisonConfigV2(c)
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("ALARMD_UPDATE_PROC_GOLDEN") == "1" {
		if err := os.WriteFile("../contract/testdata/proc-port-v1/config.json", append(wire, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
	expected, err := os.ReadFile("../contract/testdata/proc-port-v1/config.json")
	if err != nil {
		t.Fatal(err)
	}
	if string(wire)+"\n" != string(expected) {
		t.Fatalf("config differs: %s", wire)
	}
}

func TestProcPortActualDetectToFinalNativeValue(t *testing.T) {
	due, requirements, queries := procPortFrozenInput(t)
	evaluator, err := detect.NewEvaluator(detect.NewDefaultRegistry(), nil)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := evaluator.PreparePlan(due.CompiledPlan)
	if err != nil {
		t.Fatal(err)
	}
	var samples []struct {
		Raw    string `json:"raw"`
		Go     string `json:"go"`
		Python string `json:"python"`
	}
	raw, err := os.ReadFile("../contract/testdata/proc-port-v1/native-values.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &samples); err != nil {
		t.Fatal(err)
	}
	for _, sample := range samples {
		t.Run(sample.Raw, func(t *testing.T) {
			fields := []contract.DimensionFieldV2{{Name: "bk_target_cloud_id", Value: json.RawMessage(`"0"`)}, {Name: "bk_target_ip", Value: json.RawMessage(`"127.0.0.1"`)}, {Name: "display_name", Value: json.RawMessage(`"service"`)}}
			dimensionDigest, err := contract.DeriveDimensionIdentityDigestV2(due.Identity.TenantID, due.Identity.BusinessID, fields)
			if err != nil {
				t.Fatal(err)
			}
			dataset := execution.NewDataset([]contract.CanonicalRecordV2{{RecordID: strings.Repeat("b", 64), SourceTime: 120, BusinessID: "2", DimensionIdentity: contract.DimensionIdentityV2{Digest: dimensionDigest, Fields: fields}, Values: map[string]json.RawMessage{"value": json.RawMessage(sample.Raw)}, Dimensions: map[string]json.RawMessage{"nonlisten": json.RawMessage(`"[80]"`)}, ReceivedTime: 120}})
			record, _ := dataset.Record(0)
			view, err := execution.NewDatasetView(dataset, []uint32{0})
			if err != nil {
				t.Fatal(err)
			}
			consumer := execution.ConsumerRef{Plan: due.Identity, LevelID: 1, HasLevel: true}
			requirement := requirements[0]
			input := execution.SeriesEvaluationInputRequest{Consumer: consumer, SeriesIdentity: execution.SeriesIdentityDigest(record.DimensionIdentity().Digest), RequirementIDs: []execution.RequirementID{requirement.RequirementID}, Inputs: []execution.NamedInputBinding{{Consumer: consumer, RequirementID: requirement.RequirementID, DatasetName: requirement.DatasetName, Role: requirement.Role, Dataset: dataset, View: view, Completeness: execution.CompletenessFull, DataState: execution.DataStateData, Disposition: execution.AccessAvailable}}}
			facts, values, _, err := evaluator.EvaluatePreparedSeriesRecord(context.Background(), prepared, []execution.SeriesEvaluationInputRequest{input}, record)
			if err != nil || len(values) != 1 || len(facts) != 1 || facts[0].Result != "ANOMALOUS" {
				t.Fatalf("facts=%+v values=%+v err=%v", facts, values, err)
			}
			finalInput := frozenFinalFrom(t, due, requirements, queries, func(event *contract.TriggerEventV1) {
				event.LevelResults = event.LevelResults[1:]
				level := &event.LevelResults[0]
				event.EvaluationTime = 180
				event.RecordRef.SourceTime = 120
				event.RecordRef.DimensionIdentityDigest = dimensionDigest
				event.RecordRef.Dimensions = map[string]json.RawMessage{}
				for _, field := range fields {
					event.RecordRef.Dimensions[field.Name] = field.Value
				}
				level.DecisionWindow.Recovery.OldestWindowStart = 61
				level.DecisionWindow.SourceTime = 120
				level.DecisionWindow.Trigger.WindowEnd = 120
				level.DecisionWindow.Trigger.WindowStart = 61
				level.LevelID = 1
				level.Priority = 1
				level.DetectEvidence.NormalizedValue = json.RawMessage(values[0].CanonicalDecimal)
				level.DetectEvidence.PredicateDigest = facts[0].Evidence.PredicateDigest
				event.Observed.Values["value"] = json.RawMessage(sample.Raw)
				event.Observed.Unit = due.CompiledPlan.Projection().DataUnit
			})
			evidence, err := shadow.BuildGoFinalEvidenceV2(finalInput, 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			business, err := shadow.BuildGoBusinessAbnormal(finalInput, 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			if business.Primary.Values["value"] != sample.Go {
				t.Fatalf("business changed value %+v", business.Primary)
			}
			if sample.Raw == "-1.5" {
				assertProcPortComparator(t, finalInput, business)
				encoded, err := contract.EncodeBusinessAbnormalV1(business, 1<<20)
				if err != nil {
					t.Fatal(err)
				}
				path := "../contract/testdata/proc-port-v1/native-reference.json"
				if os.Getenv("ALARMD_UPDATE_PROC_GOLDEN") == "1" {
					if err := os.WriteFile(path, append(encoded.CopyBytes(), '\n'), 0600); err != nil {
						t.Fatal(err)
					}
				}
				want, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if string(want) != string(encoded.CopyBytes())+"\n" {
					t.Fatal("actual native golden differs")
				}
			}
			if evidence.Primary.Values["value"] != sample.Go {
				t.Fatalf("primary=%+v want %s", evidence.Primary, sample.Go)
			}
		})
	}
}

func TestProcPortFrozenUnitsAndBinding(t *testing.T) {
	due, req, queries := procPortFrozenInput(t, "percent")
	got, err := shadow.BuildFrozenComparisonConfigV2(due, req, queries)
	if err != nil || got.Numeric.SourceUnit != "percent" || got.Numeric.TargetUnit != "percent" || got.Numeric.Multiplier != "1" || got.Numeric.Rounding != "" {
		t.Fatalf("numeric=%+v err=%v", got.Numeric, err)
	}
	for _, kind := range []string{"metric", "identity", "projection", "missing_query"} {
		t.Run(kind, func(t *testing.T) {
			due, req, queries := procPortFrozenInput(t)
			key := req[0].LogicalQueryRef
			q := queries[key]
			switch kind {
			case "metric":
				q.QueryList[0].FieldName = "other"
			case "identity":
				req[0].InputProjection.IdentityFields = []string{"nonlisten"}
			case "projection":
				req[0].InputProjection.DimensionFields = []string{"protocol"}
			case "missing_query":
				delete(queries, key)
			}
			if kind != "missing_query" {
				queries[key] = q
			}
			if _, err := shadow.BuildFrozenComparisonConfigV2(due, req, queries); err == nil {
				t.Fatal("accepted changed frozen binding")
			}
		})
	}
}

func assertProcPortComparator(t *testing.T, input shadow.GoFrozenEvidenceInputV2, business *contract.BusinessAbnormalV1) {
	t.Helper()
	python, err := os.ReadFile("../contract/testdata/proc-port-v1/python-reference.json")
	if err != nil {
		t.Fatal(err)
	}
	goWire, err := contract.EncodeGoBusinessAbnormalV1(contract.GoBusinessAbnormalV1{Schema: "go-business-abnormal-v1", RecordType: "BUSINESS_ABNORMAL", EpochID: input.EpochID, Context: input.Context, Reference: *business}, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	p := comparator.BusinessPartition{Chain: contract.ShadowPython, Topic: "native", Partition: 0}
	g := comparator.BusinessPartition{Chain: contract.ShadowGo, Topic: "shadow", Partition: 0}
	run, err := comparator.NewBusinessRun("epoch", []comparator.BusinessRange{{BusinessPartition: p, Start: 0, End: 1}, {BusinessPartition: g, Start: 0, End: 1}}, comparator.BusinessLimits{Entries: 10, Bytes: 1 << 20, MessageBytes: 1 << 18, OffsetEntries: 20, MaxAge: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range []struct {
		part comparator.BusinessPartition
		wire []byte
	}{{p, python}, {g, goWire.CopyBytes()}} {
		if err := run.Observe(time.Unix(200, 0), comparator.BusinessOffset{BusinessPartition: message.part, Offset: 0, RawSHA256: fmt.Sprintf("%x", sha256.Sum256(message.wire))}, message.wire); err != nil {
			t.Fatal(err)
		}
	}
	if audits, err := run.Finalize(time.Unix(203, 0), time.Unix(202, 0), time.Second); err != nil || len(audits) != 0 {
		t.Fatal("missing receipt must remain pending", audits, err)
	}
	zero, one := contract.KnownShadowCountV1(0), contract.KnownShadowCountV1(1)
	receipt := &contract.ChainCoverageReceiptV1{Schema: contract.Schema{Name: contract.ChainCoverageReceiptSchemaV1, Major: 1}, RequiredFeatures: []string{}, RecordType: contract.ShadowCoverage, EpochID: input.EpochID, Chain: contract.ShadowGo, TenantID: input.Due.Identity.TenantID, BusinessID: input.Due.Identity.BusinessID, StrategyID: input.Due.Identity.StrategyID, Context: input.Context,
		Input:   contract.ShadowInputCoverageV1{QueryAttempts: contract.ShadowAttemptObservationV1{ExecutionRef: "controlled-execution", CurrentExecution: one, LogicalSlot: one, ObservedFromSlotStart: true, ContinuousThroughTerminal: true}, Completion: "FULL", Series: one, Records: one, SelectedPlanRecords: one, SourceWindow: contract.SourceWindowV2{FromTime: 120, UntilTime: 180}, ResultBytesDigest: strings.Repeat("a", 64)},
		Records: contract.ShadowRecordOutcomesV1{PrimaryAbnormal: one, PrimaryRecovery: zero, NoEvent: zero, Excluded: zero, Unavailable: zero, Terminal: zero},
		Levels:  []contract.ShadowLevelCoverageV1{{LevelID: 1, Selected: one, Normal: zero, Abnormal: one, Recovery: zero, Unavailable: zero, Terminal: zero, Excluded: zero, PythonShortCircuited: zero, PartialAcceptedAbnormal: zero, SuppressedAbnormal: zero, Primary: one, SiblingDiagnostic: zero, LateAfterComplete: zero}}, PhysicalProduced: one, PhysicalACKed: one, TerminalFact: true, TerminalFactRef: "controlled-progress", CoverageComplete: true, GapReasons: []string{}, ReasonCounts: []contract.ReasonCountV1{}}
	cfg, err := shadow.BuildFrozenComparisonConfigV2(input.Due, input.Requirements, input.Queries)
	if err != nil {
		t.Fatal(err)
	}
	if err := run.BindGoReceipt(business.Subject, receipt, cfg, time.Unix(201, 0)); err != nil {
		t.Fatal(err)
	}
	audits, err := run.Finalize(time.Unix(203, 0), time.Unix(202, 0), time.Second)
	if err != nil || len(audits) != 1 || audits[0].Verdict != "MATCHED_SAME" {
		t.Fatalf("audits=%+v err=%v", audits, err)
	}
	if offset, _ := run.Committable(p); offset != 0 {
		t.Fatal("offset crossed unACKed Audit")
	}
	_, err = comparator.EncodeBusinessAudit(&audits[0], 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := contract.DeriveCanonicalDigestV2("business-audit-payload-v1", &audits[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := run.ACKAudit(audits[0].ID, digest); err != nil {
		t.Fatal(err)
	}
	if offset, _ := run.Committable(p); offset != 1 {
		t.Fatal("ACK did not release offset")
	}
}
