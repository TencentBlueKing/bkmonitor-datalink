package evaluation

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type capacityBackend struct{ raw []byte }

func (b *capacityBackend) MGet(context.Context, []string) ([][]byte, error) {
	return [][]byte{append([]byte(nil), b.raw...)}, nil
}
func (*capacityBackend) SetMany(context.Context, []state.BackendWrite) error { return nil }

func TestCapacityProfileLegalSeries(t *testing.T) {
	if os.Getenv("ALARMD_CAPACITY_PROFILE") != "1" {
		t.Skip("opt-in capacity measurement")
	}
	if testing.Short() {
		t.Skip("capacity sample")
	}
	cfg := config.Default()
	for _, shape := range []struct {
		r          int
		window     uint32
		dimensions int
	}{{500, 1024, 128}, {500, 1024, 4096}, {32, 256, 128}, {32, 256, 4096}, {450, 450, 4096}} {
		dimensionBytes := shape.dimensions
		t.Run(fmt.Sprintf("R%d-W%d-D%d", shape.r, shape.window, dimensionBytes), func(t *testing.T) {
			plan := capacityCompiled(t, shape.window)
			records := make([]contract.CanonicalRecordV2, shape.r)
			for i := range records {
				records[i] = contract.CanonicalRecordV2{RecordID: fmt.Sprintf("%064d", i+10000), SourceTime: int64(1000000 + i*60), BusinessID: "2", DimensionIdentity: contract.DimensionIdentityV2{Digest: strings.Repeat("c", 64)}, Values: map[string]json.RawMessage{"value": json.RawMessage(`80`)}, Dimensions: map[string]json.RawMessage{"host": json.RawMessage(`"` + strings.Repeat("h", dimensionBytes) + `"`)}, ReceivedTime: int64(1000000 + i*60)}
				if dimensionBytes >= 4096 {
					for metric := 0; metric < 64; metric++ {
						records[i].Values[fmt.Sprintf("metric_%02d", metric)] = json.RawMessage(`123456.789`)
					}
				}
			}
			req := requestFixtureForPlan(t, plan, records, nil)
			refs, err := execution.DeriveRuntimeLevelContractRefs(plan)
			if err != nil {
				t.Fatal(err)
			}
			baseInput := req.Inputs[0]
			baseFact := req.Header.EffectiveTimeFacts[0]
			req.Inputs = nil
			req.Header.EffectiveTimeFacts = nil
			req.Header.Requirements[0].Consumers = nil
			req.State.Items[0].Levels = nil
			levelMutations := []execution.RuntimeLevelStateMutation{}
			for i, ref := range refs {
				input := baseInput
				input.Consumer.LevelID = uint32(5 + i)
				input.Inputs = append([]execution.NamedInputBinding(nil), baseInput.Inputs...)
				input.Inputs[0].Consumer = input.Consumer
				req.Inputs = append(req.Inputs, input)
				fact := baseFact
				fact.Consumer = input.Consumer
				req.Header.EffectiveTimeFacts = append(req.Header.EffectiveTimeFacts, fact)
				req.Header.Requirements[0].Consumers = append(req.Header.Requirements[0].Consumers, execution.DataRequirementConsumer{Consumer: input.Consumer, ConsumerDeadlineUnixMilli: 200000, DownstreamExecutionReserveMilliSec: 1000})
				req.State.Items[0].Levels = append(req.State.Items[0].Levels, execution.RuntimeLevelStateView{LevelID: uint32(5 + i), LevelStateCompatibility: ref.LevelStateCompatibility, WarmupRequirementRef: ref.WarmupRequirementRef, HistoryCompleteness: execution.HistoryFull})
				levelMutations = append(levelMutations, execution.RuntimeLevelStateMutation{LevelID: uint32(5 + i), LevelStateCompatibility: ref.LevelStateCompatibility, WarmupRequirementRef: ref.WarmupRequirementRef, HistoryCompleteness: execution.HistoryFull})
			}
			digest, err := execution.DeriveDuePlanSetDigest(req.Header.DuePlans, req.Header.Requirements)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Contract.DuePlanSetDigest = digest
			for i := range req.Inputs {
				req.Inputs[i].Contract = req.Header.Contract
			}
			version, err := execution.BuildApplyVersion(req.Header.Contract, 1)
			if err != nil {
				t.Fatal(err)
			}
			history := []execution.StateHistoryPoint{}
			var raw []byte
			for n := 1; n <= cfg.Limits.Codec.MaxPoints; n++ {
				point := execution.StateHistoryPoint{RecordID: fmt.Sprintf("%064d", n), SourceTime: int64(1000000 - (cfg.Limits.Codec.MaxPoints-n+1)*60)}
				for _, level := range plan.Levels() {
					point.Levels = append(point.Levels, execution.StateLevelFact{LevelID: level.Definition().LevelID, DetectFingerprint: level.Fingerprints().Detect, Result: execution.LevelFactAnomalous})
				}
				candidate := append(history, point)
				encoded, e := json.Marshal(map[string]any{"schema": "alarmd-runtime-state-v2", "identity": req.State.Items[0].Identity, "blob_revision": 1, "apply_version": version, "mutation_digest": "capacity", "last_event_time": point.SourceTime, "levels": levelMutations, "history": candidate})
				if e != nil {
					t.Fatal(e)
				}
				if len(encoded) > cfg.Limits.Codec.MaxEncodedBytes {
					break
				}
				history = candidate
				raw = encoded
			}
			// Make the selected legal history contiguous immediately before PRIMARY.
			for i := range history {
				history[i].SourceTime = int64(1000000 - (len(history)-i)*60)
			}
			raw, err = json.Marshal(map[string]any{"schema": "alarmd-runtime-state-v2", "identity": req.State.Items[0].Identity, "blob_revision": 1, "apply_version": version, "mutation_digest": "capacity", "last_event_time": history[len(history)-1].SourceTime, "levels": levelMutations, "history": history})
			if err != nil || len(raw) > cfg.Limits.Codec.MaxEncodedBytes {
				t.Fatalf("invalid final input: %v bytes=%d", err, len(raw))
			}
			backend := &capacityBackend{raw: raw}
			router, _ := state.NewFixedRouter("capacity", backend)
			store, _ := state.NewExecutionStore(state.ExecutionStoreOptions{Prefix: "capacity", Router: router, MaxValueBytes: cfg.Limits.Codec.MaxEncodedBytes, MaxItemsPerCall: cfg.Limits.Store.MaxKeysPerBatch, RuntimeTTL: time.Hour})
			loaded, err := store.LoadRuntime(context.Background(), execution.StatePreflightRequest{Contract: req.Header.Contract, Items: []execution.StatePreflightItem{{Identity: req.State.Items[0].Identity, ApplyVersion: version}}})
			if err != nil || len(loaded.Items) != 1 || loaded.Items[0].Status != execution.StateFoundReady {
				t.Fatalf("legal state: %v %+v", err, loaded)
			}
			req.State = loaded
			evaluator := newEvaluator(t)
			evaluator.limits.MaxRecords = cfg.Limits.Detect.MaxRecordsPerSeries
			evaluator.limits.MaxLevels = uint64(cfg.Limits.Trigger.MaxLevels)
			evaluator.limits.Trigger = cfg.TriggerLimits()
			// Keep the fixed input alive throughout the baseline and post-GC readings.
			runtime.GC()
			var before, after, live runtime.MemStats
			runtime.ReadMemStats(&before)
			var peak atomic.Uint64
			peak.Store(before.HeapAlloc)
			stop := make(chan struct{})
			done := make(chan struct{})
			go func() {
				defer close(done)
				for {
					select {
					case <-stop:
						return
					case <-time.After(time.Millisecond):
						var m runtime.MemStats
						runtime.ReadMemStats(&m)
						for old := peak.Load(); m.HeapAlloc > old; old = peak.Load() {
							if peak.CompareAndSwap(old, m.HeapAlloc) {
								break
							}
						}
					}
				}
			}()
			started := time.Now()
			result, err := evaluator.Evaluate(context.Background(), req)
			elapsed := time.Since(started)
			close(stop)
			<-done
			runtime.ReadMemStats(&after)
			if err != nil {
				t.Fatal(err)
			}
			if err = result.Validate(req); err != nil {
				t.Fatal(err)
			}
			if len(result.Plans) != 1 || len(result.Plans[0].LevelOutcomes) != shape.r*8 || len(result.Plans[0].StateResults) != 1 {
				t.Fatal("illegal output shape")
			}
			admission, admitErr := store.AdmitRuntime(context.Background(), execution.StateApplyRequest{Contract: req.Header.Contract, Items: []execution.StateMutation{result.Plans[0].StateResults[0].Mutation}})
			if admitErr != nil {
				t.Fatal(admitErr)
			}
			t.Logf("output_state_admission=%+v", admission)
			if shape.r != 500 && admission.Items[0].Status != execution.StateAdmissionAccepted {
				t.Fatal("representative output was rejected")
			}
			runtime.GC()
			runtime.ReadMemStats(&live)
			runtime.KeepAlive(req)
			runtime.KeepAlive(result)
			runtime.KeepAlive(backend)
			runtime.KeepAlive(records)
			runtime.KeepAlive(history)
			t.Logf("state_bytes=%d H=%d R=%d L=8 dimensions=%d events=%d duration=%s records_per_sec=%.2f alloc=%d heap_before=%d heap_after=%d sampled_peak=%d heap_after_gc=%d", len(raw), len(history), shape.r, dimensionBytes, len(result.Plans[0].StateResults[0].Events), elapsed, float64(shape.r)/elapsed.Seconds(), after.TotalAlloc-before.TotalAlloc, before.HeapAlloc, after.HeapAlloc, peak.Load(), live.HeapAlloc)
		})
	}
}

func capacityCompiled(t *testing.T, windowSize uint32) *strategy.CompiledPlan {
	cfg := config.Default()
	c, err := strategy.NewCompiler(strategy.NewDefaultAlgorithmCompilerRegistry(), cfg.CompilerLimits())
	if err != nil {
		t.Fatal(err)
	}
	ref := contract.StrategyRefV2{TenantID: "tenant", StrategyID: "7", Revision: "r1"}
	projection := contract.InputProjectionV2{ValueFields: []string{"value"}, DimensionFields: []string{"host"}, BusinessIdentityField: "bk_biz_id", MultiValueAlignment: "SINGLE_VALUE", DataUnit: "percent", MissingValuePolicy: contract.MissingValuePolicyRequired}
	triggerBody := map[string]any{"window_size": windowSize, "required_anomalies": uint32(1), "step_seconds": 60}
	triggerPayload, err := json.Marshal(triggerBody)
	if err != nil {
		t.Fatal(err)
	}
	triggerConfig := json.RawMessage(triggerPayload)
	level := contract.LevelIRV2{Definition: contract.LevelDefinitionV2{LevelID: 5, Priority: 1}, Connector: contract.LevelConnectorAND, DetectPlan: contract.DetectPlanV2{Algorithms: []contract.AlgorithmIRV2{{Type: "Threshold", Version: 1, Config: json.RawMessage(`{"value_field":"value","data_unit":"percent","threshold_unit_prefix":"","precision":{"decimal_places":6,"rounding":"HALF_EVEN"},"groups":[{"conditions":[{"operator":"GTE","threshold_decimal":"50"}]}]}`)}}}, TriggerPlan: contract.TypedPlanV1{Type: "N_OF_M", Version: 1, Config: triggerConfig}, RecoveryPlan: contract.TypedPlanV1{Type: "CONTINUOUS_TRIGGER_MISS", Version: 1, Config: json.RawMessage(`{"enabled":true,"consecutive_windows":1}`)}}
	levels := make([]contract.LevelIRV2, 8)
	for i := range levels {
		levels[i] = level
		levels[i].Definition.LevelID = uint32(5 + i)
		levels[i].Definition.Priority = uint32(1 + i)
	}
	p := contract.EvaluationPlanV2{PlanID: "7", StrategyRef: ref, InputProjection: projection, StrategyIR: contract.StrategyIRV2{Schema: contract.Schema{Name: contract.StrategyIRSchemaV2, Major: 2}, StrategyRef: ref, InputProjection: projection, ExecutionSemantics: contract.ExecutionSemanticsV2{EvaluationScope: contract.EvaluationScopeSeries, QueryWindow: 300, AggregationInterval: 60, EvaluationInterval: 60, LatenessTolerance: 120}, Levels: levels}}
	r, err := c.Compile(context.Background(), strategy.CompileRequest{Plan: p, DatasetContract: contract.DatasetContractV2{SchemaDigest: strings.Repeat("1", 64), NormalizationDigest: strings.Repeat("2", 64), IdentityFields: []string{"host"}, SourceTimeField: "time", ReceivedTimeField: "received_time"}, StateSemantics: strategy.StateSemantics{StateSchemaVersion: "s", CodecSemanticsVersion: "c", IdentitySchemaDigest: strings.Repeat("3", 64), SourceTimeSemanticsVersion: "t", HistoryCellSemanticsVersion: "h"}})
	if err != nil {
		t.Fatal(err)
	}
	plan, ok := r.Plan()
	if !ok {
		t.Fatalf("compile terminal=%+v", r.PlanTerminal())
	}
	return plan
}
